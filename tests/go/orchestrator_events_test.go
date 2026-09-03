package codeagent_test

import (
	"context"
	"errors"
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

type compactionEventServer struct {
	codeagentpb.UnimplementedOrchestratorServer
}

func (s *compactionEventServer) Health(context.Context, *codeagentpb.Empty) (*codeagentpb.HealthResponse, error) {
	return &codeagentpb.HealthResponse{Status: "ok", Version: "test"}, nil
}

func (s *compactionEventServer) Compact(context.Context, *codeagentpb.CompactRequest) (*codeagentpb.CompactionUpdate, error) {
	return &codeagentpb.CompactionUpdate{
		Summary:            "manual checkpoint",
		RemovedMessages:    6,
		KeepRecentMessages: 2,
		Trigger:            "manual",
	}, nil
}

func (s *compactionEventServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_CompactionUpdate{
			CompactionUpdate: &codeagentpb.CompactionUpdate{
				Summary:               "checkpoint summary",
				KeepRecentMessages:    3,
				RemovedMessages:       5,
				EstimatedBeforeTokens: 100,
				EstimatedAfterTokens:  40,
				Trigger:               "pressure",
			},
		},
	}); err != nil {
		return err
	}
	return stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_Done{Done: &codeagentpb.Done{Success: true}},
	})
}

func TestClientReceivesCompactionUpdates(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &compactionEventServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var update *codeagentpb.CompactionUpdate
	client.OnCompaction = func(value *codeagentpb.CompactionUpdate) error {
		update = value
		return nil
	}
	if _, err := client.Converse(context.Background(), "compact"); err != nil {
		t.Fatalf("converse with compaction update failed: %v", err)
	}
	if update == nil || update.GetSummary() != "checkpoint summary" || update.GetKeepRecentMessages() != 3 {
		t.Fatalf("unexpected compaction update: %+v", update)
	}
}

func TestClientPropagatesCompactionPersistenceFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &compactionEventServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	client.OnCompaction = func(*codeagentpb.CompactionUpdate) error {
		return errors.New("session persistence failed")
	}
	if _, err := client.Converse(context.Background(), "compact"); err == nil || !strings.Contains(err.Error(), "session persistence failed") {
		t.Fatalf("expected compaction persistence error, got %v", err)
	}
}

func TestClientManualCompactionInvokesDurableCallback(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &compactionEventServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var callbackUpdate *codeagentpb.CompactionUpdate
	client.OnCompaction = func(update *codeagentpb.CompactionUpdate) error {
		callbackUpdate = update
		return nil
	}
	update, err := client.Compact(context.Background(), "manual-session", []orchestrator.ConversationMessage{{
		Role:    "user",
		Content: "old context",
	}})
	if err != nil {
		t.Fatalf("manual compact failed: %v", err)
	}
	if update == nil || update.GetTrigger() != "manual" || update.GetRemovedMessages() != 6 {
		t.Fatalf("unexpected manual update: %+v", update)
	}
	if callbackUpdate == nil || callbackUpdate.GetSummary() != "manual checkpoint" {
		t.Fatalf("durable callback was not invoked: %+v", callbackUpdate)
	}
}

func TestClientEmitsToolProgressEvents(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &batchOrchestratorServer{})
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var progress []orchestrator.ToolProgress
	reply, err := client.ConverseWithEvents(context.Background(), "batch", func(ctx context.Context, event orchestrator.Event) {
		_ = ctx
		if event.ToolProgress != nil {
			progress = append(progress, *event.ToolProgress)
		}
	}, func(ctx context.Context, call orchestrator.ToolCall) orchestrator.ToolResult {
		_ = ctx
		switch call.Name {
		case "Read":
			return orchestrator.ToolResult{ToolName: call.Name, ToolCallID: call.ID, Output: "read-ok"}
		case "Glob":
			return orchestrator.ToolResult{ToolName: call.Name, ToolCallID: call.ID, Output: "glob-ok"}
		default:
			return orchestrator.ToolResult{ToolName: call.Name, ToolCallID: call.ID, Error: "unexpected tool", ExitCode: 1}
		}
	})
	if err != nil {
		t.Fatalf("converse with events failed: %v", err)
	}
	if reply != "batch complete" {
		t.Fatalf("progress leaked into reply: %q", reply)
	}
	if len(progress) != 4 {
		t.Fatalf("expected 4 progress events, got %#v", progress)
	}
	if progress[0].Phase != "start" || progress[0].ToolName != "Read" || progress[0].Index != 1 || progress[0].Total != 2 {
		t.Fatalf("unexpected first progress event: %+v", progress[0])
	}
	if progress[3].Phase != "finish" || progress[3].ToolName != "Glob" || progress[3].Index != 2 || progress[3].Total != 2 {
		t.Fatalf("unexpected final progress event: %+v", progress[3])
	}
}
