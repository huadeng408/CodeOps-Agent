package codeagent_test

import (
	"context"
	"net"
	"testing"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type planTodoSnapshotServer struct {
	codeagentpb.UnimplementedOrchestratorServer
	received *codeagentpb.PlanTodoSnapshot
}

func (s *planTodoSnapshotServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	input := message.GetUserInput()
	if input == nil || input.GetPlanTodoState() == nil {
		return statusError("plan/todo snapshot was not sent")
	}
	s.received = input.GetPlanTodoState()
	if err := stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_TodoUpdate{
			TodoUpdate: &codeagentpb.TodoUpdate{
				Revision: s.received.GetRevision() + 1,
				Todos:    []*codeagentpb.TodoItem{{Content: "patch", Status: "in_progress"}},
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

func statusError(message string) error {
	return status.Error(codes.InvalidArgument, message)
}

func TestClientSendsPlanTodoSnapshotAndInvokesAtomicUpdateCallback(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	capturing := &planTodoSnapshotServer{}
	codeagentpb.RegisterOrchestratorServer(server, capturing)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var updateRevision uint64
	client.OnPlanTodoUpdate = func(_ *codeagentpb.PlanUpdate, update *codeagentpb.TodoUpdate) error {
		updateRevision = update.GetRevision()
		return nil
	}
	state := &codeagentpb.PlanTodoSnapshot{
		SchemaVersion: 1,
		Revision:      7,
		Plan: &codeagentpb.PlanUpdate{
			Steps:        []string{"inspect", "patch"},
			CurrentIndex: 1,
			Mode:         "plan",
		},
		Todos: []*codeagentpb.TodoItem{{Content: "inspect", Status: "completed"}},
	}
	if _, err := client.ConverseWithHistoryAndState(
		context.Background(), "continue", "session-1", nil, state, nil, nil,
	); err != nil {
		t.Fatalf("converse with state failed: %v", err)
	}
	if capturing.received == nil || capturing.received.GetRevision() != 7 || capturing.received.GetPlan().GetCurrentIndex() != 1 {
		t.Fatalf("unexpected received state: %+v", capturing.received)
	}
	if updateRevision != 8 {
		t.Fatalf("unexpected update revision: %d", updateRevision)
	}
}
