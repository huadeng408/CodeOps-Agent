package session

import (
	"context"
	"encoding/json"
	"testing"
)

type canceledCommitMemory struct{}

func (canceledCommitMemory) Commit(ctx context.Context, _ string) error {
	return ctx.Err()
}

func (canceledCommitMemory) Recall(context.Context, uint, string, int) (string, error) {
	panic("recall must not run during memory commit")
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
