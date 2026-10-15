package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
)

type repeatedCommandConversation struct {
	results chan []orchestrator.ToolResult
	ids     []string
}

func (c repeatedCommandConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	results := make([]orchestrator.ToolResult, 0, 2)
	for _, id := range c.ids {
		results = append(results, handlers.Tool(ctx, orchestrator.ToolCall{
			ID: id, Name: "Bash", ParametersJSON: `{"command":"python -m unittest discover -v"}`,
		}))
	}
	c.results <- results
	if results[1].Error != "" {
		return orchestrator.ConversationResult{}, errors.New(results[1].Error)
	}
	return orchestrator.ConversationResult{Success: true, Message: "Tests ran again"}, nil
}

func TestSessionRunnerExecutesFreshCommandAfterKnownResult(t *testing.T) {
	testRepeatedCommands(t, orchestrator.ToolResult{Output: "Tests completed"}, false, false)
}

func TestSessionRunnerExecutesFreshCommandAfterKnownFailure(t *testing.T) {
	testRepeatedCommands(t, orchestrator.ToolResult{Error: "project tests failed", ExitCode: 1, RuntimeID: "completed-runtime", Stopped: true}, false, false)
}

func TestSessionRunnerReplaysSameCommandIdentityOnce(t *testing.T) {
	testRepeatedCommands(t, orchestrator.ToolResult{Output: "Tests completed"}, true, false)
}

func TestSessionRunnerFencesFreshCommandAfterUnknownOutcome(t *testing.T) {
	testRepeatedCommands(t, orchestrator.ToolResult{Error: "execution outcome unknown", ExitCode: 1, RuntimeID: "unknown-runtime", OutcomeUnknown: true}, false, true)
}

func TestSessionRunnerFencesFreshCommandWhilePriorRuntimeNotStopped(t *testing.T) {
	testRepeatedCommands(t, orchestrator.ToolResult{Error: "runtime stop unconfirmed", ExitCode: 1, RuntimeID: "live-runtime"}, false, true)
}

func TestModelTaskIdleAfterUndispatchedRepeatRefusal(t *testing.T) {
	for _, tc := range []struct {
		name, rejectedID, arguments string
		pendingPredecessor          bool
		wantIdle                    bool
	}{
		{name: "known predecessor and no dispatch", rejectedID: "refused-repeat", arguments: `{"command":"python -m unittest discover -v"}`, wantIdle: true},
		{name: "unknown dispatched identity", rejectedID: "completed", arguments: `{"command":"python -m unittest discover -v"}`},
		{name: "unfinished predecessor", rejectedID: "refused-repeat", arguments: `{"command":"python -m unittest discover -v"}`, pendingPredecessor: true},
		{name: "no matching known predecessor", rejectedID: "refused-repeat", arguments: `{"command":"another command"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testModelTaskIdleAfterRefusal(t, tc.rejectedID, tc.arguments, tc.pendingPredecessor, tc.wantIdle)
		})
	}
}

func testModelTaskIdleAfterRefusal(t *testing.T, rejectedID, arguments string, pendingPredecessor, wantIdle bool) {
	t.Helper()
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "stopped repeat", "Retain refused request")
	if err != nil {
		t.Fatal(err)
	}
	conversation := repeatedCommandConversation{results: make(chan []orchestrator.ToolResult, 1), ids: []string{"completed", "completed"}}
	runner := NewSessionRunner(workbench, conversation, ToolExecutionFunc(func(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult {
		return orchestrator.ToolResult{Output: "known tests result", RuntimeID: "stopped-runtime", Stopped: true}
	}), SessionRunnerOptions{WorkerID: "stopped-repeat", LeaseDuration: time.Minute})
	t.Cleanup(func() { _ = runner.Close() })
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{
		RequestID: "complete-before-refusal", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: int64(created.EventCount), Content: "Run tests", Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, runner, created.ID, run.RunID, RunCompleted)
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	seq := int64(len(snapshot.Events))
	if pendingPredecessor {
		call := toolCallPayload{RunID: run.RunID, ToolCallID: "unfinished", ToolName: "Bash", ArgumentsJSON: `{"command":"python -m unittest discover -v"}`}
		for _, eventType := range []string{"tool/call", commitPendingEventType, toolDispatchedType} {
			if _, err := ledger.Append(ctx, created.ID, seq, eventType, call); err != nil {
				t.Fatal(err)
			}
			seq++
		}
	}
	// Older runners recorded this refusal before preparing or dispatching the new identity.
	if _, err := ledger.Append(ctx, created.ID, seq, toolUnknownEventType, toolCallPayload{
		RunID: run.RunID, ToolCallID: rejectedID, ToolName: "Bash", ArgumentsJSON: arguments,
	}); err != nil {
		t.Fatal(err)
	}
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	err = ModelTaskIdle(ctx, ledger, actor)
	if wantIdle && err != nil {
		t.Fatalf("known stopped predecessor and undispatched refusal blocked task completion: %v", err)
	}
	if !wantIdle && err == nil {
		t.Fatal("task completion allowed an unproven or unresolved effect")
	}
}

func testRepeatedCommands(t *testing.T, first orchestrator.ToolResult, sameID, wantBlocked bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "rerun tests", "Run tests after a repair")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"before-repair", "after-repair"}
	if sameID {
		ids[1] = ids[0]
	}
	conversation := repeatedCommandConversation{results: make(chan []orchestrator.ToolResult, 1), ids: ids}
	var calls []orchestrator.ToolCall
	runner := NewSessionRunner(workbench, conversation, ToolExecutionFunc(func(_ context.Context, _ identity.Actor, _ string, call orchestrator.ToolCall) orchestrator.ToolResult {
		calls = append(calls, call)
		if len(calls) == 1 {
			return first
		}
		return orchestrator.ToolResult{Output: "Tests completed"}
	}), SessionRunnerOptions{WorkerID: "test-rerun", LeaseDuration: time.Minute})
	t.Cleanup(func() { _ = runner.Close() })
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{
		RequestID: "rerun-tests", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: int64(created.EventCount), Content: "Run the tests again after repair", Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case results := <-conversation.results:
		if results[0].Error != first.Error || results[0].ExitCode != first.ExitCode {
			t.Fatalf("initial observed result changed: %+v", results[0])
		}
		if wantBlocked && results[1].Error == "" {
			t.Fatalf("fresh command ran before the prior effect was known and stopped: %+v", results[1])
		}
		if !wantBlocked && (results[1].Error != "" || results[1].ExitCode != 0) {
			t.Fatalf("fresh command with a known prior result was blocked: %+v", results[1])
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	wantStatus, wantCalls := RunCompleted, 2
	if sameID || wantBlocked {
		wantCalls = 1
	}
	if wantBlocked {
		wantStatus = RunFailed
	}
	waitForRunStatus(t, runner, created.ID, run.RunID, wantStatus)
	if len(calls) != wantCalls || calls[0].OperationID == "" || wantCalls == 2 && calls[0].OperationID == calls[1].OperationID {
		t.Fatalf("fresh authorized commands did not execute as two operations: %+v", calls)
	}
	if _, err := ReadVerifiedSnapshot(ctx, ledger, created.ID); err != nil {
		t.Fatalf("verify completed Session: %v", err)
	}
}
