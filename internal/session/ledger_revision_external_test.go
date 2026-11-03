package session_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"code-agent/internal/admission"
	"code-agent/internal/session"
)

func TestStartupFenceRejectsSameConnectionTempShadow(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "isolated.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	if _, err := budget.AcquireTask(ctx, 7, "candidate"); err != nil {
		t.Fatal(err)
	}
	if allowed, err := budget.StartupRecoveryAllowed(ctx, 7, "candidate"); err != nil || !allowed {
		t.Fatalf("warm fence: %t %v", allowed, err)
	}
	if err := session.ShadowLedgerForRevisionTest(ledger); err != nil {
		t.Fatal(err)
	}
	if allowed, err := budget.StartupRecoveryAllowed(ctx, 7, "candidate"); allowed || !errors.Is(err, session.ErrEventIntegrity) {
		t.Fatalf("TEMP source shadow was trusted: %t %v", allowed, err)
	}
}
