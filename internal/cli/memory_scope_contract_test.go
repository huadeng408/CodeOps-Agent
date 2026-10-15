package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/memory"
	"code-agent/internal/session"
	"google.golang.org/grpc"
)

// Only the orchestrator transport is a fixture; App, approvals and Ledger are real.
type memoryScopeTransport struct {
	pb.UnimplementedOrchestratorServer
	context    chan string
	result     chan *pb.ToolResult
	reflection chan time.Duration
}

func (fixture memoryScopeTransport) ReflectMemory(ctx context.Context, request *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
	deadline, _ := ctx.Deadline()
	fixture.reflection <- time.Until(deadline)
	return &pb.MemoryReflectionResponse{SourceChecksum: request.SourceChecksum}, nil
}

func (memoryScopeTransport) Health(context.Context, *pb.Empty) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok"}, nil
}

func (fixture memoryScopeTransport) Converse(stream grpc.BidiStreamingServer[pb.HarnessMessage, pb.OrchestratorMessage]) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	fixture.context <- message.GetUserInput().GetMemoryContextJson()
	if err := stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_ToolRequest{ToolRequest: &pb.ToolRequest{ToolCallId: "recall", ToolName: "RecallMemory", ParametersJson: `{"query":"CLI repository memory","max_tokens":8000}`}}}); err != nil {
		return err
	}
	message, err = stream.Recv()
	if err != nil {
		return err
	}
	fixture.result <- message.GetToolResult()
	return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: true, Message: "Completed source verification"}}})
}

func TestCLIConversationMemoryUsesCurrentRepository(t *testing.T) {
	t.Setenv("CODE_AGENT_MODEL_ADMISSION", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	t.Setenv("CODE_AGENT_MODEL_REQUEST_TIMEOUT_SECONDS", "180")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg := config.Default(t.TempDir())
	cfg.OrchestratorAutoStart, cfg.RAGEnabled, cfg.Sandbox.Enabled = false, false, false
	cfg.Permissions.Allow = []config.PermissionRule{{Tool: "RecallMemory"}}
	store := session.NewSQLiteEventStore(cfg.SessionDBPath)
	manager := session.NewManager(store)
	ledger, err := store.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	module := memory.NewLedgerMemory(ledger)
	for _, source := range []struct{ directory, text string }{{cfg.WorkingDir, "CLI repository memory OWN_REPOSITORY_ANCHOR"}, {t.TempDir(), "CLI repository memory OTHER_REPOSITORY_SENTINEL"}} {
		created := manager.NewSession(source.directory)
		actor, err := identity.Default().BindSession(created.ID)
		if err != nil {
			t.Fatal(err)
		}
		manager.SetActor(actor)
		manager.Append(session.RoleUser, source.text)
		if err := module.CommitCLIOutcome(ctx, created.ID, "Completed "+source.text); err != nil {
			t.Fatal(err)
		}
		if _, err := module.Manage(ctx, created.ID, session.MemoryCommand{Action: "remember", Kind: "patterns", Key: "repository-memory", Content: source.text}); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	transport := memoryScopeTransport{context: make(chan string, 1), result: make(chan *pb.ToolResult, 1), reflection: make(chan time.Duration, 10)}
	server := grpc.NewServer()
	pb.RegisterOrchestratorServer(server, transport)
	go server.Serve(listener)
	defer server.Stop()
	cfg.OrchestratorAddr = listener.Addr().String()
	if err := cli.NewApp(cfg, strings.NewReader("CLI repository memory\n"), io.Discard, io.Discard).Run(ctx); err != nil {
		t.Fatal(err)
	}
	var tool *pb.ToolResult
	select {
	case tool = <-transport.result:
	default:
		t.Fatal("CLI did not execute its Memory tool")
	}
	if tool == nil || tool.Error != "" || tool.ExitCode != 0 {
		t.Fatalf("CLI Memory tool failed: %+v", tool)
	}
	for name, text := range map[string]string{"automatic context": <-transport.context, "RecallMemory": tool.Output} {
		var envelope map[string]any
		if json.Unmarshal([]byte(text), &envelope) != nil || !strings.Contains(text, "OWN_REPOSITORY_ANCHOR") || strings.Contains(text, "OTHER_REPOSITORY_SENTINEL") {
			t.Fatalf("%s crossed the CLI repository boundary: %s", name, text)
		}
	}
	select {
	case remaining := <-transport.reflection:
		if remaining < 179*time.Second || remaining > 180*time.Second {
			t.Fatalf("CLI reflection ignored explicit Go request profile: %s", remaining)
		}
	default:
		t.Fatal("CLI reflection did not reach the owned transport")
	}
}
