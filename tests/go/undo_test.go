package codeagent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/undo"
)

func TestUndoManagerRestoreAndListCloneEntries(t *testing.T) {
	manager := undo.NewManager()
	manager.Restore([]undo.Entry{{
		ID:          "undo-1",
		Description: "Write",
		Changes: []undo.Change{{
			Path:   "notes/demo.txt",
			Before: "before",
			After:  "after",
		}},
	}})

	entries := manager.List()
	if len(entries) != 1 || entries[0].Changes[0].Before != "before" {
		t.Fatalf("unexpected restored undo entries: %#v", entries)
	}
	entries[0].Changes[0].Before = "mutated"

	again := manager.List()
	if got := again[0].Changes[0].Before; got != "before" {
		t.Fatalf("undo list leaked mutable changes, got %q", got)
	}
}

func TestApplyEntryRestoresAndDeletesFiles(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "notes", "demo.txt")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(root, "notes", "new.txt")
	if err := os.WriteFile(created, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := undo.ApplyEntry(root, undo.Entry{
		Description: "Write",
		Changes: []undo.Change{
			{Path: "notes/demo.txt", Before: "before", After: "after"},
			{Path: "notes/new.txt", Before: "", After: "new"},
		},
	})
	if err != nil {
		t.Fatalf("apply undo: %v", err)
	}
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before" {
		t.Fatalf("expected file restore, got %q", string(data))
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("expected created file to be removed, stat err=%v", err)
	}
}

func TestApplyEntryRejectsWorkspaceEscape(t *testing.T) {
	err := undo.ApplyEntry(t.TempDir(), undo.Entry{
		Changes: []undo.Change{{Path: "../outside.txt", Before: "x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "path escapes workspace") {
		t.Fatalf("expected workspace escape error, got %v", err)
	}
}

func TestApplyEntryValidatesAllPathsBeforeMutating(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "safe.txt")
	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := undo.ApplyEntry(root, undo.Entry{Changes: []undo.Change{
		{Path: "../outside.txt", Before: "unsafe"},
		{Path: "safe.txt", Before: "before"},
	}})
	if err == nil {
		t.Fatal("expected invalid path error")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "after" {
		t.Fatalf("failed validation mutated file: %q", data)
	}
}
