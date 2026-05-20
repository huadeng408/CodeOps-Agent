package codeagent_test

import (
	"context"
	"net"
	"strings"
	"testing"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"

	"google.golang.org/grpc"
)

type todoEventServer struct {
	codeagentpb.UnimplementedOrchestratorServer
}

func (s *todoEventServer) Health(context.Context, *codeagentpb.Empty) (*codeagentpb.HealthResponse, error) {
	return &codeagentpb.HealthResponse{Status: "ok", Version: "test"}, nil
}

func (s *todoEventServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_TodoUpdate{
			TodoUpdate: &codeagentpb.TodoUpdate{
				Todos: []*codeagentpb.TodoItem{
					{Content: "Draft plan", ActiveForm: "drafting plan", Status: "in_progress"},
				},
			},
		},
	}); err != nil {
		return err
	}
	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_Text{
			Text: &codeagentpb.TextChunk{Text: "task list updated"},
		},
	}); err != nil {
		return err
	}
	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_SessionMeta{
			SessionMeta: &codeagentpb.SessionMeta{
				Turn:      1,
				TokensIn:  100,
				TokensOut: 50,
				Cost:      0.00075,
				Model:     "gpt-4o",
			},
		},
	}); err != nil {
		return err
	}
	return stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_Done{
			Done: &codeagentpb.Done{Success: true},
		},
	})
}

func TestClientReceivesTodoUpdates(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &todoEventServer{})
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var updates []string
	var meta *codeagentpb.SessionMeta
	reply, err := client.ConverseWithEvents(context.Background(), "track tasks", func(ctx context.Context, event orchestrator.Event) {
		_ = ctx
		if event.SessionMeta != nil {
			meta = event.SessionMeta
		}
		if event.TodoUpdate == nil {
			return
		}
		for _, item := range event.TodoUpdate.Todos {
			if item != nil {
				updates = append(updates, item.Content+":"+item.Status)
			}
		}
	}, func(context.Context, orchestrator.ToolCall) orchestrator.ToolResult {
		return orchestrator.ToolResult{ToolName: "noop", Error: "unexpected tool request", ExitCode: 1}
	})
	if err != nil {
		t.Fatalf("converse with events failed: %v", err)
	}
	if !strings.Contains(reply, "task list updated") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(updates) != 1 || updates[0] != "Draft plan:in_progress" {
		t.Fatalf("unexpected todo updates: %#v", updates)
	}
	if meta == nil || meta.GetTokensIn() != 100 || meta.GetTokensOut() != 50 || meta.GetModel() != "gpt-4o" {
		t.Fatalf("unexpected session meta: %+v", meta)
	}
}
