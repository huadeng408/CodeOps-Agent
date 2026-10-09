package admission_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"code-agent/internal/admission"
	"code-agent/internal/session"
)

func TestUnknownUsageRetainsReservationAndBlocksAdmission(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	if _, err := budget.Reserve(ctx, 7, "task-a", "call-a", 50_000_000); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.MarkUnknown(ctx, 7, "call-a"); err != nil {
		t.Fatal(err)
	}
	recovered := admission.NewBudget(ledger)
	batch, err := recovered.Open(ctx, 7)
	if err != nil || !batch.UnknownUsage || batch.ReservedTokens != 50_000_000 || batch.UsedTokens != 0 {
		t.Fatal("unconfirmed usage was released or reported as confirmed consumption")
	}
	if _, err := recovered.Reserve(ctx, 7, "task-a", "call-b", 1); !errors.Is(err, admission.ErrUsageUnknown) {
		t.Fatalf("unknown usage admitted another call: %v", err)
	}
	batch, err = recovered.Settle(ctx, 7, "call-a", 20_000_000)
	if err != nil || batch.UnknownUsage || batch.ReservedTokens != 0 || batch.UsedTokens != 20_000_000 {
		t.Fatal("confirmed reconciliation did not preserve actual consumption")
	}
}

func TestVerificationRelayLimitsOutstandingCalls(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	for index := 0; index < 10; index++ {
		if _, err := budget.Reserve(ctx, 7, "task-a", fmt.Sprintf("call-%d", index), 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := budget.Reserve(ctx, 7, "task-a", "call-11", 1); !errors.Is(err, admission.ErrRelayBusy) {
		t.Fatalf("eleventh outstanding model request admitted: %v", err)
	}
	if _, err := budget.Settle(ctx, 7, "call-0", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Reserve(ctx, 7, "task-a", "call-11", 1); err != nil {
		t.Fatal("settlement did not free a relay slot")
	}
}

func TestVerificationBudgetSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	budget := admission.NewBudget(ledger)
	batch, err := budget.Open(ctx, 7)
	if err != nil || batch.LimitTokens != 100_000_000 {
		t.Fatalf("authorized batch unavailable: %v", err)
	}
	if _, err := budget.Reserve(ctx, 7, "task-a", "call-a", 60_000_000); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Settle(ctx, 7, "call-a", 20_000_000); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	ledger, err = session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget = admission.NewBudget(ledger)
	restored, err := budget.Open(ctx, 7)
	if err != nil || restored.ID != batch.ID || restored.UsedTokens != 20_000_000 || restored.ReservedTokens != 0 {
		t.Fatalf("restart reset or lost the batch: %v", err)
	}
	if _, err := budget.Reserve(ctx, 7, "task-a", "call-b", 90_000_000); err == nil {
		t.Fatal("restarted budget admitted more than its remaining tokens")
	}
}

func TestDuplicateCallDoesNotAuthorizeAnotherDispatch(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	if _, err := budget.Reserve(ctx, 7, "task-a", "call-a", 20_000_000); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Reserve(ctx, 7, "task-a", "call-a", 20_000_000); !errors.Is(err, admission.ErrCallReserved) {
		t.Fatalf("repeated call may dispatch twice: %v", err)
	}
	if _, err := budget.Settle(ctx, 7, "call-a", 5_000_000); err != nil {
		t.Fatal(err)
	}
	batch, err := budget.Settle(ctx, 7, "call-a", 5_000_000)
	if err != nil || batch.UsedTokens != 5_000_000 {
		t.Fatal("replayed settlement counted consumption twice")
	}
	if _, err := budget.Settle(ctx, 7, "call-a", 0); !errors.Is(err, admission.ErrInvalidAdmission) {
		t.Fatal("replayed settlement erased historical consumption")
	}
}

func TestVerificationBatchHasOneTaskAcrossOwners(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	first, err := budget.Reserve(ctx, 7, "task-a", "call-a", 30_000_000)
	if err != nil {
		t.Fatal(err)
	}
	second := admission.NewBudget(ledger)
	if _, err := second.Reserve(ctx, 8, "task-b", "call-b", 1); !errors.Is(err, admission.ErrTaskBusy) {
		t.Fatalf("another owner bypassed the one-task limit: %v", err)
	}
	if _, err := second.Settle(ctx, 8, "call-a", 0); !errors.Is(err, admission.ErrInvalidAdmission) {
		t.Fatal("foreign owner settled another owner's request")
	}
	batch, err := second.Open(ctx, 8)
	if err != nil || batch.ID != first.ID || batch.ReservedTokens != 30_000_000 {
		t.Fatal("another owner received a separate verification allowance")
	}
}

func TestTaskReleasePreservesConsumptionAndRejectsLateCalls(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	first, err := budget.Reserve(ctx, 7, "task-a", "call-a", 60_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := budget.FinishTask(ctx, 7, "task-a"); !errors.Is(err, admission.ErrUsageUnknown) {
		t.Fatal("unfinished request released its task slot")
	}
	if _, err := budget.Settle(ctx, 7, "call-a", 20_000_000); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.FinishTask(ctx, 8, "task-a"); !errors.Is(err, admission.ErrInvalidAdmission) {
		t.Fatal("foreign owner released a task")
	}
	closed, err := budget.FinishTask(ctx, 7, "task-a")
	if err != nil || closed.ID != first.ID || closed.TaskID != "" || closed.UsedTokens != 20_000_000 {
		t.Fatal("closing a task reset its allowance or retained its slot")
	}
	recovered := admission.NewBudget(ledger)
	if _, err := recovered.Reserve(ctx, 7, "task-a", "late-call", 1); !errors.Is(err, admission.ErrTaskClosed) {
		t.Fatal("late auxiliary call reopened a completed task")
	}
	if _, err := recovered.Reserve(ctx, 8, "task-b", "call-b", 90_000_000); !errors.Is(err, admission.ErrBudgetExhausted) {
		t.Fatal("next task received a new allowance")
	}
	if _, err := recovered.Reserve(ctx, 8, "task-b", "call-b", 80_000_000); err != nil {
		t.Fatal(err)
	}
	batch, err := recovered.FinishTask(ctx, 7, "task-a")
	if err != nil || batch.TaskID != "task-b" || batch.TaskOwnerID != 8 {
		t.Fatal("replayed completion released the next owner's task")
	}
}

func TestInvalidAdmissionDoesNotCreateAllowance(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	for _, input := range []struct {
		owner      uint
		task, call string
		tokens     int64
	}{
		{0, "task", "call", 1}, {7, "", "call", 1}, {7, "task", "call\n", 1},
		{7, "task", "call", 0}, {7, "task", "call", -1}, {7, "task", "call", 100_000_001},
	} {
		if _, err := budget.Reserve(ctx, input.owner, input.task, input.call, input.tokens); !errors.Is(err, admission.ErrInvalidAdmission) {
			t.Fatalf("invalid request admitted: %v", err)
		}
	}
	ids, err := ledger.SessionIDs(ctx)
	if err != nil || len(ids) != 0 {
		t.Fatal("invalid requests created canonical facts")
	}
}

func TestUsageBeyondReservationBlocksFurtherCalls(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	if _, err := budget.Reserve(ctx, 7, "task-a", "call-a", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Settle(ctx, 7, "call-a", 11); !errors.Is(err, admission.ErrUsageUnknown) {
		t.Fatalf("invalid request bound did not require reconciliation: %v", err)
	}
	batch, err := admission.NewBudget(ledger).Open(ctx, 7)
	if err != nil || !batch.UnknownUsage || batch.ReservedTokens != 10 {
		t.Fatal("violated upper bound discarded its reservation")
	}
	if _, err := budget.Reserve(ctx, 7, "task-a", "call-b", 1); !errors.Is(err, admission.ErrUsageUnknown) {
		t.Fatal("violated upper bound still admitted further calls")
	}
	if _, err := budget.Settle(ctx, 7, "call-a", 10); !errors.Is(err, admission.ErrUsageUnknown) {
		t.Fatal("smaller settlement silently erased a recorded bound violation")
	}
}

func TestReplayRejectsAdmissionAfterUnknownUsage(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	if _, err := budget.Reserve(ctx, 7, "task-a", "call-a", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.MarkUnknown(ctx, 7, "call-a"); err != nil {
		t.Fatal(err)
	}
	ids, err := ledger.SessionIDs(ctx)
	if err != nil || len(ids) != 1 {
		t.Fatal("expected one canonical batch stream")
	}
	events, err := ledger.Events(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	// A valid hash chain is insufficient when an imported writer violated
	// the admission state machine. Replay must reject the contradictory fact.
	if _, err := ledger.Append(ctx, ids[0], int64(len(events)), "admission/call_reserved", map[string]any{
		"owner_id": 7, "task_id": "task-a", "call_id": "call-b", "tokens": 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Open(ctx, 7); !errors.Is(err, session.ErrEventIntegrity) {
		t.Fatalf("contradictory canonical facts accepted: %v", err)
	}
}

func TestCorruptLedgerCannotResetOrConfirmBudget(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	if _, err := budget.Reserve(ctx, 7, "task-a", "call-a", 60_000_000); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Settle(ctx, 7, "call-a", 20_000_000); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.FinishTask(ctx, 7, "task-a"); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(ctx, `UPDATE session_events SET payload = ? WHERE type = 'admission/call_settled'`, `{"owner_id":7,"call_id":"call-a","tokens":0}`); err != nil {
		t.Fatal(err)
	}
	for name, operation := range map[string]func() error{
		"read":                func() error { _, err := budget.Open(ctx, 7); return err },
		"replayed settlement": func() error { _, err := budget.Settle(ctx, 7, "call-a", 0); return err },
		"replayed completion": func() error { _, err := budget.FinishTask(ctx, 7, "task-a"); return err },
	} {
		if err := operation(); !errors.Is(err, session.ErrEventIntegrity) {
			t.Fatalf("%s accepted corrupt budget facts: %v", name, err)
		}
	}
}

func TestBudgetStreamCoexistsWithExistingSessionReaders(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if _, err := admission.NewBudget(ledger).Open(ctx, 7); err != nil {
		t.Fatal(err)
	}
	legacy := session.NewSQLiteEventStore(path)
	defer legacy.Close()
	if err := legacy.Save(ctx, session.Session{ID: "legacy-history"}); err != nil {
		t.Fatal(err)
	}
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "fixture", "Existing history", "Keep history")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := legacy.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].ID != "legacy-history" {
		t.Fatalf("new budget poisoned the legacy session list: %v", err)
	}
	views, err := workbench.List(ctx, 7)
	if err != nil || len(views) != 1 || views[0].ID != created.ID {
		t.Fatalf("new budget became an owner-visible conversation: %v", err)
	}
	runner := session.NewSessionRunner(workbench, nil, nil, session.SessionRunnerOptions{})
	defer runner.Close()
	if err := runner.Recover(ctx); err != nil {
		t.Fatalf("new budget poisoned conversation recovery: %v", err)
	}
	if _, err := ledger.Append(ctx, "unknown-admission-fixture", 0, session.VerificationAdmissionCreatedEventType, map[string]any{
		"owner_id": 7, "limit_tokens": 100_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.List(ctx); !errors.Is(err, session.ErrNotFound) {
		t.Fatal("legacy reader silently hid a stream impersonating the batch")
	}
	if err := runner.Recover(ctx); err == nil {
		t.Fatal("runner silently hid a stream impersonating the batch")
	}
}
