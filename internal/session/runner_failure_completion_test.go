package session

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
)

type failingReadConversation struct{ calls int }

func (conversation failingReadConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	for index := 0; index < max(1, conversation.calls); index++ {
		result := handlers.Tool(ctx, orchestrator.ToolCall{ID: fmt.Sprintf("known-read-%d", index), Name: "Read", ParametersJSON: `{"path":"source.go"}`})
		if result.Error != "" || result.ExitCode != 0 {
			return orchestrator.ConversationResult{}, context.Canceled
		}
	}
	return orchestrator.ConversationResult{}, ErrEventIntegrity
}

type boundedCommitClosureLog struct {
	EventLog
	mu              sync.Mutex
	lastContext     context.Context
	closed          int
	perContextLimit int
	refused         atomic.Int32
}

func (log *boundedCommitClosureLog) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	if kind != commitDoneEventType {
		return log.EventLog.Append(ctx, id, seq, kind, payload)
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.lastContext != ctx {
		log.lastContext, log.closed = ctx, 0
	}
	if log.closed == log.perContextLimit {
		log.refused.Add(1)
		return Event{}, context.DeadlineExceeded
	}
	event, err := log.EventLog.Append(ctx, id, seq, kind, payload)
	if err == nil {
		log.closed++
	}
	return event, err
}

func TestSessionRunnerCompletesFailureWhileCommitClosureMakesProgress(t *testing.T) {
	ctx := context.Background()
	base := openWorkbenchTestLedger(t)
	ledger := &boundedCommitClosureLog{EventLog: base, perContextLimit: 3}
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "bounded failure closure", "Read source")
	if err != nil {
		t.Fatal(err)
	}
	var executions atomic.Int32
	runner := NewSessionRunner(workbench, failingReadConversation{calls: 7}, ToolExecutionFunc(func(_ context.Context, _ identity.Actor, _ string, call orchestrator.ToolCall) orchestrator.ToolResult {
		executions.Add(1)
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Output: "Read completed"}
	}), SessionRunnerOptions{WorkerID: "progressing-close", LeaseDuration: time.Minute})
	t.Cleanup(func() { _ = runner.Close() })
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "progressing-close", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: int64(created.EventCount), Content: "Read source", Actor: testRunnerActor()})
	if err != nil {
		t.Fatal(err)
	}
	_ = waitForRunStatus(t, runner, created.ID, run.RunID, RunFailed)
	if executions.Load() != 7 {
		t.Fatalf("tool executions=%d, want seven without replay", executions.Load())
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, base, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	failed, done := 0, 0
	for _, event := range snapshot.Events {
		if event.Type == runFailedEventType {
			failed++
		}
		if event.Type == commitDoneEventType {
			done++
		}
	}
	if failed != 1 || done != 7 {
		t.Fatalf("failed=%d done=%d, want one terminal and seven retained commits", failed, done)
	}
}

func TestSessionRunnerStopsFailureClosureWithoutDurableProgress(t *testing.T) {
	ctx := context.Background()
	base := openWorkbenchTestLedger(t)
	ledger := &boundedCommitClosureLog{EventLog: base}
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "unavailable closure", "Read source")
	if err != nil {
		t.Fatal(err)
	}
	var executions atomic.Int32
	runner := NewSessionRunner(workbench, failingReadConversation{}, ToolExecutionFunc(func(_ context.Context, _ identity.Actor, _ string, call orchestrator.ToolCall) orchestrator.ToolResult {
		executions.Add(1)
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Output: "Read completed"}
	}), SessionRunnerOptions{WorkerID: "stalled-close", LeaseDuration: time.Minute})
	t.Cleanup(func() { _ = runner.Close() })
	if _, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "stalled-close", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: int64(created.EventCount), Content: "Read source", Actor: testRunnerActor()}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for ledger.refused.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	closed := make(chan struct{})
	go func() { _ = runner.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("failure closure did not stop without durable progress")
	}
	if attempts := ledger.refused.Load(); attempts < 2 || attempts > 10 || executions.Load() != 1 {
		t.Fatalf("unbounded closure or tool replay: attempts=%d executions=%d", attempts, executions.Load())
	}
	events, err := base.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending, done, terminal := 0, 0, 0
	for _, event := range events {
		switch event.Type {
		case commitPendingEventType:
			pending++
		case commitDoneEventType:
			done++
		case runFailedEventType:
			terminal++
		}
	}
	if pending != 1 || done != 0 || terminal != 0 {
		t.Fatalf("unconfirmed commit fence changed: pending=%d done=%d terminal=%d", pending, done, terminal)
	}
}

type cleanupReadDeadlineLog struct {
	EventLog
	mu    sync.Mutex
	armed bool
	fired chan struct{}
}

func (log *cleanupReadDeadlineLog) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	event, err := log.EventLog.Append(ctx, id, seq, kind, payload)
	if err == nil && kind == commitDoneEventType {
		log.mu.Lock()
		log.armed = true
		log.mu.Unlock()
	}
	return event, err
}

func (log *cleanupReadDeadlineLog) Events(ctx context.Context, id string) ([]Event, error) {
	log.mu.Lock()
	_, bounded := ctx.Deadline()
	if log.armed && bounded {
		log.armed = false
		close(log.fired)
		log.mu.Unlock()
		return nil, context.DeadlineExceeded
	}
	log.mu.Unlock()
	return log.EventLog.Events(ctx, id)
}

func TestSessionRunnerPersistsFailureAfterCleanupReadTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	base := openWorkbenchTestLedger(t)
	ledger := &cleanupReadDeadlineLog{EventLog: base, fired: make(chan struct{})}
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "failed read", "Inspect repository")
	if err != nil {
		t.Fatal(err)
	}
	var executions atomic.Int32
	runner := NewSessionRunner(workbench, failingReadConversation{}, ToolExecutionFunc(func(_ context.Context, _ identity.Actor, _ string, call orchestrator.ToolCall) orchestrator.ToolResult {
		executions.Add(1)
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Output: "Read completed"}
	}), SessionRunnerOptions{WorkerID: "cleanup-timeout", LeaseDuration: time.Minute})
	t.Cleanup(func() { _ = runner.Close() })
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{
		RequestID: "cleanup-timeout", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: int64(created.EventCount), Content: "Inspect repository", Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ledger.fired:
	case <-ctx.Done():
		t.Fatal("cleanup did not reach the storage deadline")
	}
	_ = waitForRunStatus(t, runner, created.ID, run.RunID, RunFailed)
	if executions.Load() != 1 {
		t.Fatalf("tool executions = %d, want one without replay", executions.Load())
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, base, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	failed, done := 0, 0
	for _, event := range snapshot.Events {
		if event.Type == runFailedEventType {
			failed++
		}
		if event.Type == commitDoneEventType {
			done++
		}
	}
	if failed != 1 || done != 1 {
		t.Fatalf("failed=%d done=%d, want one terminal and retained commit", failed, done)
	}
}
