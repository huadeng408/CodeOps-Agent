package session

import (
	"context"
	"encoding/json"
	"testing"

	"code-agent/internal/orchestrator"
)

type canceledCommitMemory struct{}

func (canceledCommitMemory) Commit(ctx context.Context, _ string) error {
	return ctx.Err()
}

func (canceledCommitMemory) Recall(context.Context, uint, string, int) (string, error) {
	panic("recall must not run during memory commit")
}

type scopedRecallMemory struct{ query MemoryQuery }

func (*scopedRecallMemory) Commit(context.Context, string) error { return nil }
func (*scopedRecallMemory) Recall(context.Context, uint, string, int) (string, error) {
	return `{"entries":[]}`, nil
}
func (m *scopedRecallMemory) RecallWithOptions(_ context.Context, _ uint, query MemoryQuery) (string, error) {
	m.query = query
	return `{"entries":[]}`, nil
}
func (*scopedRecallMemory) Manage(context.Context, string, MemoryCommand) (string, error) {
	return "", nil
}

func TestMemoryCommitCancellationStillRecordsBlockedReceipt(t *testing.T) {
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	view, err := workbench.Create(context.Background(), 7, "repo", "fixture memory cancellation", "")
	if err != nil {
		t.Fatal(err)
	}
	runner := NewSessionRunner(workbench, nil, nil, SessionRunnerOptions{Memory: canceledCommitMemory{}})
	defer runner.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner.commitSessionMemory(ctx, runKey{sessionID: view.ID, runID: "fixture-run"})
	events, err := ledger.Events(context.Background(), view.ID)
	if err != nil || len(events) != 2 || events[1].Type != "memory/commit-blocked" {
		t.Fatalf("canceled memory commit lost its failure receipt: events=%d, err=%v", len(events), err)
	}
	var receipt struct {
		RunID     string `json:"run_id"`
		ErrorCode string `json:"error_code"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(events[1].Payload, &receipt); err != nil || receipt.RunID != "fixture-run" || receipt.ErrorCode != "memory_commit_unavailable" || !receipt.Retryable {
		t.Fatal("memory cancellation receipt must retain the fixed, retryable category")
	}
}

func TestIndependentAgentRecallIsScopedToChildSession(t *testing.T) {
	runner, parent, actor := agentTestRunner(t, &independentAgentFixture{})
	task := agentTestCall(t, runner, actor, "memory-scope", "SpawnAgent", `{"kind":"explore","title":"recall","objective":"recall only child history"}`)
	memory := &scopedRecallMemory{}
	runner.options.Memory = memory

	childEvents, err := runner.workbench.ledger.Events(context.Background(), task.ChildSessionId)
	if err != nil {
		t.Fatal(err)
	}
	result := runner.recallSessionMemory(context.Background(), childEvents, orchestrator.ToolCall{
		ID: "child-recall", Name: "RecallMemory", ParametersJSON: `{"query":"history"}`,
	})
	if result.ExitCode != 0 || memory.query.SourceSessionID != task.ChildSessionId {
		t.Fatalf("child recall scope = %q, result=%+v", memory.query.SourceSessionID, result)
	}

	parentEvents, err := runner.workbench.ledger.Events(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	result = runner.recallSessionMemory(context.Background(), parentEvents, orchestrator.ToolCall{
		ID: "parent-recall", Name: "RecallMemory", ParametersJSON: `{"query":"history"}`,
	})
	if result.ExitCode != 0 || memory.query.SourceSessionID != "" {
		t.Fatalf("parent recall unexpectedly scoped = %q, result=%+v", memory.query.SourceSessionID, result)
	}
}
