package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestApplySingleFilePreservesDirtyBaselineAndIndex(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first exclusive application contract")
	}
	ctx := context.Background()
	root := taskRepository(t)
	writeBaselineFile(t, root, "main.go", "dirty starting content")
	writeBaselineFile(t, root, "other.go", "unrelated dirty content")
	manager := NewManager(root, "HEAD")
	baseline, err := manager.CaptureBaseline(ctx)
	if err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	intents := 0
	known, err := manager.ApplySingleFile(ctx, baseline, TaskFileChange{Path: "main.go", Before: "dirty starting content", After: "reviewed content", Operation: "Edit"}, func() error { intents++; return nil })
	if err != nil || !known || intents != 1 {
		t.Fatalf("apply: known=%v intents=%d err=%v", known, intents, err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if string(data) != "reviewed content" {
		t.Fatal("reviewed content was not applied")
	}
	other, _ := os.ReadFile(filepath.Join(root, "other.go"))
	afterIndex, _ := os.ReadFile(filepath.Join(root, ".git", "index"))
	if string(other) != "unrelated dirty content" || string(index) != string(afterIndex) {
		t.Fatal("application changed unrelated content or index")
	}
}

func TestApplySingleFileDistinguishesEmptyMissingAndConflicts(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first exclusive application contract")
	}
	for _, scenario := range []string{"empty", "missing", "content changed", "index changed", "intent refused", "hardlink", "concurrent writer"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			root := taskRepository(t)
			writeBaselineFile(t, root, "main.go", "")
			manager := NewManager(root, "HEAD")
			baseline, err := manager.CaptureBaseline(ctx)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "main.go")
			switch scenario {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "content changed":
				writeBaselineFile(t, root, "main.go", "user edit")
			case "index changed":
				baselineGit(t, root, "add", "main.go")
			case "hardlink":
				if err := os.Link(path, filepath.Join(t.TempDir(), "alias.go")); err != nil {
					t.Fatal(err)
				}
			}
			intents := 0
			known, err := manager.ApplySingleFile(ctx, baseline, TaskFileChange{Path: "main.go", Before: "", After: "approved", Operation: "Edit"}, func() error {
				intents++
				if scenario == "intent refused" {
					return errors.New("CAS refused")
				}
				if scenario == "concurrent writer" && os.WriteFile(path, []byte("racing editor"), 0600) == nil {
					t.Fatal("exclusive application allowed a concurrent writer")
				}
				return nil
			})
			data, readErr := os.ReadFile(path)
			if scenario == "empty" || scenario == "concurrent writer" {
				if err != nil || !known || intents != 1 || string(data) != "approved" {
					t.Fatalf("existing empty file: %v %v %d %q", known, err, intents, data)
				}
			} else {
				if err == nil || !known {
					t.Fatal("conflict accepted or reported as a write")
				}
				if scenario == "missing" && !os.IsNotExist(readErr) {
					t.Fatal("missing file was recreated")
				}
				if scenario == "content changed" && string(data) != "user edit" {
					t.Fatal("user edit was overwritten")
				}
				if scenario != "missing" && scenario != "content changed" && len(data) != 0 {
					t.Fatal("refused intent changed content")
				}
			}
		})
	}
}
