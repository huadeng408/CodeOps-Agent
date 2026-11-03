package admission_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"code-agent/internal/admission"
	"code-agent/internal/session"
)

func TestStartupRecoveryFenceReusesUnchangedLedger(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "isolated.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	if _, err := budget.Open(ctx, 7); err != nil {
		t.Fatal(err)
	}
	// Valid historical scope facts exercise the same global replay as production
	// without creating model reservations, processes, or external resources.
	for i := 0; i < 512; i++ {
		if _, err := ledger.Append(ctx, session.VerificationAdmissionLedgerID, int64(i+1), session.VerificationAdmissionScopeBoundEventType, map[string]any{
			"owner_id": 7, "task_id": "historical", "scope_id": fmt.Sprintf("retained-%d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := budget.StartupRecoveryAllowed(ctx, 7, "candidate"); err != nil || !allowed {
		t.Fatalf("initial fence = %t, %v", allowed, err)
	}
	var initial, final runtime.MemStats
	runtime.ReadMemStats(&initial)
	for i := 0; i < 32; i++ {
		if allowed, err := budget.StartupRecoveryAllowed(ctx, 7, "candidate"); err != nil || !allowed {
			t.Fatalf("unchanged fence %d = %t, %v", i, allowed, err)
		}
	}
	runtime.ReadMemStats(&final)
	allocated := final.TotalAlloc - initial.TotalAlloc
	t.Logf("startup fence allocated=%d bytes; canonical_events=%d; checks=32", allocated, len(before))
	// A coarse allocation budget catches repeated global replay without a
	// timing assertion or prescribing a private cache representation.
	if allocated > 16<<20 {
		t.Errorf("unchanged startup fences allocated %d bytes; budget is 16 MiB", allocated)
	}
	after, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read-only startup checks changed canonical facts: %v", err)
	}
}

func TestStartupRecoveryFenceRetainsValidationAndOwnerScope(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "isolated.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	for _, owner := range []uint{7, 8} {
		if _, err := budget.AcquireTask(ctx, owner, "candidate"); err != nil {
			t.Fatal(err)
		}
		if allowed, err := budget.StartupRecoveryAllowed(ctx, owner, "candidate"); err != nil || !allowed {
			t.Fatalf("initial: %t %v", allowed, err)
		}
		if _, err := budget.YieldTask(ctx, owner, "candidate"); err != nil {
			t.Fatal(err)
		}
		if allowed, err := budget.StartupRecoveryAllowed(ctx, owner, "candidate"); err != nil || allowed {
			t.Fatalf("same-connection yield: %t %v", allowed, err)
		}
	}
	if _, err := budget.AcquireTask(ctx, 7, "candidate"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		owner uint
		want  bool
	}{{7, true}, {8, false}} {
		if allowed, err := budget.StartupRecoveryAllowed(ctx, item.owner, "candidate"); err != nil || allowed != item.want {
			t.Fatalf("owner %d: %t %v", item.owner, allowed, err)
		}
	}
	for _, item := range []struct {
		owner uint
		task  string
	}{{0, "candidate"}, {7, ""}, {7, " invalid"}, {7, "invalid\n"}} {
		if allowed, err := budget.StartupRecoveryAllowed(ctx, item.owner, item.task); allowed || !errors.Is(err, admission.ErrInvalidAdmission) {
			t.Fatalf("invalid scope accepted: %t %v", allowed, err)
		}
	}
	for _, invalid := range []*admission.Budget{nil, admission.NewBudget(nil), admission.NewBudget((*session.SQLiteEventLog)(nil))} {
		if allowed, err := invalid.StartupRecoveryAllowed(ctx, 7, "candidate"); allowed || !errors.Is(err, admission.ErrInvalidAdmission) {
			t.Fatalf("nil scope accepted: %t %v", allowed, err)
		}
	}
}

func TestStartupRecoveryFenceRevalidatesExternalMutation(t *testing.T) {
	for _, mode := range []string{"yield", "old_payload"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "isolated.sqlite")
			ledger, err := session.OpenSQLiteEventLog(path)
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			budget := admission.NewBudget(ledger)
			if _, err := budget.AcquireTask(ctx, 7, "candidate"); err != nil {
				t.Fatal(err)
			}
			if mode == "old_payload" {
				if _, err := budget.Reserve(ctx, 7, "candidate", "fixture-call", 1); err != nil {
					t.Fatal(err)
				}
				if _, err := budget.Settle(ctx, 7, "fixture-call", 1); err != nil {
					t.Fatal(err)
				}
			}
			if allowed, err := budget.StartupRecoveryAllowed(ctx, 7, "candidate"); err != nil || !allowed {
				t.Fatalf("warm fence: %t %v", allowed, err)
			}
			if mode == "yield" {
				other, err := session.OpenSQLiteEventLog(path)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				if _, err := admission.NewBudget(other).YieldTask(ctx, 7, "candidate"); err != nil {
					t.Fatal(err)
				}
				if allowed, err := budget.StartupRecoveryAllowed(ctx, 7, "candidate"); err != nil || allowed {
					t.Fatalf("external yield: %t %v", allowed, err)
				}
			} else {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err := db.ExecContext(ctx, `UPDATE session_events SET payload = ? WHERE type = 'admission/call_settled'`, `{"owner_id":7,"call_id":"fixture-call","tokens":0}`); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					if allowed, err := budget.StartupRecoveryAllowed(ctx, 7, "candidate"); allowed || !errors.Is(err, session.ErrEventIntegrity) {
						t.Fatalf("old payload mutation accepted: %t %v", allowed, err)
					}
				}
			}
		})
	}
}

type startupFallbackLedger struct {
	session.EventLog
	reads   int
	corrupt bool
}

func (l *startupFallbackLedger) Events(ctx context.Context, id string) ([]session.Event, error) {
	l.reads++
	events, err := l.EventLog.Events(ctx, id)
	if err == nil && l.corrupt && len(events) > 0 {
		events[0].Payload = []byte(`{"invalid":true}`)
	}
	return events, err
}

func TestStartupRecoveryFenceFallsBackForNonNativeAndClosedReader(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "isolated.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if _, err := admission.NewBudget(ledger).Open(ctx, 7); err != nil {
		t.Fatal(err)
	}
	wrapper := &startupFallbackLedger{EventLog: ledger}
	budget := admission.NewBudget(wrapper)
	for i := 0; i < 2; i++ {
		if allowed, err := budget.StartupRecoveryAllowed(ctx, 7, "candidate"); err != nil || !allowed {
			t.Fatalf("fallback: %t %v", allowed, err)
		}
	}
	if wrapper.reads != 2 {
		t.Fatalf("unsupported reader reused a projection: reads=%d", wrapper.reads)
	}
	wrapper.corrupt = true
	if allowed, err := budget.StartupRecoveryAllowed(ctx, 7, "candidate"); allowed || !errors.Is(err, session.ErrEventIntegrity) {
		t.Fatalf("fallback did not revalidate: %t %v", allowed, err)
	}
	native := admission.NewBudget(ledger)
	if allowed, err := native.StartupRecoveryAllowed(ctx, 7, "candidate"); err != nil || !allowed {
		t.Fatalf("native warm: %t %v", allowed, err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	if allowed, err := native.StartupRecoveryAllowed(ctx, 7, "candidate"); allowed || err == nil {
		t.Fatalf("closed source reused a projection: %t %v", allowed, err)
	}
}

func TestStartupRecoveryFenceSeesIndependentProcessYield(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "isolated.sqlite")
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	if _, err := budget.AcquireTask(ctx, 7, "candidate"); err != nil {
		t.Fatal(err)
	}
	if allowed, err := budget.StartupRecoveryAllowed(ctx, 7, "candidate"); err != nil || !allowed {
		t.Fatalf("warm: %t %v", allowed, err)
	}
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartupRecoveryFenceProcessHelper$", "-test.count=1")
	child.Env = append(os.Environ(), "CODEOPS_STARTUP_FENCE_DB="+path)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("owned yield process failed: %v %s", err, output)
	}
	if allowed, err := budget.StartupRecoveryAllowed(ctx, 7, "candidate"); err != nil || allowed {
		t.Fatalf("cross-process yield missed: %t %v", allowed, err)
	}
}

func TestStartupRecoveryFenceProcessHelper(t *testing.T) {
	path := os.Getenv("CODEOPS_STARTUP_FENCE_DB")
	if path == "" {
		t.Skip("owned cross-process startup fence helper")
	}
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if _, err := admission.NewBudget(ledger).YieldTask(context.Background(), 7, "candidate"); err != nil {
		t.Fatal(err)
	}
}
