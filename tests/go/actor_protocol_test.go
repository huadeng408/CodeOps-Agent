package codeagent_test

import (
	"context"
	"io"
	"net"
	"testing"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"

	"google.golang.org/grpc"
)

type actorCaptureServer struct {
	codeagentpb.UnimplementedOrchestratorServer
	actor        *codeagentpb.ActorContext
	modelGateway *codeagentpb.ModelGatewayBinding
}

func (s *actorCaptureServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	input := message.GetUserInput()
	if input == nil {
		return io.ErrUnexpectedEOF
	}
	s.actor = input.GetActor()
	s.modelGateway = input.GetModelGateway()
	return stream.Send(&codeagentpb.OrchestratorMessage{
		Payload: &codeagentpb.OrchestratorMessage_Done{
			Done: &codeagentpb.Done{Success: true, Message: "authorized"},
		},
	})
}

func TestOrchestratorClientSendsTransientModelScope(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	capture := &actorCaptureServer{}
	codeagentpb.RegisterOrchestratorServer(server, capture)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	released := false
	client.SetModelScopeFactory(func(actor orchestrator.ActorIdentity, taskID string) (*codeagentpb.ModelGatewayBinding, func(), error) {
		if actor.SessionID != "session-1" || taskID != "session-1" {
			t.Fatal("scope not bound to the authenticated session")
		}
		return &codeagentpb.ModelGatewayBinding{SchemaVersion: 1, Address: "127.0.0.1:1234", Capability: make([]byte, 32), Protocol: "openai", Model: "fixture-model"}, func() { released = true }, nil
	})
	if _, err := client.ConverseWithHistory(context.Background(), "hello", "session-1", nil); err != nil {
		t.Fatal(err)
	}
	if capture.modelGateway == nil || capture.modelGateway.Model != "fixture-model" || !released {
		t.Fatal("model scope was omitted or not released")
	}
}

func TestOrchestratorClientPropagatesVersionedActorContext(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	capturing := &actorCaptureServer{}
	codeagentpb.RegisterOrchestratorServer(server, capturing)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetActor(orchestrator.ActorIdentity{
		SchemaVersion: 1,
		ActorID:       "user:42",
		Subject:       "alice",
		TenantID:      "org:7",
		Roles:         []string{"USER"},
	})

	if _, err := client.ConverseWithHistory(context.Background(), "hello", "session-1", nil); err != nil {
		t.Fatalf("ConverseWithHistory() error = %v", err)
	}
	if capturing.actor == nil {
		t.Fatal("actor context was not sent")
	}
	if capturing.actor.GetSchemaVersion() != 1 || capturing.actor.GetActorId() != "user:42" || capturing.actor.GetSubject() != "alice" || capturing.actor.GetTenantId() != "org:7" {
		t.Fatalf("unexpected actor context: %+v", capturing.actor)
	}
	if capturing.actor.GetSessionId() != "session-1" || len(capturing.actor.GetRoles()) != 1 || capturing.actor.GetRoles()[0] != "USER" {
		t.Fatalf("actor was not bound to session: %+v", capturing.actor)
	}
}
