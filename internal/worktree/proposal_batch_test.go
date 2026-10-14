package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestApplyFilesPreflightsAllAndRecordsEachLifecycle(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first exclusive application")
	}
	ctx := context.Background()
	root := taskRepository(t)
	for path, content := range map[string]string{"main.go": "dirty", "empty.txt": "", "old.txt": "rename bytes"} {
		writeBaselineFile(t, root, path, content)
	}
	manager := NewManager(root, "HEAD")
	baseline, err := manager.CaptureBaseline(ctx)
	if err != nil {
		t.Fatal(err)
	}
	index, _ := os.ReadFile(filepath.Join(root, ".git", "index"))
	operations := []FileApplication{
		{Path: "main.go", Operation: "edit", BeforeExists: true, AfterExists: true, Before: "dirty", After: "reviewed", BeforeMode: 0666, AfterMode: 0666},
		{Path: "nested/new.txt", Operation: "create", AfterExists: true, After: "", AfterMode: 0666},
		{Path: "empty.txt", Operation: "delete", BeforeExists: true, BeforeMode: 0666},
		{Path: "old.txt", Destination: "moved.txt", Operation: "rename", BeforeExists: true, AfterExists: true, Before: "rename bytes", After: "rename bytes", BeforeMode: 0666, AfterMode: 0666},
	}
	intents, outcomes := []int{}, []int{}
	results, err := manager.ApplyFiles(ctx, baseline, operations, func(index int, outcome *FileApplicationResult) error {
		if outcome == nil {
			intents = append(intents, index)
		} else {
			outcomes = append(outcomes, index)
			if outcome.State != "applied" {
				t.Fatalf("item: %+v", outcome)
			}
		}
		return nil
	})
	if err != nil || len(results) != 4 || len(intents) != 4 || len(outcomes) != 4 {
		t.Fatalf("batch: %+v %v", results, err)
	}
	for i, result := range results {
		if result.State != "applied" || intents[i] != i || outcomes[i] != i {
			t.Fatal("batch did not keep item outcomes")
		}
	}
	data, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if string(data) != "reviewed" {
		t.Fatal("edit not applied")
	}
	if data, err := os.ReadFile(filepath.Join(root, "nested", "new.txt")); err != nil || len(data) != 0 {
		t.Fatal("new empty file missing")
	}
	if _, err := os.Stat(filepath.Join(root, "empty.txt")); !os.IsNotExist(err) {
		t.Fatal("empty file not deleted")
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(err) {
		t.Fatal("rename source remained")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "moved.txt")); string(data) != "rename bytes" {
		t.Fatal("rename changed content")
	}
	afterIndex, _ := os.ReadFile(filepath.Join(root, ".git", "index"))
	if string(index) != string(afterIndex) {
		t.Fatal("batch changed index")
	}
}

func TestApplyFilesRefusesKnownConflictsBeforeAnyEffect(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first exclusive application")
	}
	for _, scenario := range []string{"later source changed", "rename target exists"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			root := taskRepository(t)
			writeBaselineFile(t, root, "a.txt", "original a")
			writeBaselineFile(t, root, "b.txt", "original b")
			if scenario == "rename target exists" {
				writeBaselineFile(t, root, "target.txt", "user target")
			}
			manager := NewManager(root, "HEAD")
			baseline, err := manager.CaptureBaseline(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "later source changed" {
				writeBaselineFile(t, root, "b.txt", "later user edit")
			}
			second := FileApplication{Path: "b.txt", Operation: "edit", BeforeExists: true, AfterExists: true, Before: "original b", After: "approved b", BeforeMode: 0666, AfterMode: 0666}
			if scenario == "rename target exists" {
				second.Operation = "rename"
				second.Destination = "target.txt"
				second.After = "original b"
			}
			calls := 0
			_, err = manager.ApplyFiles(ctx, baseline, []FileApplication{{Path: "a.txt", Operation: "edit", BeforeExists: true, AfterExists: true, Before: "original a", After: "approved a", BeforeMode: 0666, AfterMode: 0666}, second}, func(int, *FileApplicationResult) error { calls++; return nil })
			data, _ := os.ReadFile(filepath.Join(root, "a.txt"))
			if err == nil || calls != 0 || string(data) != "original a" {
				t.Fatal("known later conflict allowed an earlier effect")
			}
		})
	}
}

func TestApplyFilesStopsAfterUnpersistedOutcomeAndRetainsDenominator(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first exclusive application")
	}
	ctx := context.Background()
	root := taskRepository(t)
	for _, name := range []string{"a.txt", "b.txt"} {
		writeBaselineFile(t, root, name, "original")
	}
	manager := NewManager(root, "HEAD")
	baseline, err := manager.CaptureBaseline(ctx)
	if err != nil {
		t.Fatal(err)
	}
	intents := 0
	results, err := manager.ApplyFiles(ctx, baseline, []FileApplication{{Path: "a.txt", Operation: "edit", BeforeExists: true, AfterExists: true, Before: "original", After: "approved", BeforeMode: 0666, AfterMode: 0666}, {Path: "b.txt", Operation: "edit", BeforeExists: true, AfterExists: true, Before: "original", After: "approved", BeforeMode: 0666, AfterMode: 0666}}, func(_ int, result *FileApplicationResult) error {
		if result == nil {
			intents++
			return nil
		}
		return errors.New("outcome unavailable")
	})
	if err == nil || len(results) != 2 || results[0].State != "unknown" || results[1].State != "not_started" || intents != 1 {
		t.Fatalf("lost outcome: %+v %v", results, err)
	}
	a, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	b, _ := os.ReadFile(filepath.Join(root, "b.txt"))
	if string(a) != "approved" || string(b) != "original" {
		t.Fatal("unknown outcome caused another effect")
	}
}
