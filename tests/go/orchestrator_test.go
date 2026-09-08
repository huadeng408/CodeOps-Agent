package codeagent_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"
	"code-agent/internal/telemetry/genai"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type toolSpanContextKey struct{}

type contextRecordingTracer struct{}

type contextRecordingSpan struct{}

func (contextRecordingTracer) StartSpan(ctx context.Context, name, _ string, _ string) (context.Context, genai.Span) {
	return context.WithValue(ctx, toolSpanContextKey{}, name), contextRecordingSpan{}
}

func (contextRecordingTracer) Shutdown(context.Context) error    { return nil }
func (contextRecordingSpan) End()                                {}
func (contextRecordingSpan) SetAttributes(...attribute.KeyValue) {}
func (contextRecordingSpan) RecordError(error)                   {}
func (contextRecordingSpan) AddEvent(string)                     {}

type testOrchestratorServer struct {
	codeagentpb.UnimplementedOrchestratorServer
	toolResult *codeagentpb.ToolResult
}

func (s *testOrchestratorServer) Health(context.Context, *codeagentpb.Empty) (*codeagentpb.HealthResponse, error) {
	return &codeagentpb.HealthResponse{Status: "ok", Version: "test"}, nil
}

func (s *testOrchestratorServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	var input string
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	if payload := msg.GetUserInput(); payload != nil {
		input = payload.Text
	}
	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_ToolRequest{
			ToolRequest: &codeagentpb.ToolRequest{
				ToolName:           "Echo",
				ParametersJson:     `{"value":"x"}`,
				RequiredPermission: codeagentpb.PermissionLevel_AUTO_ALLOW,
			},
		},
	}); err != nil {
		return err
	}

	toolMessage, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			return err
		}
		return err
	}
	toolOutput := ""
	if payload := toolMessage.GetToolResult(); payload != nil {
		s.toolResult = payload
		toolOutput = payload.Output
		if len(payload.ContentBlocks) == 1 {
			block := payload.ContentBlocks[0]
			toolOutput += fmt.Sprintf(" %s:%s", block.Mime, block.ImageBlob)
		}
	}

	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_Text{
			Text: &codeagentpb.TextChunk{Text: "hello " + input + " " + toolOutput},
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

type historyOrchestratorServer struct {
	codeagentpb.UnimplementedOrchestratorServer
	sessionID string
	history   []*codeagentpb.ConversationMessage
}

type failedOrchestratorServer struct {
	codeagentpb.UnimplementedOrchestratorServer
}

type incompleteOrchestratorServer struct {
	codeagentpb.UnimplementedOrchestratorServer
}

func (s *incompleteOrchestratorServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	return stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_Text{Text: &codeagentpb.TextChunk{Text: "partial"}},
	})
}

func (s *failedOrchestratorServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	return stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_Done{
			Done: &codeagentpb.Done{Success: false, Message: "checkpoint persistence unavailable"},
		},
	})
}

type requestScopedObservation struct {
	actorID       string
	sessionID     string
	runID         string
	resume        string
	surfaceSHA256 string
	history       string
	stateRevision uint64
}

type requestScopedOrchestratorServer struct {
	codeagentpb.UnimplementedOrchestratorServer
	mu           sync.Mutex
	observations map[string]requestScopedObservation
}

func (s *requestScopedOrchestratorServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	input := message.GetUserInput()
	if input == nil {
		return errors.New("missing user input")
	}
	tag := input.GetText()
	observation := requestScopedObservation{
		actorID:   input.GetActor().GetActorId(),
		sessionID: input.GetSessionId(),
	}
	if incoming, ok := metadata.FromIncomingContext(stream.Context()); ok {
		observation.runID = firstMetadataValue(incoming.Get("x-code-agent-run-id"))
		observation.resume = firstMetadataValue(incoming.Get("x-code-agent-resume"))
		observation.surfaceSHA256 = firstMetadataValue(incoming.Get("x-code-agent-surface-sha256"))
	}
	if len(input.GetHistory()) > 0 {
		observation.history = input.GetHistory()[0].GetContent()
	}
	if input.GetPlanTodoState() != nil {
		observation.stateRevision = input.GetPlanTodoState().GetRevision()
	}
	s.mu.Lock()
	if s.observations == nil {
		s.observations = make(map[string]requestScopedObservation)
	}
	s.observations[tag] = observation
	s.mu.Unlock()

	for _, outgoing := range []*codeagentpb.OrchestratorMessage{
		{Payload: &codeagentpb.OrchestratorMessage_Text{Text: &codeagentpb.TextChunk{Text: "text-" + tag}}},
		{Payload: &codeagentpb.OrchestratorMessage_TodoUpdate{TodoUpdate: &codeagentpb.TodoUpdate{
			Revision: 2, Todos: []*codeagentpb.TodoItem{{Content: tag, Status: "pending"}},
		}}},
		{Payload: &codeagentpb.OrchestratorMessage_PlanUpdate{PlanUpdate: &codeagentpb.PlanUpdate{
			Revision: 3, Steps: []string{tag}, Mode: "chat",
		}}},
		{Payload: &codeagentpb.OrchestratorMessage_CompactionUpdate{CompactionUpdate: &codeagentpb.CompactionUpdate{Summary: tag}}},
		{Payload: &codeagentpb.OrchestratorMessage_AgentSpawn{AgentSpawn: &codeagentpb.AgentSpawn{
			RequestId: "spawn-" + tag, ParentSessionId: input.GetSessionId(), ChildSessionId: input.GetSessionId() + ":child",
		}}},
	} {
		if err := stream.Send(outgoing); err != nil {
			return err
		}
	}
	decision, err := stream.Recv()
	if err != nil {
		return err
	}
	if got := decision.GetAgentSpawnDecision(); got == nil || !got.GetAccepted() || got.GetRequestId() != "spawn-"+tag {
		return fmt.Errorf("invalid spawn decision for %s: %#v", tag, got)
	}

	for _, outgoing := range []*codeagentpb.OrchestratorMessage{
		{Payload: &codeagentpb.OrchestratorMessage_AgentLifecycle{AgentLifecycle: &codeagentpb.AgentLifecycle{
			RequestId: "spawn-" + tag, ChildSessionId: input.GetSessionId() + ":child", Status: "completed", Reason: tag,
		}}},
		{Payload: &codeagentpb.OrchestratorMessage_AskUserRequest{AskUserRequest: &codeagentpb.AskUserRequest{
			AskUserId: "ask-" + tag, Question: tag,
		}}},
	} {
		if err := stream.Send(outgoing); err != nil {
			return err
		}
	}
	askResult, err := stream.Recv()
	if err != nil {
		return err
	}
	if got := askResult.GetToolResult(); got == nil || got.GetOutput() != "ask-"+tag {
		return fmt.Errorf("invalid ask result for %s: %#v", tag, got)
	}

	if err := stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_ToolRequest{
		ToolRequest: &codeagentpb.ToolRequest{ToolCallId: "tool-" + tag, ToolName: "Echo", ParametersJson: tag},
	}}); err != nil {
		return err
	}
	toolResult, err := stream.Recv()
	if err != nil {
		return err
	}
	if got := toolResult.GetToolResult(); got == nil || got.GetOutput() != "tool-"+tag {
		return fmt.Errorf("invalid tool result for %s: %#v", tag, got)
	}
	return stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_Done{
		Done: &codeagentpb.Done{Success: true, Message: "done-" + tag},
	}})
}

func firstMetadataValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

type subAgentMetadataServer struct {
	codeagentpb.UnimplementedOrchestratorServer
}

func (s *subAgentMetadataServer) Health(context.Context, *codeagentpb.Empty) (*codeagentpb.HealthResponse, error) {
	return &codeagentpb.HealthResponse{Status: "ok", Version: "test"}, nil
}

func (s *subAgentMetadataServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_AgentSpawn{
			AgentSpawn: &codeagentpb.AgentSpawn{
				Kind:            "review",
				Task:            "Review files",
				ProtocolVersion: "agent.v1",
				RequestId:       "spawn-1",
				ParentSessionId: "parent",
				ChildSessionId:  "parent:subagent:spawn-1",
				WorktreeName:    "agent-spawn-1",
				Isolation:       "worktree",
			},
		},
	}); err != nil {
		return err
	}
	return stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_Done{Done: &codeagentpb.Done{Success: true}},
	})
}

func TestOrchestratorClientPreservesSubAgentProtocolMetadata(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &subAgentMetadataServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var received *codeagentpb.AgentSpawn
	_, err = client.ConverseWithEvents(context.Background(), "spawn", func(_ context.Context, event orchestrator.Event) {
		received = event.AgentSpawn
	})
	if err != nil {
		t.Fatalf("converse failed: %v", err)
	}
	if received == nil || received.GetProtocolVersion() != "agent.v1" || received.GetRequestId() != "spawn-1" || received.GetParentSessionId() != "parent" || received.GetChildSessionId() != "parent:subagent:spawn-1" {
		t.Fatalf("sub-agent metadata lost at Go boundary: %#v", received)
	}
}

func TestOrchestratorClientPreparesAgentSpawnBeforeRendering(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &subAgentMetadataServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	prepared := false
	client.OnAgentSpawn = func(_ context.Context, spawn *codeagentpb.AgentSpawn) error {
		if spawn.GetWorktreeName() == "" || spawn.GetIsolation() != "worktree" {
			t.Fatalf("spawn isolation metadata missing: %#v", spawn)
		}
		prepared = true
		return nil
	}
	if _, err := client.ConverseWithEvents(context.Background(), "spawn", nil); err != nil {
		t.Fatalf("converse failed: %v", err)
	}
	if !prepared {
		t.Fatal("agent spawn callback was not invoked")
	}
}

type lifecycleMetadataServer struct {
	codeagentpb.UnimplementedOrchestratorServer
}

func (s *lifecycleMetadataServer) Health(context.Context, *codeagentpb.Empty) (*codeagentpb.HealthResponse, error) {
	return &codeagentpb.HealthResponse{Status: "ok", Version: "test"}, nil
}

func (s *lifecycleMetadataServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_AgentLifecycle{
		AgentLifecycle: &codeagentpb.AgentLifecycle{
			RequestId: "request-1", ChildSessionId: "child-1", LeaseId: "lease-1", Status: "completed", Reason: "child finished",
		},
	}}); err != nil {
		return err
	}
	return stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_Done{Done: &codeagentpb.Done{Success: true}}})
}

func TestOrchestratorClientForwardsAgentLifecycle(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &lifecycleMetadataServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	called := false
	client.OnAgentLifecycle = func(_ context.Context, lifecycle *codeagentpb.AgentLifecycle) error {
		called = lifecycle.GetStatus() == "completed" && lifecycle.GetLeaseId() == "lease-1"
		return nil
	}
	if _, err := client.ConverseWithEvents(context.Background(), "lifecycle", nil); err != nil {
		t.Fatalf("converse failed: %v", err)
	}
	if !called {
		t.Fatal("agent lifecycle callback was not invoked")
	}
}

func (s *historyOrchestratorServer) Health(context.Context, *codeagentpb.Empty) (*codeagentpb.HealthResponse, error) {
	return &codeagentpb.HealthResponse{Status: "ok", Version: "test"}, nil
}

func (s *historyOrchestratorServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	if input := msg.GetUserInput(); input != nil {
		s.sessionID = input.GetSessionId()
		s.history = input.GetHistory()
	}
	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_Text{
			Text: &codeagentpb.TextChunk{Text: "history received"},
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

type batchOrchestratorServer struct {
	codeagentpb.UnimplementedOrchestratorServer
}

func (s *batchOrchestratorServer) Health(context.Context, *codeagentpb.Empty) (*codeagentpb.HealthResponse, error) {
	return &codeagentpb.HealthResponse{Status: "ok", Version: "test"}, nil
}

func (s *batchOrchestratorServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_ToolRequestBatch{
			ToolRequestBatch: &codeagentpb.ToolRequestBatch{
				Parallel: true,
				Requests: []*codeagentpb.ToolRequest{
					{
						ToolName:           "Read",
						ParametersJson:     `{"path":"a.txt"}`,
						RequiredPermission: codeagentpb.PermissionLevel_AUTO_ALLOW,
						ToolCallId:         "read-1",
					},
					{
						ToolName:           "Glob",
						ParametersJson:     `{"pattern":"*.go"}`,
						RequiredPermission: codeagentpb.PermissionLevel_AUTO_ALLOW,
						ToolCallId:         "glob-1",
					},
				},
			},
		},
	}); err != nil {
		return err
	}

	results := map[string]string{}
	for len(results) < 2 {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		if payload := msg.GetToolResult(); payload != nil {
			results[payload.ToolCallId] = payload.Output
		}
	}

	if results["read-1"] != "read-ok" || results["glob-1"] != "glob-ok" {
		return fmt.Errorf("unexpected batch results: %#v", results)
	}

	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_Text{
			Text: &codeagentpb.TextChunk{Text: "batch complete"},
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

type askOrchestratorServer struct {
	codeagentpb.UnimplementedOrchestratorServer
}

func (s *askOrchestratorServer) Health(context.Context, *codeagentpb.Empty) (*codeagentpb.HealthResponse, error) {
	return &codeagentpb.HealthResponse{Status: "ok", Version: "test"}, nil
}

func (s *askOrchestratorServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_AskUserRequest{
			AskUserRequest: &codeagentpb.AskUserRequest{
				Question: "Choose an option",
				Options: []*codeagentpb.Option{
					{Label: "alpha"},
					{Label: "beta"},
				},
				AskUserId: "ask-1",
			},
		},
	}); err != nil {
		return err
	}

	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	result := msg.GetToolResult()
	if result == nil || result.ToolCallId != "ask-1" || result.Output != "beta" {
		return fmt.Errorf("unexpected ask user result: %#v", result)
	}

	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_Text{
			Text: &codeagentpb.TextChunk{Text: "ask answered"},
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

type cancelAskOrchestratorServer struct {
	codeagentpb.UnimplementedOrchestratorServer
	result *codeagentpb.ToolResult
}

func (s *cancelAskOrchestratorServer) Health(context.Context, *codeagentpb.Empty) (*codeagentpb.HealthResponse, error) {
	return &codeagentpb.HealthResponse{Status: "ok", Version: "test"}, nil
}

func (s *cancelAskOrchestratorServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_AskUserRequest{
			AskUserRequest: &codeagentpb.AskUserRequest{
				Question:  "Choose an option",
				AskUserId: "ask-cancel",
			},
		},
	}); err != nil {
		return err
	}

	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	s.result = msg.GetToolResult()

	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_Text{
			Text: &codeagentpb.TextChunk{Text: "ask fallback handled"},
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

func TestOrchestratorClientHealthAndConverse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	capturing := &testOrchestratorServer{}
	codeagentpb.RegisterOrchestratorServer(server, capturing)
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("health failed: %v", err)
	}
	if health.Status != "ok" {
		t.Fatalf("unexpected health: %+v", health)
	}

	reply, err := client.Converse(context.Background(), "agent", func(context.Context, orchestrator.ToolCall) orchestrator.ToolResult {
		return orchestrator.ToolResult{
			ToolName: "Echo",
			Output:   "tool-ok",
			Spill:    &orchestrator.SpillRef{Locator: "spill://" + strings.Repeat("a", 64), SHA256: strings.Repeat("a", 64), Bytes: 128},
			ContentBlocks: []orchestrator.ContentBlock{{
				ImageBlob: []byte("png"),
				MIME:      "image/png",
			}},
		}
	})
	if err != nil {
		t.Fatalf("converse failed: %v", err)
	}
	if !strings.Contains(reply, "hello agent tool-ok image/png:png") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if capturing.toolResult == nil || capturing.toolResult.SpillLocator != "spill://"+strings.Repeat("a", 64) || capturing.toolResult.SpillBytes != 128 {
		t.Fatalf("spill metadata was not sent over gRPC: %#v", capturing.toolResult)
	}
}

func TestOrchestratorClientPassesToolSpanContextToHandler(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &testOrchestratorServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetTracer(contextRecordingTracer{})

	var observed string
	if _, err := client.Converse(context.Background(), "trace", func(ctx context.Context, call orchestrator.ToolCall) orchestrator.ToolResult {
		observed, _ = ctx.Value(toolSpanContextKey{}).(string)
		return orchestrator.ToolResult{ToolName: call.Name, Output: "ok"}
	}); err != nil {
		t.Fatalf("converse failed: %v", err)
	}
	if observed != "execute_tool Echo" {
		t.Fatalf("tool handler did not receive execute_tool context: %q", observed)
	}
}

func TestOrchestratorClientSendsSessionHistory(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	capturing := &historyOrchestratorServer{}
	codeagentpb.RegisterOrchestratorServer(server, capturing)
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	reply, err := client.ConverseWithHistory(context.Background(), "next", "session-1", []orchestrator.ConversationMessage{
		{Role: "user", Content: "previous request", CreatedAt: "2026-06-02T00:00:00Z"},
		{Role: "assistant", Content: "previous answer", CreatedAt: "2026-06-02T00:00:01Z"},
	})
	if err != nil {
		t.Fatalf("converse with history failed: %v", err)
	}
	if reply != "history received" {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if capturing.sessionID != "session-1" || len(capturing.history) != 2 {
		t.Fatalf("history not sent: session=%q history=%#v", capturing.sessionID, capturing.history)
	}
	if capturing.history[0].Role != "user" || capturing.history[0].Content != "previous request" {
		t.Fatalf("unexpected history payload: %#v", capturing.history)
	}
}

func TestOrchestratorClientPreservesEmptyAssistantToolCallAndResult(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	capturing := &historyOrchestratorServer{}
	codeagentpb.RegisterOrchestratorServer(server, capturing)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	history := make([]orchestrator.ConversationMessage, 0, 42)
	for index := 0; index < 39; index++ {
		history = append(history, orchestrator.ConversationMessage{Role: "user", Content: fmt.Sprintf("old-%02d", index)})
	}
	history = append(history,
		orchestrator.ConversationMessage{
			Role: "assistant",
			ToolCalls: []orchestrator.ConversationToolCall{{
				ID: "call-1", Name: "Read", ArgumentsJSON: `{"path":"README.md"}`,
			}},
		},
		orchestrator.ConversationMessage{Role: "tool", Content: "contents", Name: "Read", ToolCallID: "call-1"},
		orchestrator.ConversationMessage{Role: "user", Content: "continue after tool"},
	)

	if _, err := client.ConverseWithHistory(context.Background(), "next", "session-tools", history); err != nil {
		t.Fatalf("converse with tool history failed: %v", err)
	}
	if len(capturing.history) != 40 {
		t.Fatalf("trimmed history length = %d, want 40", len(capturing.history))
	}
	var callIndex, resultIndex = -1, -1
	for index, item := range capturing.history {
		if len(item.GetToolCalls()) == 1 && item.GetToolCalls()[0].GetId() == "call-1" {
			callIndex = index
		}
		if item.GetRole() == "tool" && item.GetToolCallId() == "call-1" {
			resultIndex = index
		}
	}
	if callIndex < 0 || resultIndex != callIndex+1 {
		t.Fatalf("tool call/result pair was split or dropped: call=%d result=%d history=%#v", callIndex, resultIndex, capturing.history)
	}
	if capturing.history[callIndex].GetContent() != "" {
		t.Fatalf("empty tool-call assistant content changed to %q", capturing.history[callIndex].GetContent())
	}
}

func TestOrchestratorClientReturnsErrorForUnsuccessfulDone(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &failedOrchestratorServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	result, err := client.RunConversation(context.Background(), orchestrator.ConversationRequest{
		Input: "resume", SessionID: "failed-session", RunID: "failed-run", Resume: true, SurfaceSHA256: "surface-failed",
		Actor: orchestrator.ActorIdentity{
			ActorID: "actor-failed", Subject: "user-failed", TenantID: "tenant-failed", Roles: []string{"USER"},
		},
	}, orchestrator.ConversationHandlers{})
	if !errors.Is(err, orchestrator.ErrConversationFailed) {
		t.Fatalf("converse error = %v, want ErrConversationFailed", err)
	}
	if result.Success || result.Message != "" || !strings.Contains(err.Error(), "checkpoint persistence unavailable") {
		t.Fatalf("failed conversation result=%+v error=%v", result, err)
	}
}

func TestOrchestratorClientRejectsEOFWithoutTerminalDone(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &incompleteOrchestratorServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.RunConversation(context.Background(), orchestrator.ConversationRequest{
		Input: "resume", SessionID: "incomplete-session", RunID: "incomplete-run",
		Resume: true, SurfaceSHA256: "surface-incomplete",
		Actor: orchestrator.ActorIdentity{ActorID: "actor", Subject: "user", TenantID: "tenant", Roles: []string{"USER"}},
	}, orchestrator.ConversationHandlers{})
	if !errors.Is(err, orchestrator.ErrConversationFailed) || result.Success || result.Message != "" {
		t.Fatalf("incomplete stream result=%+v error=%v", result, err)
	}
}

func TestOrchestratorClientConversationRequestIsolatesConcurrentActorsAndHandlers(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	capturing := &requestScopedOrchestratorServer{}
	codeagentpb.RegisterOrchestratorServer(server, capturing)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetActor(orchestrator.ActorIdentity{
		ActorID: "legacy", Subject: "legacy", TenantID: "legacy", Roles: []string{"LEGACY"},
	}); err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		tag   string
		reply string
		err   error
		seen  map[string]bool
	}
	outcomes := make(chan outcome, 2)
	for index, tag := range []string{"alpha", "beta"} {
		index, tag := index, tag
		go func() {
			seen := make(map[string]bool)
			mark := func(key string) { seen[key] = true }
			result, err := client.RunConversation(context.Background(), orchestrator.ConversationRequest{
				Input:         tag,
				SessionID:     "session-" + tag,
				RunID:         "run-" + tag,
				Resume:        true,
				SurfaceSHA256: "surface-" + tag,
				History:       []orchestrator.ConversationMessage{{Role: "user", Content: "history-" + tag}},
				State: &codeagentpb.PlanTodoSnapshot{
					Revision: uint64(index + 10),
				},
				Actor: orchestrator.ActorIdentity{
					ActorID: "actor-" + tag, Subject: "user-" + tag, TenantID: "tenant-" + tag, Roles: []string{"USER"},
				},
			}, orchestrator.ConversationHandlers{
				TextDelta: func(delta string) {
					if delta == "text-"+tag {
						mark("text")
					}
				},
				Compaction: func(update *codeagentpb.CompactionUpdate) error {
					if update.GetSummary() != tag {
						return fmt.Errorf("wrong compaction for %s: %s", tag, update.GetSummary())
					}
					mark("compaction")
					return nil
				},
				PlanTodo: func(plan *codeagentpb.PlanUpdate, todo *codeagentpb.TodoUpdate) error {
					switch {
					case plan != nil && len(plan.GetSteps()) == 1 && plan.GetSteps()[0] == tag:
						mark("plan")
					case todo != nil && len(todo.GetTodos()) == 1 && todo.GetTodos()[0].GetContent() == tag:
						mark("todo")
					default:
						return fmt.Errorf("wrong plan/todo callback for %s", tag)
					}
					return nil
				},
				AgentSpawn: func(_ context.Context, spawn *codeagentpb.AgentSpawn) error {
					if spawn.GetRequestId() != "spawn-"+tag {
						return fmt.Errorf("wrong spawn for %s", tag)
					}
					mark("spawn")
					return nil
				},
				AgentLifecycle: func(_ context.Context, lifecycle *codeagentpb.AgentLifecycle) error {
					if lifecycle.GetReason() != tag {
						return fmt.Errorf("wrong lifecycle for %s", tag)
					}
					mark("lifecycle")
					return nil
				},
				Event: func(_ context.Context, event orchestrator.Event) {
					if event.AskUserRequest != nil && event.AskUserRequest.GetQuestion() == tag {
						mark("event")
					}
				},
				AskUser: func(_ context.Context, request *codeagentpb.AskUserRequest) (orchestrator.ToolResult, error) {
					if request.GetQuestion() != tag {
						return orchestrator.ToolResult{}, fmt.Errorf("wrong ask request for %s", tag)
					}
					mark("ask")
					return orchestrator.ToolResult{Output: "ask-" + tag}, nil
				},
				Tool: func(_ context.Context, call orchestrator.ToolCall) orchestrator.ToolResult {
					if call.ParametersJSON == tag {
						mark("tool")
					}
					return orchestrator.ToolResult{Output: "tool-" + tag}
				},
			})
			if !result.Success && err == nil {
				err = errors.New("conversation result was not successful")
			}
			outcomes <- outcome{tag: tag, reply: result.Message, err: err, seen: seen}
		}()
	}

	for range 2 {
		result := <-outcomes
		if result.err != nil {
			t.Fatalf("request %s failed: %v", result.tag, result.err)
		}
		if result.reply != "text-"+result.tag+"done-"+result.tag {
			t.Fatalf("request %s reply = %q", result.tag, result.reply)
		}
		for _, callback := range []string{"text", "compaction", "plan", "todo", "spawn", "lifecycle", "event", "ask", "tool"} {
			if !result.seen[callback] {
				t.Errorf("request %s missed %s callback", result.tag, callback)
			}
		}
	}

	capturing.mu.Lock()
	defer capturing.mu.Unlock()
	for index, tag := range []string{"alpha", "beta"} {
		got := capturing.observations[tag]
		if got.actorID != "actor-"+tag || got.sessionID != "session-"+tag || got.runID != "run-"+tag || got.resume != "true" || got.surfaceSHA256 != "surface-"+tag || got.history != "history-"+tag || got.stateRevision != uint64(index+10) {
			t.Errorf("request-scoped input crossed sessions for %s: %+v", tag, got)
		}
	}
}

func TestOrchestratorClientTrimsLargeSessionHistory(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	capturing := &historyOrchestratorServer{}
	codeagentpb.RegisterOrchestratorServer(server, capturing)
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	history := make([]orchestrator.ConversationMessage, 0, 60)
	for idx := 0; idx < 59; idx++ {
		history = append(history, orchestrator.ConversationMessage{
			Role:      "user",
			Content:   fmt.Sprintf("old message %02d", idx),
			CreatedAt: "2026-06-02T00:00:00Z",
		})
	}
	history = append(history, orchestrator.ConversationMessage{
		Role:      "assistant",
		Content:   strings.Repeat("x", 5000),
		CreatedAt: "2026-06-02T00:00:01Z",
	})

	if _, err := client.ConverseWithHistory(context.Background(), "next", "session-1", history); err != nil {
		t.Fatalf("converse with history failed: %v", err)
	}
	if len(capturing.history) > 40 {
		t.Fatalf("history was not capped: %d", len(capturing.history))
	}
	if capturing.history[0].Role != "system" || !strings.Contains(capturing.history[0].Content, "History truncated") {
		t.Fatalf("missing truncation summary: %#v", capturing.history[0])
	}
	last := capturing.history[len(capturing.history)-1]
	if last.Role != "assistant" || !strings.Contains(last.Content, "[history message truncated]") {
		t.Fatalf("long recent message was not truncated: role=%q len=%d", last.Role, len(last.Content))
	}
}

func TestOrchestratorClientHandlesToolRequestBatch(t *testing.T) {
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

	var mu sync.Mutex
	started := 0
	release := make(chan struct{})
	reply, err := client.Converse(context.Background(), "batch", func(ctx context.Context, call orchestrator.ToolCall) orchestrator.ToolResult {
		mu.Lock()
		started++
		if started == 2 {
			close(release)
		}
		mu.Unlock()

		select {
		case <-release:
		case <-ctx.Done():
			return orchestrator.ToolResult{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Error:      ctx.Err().Error(),
				ExitCode:   1,
			}
		case <-time.After(2 * time.Second):
			return orchestrator.ToolResult{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Error:      "batch did not run in parallel",
				ExitCode:   1,
			}
		}

		return orchestrator.ToolResult{
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Output:     strings.ToLower(call.Name) + "-ok",
		}
	})
	if err != nil {
		t.Fatalf("converse failed: %v", err)
	}
	if started != 2 {
		t.Fatalf("expected 2 batch tool calls, got %d", started)
	}
	if !strings.Contains(reply, "batch complete") {
		t.Fatalf("unexpected reply: %q", reply)
	}
}

func TestOrchestratorClientPassesDistinctToolContextsForBatch(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &batchOrchestratorServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetTracer(contextRecordingTracer{})

	var mu sync.Mutex
	observed := map[string]string{}
	if _, err := client.Converse(context.Background(), "batch", func(ctx context.Context, call orchestrator.ToolCall) orchestrator.ToolResult {
		mu.Lock()
		observed[call.Name], _ = ctx.Value(toolSpanContextKey{}).(string)
		mu.Unlock()
		return orchestrator.ToolResult{ToolName: call.Name, Output: strings.ToLower(call.Name) + "-ok"}
	}); err != nil {
		t.Fatalf("converse failed: %v", err)
	}
	if observed["Read"] != "execute_tool Read" || observed["Glob"] != "execute_tool Glob" {
		t.Fatalf("batch handlers did not receive distinct execute_tool contexts: %#v", observed)
	}
}

func TestOrchestratorClientHandlesAskUserRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &askOrchestratorServer{})
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var askedQuestion string
	var askedOptions int
	reply, err := client.ConverseWithPrompts(
		context.Background(),
		"choose",
		func(ctx context.Context, event orchestrator.Event) {
			_ = ctx
			if event.AskUserRequest == nil {
				return
			}
			askedQuestion = event.AskUserRequest.GetQuestion()
			askedOptions = len(event.AskUserRequest.GetOptions())
		},
		func(ctx context.Context, request *codeagentpb.AskUserRequest) (orchestrator.ToolResult, error) {
			_ = ctx
			return orchestrator.ToolResult{
				ToolCallID: request.GetAskUserId(),
				ToolName:   "AskUser",
				Output:     "beta",
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("converse failed: %v", err)
	}
	if askedQuestion != "Choose an option" || askedOptions != 2 {
		t.Fatalf("unexpected ask user event: %q %d", askedQuestion, askedOptions)
	}
	if !strings.Contains(reply, "ask answered") {
		t.Fatalf("unexpected reply: %q", reply)
	}
}

func TestOrchestratorClientCancelsAskUserWithoutHandler(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	askServer := &cancelAskOrchestratorServer{}
	codeagentpb.RegisterOrchestratorServer(server, askServer)
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	reply, err := client.ConverseWithPrompts(context.Background(), "choose", nil, nil)
	if err != nil {
		t.Fatalf("converse failed: %v", err)
	}
	if reply != "ask fallback handled" {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if askServer.result == nil || askServer.result.ToolCallId != "ask-cancel" || askServer.result.ExitCode != 1 || !strings.Contains(askServer.result.Error, "not configured") {
		t.Fatalf("unexpected ask fallback result: %+v", askServer.result)
	}
}

func TestOrchestratorClientTimesOutAskUserHandler(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	askServer := &cancelAskOrchestratorServer{}
	codeagentpb.RegisterOrchestratorServer(server, askServer)
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetAskUserTimeout(10 * time.Millisecond)

	reply, err := client.ConverseWithPrompts(
		context.Background(),
		"choose",
		nil,
		func(ctx context.Context, request *codeagentpb.AskUserRequest) (orchestrator.ToolResult, error) {
			_ = ctx
			_ = request
			time.Sleep(200 * time.Millisecond)
			return orchestrator.ToolResult{ToolName: "AskUser", Output: "late"}, nil
		},
	)
	if err != nil {
		t.Fatalf("converse failed: %v", err)
	}
	if reply != "ask fallback handled" {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if askServer.result == nil || askServer.result.ToolCallId != "ask-cancel" || askServer.result.ExitCode != 1 || !strings.Contains(askServer.result.Error, "timed out") {
		t.Fatalf("unexpected ask timeout result: %+v", askServer.result)
	}
}
