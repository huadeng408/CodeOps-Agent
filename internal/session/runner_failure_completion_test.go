package session

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
)

type failingReadConversation struct{}

func (failingReadConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	result := handlers.Tool(ctx, orchestrator.ToolCall{ID: "known-read", Name: "Read", ParametersJSON: `{"path":"source.go"}`})
	if result.Error != "" || result.ExitCode != 0 {
		return orchestrator.ConversationResult{}, context.Canceled
	}
	return orchestrator.ConversationResult{}, ErrEventIntegrity
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
