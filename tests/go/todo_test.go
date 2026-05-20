package codeagent_test

import (
	"strings"
	"testing"

	"code-agent/internal/todo"
)

func TestTodoManagerNormalizesAndFormatsTasks(t *testing.T) {
	manager := todo.NewManager()
	manager.Update([]todo.Item{
		{Content: "Draft plan", ActiveForm: "drafting plan", Status: "in_progress"},
		{Content: "Review plan", ActiveForm: "reviewing plan", Status: "in_progress"},
		{Content: "Ship plan", ActiveForm: "", Status: "completed"},
	})

	items := manager.Snapshot()
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[1].Status != string(todo.StatusPending) {
		t.Fatalf("expected second in-progress item to be normalized to pending, got %q", items[1].Status)
	}
	if items[2].ActiveForm != "Ship plan" {
		t.Fatalf("expected active form to default to content, got %q", items[2].ActiveForm)
	}

	lines := manager.Lines()
	want := []string{"1. [~] drafting plan", "2. [ ] reviewing plan", "3. [x] Ship plan"}
	for _, fragment := range want {
		if !strings.Contains(strings.Join(lines, "\n"), fragment) {
			t.Fatalf("task lines missing %q: %#v", fragment, lines)
		}
	}
}

func TestTodoManagerShowsEmptyState(t *testing.T) {
	manager := todo.NewManager()
	lines := manager.Lines()
	if len(lines) != 1 || lines[0] != "no active tasks" {
		t.Fatalf("unexpected empty state: %#v", lines)
	}
}
