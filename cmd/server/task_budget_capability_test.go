package main

import (
	"context"
	"path/filepath"
	"testing"

	"code-agent/internal/admission"
	"code-agent/internal/session"
)

func TestTaskBudgetCapabilityReadsAuthenticatedOwnersBatch(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if _, err := admission.NewBudget(ledger).Open(ctx, 7); err != nil {
		t.Fatal(err)
	}
	capabilities := map[string]CapabilityStatus{}
	addModelAdmissionCapabilities(ctx, ledger, capabilities, 7)
	if got := capabilities["budget"]; got.State != "ready" || got.Tokens == nil || got.Tokens.Used != 0 || got.Tokens.Reserved != 0 {
		t.Fatalf("owner's baseline budget was unavailable: %+v", got)
	}
}
