package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"code-agent/internal/session"
)

func TestEvaluationMemoryWritesAreScopedToTrialRoot(t *testing.T) {
	ctx := context.Background()
	ledger := openMemoryLedger(t)
	workbench := session.NewWorkbench(ledger, nil)
	directory := t.TempDir()
	module := NewLedgerMemory(ledger)
	ids := []string{}
	entries := []Experience{}
	for _, anchor := range []string{"FIRST_TRIAL_ANCHOR", "SECOND_TRIAL_ANCHOR"} {
		view, err := workbench.CreateWithWorkingDir(ctx, 7, "same-source", "trial", "", directory)
		if err != nil {
			t.Fatal(err)
		}
		policy := session.EvaluationPolicy{ContextStrategy: "metadata_on_demand", ExecutionStrategy: "single", MaxToolRounds: 4, ContextMaxBytes: 12000}
		if err := session.BindEvaluation(ctx, ledger, view.ID, session.EvaluationBinding{MemoryNamespace: view.ID, SchemaVersion: 1, RunID: anchor, TaskID: "same-task", Workspace: directory, Policy: policy}); err != nil {
			t.Fatal(err)
		}
		if _, err := workbench.AppendUserMessage(ctx, 7, view.ID, 2, anchor); err != nil {
			t.Fatal(err)
		}
		body, err := module.Manage(ctx, view.ID, session.MemoryCommand{Action: "remember", Kind: "patterns", Key: "shared-key", Content: anchor})
		if err != nil {
			t.Fatal(err)
		}
		var entry Experience
		if json.Unmarshal([]byte(body), &entry) != nil {
			t.Fatal("invalid retained experience")
		}
		ids = append(ids, view.ID)
		entries = append(entries, entry)
	}
	if entries[0].ID == entries[1].ID || entries[0].Repository == entries[1].Repository {
		t.Fatal("evaluation trials shared a mutable repository catalog key")
	}
	for index, id := range ids {
		body, err := module.Manage(ctx, id, session.MemoryCommand{Action: "list"})
		if err != nil || !strings.Contains(body, entries[index].ID) || strings.Contains(body, entries[1-index].ID) {
			t.Fatalf("trial read another trial's live catalog: %v", err)
		}
		if _, err := module.Manage(ctx, id, session.MemoryCommand{Action: "read", ID: entries[1-index].ID}); err == nil {
			t.Fatal("cross-trial memory read was accepted")
		}
	}
}
