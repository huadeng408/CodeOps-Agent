package codeagent_test

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"

	"google.golang.org/grpc"
)

type testOrchestratorServer struct {
	codeagentpb.UnimplementedOrchestratorServer
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
		toolOutput = payload.Output
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

func TestOrchestratorClientHealthAndConverse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &testOrchestratorServer{})
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
		return orchestrator.ToolResult{ToolName: "Echo", Output: "tool-ok"}
	})
	if err != nil {
		t.Fatalf("converse failed: %v", err)
	}
	if !strings.Contains(reply, "hello agent tool-ok") {
		t.Fatalf("unexpected reply: %q", reply)
	}
}
