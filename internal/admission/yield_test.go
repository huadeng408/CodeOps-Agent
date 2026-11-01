package admission_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"code-agent/internal/admission"
	"code-agent/internal/session"
)

func TestYieldPreservesUnknownUsageAndResumableTaskIdentity(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	original, err := budget.Reserve(ctx, 7, "original", "original-call", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := budget.YieldTask(ctx, 7, "original"); !errors.Is(err, admission.ErrUsageUnknown) {
		t.Fatalf("live call released the task slot: %v", err)
	}
	if _, err := budget.MarkUnknown(ctx, 7, "original-call"); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.YieldTask(ctx, 8, "original"); !errors.Is(err, admission.ErrInvalidAdmission) {
		t.Fatalf("foreign owner released the task slot: %v", err)
	}
	yielded, err := budget.YieldTask(ctx, 7, "original")
	if err != nil || yielded.ID != original.ID || yielded.TaskID != "" || !yielded.UnknownUsage || yielded.ReservedTokens != 1000 || yielded.UsedTokens != 0 {
		t.Fatalf("yield changed retained accounting: %+v %v", yielded, err)
	}
	reopened := admission.NewBudget(ledger)
	if _, err := reopened.Reserve(ctx, 8, "evaluation", "evaluation-call", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.YieldTask(ctx, 7, "original"); !errors.Is(err, admission.ErrInvalidAdmission) {
		t.Fatalf("replayed yield released another task: %v", err)
	}
	if _, err := reopened.Reserve(ctx, 7, "original", "resume-call", 50); !errors.Is(err, admission.ErrTaskBusy) {
		t.Fatalf("yield weakened the single-task gate: %v", err)
	}
	if _, err := reopened.Settle(ctx, 8, "evaluation-call", 30); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.FinishTask(ctx, 8, "evaluation"); err != nil {
		t.Fatal(err)
	}
	resumed, err := reopened.Reserve(ctx, 7, "original", "resume-call", 50)
	if err != nil || resumed.ID != original.ID || resumed.UsedTokens != 30 || resumed.ReservedTokens != 1050 || !resumed.UnknownUsage {
		t.Fatalf("original identity was closed or accounting reset: %+v %v", resumed, err)
	}
	if _, err := reopened.Reserve(ctx, 7, "original", "original-call", 1000); !errors.Is(err, admission.ErrCallReserved) {
		t.Fatalf("yield authorized a duplicate original call: %v", err)
	}
}
