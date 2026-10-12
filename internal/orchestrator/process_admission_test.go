package orchestrator_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"
	"google.golang.org/grpc"
)

type admissionProcess struct {
	pb.UnimplementedOrchestratorServer
}

func (admissionProcess) Health(context.Context, *pb.Empty) (*pb.HealthResponse, error) {
	version := "admitted"
	if os.Getenv("CODE_AGENT_MODEL_ADMISSION") != "required" {
		version = "admission_bypassed"
	}
	for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CODE_AGENT_PROVIDER_CONFIG"} {
		if os.Getenv(name) != "" {
			version = "credential_exposed"
		}
	}
	return &pb.HealthResponse{Status: "ok", Version: version}, nil
}

func TestHarnessProcessAlwaysRequiresAdmissionWithoutProviderSecrets(t *testing.T) {
	t.Setenv("CODE_AGENT_MODEL_ADMISSION", "disabled")
	for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CODE_AGENT_PROVIDER_CONFIG"} {
		t.Setenv(name, "fixture-must-stay-in-go")
	}
	t.Setenv("CODE_AGENT_ADMISSION_PROCESS_HELPER", "1")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	root := t.TempDir()
	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address: address, AutoStart: true, Command: os.Args[0], Args: []string{"-test.run=^TestAdmissionProcessHelper$", "--"},
		ProjectRoot: root, WorkingDir: root, MemoryDir: filepath.Join(root, "memory"), RequireHarnessWorktree: true,
		StartupTimeout: 5 * time.Second,
	})
	defer manager.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := manager.Client(ctx)
	if err != nil {
		t.Fatal(err)
	}
	health, err := client.Health(ctx)
	if err != nil || health.Version != "admitted" {
		t.Fatalf("Go-owned child did not enforce admission/credential boundary: %v %v", health, err)
	}
}

// The real subprocess exposes only its safe verdict through external gRPC.
func TestAdmissionProcessHelper(t *testing.T) {
	if os.Getenv("CODE_AGENT_ADMISSION_PROCESS_HELPER") != "1" {
		t.Skip("subprocess fixture")
	}
	var host, port string
	for i := 1; i+1 < len(os.Args); i++ {
		switch os.Args[i] {
		case "--host":
			host = os.Args[i+1]
		case "--port":
			port = os.Args[i+1]
		}
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterOrchestratorServer(server, admissionProcess{})
	if err := server.Serve(listener); err != nil {
		t.Fatal(err)
	}
}
