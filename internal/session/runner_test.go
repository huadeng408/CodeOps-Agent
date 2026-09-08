package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
)

type recordingConversationAdapter struct {
	mu       sync.Mutex
	requests []orchestrator.ConversationRequest
	reply    string
	err      error
}

type blockingConversationAdapter struct {
	started chan struct{}
}

type recoverableConversationAdapter struct {
	mu    sync.Mutex
	calls int
}

func (a *blockingConversationAdapter) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, _ orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	close(a.started)
	<-ctx.Done()
	return orchestrator.ConversationResult{}, ctx.Err()
}

func (a *recordingConversationAdapter) RunConversation(_ context.Context, request orchestrator.ConversationRequest, _ orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	a.mu.Lock()
	a.requests = append(a.requests, request)
	a.mu.Unlock()
	if a.err != nil {
		return orchestrator.ConversationResult{}, a.err
	}
	return orchestrator.ConversationResult{Success: true, Message: a.reply}, nil
}

func (a *recordingConversationAdapter) lastRequest(t *testing.T) orchestrator.ConversationRequest {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.requests) == 0 {
		t.Fatal("conversation adapter was not called")
	}
	return a.requests[len(a.requests)-1]
}

func (a *recoverableConversationAdapter) RunConversation(_ context.Context, _ orchestrator.ConversationRequest, _ orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.calls == 1 {
		return orchestrator.ConversationResult{}, errors.New("rpc error: code = Unavailable desc = transport is closing")
	}
	return orchestrator.ConversationResult{Success: true, Message: "resumed after transport recovery"}, nil
}

func TestSessionRunnerContinuationIsIdempotentAndCommitsAssistantTerminal(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "title", "goal")
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "continue this task")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 2, message.ID, "resume")
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.UpdateStatus(ctx, 7, created.ID, 3, "paused"); err != nil {
		t.Fatal(err)
	}

	conversation := &recordingConversationAdapter{reply: "resumed answer"}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{
		WorkerID: "worker-test", LeaseDuration: time.Second, HeartbeatInterval: 100 * time.Millisecond,
	})
	t.Cleanup(func() { _ = runner.Close() })
	command := ContinueCommand{
		RequestID: "request-1", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4,
		Actor: identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "alice", TenantID: "org:one", Roles: []string{"USER"}},
	}
	accepted, err := runner.RequestContinuation(ctx, command)
	if err != nil {
		t.Fatalf("request continuation: %v", err)
	}
	if accepted.RunID == "" || accepted.RequestID != command.RequestID || accepted.Status != RunQueued {
		t.Fatalf("accepted run = %+v", accepted)
	}

	// A retry after a lost HTTP response must return the original run even after
	// the ledger advanced; it must not append a second continuation request.
	retried, err := runner.RequestContinuation(ctx, command)
	if err != nil {
		t.Fatalf("retry continuation: %v", err)
	}
	if retried.RunID != accepted.RunID {
		t.Fatalf("retry run id = %q, want %q", retried.RunID, accepted.RunID)
	}
	if _, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-1", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: "different-checkpoint", ExpectedSeq: command.ExpectedSeq,
		Actor: command.Actor,
	}); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("request id reuse with a different checkpoint = %v, want conflict", err)
	}

	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	if terminal.Attempt != 1 || terminal.Error != "" {
		t.Fatalf("terminal run = %+v", terminal)
	}
	request := conversation.lastRequest(t)
	if request.SessionID != created.ID || request.RunID != accepted.RunID || !request.Resume || request.Input != "continue this task" {
		t.Fatalf("conversation request = %+v", request)
	}
	if request.Actor.SessionID != created.ID || request.Actor.ActorID != "user:7" {
		t.Fatalf("bound actor = %+v", request.Actor)
	}

	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []string{
		"session/created", "user/message", "checkpoint/create", "session/status-updated",
		continuationEventType, runLeasedEventType, "assistant/message", runCompletedEventType,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %d, want %d: %+v", len(events), len(wantTypes), events)
	}
	for index, want := range wantTypes {
		if events[index].Type != want {
			t.Fatalf("event[%d] = %q, want %q", index, events[index].Type, want)
		}
	}
	view, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "done" || view.Run == nil || view.Run.Status != RunCompleted {
		t.Fatalf("completed session = %+v", view)
	}
	surface, err := ledger.Surface(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := surface[len(surface)-1].Type; got != "assistant/message" {
		t.Fatalf("surface tail = %q", got)
	}
}

func TestSessionRunnerFailureWritesOneTerminalAndReturnsSessionToPaused(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	conversation := &recordingConversationAdapter{err: errors.New("orchestrator unavailable")}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-fail", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-fail", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunFailed)
	if terminal.Error != "agent continuation failed" {
		t.Fatalf("public terminal error = %q", terminal.Error)
	}
	view, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "paused" || view.Run == nil || view.Run.Status != RunFailed {
		t.Fatalf("failed session = %+v", view)
	}
}

func TestSessionRunnerRecoverClaimsExpiredLeaseWithNextAttempt(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-recover", "run-recover", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{
		RunID: "run-recover", RequestID: "request-recover", LeaseID: "dead-lease",
		WorkerID: "dead-worker", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	conversation := &recordingConversationAdapter{reply: "recovered"}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-recovery", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	if err := runner.Recover(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, "run-recover", RunCompleted)
	if terminal.Attempt != 2 || terminal.WorkerID != "worker-recovery" {
		t.Fatalf("recovered run = %+v", terminal)
	}
}

func TestSessionRunnerRetriesTransportFailureAfterLeaseWithoutTerminalFailure(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	conversation := &recoverableConversationAdapter{}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{
		WorkerID: "worker-transport-retry", LeaseDuration: 80 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond,
	})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-transport-retry", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	if terminal.Attempt != 2 || terminal.Error != "" {
		t.Fatalf("transport-recovered run = %+v", terminal)
	}
	conversation.mu.Lock()
	calls := conversation.calls
	conversation.mu.Unlock()
	if calls != 2 {
		t.Fatalf("conversation calls = %d, want 2", calls)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == runFailedEventType {
			t.Fatalf("recoverable transport failure wrote terminal event: %+v", event)
		}
	}
}

func TestSessionRunnerGracefulCloseLeavesRunRecoverable(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	blocking := &blockingConversationAdapter{started: make(chan struct{})}
	first := NewSessionRunner(workbench, blocking, nil, SessionRunnerOptions{
		WorkerID: "worker-first", LeaseDuration: 80 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond,
	})
	accepted, err := first.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-restart", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("first runner did not start")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	view, err := first.Run(ctx, created.ID, accepted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != RunRunning || view.CompletedAt != nil {
		t.Fatalf("graceful close wrote a terminal outcome: %+v", view)
	}

	time.Sleep(100 * time.Millisecond)
	conversation := &recordingConversationAdapter{reply: "resumed after restart"}
	second := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-second", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = second.Close() })
	if err := second.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, second, created.ID, accepted.RunID, RunCompleted)
	if terminal.Attempt != 2 || terminal.WorkerID != "worker-second" {
		t.Fatalf("restarted run = %+v", terminal)
	}
}

func TestSessionRunnerCompletesPersistedAssistantWithoutCallingModelAgain(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-output", "run-output", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{
		RunID: "run-output", RequestID: "request-output", LeaseID: "expired-output",
		WorkerID: "dead-worker", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.AppendSurface(ctx, created.ID, 6, "assistant/message", messagePayload{
		Author: "assistant", Content: "already committed", RunID: "run-output",
	}, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}

	conversation := &recordingConversationAdapter{reply: "must not run"}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-output", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	if err := runner.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, "run-output", RunCompleted)
	if terminal.Attempt != 2 {
		t.Fatalf("persisted output recovery = %+v", terminal)
	}
	conversation.mu.Lock()
	defer conversation.mu.Unlock()
	if len(conversation.requests) != 0 {
		t.Fatalf("model was called again after output commit: %#v", conversation.requests)
	}
}

func TestSessionRunnerResumesHistoryOnlyToolCheckpoint(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "history-only", "goal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "inspect the repository"); err != nil {
		t.Fatal(err)
	}
	call := toolCallPayload{RunID: "run-pending", ToolCallID: "call-1", ToolName: "Glob", ArgumentsJSON: `{"pattern":"**/*.go"}`}
	if _, err := ledger.AppendSurface(ctx, created.ID, 2, "tool/call", call, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	result := toolResultPayload{RunID: "run-pending", ToolCallID: "call-1", ToolName: "Glob", Output: "main.go"}
	resultEvent, err := ledger.AppendSurface(ctx, created.ID, 3, "tool/result", result, SurfaceOperation{Op: "append"})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 4, resultEvent.EventID, "after-tool")
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.UpdateStatus(ctx, 7, created.ID, 5, "paused"); err != nil {
		t.Fatal(err)
	}

	conversation := &recordingConversationAdapter{reply: "continued from tool history"}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-history", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-history", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 6, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	if terminal.Status != RunCompleted {
		t.Fatalf("history-only run = %+v", terminal)
	}
	request := conversation.lastRequest(t)
	if request.Input != "" || len(request.History) != 3 {
		t.Fatalf("history-only request = input=%q history=%#v", request.Input, request.History)
	}
	if len(request.History[1].ToolCalls) != 1 || request.History[2].ToolCallID != "call-1" {
		t.Fatalf("tool pairing was not preserved: %#v", request.History)
	}
}

func pausedSessionWithCheckpoint(t *testing.T, ledger EventLog) (*Workbench, SessionView, CheckpointView) {
	t.Helper()
	ctx := context.Background()
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "title", "goal")
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "resume")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 2, message.ID, "resume")
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.UpdateStatus(ctx, 7, created.ID, 3, "paused"); err != nil {
		t.Fatal(err)
	}
	return workbench, created, checkpoint
}

func testRunnerActor() identity.Actor {
	return identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "alice", TenantID: "org:one", Roles: []string{"USER"}}
}

func continuationRequestPayloadForTest(ctx context.Context, workbench *Workbench, sessionID, checkpointHash, requestID, runID string, actor identity.Actor) (continuationPayload, error) {
	return workbench.continuationRequestPayload(ctx, sessionID, checkpointHash, requestID, runID, actor)
}

func waitForRunStatus(t *testing.T, runner *SessionRunner, sessionID, runID string, status RunStatus) RunView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, err := runner.Run(context.Background(), sessionID, runID)
		if err == nil && view.Status == status {
			return view
		}
		time.Sleep(10 * time.Millisecond)
	}
	view, err := runner.Run(context.Background(), sessionID, runID)
	t.Fatalf("run did not reach %q: view=%+v err=%v", status, view, err)
	return RunView{}
}
