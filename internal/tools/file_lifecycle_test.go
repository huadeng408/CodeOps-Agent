package tools_test

import (
	"code-agent/internal/tools"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteReceiptDistinguishesExistingEmptyAndNewEmpty(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(root)
	defer executor.Close()
	for _, name := range []string{"existing.txt", "new.txt"} {
		result, err := executor.Execute(context.Background(), tools.ToolRequest{Name: "Write", Arguments: map[string]any{"path": name, "content": ""}})
		if err != nil || result.ExitCode != 0 || len(result.Changes) != 1 {
			t.Fatalf("Write: %+v %v", result, err)
		}
		change := result.Changes[0]
		if change.BeforeExists == nil || *change.BeforeExists != (name == "existing.txt") || change.AfterExists == nil || !*change.AfterExists || change.Before != "" || change.After != "" {
			t.Fatal("empty bytes hid file existence")
		}
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal("empty Write removed the file")
		}
	}
}

func TestDeleteEmptyAndRenameKeepExplicitLifecycleReceipts(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first exclusive file lifecycle")
	}
	root := t.TempDir()
	for name, content := range map[string]string{"empty.txt": "", "source.txt": "rename content"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	executor := tools.NewExecutor(root)
	defer executor.Close()
	deleted, err := executor.Execute(context.Background(), tools.ToolRequest{Name: "Delete", Arguments: map[string]any{"path": "empty.txt"}})
	if err != nil || deleted.ExitCode != 0 || len(deleted.Changes) != 1 {
		t.Fatalf("Delete: %+v %v", deleted, err)
	}
	change := deleted.Changes[0]
	if change.BeforeExists == nil || !*change.BeforeExists || change.AfterExists == nil || *change.AfterExists || change.Before != "" {
		t.Fatal("empty deletion was not explicit")
	}
	if _, err := os.Stat(filepath.Join(root, "empty.txt")); !os.IsNotExist(err) {
		t.Fatal("Delete left the empty file")
	}
	renamed, err := executor.Execute(context.Background(), tools.ToolRequest{Name: "Rename", Arguments: map[string]any{"path": "source.txt", "destination": "target.txt"}})
	if err != nil || renamed.ExitCode != 0 || len(renamed.Changes) != 1 {
		t.Fatalf("Rename: %+v %v", renamed, err)
	}
	change = renamed.Changes[0]
	if change.Path != "source.txt" || change.Destination != "target.txt" || change.Before != "rename content" || change.After != "rename content" || change.BeforeExists == nil || !*change.BeforeExists || change.AfterExists == nil || !*change.AfterExists {
		t.Fatal("rename lost source/target identity")
	}
	if _, err := os.Stat(filepath.Join(root, "source.txt")); !os.IsNotExist(err) {
		t.Fatal("rename kept source")
	}
	data, _ := os.ReadFile(filepath.Join(root, "target.txt"))
	if string(data) != "rename content" {
		t.Fatal("rename changed bytes")
	}
}

func TestFileLifecycleRefusesEscapesAliasesMissingAndOccupiedTargets(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first exclusive file lifecycle")
	}
	root := t.TempDir()
	for _, name := range []string{"source.txt", "occupied.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(filepath.Join(root, "source.txt"), filepath.Join(root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(root)
	defer executor.Close()
	for _, request := range []tools.ToolRequest{
		{Name: "Delete", Arguments: map[string]any{"path": "../source.txt"}},
		{Name: "Delete", Arguments: map[string]any{"path": "missing.txt"}},
		{Name: "Delete", Arguments: map[string]any{"path": "alias.txt"}},
		{Name: "Rename", Arguments: map[string]any{"path": "source.txt", "destination": "../escaped.txt"}},
		{Name: "Rename", Arguments: map[string]any{"path": "source.txt", "destination": "occupied.txt"}},
		{Name: "Rename", Arguments: map[string]any{"path": "alias.txt", "destination": "target.txt"}},
	} {
		result, err := executor.Execute(context.Background(), request)
		if err == nil || result.ExitCode == 0 || len(result.Changes) != 0 || result.OutcomeUnknown {
			t.Fatalf("unsafe lifecycle not refused before effects: %+v %v", result, err)
		}
	}
	for _, name := range []string{"source.txt", "occupied.txt", "alias.txt"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		want := name
		if name == "alias.txt" {
			want = "source.txt"
		}
		if err != nil || string(data) != want {
			t.Fatal("refused lifecycle changed existing bytes")
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "target.txt")); !os.IsNotExist(err) {
		t.Fatal("refused rename created a target")
	}
}
