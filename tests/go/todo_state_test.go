package codeagent_test

import (
	"context"
	"testing"

	"code-agent/internal/session"
)

func TestSessionAppliesPlanTodoStateAtomicallyWithMonotonicRevision(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	created := manager.NewSession(".")

	updated, ok := manager.ApplyPlanTodoState(
		session.PlanState{Steps: []string{"inspect", "patch"}, CurrentIndex: 0, Mode: "plan"},
		[]session.TodoItem{{Content: "inspect", Status: "in_progress"}},
		1,
	)
	if !ok {
		t.Fatal("expected first state revision to commit")
	}
	if updated.ID != created.ID || updated.PlanTodoRevision != 1 {
		t.Fatalf("unexpected committed state: %+v", updated)
	}

	stale, ok := manager.ApplyPlanTodoState(
		session.PlanState{Steps: []string{"stale"}, CurrentIndex: 0, Mode: "plan"},
		nil,
		1,
	)
	if ok {
		t.Fatal("expected stale revision to be rejected")
	}
	if stale.PlanTodoRevision != 1 || len(stale.Plan.Steps) != 2 {
		t.Fatalf("stale write changed session: %+v", stale)
	}
}

func TestSessionPlanTodoStateSurvivesEventStoreReload(t *testing.T) {
	dbPath := t.TempDir() + "/session.sqlite"
	store := session.NewSQLiteEventStore(dbPath)
	manager := session.NewManager(store)
	created := manager.NewSession(".")
	if _, ok := manager.ApplyPlanTodoState(
		session.PlanState{Steps: []string{"inspect"}, Mode: "plan"},
		[]session.TodoItem{{Content: "inspect", Status: "completed"}},
		1,
	); !ok {
		t.Fatal("expected state commit")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := session.NewSQLiteEventStore(dbPath)
	reloaded := session.NewManager(reopened)
	current, err := reloaded.Resume(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.PlanTodoRevision != 1 || current.Plan.Mode != "plan" || len(current.Todos) != 1 {
		t.Fatalf("state did not survive reload: %+v", current)
	}
	_ = reloaded.Close()
}
