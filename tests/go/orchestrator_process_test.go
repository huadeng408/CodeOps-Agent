package codeagent_test

import (
	"context"
	"net"
	"testing"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"

	"google.golang.org/grpc"
)

func TestProcessManagerUsesExistingHealthyServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &testOrchestratorServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address:             listener.Addr().String(),
		AutoStart:           true,
		Command:             "definitely-not-used",
		StartupTimeout:      200 * time.Millisecond,
		ConversationTimeout: 2 * time.Minute,
	})
	defer manager.Stop()

	client, err := manager.Client(context.Background())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if client == nil {
		t.Fatal("expected client")
	}
	if manager.Owned() {
		t.Fatal("manager should not own an externally healthy server")
	}
	if client.ConversationTimeout() != 2*time.Minute {
		t.Fatalf("unexpected conversation timeout: %s", client.ConversationTimeout())
	}
}

func TestProcessManagerReportsDisabledAutoStart(t *testing.T) {
	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address:        "127.0.0.1:1",
		AutoStart:      false,
		StartupTimeout: 100 * time.Millisecond,
	})
	defer manager.Stop()

	_, err := manager.Client(context.Background())
	if err == nil {
		t.Fatal("expected error when auto-start is disabled")
	}
}
