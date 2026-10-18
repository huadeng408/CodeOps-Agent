package session

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/tools"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

type dispatchTraceServer struct {
	pb.UnimplementedOrchestratorServer
	batch bool
}

func (server dispatchTraceServer) Converse(stream pb.Orchestrator_ConverseServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	calls := []*pb.ToolRequest{{ToolCallId: "trace-0", ToolName: "Write", ParametersJson: `{"path":"trace-0.txt","content":"TOOL_ARGUMENT_SECRET_SENTINEL"}`}}
	message := &pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_ToolRequest{ToolRequest: calls[0]}}
	if server.batch {
		calls = append(calls, &pb.ToolRequest{ToolCallId: "trace-1", ToolName: "Write", ParametersJson: `{"path":"trace-1.txt","content":"TOOL_ARGUMENT_SECRET_SENTINEL"}`})
		message = &pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_ToolRequestBatch{ToolRequestBatch: &pb.ToolRequestBatch{Requests: calls, Parallel: true}}}
	}
	if err := stream.Send(message); err != nil {
		return err
	}
	for range calls {
		if _, err := stream.Recv(); err != nil {
			return err
		}
	}
	return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: true, Message: "done"}}})
}

func TestManagedToolTraceStartsOnlyAfterDurableDispatch(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, decision := range []string{"approved", "denied", "canceled", "failed"} {
			t.Run(fmt.Sprintf("batch=%t/%s", batch, decision), func(t *testing.T) {
				ctx := context.Background()
				recorder := tracetest.NewSpanRecorder()
				tracer := newSessionTelemetryTracer(recorder)
				t.Cleanup(func() { _ = tracer.Shutdown(ctx) })
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				server := grpc.NewServer()
				pb.RegisterOrchestratorServer(server, dispatchTraceServer{batch: batch})
				go func() { _ = server.Serve(listener) }()
				t.Cleanup(server.Stop)
				client, err := orchestrator.NewClient(listener.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Close() })
				client.SetTracer(tracer)
				root := t.TempDir()
				count := 1
				if batch {
					count = 2
				}
				if decision == "failed" {
					for i := 0; i < count; i++ {
						if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("trace-%d.txt", i)), 0700); err != nil {
							t.Fatal(err)
						}
					}
				}
				executor := tools.NewExecutor(root)
				t.Cleanup(func() { _ = executor.Close() })
				workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
				created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "dispatch trace", "", root)
				if err != nil {
					t.Fatal(err)
				}
				runner := NewSessionRunner(workbench, client, executorToolAdapter{executor: executor}, SessionRunnerOptions{Tracer: tracer, Permissions: permission.NewController(map[string]permission.Level{"Write": permission.AskSession}, nil)})
				t.Cleanup(func() { _ = runner.Close() })
				actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
				run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "trace-turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "write", Actor: actor})
				if err != nil {
					t.Fatal(err)
				}
				var pending []EventView
				deadline := time.Now().Add(5 * time.Second)
				for len(pending) < count && time.Now().Before(deadline) {
					events, err := workbench.Events(ctx, 7, created.ID, 0)
					if err != nil {
						t.Fatal(err)
					}
					pending = nil
					for _, event := range events {
						if event.Type == approvalPendingEventType {
							pending = append(pending, event)
						}
					}
					if len(pending) < count {
						time.Sleep(time.Millisecond)
					}
				}
				if len(pending) != count {
					t.Fatal("tool requests did not reach approval")
				}
				var rootContext trace.SpanContext
				for _, span := range recorder.Started() {
					if span.Name() == "agent.main" {
						rootContext = span.SpanContext()
					}
					if strings.HasPrefix(span.Name(), "execute_tool ") {
						t.Fatalf("execution span started before approval/dispatch: %s", span.Name())
					}
				}
				if !rootContext.IsValid() {
					t.Fatal("managed run has no valid Go root")
				}
				status := RunCompleted
				if decision == "canceled" {
					_, err = runner.CancelRun(ctx, CancelRunCommand{RequestID: "trace-cancel", SessionID: created.ID, RunID: run.RunID, OwnerID: 7, Actor: actor})
					status = RunCanceled
				} else {
					approval := ApprovalDecision(decision)
					if decision == "failed" {
						approval = ApprovalApproved
					}
					for _, event := range pending {
						_, err = workbench.DecideToolApproval(ctx, 7, created.ID, ToolApprovalDecisionCommand{RunID: run.RunID, ToolCallID: event.Approval.ToolCallID, PendingEventID: event.ID, PendingSeq: event.Seq, Decision: approval})
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				waitForRunStatus(t, runner, created.ID, run.RunID, status)
				events, err := workbench.Events(ctx, 7, created.ID, 0)
				if err != nil {
					t.Fatal(err)
				}
				dispatches := map[string]time.Time{}
				for _, event := range events {
					if event.Type == toolDispatchedType {
						for _, requested := range pending {
							if event.ToolName == requested.Approval.ToolName && strings.TrimPrefix(event.Content, event.ToolName+" ") == requested.Approval.ArgumentsJSON {
								dispatches[requested.Approval.ToolCallID] = event.CreatedAt
							}
						}
					}
				}
				spans := 0
				seen := map[string]bool{}
				for _, span := range recorder.Ended() {
					assertSpanHasNoSentinel(t, span)
					if !strings.HasPrefix(span.Name(), "execute_tool ") {
						continue
					}
					spans++
					id := spanStringAttribute(span, "gen_ai.tool.call.id")
					dispatchedAt, ok := dispatches[id]
					if !ok || seen[id] || span.StartTime().Before(dispatchedAt) || span.Parent().SpanID() != rootContext.SpanID() || span.SpanContext().TraceID() != rootContext.TraceID() {
						t.Fatal("execution span has no preceding durable dispatch")
					}
					seen[id] = true
					if decision == "failed" && span.Status().Code != codes.Error {
						t.Fatal("known executor failure lost its error status")
					}
				}
				want := 0
				if decision == "approved" || decision == "failed" {
					want = count
				}
				if spans != want || len(dispatches) != want {
					t.Fatalf("execution spans=%d dispatches=%d, want %d", spans, len(dispatches), want)
				}
				for i := 0; i < count; i++ {
					if decision == "failed" {
						info, err := os.Stat(filepath.Join(root, fmt.Sprintf("trace-%d.txt", i)))
						if err != nil || !info.IsDir() {
							t.Fatal("failed write changed the existing directory")
						}
						continue
					}
					contents, err := os.ReadFile(filepath.Join(root, fmt.Sprintf("trace-%d.txt", i)))
					if decision == "approved" && (err != nil || string(contents) != "TOOL_ARGUMENT_SECRET_SENTINEL") {
						t.Fatalf("approved write missing: %v", err)
					}
					if decision != "approved" && !os.IsNotExist(err) {
						t.Fatalf("unapproved tool wrote a file: %v", err)
					}
				}
			})
		}
	}
}

func TestUnmanagedClientRetainsToolExecutionTracing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	recorder := tracetest.NewSpanRecorder()
	tracer := newSessionTelemetryTracer(recorder)
	t.Cleanup(func() { _ = tracer.Shutdown(context.Background()) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterOrchestratorServer(server, dispatchTraceServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	client, err := orchestrator.NewClient(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.SetTracer(tracer)
	calls := 0
	result, err := client.RunConversation(ctx, orchestrator.ConversationRequest{Input: "work", SessionID: "legacy-trace", RunID: "legacy-run", Actor: identity.Default()}, orchestrator.ConversationHandlers{Tool: func(context.Context, orchestrator.ToolCall) orchestrator.ToolResult {
		calls++
		return orchestrator.ToolResult{Error: "TOOL_ERROR_SECRET_SENTINEL", ExitCode: 1}
	}})
	if err != nil || !result.Success || calls != 1 {
		t.Fatalf("legacy conversation changed: %+v, %v, calls=%d", result, err, calls)
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "execute_tool Write" {
		t.Fatalf("legacy tool trace missing: %v", spanNames(spans))
	}
	assertSpanHasNoSentinel(t, spans[0])
}
