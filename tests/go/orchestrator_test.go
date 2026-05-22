package codeagent_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

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
