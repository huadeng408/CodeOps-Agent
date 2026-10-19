package cli_test

import (
	"context"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/orchestrator"
	"google.golang.org/grpc"
)

// Only the external orchestrator is a fixture; CLI startup and child ownership are real.
type contextWindowProcess struct {
	pb.UnimplementedOrchestratorServer
	window string
}

func (s contextWindowProcess) Health(context.Context, *pb.Empty) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok", Version: s.window}, nil
}

func TestCLIConfiguredContextWindowReachesManagedChild(t *testing.T) {
	t.Setenv("CODE_AGENT_CONTEXT_WINDOW_PROCESS_HELPER", "1")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	for _, tc := range []struct {
		name   string
		window int
		args   []string
		want   string
	}{
		{name: "configured", window: 4096, want: "4096"},
		{name: "default", window: 256000, want: "256000"},
		{name: "unset", want: "unset"},
		{name: "explicit-split", window: 4096, args: []string{"--context-window", "8192"}, want: "8192"},
		{name: "explicit-equals", window: 4096, args: []string{"--context-window=16384"}, want: "16384"},
		{name: "explicit-invalid", window: 4096, args: []string{"--context-window=invalid"}, want: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			listener.Close()
			cfg := config.Default(t.TempDir())
			cfg.ContextWindow = tc.window
			cfg.Sandbox.Enabled = false
			cfg.OrchestratorAddr = address
			cfg.OrchestratorCommand = os.Args[0]
			backing := append([]string{"-test.run=^TestCLIContextWindowProcessHelper$", "--"}, tc.args...)
			backing = append(backing, "caller-owned-tail", "must-survive")
			original := strings.Join(backing, "\n")
			cfg.OrchestratorArgs = backing[:len(backing)-2]
			app := cli.NewApp(cfg, strings.NewReader("/exit\n"), io.Discard, io.Discard)
			defer func() {
				if err := app.Run(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			if strings.Join(backing, "\n") != original {
				t.Fatal("CLI startup mutated the caller's argument storage")
			}
			client, err := orchestrator.NewClient(address)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			health, err := client.Health(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if health.Version != tc.want {
				t.Fatalf("managed child context window = %q, want %s", health.Version, tc.want)
			}
		})
	}
}

func TestCLIContextWindowProcessHelper(t *testing.T) {
	if os.Getenv("CODE_AGENT_CONTEXT_WINDOW_PROCESS_HELPER") != "1" {
		t.Skip("subprocess fixture")
	}
	host, port, window := "", "", "unset"
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if strings.HasPrefix(arg, "--context-window=") {
			window = strings.TrimPrefix(arg, "--context-window=")
		}
		if i+1 < len(os.Args) {
			switch arg {
			case "--host":
				host = os.Args[i+1]
			case "--port":
				port = os.Args[i+1]
			case "--context-window":
				window = os.Args[i+1]
			}
		}
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterOrchestratorServer(server, contextWindowProcess{window: window})
	if err := server.Serve(listener); err != nil {
		t.Fatal(err)
	}
}
