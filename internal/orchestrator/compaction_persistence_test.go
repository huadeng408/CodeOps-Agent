package orchestrator

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"google.golang.org/grpc"
)

type compactionThenToolServer struct {
	codeagentpb.UnimplementedOrchestratorServer
}

func (compactionThenToolServer) Converse(stream codeagentpb.Orchestrator_ConverseServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	for _, message := range []*codeagentpb.OrchestratorMessage{
		{Payload: &codeagentpb.OrchestratorMessage_CompactionUpdate{CompactionUpdate: &codeagentpb.CompactionUpdate{Summary: "original constraints", RemovedMessages: 2, KeepRecentMessages: 1}}},
		{Payload: &codeagentpb.OrchestratorMessage_ToolRequest{ToolRequest: &codeagentpb.ToolRequest{ToolName: "Write", ToolCallId: "after-compaction", ParametersJson: `{}`}}},
	} {
		if err := stream.Send(message); err != nil {
			return err
		}
	}
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_Text{Text: &codeagentpb.TextChunk{Text: "finished"}}}); err != nil {
		return err
	}
	return stream.Send(&codeagentpb.OrchestratorMessage{Payload: &codeagentpb.OrchestratorMessage_Done{Done: &codeagentpb.Done{Success: true}}})
}

func TestCompactionMustPersistBeforeSubsequentTool(t *testing.T) {
	for _, mode := range []string{"missing", "failed", "failed-transport", "persisted"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer()
			codeagentpb.RegisterOrchestratorServer(server, compactionThenToolServer{})
			go func() { _ = server.Serve(listener) }()
			defer server.Stop()
			client, err := NewClient(listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			persisted, calls := false, 0
			handlers := ConversationHandlers{Tool: func(context.Context, ToolCall) ToolResult {
				if mode == "persisted" && !persisted {
					t.Error("tool ran before compaction persistence completed")
				}
				calls++
				return ToolResult{Output: "written"}
			}}
			if mode != "missing" {
				handlers.Compaction = func(*codeagentpb.CompactionUpdate) error {
					if mode == "failed-transport" {
						return errors.New("ledger connection refused")
					}
					if mode == "failed" {
						return errors.New("ledger unavailable")
					}
					persisted = true
					return nil
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := client.RunConversation(ctx, ConversationRequest{Input: "continue", SessionID: "compaction-test", RunID: "run-1", Resume: true, SurfaceSHA256: "surface-test", Actor: identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "test", TenantID: "test", Roles: []string{"USER"}}}, handlers)
			if mode == "persisted" {
				if err != nil || !result.Success || !persisted || calls != 1 {
					t.Fatalf("persisted=%v calls=%d result=%+v err=%v", persisted, calls, result, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "compaction") || calls != 0 || result.Success {
				t.Fatalf("unpersisted compaction continued: calls=%d result=%+v err=%v", calls, result, err)
			}
			if mode != "persisted" && IsConnectionError(err) {
				t.Fatal("persistence failure was classified as retryable orchestrator transport")
			}
		})
	}
}
