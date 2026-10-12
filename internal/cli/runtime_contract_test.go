package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
	"google.golang.org/grpc"
)

// Only the external orchestrator transport is a fixture; CLI and Ledger are real.
type hookTransport struct {
	codeagentpb.UnimplementedOrchestratorServer
	IDs []string
}

func (hookTransport) Health(context.Context, *codeagentpb.Empty) (*codeagentpb.HealthResponse, error) {
	return &codeagentpb.HealthResponse{Status: "ok"}, nil
}

func (fixture hookTransport) Converse(stream grpc.BidiStreamingServer[codeagentpb.HarnessMessage, codeagentpb.OrchestratorMessage]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	ids := fixture.IDs
	if ids == nil {
		ids = []string{"blocked-hook", "blocked-hook"}
	}
	for _, id := range ids {
		if err := stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_ToolRequest{ToolRequest: &codeagentpb.ToolRequest{ToolCallId: id, ToolName: "Write", ParametersJson: `{"path":"tool-marker.txt","content":"must not execute"}`}}}); err != nil {
			return err
		}
		result, err := stream.Recv()
		if err != nil {
			return err
		}
		if result.GetToolResult() == nil || result.GetToolResult().Error == "" {
			return stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_Done{Done: &codeagentpb.Done{Message: "unexpected tool success"}}})
		}
	}
	return stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_Done{Done: &codeagentpb.Done{Message: "expected boundary refusal"}}})
}

func TestCLIRejectsMissingInvocationIdentityBeforeEffects(t *testing.T) {
	t.Setenv("CODE_AGENT_MODEL_ADMISSION", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, hookTransport{IDs: []string{"", " \t "}})
	go server.Serve(listener)
	defer server.Stop()
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.WorkingDir = root
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.RAGEnabled = listener.Addr().String(), false, false
	cfg.Sandbox = config.SandboxConfig{Backend: sandbox.BackendOpenSandbox, TrustRoot: root}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	app := cli.NewApp(cfg, strings.NewReader("verify identity\ny\ny\n"), io.Discard, io.Discard)
	if err := app.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tool-marker.txt")); !os.IsNotExist(err) {
		t.Fatalf("missing identity bypassed durable intent before Write: %v", err)
	}
	manager := session.NewManager(session.NewSQLiteEventStore(cfg.SessionDBPath))
	defer manager.Close()
	sessions, err := manager.List(context.Background())
	if err != nil || len(sessions) != 1 || len(sessions[0].Invocations) != 0 {
		t.Fatalf("missing identity fabricated an invocation: %+v %v", sessions, err)
	}
}

func TestCLIRefusesUnapprovedHookAndPersistsItsInvocation(t *testing.T) {
	t.Setenv("CODE_AGENT_MODEL_ADMISSION", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, hookTransport{})
	go server.Serve(listener)
	defer server.Stop()
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart = listener.Addr().String(), false
	cfg.RAGEnabled = false
	cfg.Sandbox = config.SandboxConfig{Backend: sandbox.BackendOpenSandbox, TrustRoot: root}
	cfg.Hooks = []config.HookConfig{{Type: "pre_tool", Matcher: "Write", Command: "echo escaped > hook-marker.txt"}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	app := cli.NewApp(cfg, strings.NewReader("verify boundary\ny\n"), io.Discard, io.Discard)
	if err := app.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hook-marker.txt", "tool-marker.txt"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("selected boundary was bypassed by %s: %v", name, err)
		}
	}
	manager := session.NewManager(session.NewSQLiteEventStore(cfg.SessionDBPath))
	defer manager.Close()
	sessions, err := manager.List(context.Background())
	if err != nil || len(sessions) != 1 || len(sessions[0].Invocations) != 1 {
		t.Fatalf("CLI lost or duplicated its blocked invocation: %+v %v", sessions, err)
	}
	invocation := sessions[0].Invocations[0]
	encoded, _ := json.Marshal(invocation)
	var identity struct {
		OperationID string `json:"operation_id"`
	}
	if json.Unmarshal(encoded, &identity) != nil || invocation.ID != "blocked-hook" || len(identity.OperationID) != 64 || invocation.Status != session.InvocationCommitted || invocation.Error == "" || invocation.ExitCode == 0 {
		t.Fatalf("CLI did not retain the selected boundary refusal: %+v", invocation)
	}
}
