package cli

import (
	"testing"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/session"
	"code-agent/internal/todo"
)

func TestHandlePlanTodoUpdateUsesAtomicRevisionProjection(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	manager.NewSession(".")
	app := &App{session: manager, todos: todo.NewManager()}

	if err := app.handlePlanTodoUpdate(nil, &codeagentpb.TodoUpdate{
		Revision: 1,
		Todos:    []*codeagentpb.TodoItem{{Content: "patch", ActiveForm: "patching", Status: "in_progress"}},
	}); err != nil {
		t.Fatalf("todo update failed: %v", err)
	}
	if err := app.handlePlanTodoUpdate(&codeagentpb.PlanUpdate{
		Revision:     2,
		Steps:        []string{"inspect", "patch"},
		CurrentIndex: 1,
		Mode:         "plan",
	}, nil); err != nil {
		t.Fatalf("plan update failed: %v", err)
	}
	current := manager.Current()
	if current.PlanTodoRevision != 2 || current.Plan.CurrentIndex != 1 || len(current.Todos) != 1 {
		t.Fatalf("unexpected projection: %+v", current)
	}
	if err := app.handlePlanTodoUpdate(nil, &codeagentpb.TodoUpdate{Revision: 2}); err == nil {
		t.Fatal("expected stale revision rejection")
	}
}

func TestPlanTodoSnapshotIncludesCurrentRevisionAndWholeProjection(t *testing.T) {
	current := session.Session{
		PlanTodoRevision: 4,
		Plan:             session.PlanState{Steps: []string{"inspect"}, CurrentIndex: 0, Mode: "plan"},
		Todos:            []session.TodoItem{{Content: "inspect", Status: "completed"}},
	}
	snapshot := planTodoSnapshot(current)
	if snapshot.GetRevision() != 4 || snapshot.GetPlan().GetRevision() != 4 || snapshot.GetTodos()[0].GetStatus() != "completed" {
		t.Fatalf("snapshot lost current projection: %+v", snapshot)
	}
}
