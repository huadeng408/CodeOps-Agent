package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type retryToolConversationAdapter struct {
	mu          sync.Mutex
	toolResults []orchestrator.ToolResult
}

func (a *retryToolConversationAdapter) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	if handlers.Tool == nil {
		return orchestrator.ConversationResult{}, errors.New("tool handler is missing")
	}
	result := handlers.Tool(ctx, orchestrator.ToolCall{
		ID:             "retry-call",
		Name:           "Write",
		ParametersJSON: `{"content":"retry","path":"runtime/retry.txt"}`,
	})
	a.mu.Lock()
	a.toolResults = append(a.toolResults, result)
	a.mu.Unlock()
	return orchestrator.ConversationResult{Success: true, Message: "reused committed result"}, nil
}

type countingToolAdapter struct {
	mu    sync.Mutex
	calls int
}

func (a *countingToolAdapter) Execute(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	return orchestrator.ToolResult{Output: "unexpected external execution"}
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

type recoverSessionErrorLog struct {
	EventLog
	badSessionID string
	healthyID    string
	err          error
}

func (l *recoverSessionErrorLog) SessionIDs(context.Context) ([]string, error) {
	return []string{l.badSessionID, l.healthyID}, nil
}

func (l *recoverSessionErrorLog) Events(ctx context.Context, sessionID string) ([]Event, error) {
	if sessionID == l.badSessionID {
		return nil, l.err
	}
	return l.EventLog.Events(ctx, sessionID)
}

func TestSessionRunnerRecoverContinuesHealthySessionsAfterOneLedgerFailure(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	seedWorkbench := NewWorkbench(ledger, nil)
	_, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	if created.ID == "healthy-session" {
		t.Fatal("test fixture unexpectedly used reserved session id")
	}
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := continuationRequestPayloadForTest(ctx, seedWorkbench, created.ID, checkpoint.Hash, "request-recover-after-error", "run-recover-after-error", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{
		RunID: "run-recover-after-error", RequestID: "request-recover-after-error", LeaseID: "expired-recover-after-error",
		WorkerID: "dead-worker", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	corruptErr := fmt.Errorf("corrupt session ledger")
	recoverLedger := &recoverSessionErrorLog{EventLog: ledger, badSessionID: "bad-session", healthyID: created.ID, err: corruptErr}
	workbench := NewWorkbench(recoverLedger, nil)
	conversation := &recordingConversationAdapter{reply: "recovered despite neighboring failure"}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-recover-after-error", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	if err := runner.Recover(ctx); !errors.Is(err, corruptErr) {
		t.Fatalf("recover error = %v, want joined corrupt ledger error", err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, "run-recover-after-error", RunCompleted)
	if terminal.Attempt != 2 || terminal.WorkerID != "worker-recover-after-error" {
		t.Fatalf("healthy session was not recovered after neighboring failure: %+v", terminal)
	}
}

func TestSessionRunnerConcurrentRecoverSingleLeaseClaim(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-concurrent-recover", "run-concurrent-recover", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{
		RunID: "run-concurrent-recover", RequestID: "request-concurrent-recover", LeaseID: "expired-concurrent-lease",
		WorkerID: "dead-worker", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	firstConversation := &recordingConversationAdapter{reply: "recovered"}
	secondConversation := &recordingConversationAdapter{reply: "recovered"}
	first := NewSessionRunner(workbench, firstConversation, nil, SessionRunnerOptions{WorkerID: "worker-concurrent-1", LeaseDuration: time.Second})
	second := NewSessionRunner(workbench, secondConversation, nil, SessionRunnerOptions{WorkerID: "worker-concurrent-2", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = first.Recover(ctx) }()
	go func() { defer wg.Done(); _ = second.Recover(ctx) }()
	wg.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, readErr := ledger.Events(ctx, created.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		projection, projectErr := projectRun(events, "run-concurrent-recover")
		if projectErr != nil {
			t.Fatal(projectErr)
		}
		if projection.terminal && projection.view.Status == RunCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectRun(events, "run-concurrent-recover")
	if err != nil {
		t.Fatal(err)
	}
	if !projection.terminal || projection.view.Status != RunCompleted {
		t.Fatalf("concurrent recovery projection = %+v", projection.view)
	}
	var leases, assistantMessages int
	for _, event := range events {
		switch event.Type {
		case runLeasedEventType:
			var lease runLeasePayload
			if json.Unmarshal(event.Payload, &lease) == nil && lease.RunID == "run-concurrent-recover" && lease.Attempt == 2 {
				leases++
			}
		case "assistant/message":
			var message messagePayload
			if json.Unmarshal(event.Payload, &message) == nil && message.RunID == "run-concurrent-recover" {
				assistantMessages++
			}
		}
	}
	if leases != 1 {
		t.Fatalf("attempt-2 lease events = %d, want 1", leases)
	}
	if assistantMessages != 1 {
		t.Fatalf("assistant messages = %d, want 1", assistantMessages)
	}
	firstConversation.mu.Lock()
	firstCalls := len(firstConversation.requests)
	firstConversation.mu.Unlock()
	secondConversation.mu.Lock()
	secondCalls := len(secondConversation.requests)
	secondConversation.mu.Unlock()
	if firstCalls+secondCalls != 1 {
		t.Fatalf("conversation calls = %d, want 1", firstCalls+secondCalls)
	}
}

func TestSessionRunnerRecoverStopsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ledger := openWorkbenchTestLedger(t)
	workbench, _, _ := pausedSessionWithCheckpoint(t, ledger)
	runner := NewSessionRunner(workbench, &recordingConversationAdapter{reply: "unused"}, nil, SessionRunnerOptions{WorkerID: "worker-canceled-recover"})
	t.Cleanup(func() { _ = runner.Close() })
	if err := runner.Recover(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("recover canceled error = %v, want context canceled", err)
	}
}

func TestSessionRunnerCloseClearsQueuedRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &SessionRunner{
		ctx: ctx, cancel: cancel, queue: make(chan runKey, 1),
		queued: make(map[runKey]struct{}),
	}
	key := runKey{sessionID: "session-close", runID: "run-close"}
	runner.queued[key] = struct{}{}
	runner.queue <- key
	if err := runner.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	runner.queuedMu.Lock()
	queued := len(runner.queued)
	runner.queuedMu.Unlock()
	if queued != 0 {
		t.Fatalf("queued runs after close = %d, want 0", queued)
	}
	if len(runner.queue) != 0 {
		t.Fatalf("buffered runs after close = %d, want 0", len(runner.queue))
	}
}

func TestSessionRunnerRejectsContinuationAfterClose(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	runner := NewSessionRunner(workbench, &recordingConversationAdapter{reply: "unused"}, nil, SessionRunnerOptions{WorkerID: "worker-closed-request"})
	if err := runner.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-after-close", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	}); !errors.Is(err, ErrSessionRunnerClosed) {
		t.Fatalf("request after close error = %v, want %v", err, ErrSessionRunnerClosed)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("request after close changed canonical ledger: %d events", len(events))
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

func TestSessionRunnerRetryReusesCommittedToolResultWithoutExecutingAgain(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldPayload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-failed", "run-failed", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, oldPayload); err != nil {
		t.Fatal(err)
	}
	oldLease := runLeasePayload{
		RunID: "run-failed", RequestID: "request-failed", LeaseID: "lease-failed",
		WorkerID: "worker-failed", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, oldLease); err != nil {
		t.Fatal(err)
	}
	call := toolCallPayload{RunID: "run-failed", ToolCallID: "retry-call", ToolName: "Write", ArgumentsJSON: `{"content":"retry","path":"runtime/retry.txt"}`}
	if _, err := ledger.AppendSurface(ctx, created.ID, 6, "tool/call", call, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 7, toolDispatchedType, call); err != nil {
		t.Fatal(err)
	}
	committed := toolResultPayload{RunID: "run-failed", ToolCallID: "retry-call", ToolName: "Write", Output: "committed once"}
	if _, err := ledger.AppendSurface(ctx, created.ID, 8, "tool/result", committed, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 9, runFailedEventType, runTerminalPayload{
		RunID: "run-failed", RequestID: "request-failed", LeaseID: "lease-failed", Attempt: 1, Error: "agent continuation failed",
	}); err != nil {
		t.Fatal(err)
	}

	conversation := &retryToolConversationAdapter{}
	external := &countingToolAdapter{}
	runner := NewSessionRunner(workbench, conversation, external, SessionRunnerOptions{WorkerID: "worker-retry", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-retry", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 10, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	if terminal.Attempt != 1 {
		t.Fatalf("new retry run attempt = %d, want 1", terminal.Attempt)
	}
	external.mu.Lock()
	toolCalls := external.calls
	external.mu.Unlock()
	if toolCalls != 0 {
		t.Fatalf("retry executed external tool %d times, want 0", toolCalls)
	}
	conversation.mu.Lock()
	defer conversation.mu.Unlock()
	if len(conversation.toolResults) != 1 || conversation.toolResults[0].Output != "committed once" {
		t.Fatalf("retry tool result = %#v, want committed receipt", conversation.toolResults)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var currentResults int
	for _, event := range events {
		if event.Type != "tool/result" {
			continue
		}
		var payload toolResultPayload
		if json.Unmarshal(event.Payload, &payload) == nil && payload.RunID == accepted.RunID {
			currentResults++
			if payload.Output != "committed once" {
				t.Fatalf("retry result output = %q, want committed receipt", payload.Output)
			}
		}
	}
	if currentResults != 1 {
		t.Fatalf("current retry result receipts = %d, want 1", currentResults)
	}
}

func TestSessionRunnerRetrySearchesAllFailedPredecessorsForToolReceipt(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	appendRun := func(requestID, runID string, seq int64) {
		t.Helper()
		payload, payloadErr := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, requestID, runID, actor)
		if payloadErr != nil {
			t.Fatal(payloadErr)
		}
		if _, appendErr := ledger.Append(ctx, created.ID, seq, continuationEventType, payload); appendErr != nil {
			t.Fatal(appendErr)
		}
		if _, appendErr := ledger.Append(ctx, created.ID, seq+1, runLeasedEventType, runLeasePayload{
			RunID: runID, RequestID: requestID, LeaseID: "lease-" + runID,
			WorkerID: "worker-" + runID, Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
		}); appendErr != nil {
			t.Fatal(appendErr)
		}
	}
	appendRun("request-root", "run-root", 4)
	if _, err := ledger.Append(ctx, created.ID, 6, runFailedEventType, runTerminalPayload{
		RunID: "run-root", RequestID: "request-root", LeaseID: "lease-run-root", Attempt: 1, Error: "root failed",
	}); err != nil {
		t.Fatal(err)
	}
	appendRun("request-middle", "run-middle", 7)
	call := toolCallPayload{RunID: "run-middle", ToolCallID: "retry-call", ToolName: "Write", ArgumentsJSON: `{"content":"retry","path":"runtime/retry.txt"}`}
	if _, err := ledger.AppendSurface(ctx, created.ID, 9, "tool/call", call, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 10, toolDispatchedType, call); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.AppendSurface(ctx, created.ID, 11, "tool/result", toolResultPayload{
		RunID: "run-middle", ToolCallID: "retry-call", ToolName: "Write", Output: "committed by middle",
	}, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 12, runFailedEventType, runTerminalPayload{
		RunID: "run-middle", RequestID: "request-middle", LeaseID: "lease-run-middle", Attempt: 1, Error: "middle failed",
	}); err != nil {
		t.Fatal(err)
	}

	conversation := &retryToolConversationAdapter{}
	external := &countingToolAdapter{}
	runner := NewSessionRunner(workbench, conversation, external, SessionRunnerOptions{WorkerID: "worker-latest", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-latest", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 13, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	if terminal.Status != RunCompleted {
		t.Fatalf("latest retry status = %+v", terminal)
	}
	if terminal.RetryOfRunID != "run-root" || len(terminal.RetryOfRunIDs) != 2 || terminal.RetryOfRunIDs[0] != "run-root" || terminal.RetryOfRunIDs[1] != "run-middle" {
		t.Fatalf("retry lineage = %+v, want root and middle predecessors", terminal)
	}
	conversation.mu.Lock()
	if len(conversation.toolResults) != 1 || conversation.toolResults[0].Output != "committed by middle" {
		t.Fatalf("latest retry did not reuse middle receipt: %#v", conversation.toolResults)
	}
	conversation.mu.Unlock()
	external.mu.Lock()
	if external.calls != 0 {
		t.Fatalf("latest retry executed external tool %d times, want 0", external.calls)
	}
	external.mu.Unlock()
}

func TestWorkbenchRunHistoryKeepsCreationOrderAfterLaterHeartbeat(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	appendContinuation := func(requestID, runID string, seq int64) {
		t.Helper()
		payload, payloadErr := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, requestID, runID, actor)
		if payloadErr != nil {
			t.Fatal(payloadErr)
		}
		if _, appendErr := ledger.Append(ctx, created.ID, seq, continuationEventType, payload); appendErr != nil {
			t.Fatal(appendErr)
		}
	}
	appendContinuation("request-old", "run-old", 4)
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{
		RunID: "run-old", RequestID: "request-old", LeaseID: "lease-old", WorkerID: "worker-old", Attempt: 1,
		LeaseUntil: time.Now().Add(time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	appendContinuation("request-new", "run-new", 6)
	if _, err := ledger.Append(ctx, created.ID, 7, runLeasedEventType, runLeasePayload{
		RunID: "run-new", RequestID: "request-new", LeaseID: "lease-new", WorkerID: "worker-new", Attempt: 1,
		LeaseUntil: time.Now().Add(time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 8, runHeartbeatEventType, runLeasePayload{
		RunID: "run-old", RequestID: "request-old", LeaseID: "lease-old", WorkerID: "worker-old", Attempt: 1,
		LeaseUntil: time.Now().Add(time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	view, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Runs) != 2 || view.Runs[0].RunID != "run-old" || view.Runs[1].RunID != "run-new" {
		t.Fatalf("run history = %+v, want creation order old,new", view.Runs)
	}
	if view.Run == nil || view.Run.RunID != "run-new" {
		t.Fatalf("current run = %+v, want run-new", view.Run)
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
