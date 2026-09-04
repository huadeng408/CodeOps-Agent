package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileToolsRejectWorkspaceTraversalAndSymlinkWrites(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	executor := NewExecutor(root)

	outsideFile := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}

	for _, tc := range []struct {
		name string
		req  ToolRequest
	}{
		{
			name: "read traversal",
			req:  ToolRequest{Name: "Read", Arguments: map[string]any{"path": filepath.Join("..", filepath.Base(outside), "outside.txt")}},
		},
		{
			name: "write traversal",
			req:  ToolRequest{Name: "Write", Arguments: map[string]any{"path": filepath.Join("..", filepath.Base(outside), "new.txt"), "content": "blocked"}},
		},
		{
			name: "edit traversal",
			req:  ToolRequest{Name: "Edit", Arguments: map[string]any{"path": filepath.Join("..", filepath.Base(outside), "outside.txt"), "old": "outside", "new": "blocked"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := executor.Execute(context.Background(), tc.req); err == nil {
				t.Fatalf("%s unexpectedly succeeded", tc.name)
			}
		})
	}

	fileLink := filepath.Join(root, "file-link.txt")
	if err := os.Symlink(outsideFile, fileLink); err != nil {
		if runtime.GOOS == "windows" {
			t.Logf("creating Windows symlink requires an enabled symlink policy; skipping file-link checks: %v", err)
		} else {
			t.Fatalf("create file symlink: %v", err)
		}
	} else {
		for _, name := range []string{"Read", "Write", "Edit"} {
			t.Run("file symlink "+name, func(t *testing.T) {
				args := map[string]any{"path": "file-link.txt"}
				switch name {
				case "Write":
					args["content"] = "blocked"
				case "Edit":
					args["old"] = "outside"
					args["new"] = "blocked"
				}
				if _, err := executor.Execute(context.Background(), ToolRequest{Name: name, Arguments: args}); err == nil {
					t.Fatalf("%s through symlink unexpectedly succeeded", name)
				}
			})
		}
	}

	parentLink := filepath.Join(root, "parent-link")
	if err := os.Symlink(outside, parentLink); err != nil {
		if runtime.GOOS == "windows" {
			t.Logf("creating Windows symlink requires an enabled symlink policy; skipping parent-link checks: %v", err)
			return
		}
		t.Fatalf("create parent symlink: %v", err)
	}
	if _, err := executor.Execute(context.Background(), ToolRequest{
		Name:      "Write",
		Arguments: map[string]any{"path": filepath.Join("parent-link", "created.txt"), "content": "blocked"},
	}); err == nil {
		t.Fatal("write through symlinked parent unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "created.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlinked parent was modified: %v", err)
	}
}

func TestFileToolsWriteAndEditProduceCompleteFileAndChanges(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(root)
	path := filepath.Join("nested", "file.txt")

	written, err := executor.Execute(context.Background(), ToolRequest{
		Name:      "Write",
		Arguments: map[string]any{"path": path, "content": strings.Repeat("new-content\n", 1024)},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(written.Changes) != 1 || written.Changes[0].Path != filepath.ToSlash(path) {
		t.Fatalf("Write changes = %+v", written.Changes)
	}
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != written.Changes[0].After {
		t.Fatalf("written file differs from reported change: got %d bytes, want %d", len(data), len(written.Changes[0].After))
	}

	edited, err := executor.Execute(context.Background(), ToolRequest{
		Name:      "Edit",
		Arguments: map[string]any{"path": path, "old": "new-content", "new": "edited-content", "replace_all": true},
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	editedData, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if string(editedData) != edited.Changes[0].After || strings.Contains(string(editedData), "new-content") {
		t.Fatalf("edited file = %q", string(editedData))
	}
}

func TestFileToolsRejectSymlinkAliasInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	realPath := filepath.Join(root, "real.txt")
	if err := os.WriteFile(realPath, []byte("original"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	alias := filepath.Join(root, "alias.txt")
	if err := os.Symlink(realPath, alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating Windows symlink requires an enabled symlink policy: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}

	executor := NewExecutor(root)
	for _, req := range []ToolRequest{
		{Name: "Read", Arguments: map[string]any{"path": "alias.txt"}},
		{Name: "Write", Arguments: map[string]any{"path": "alias.txt", "content": "blocked"}},
		{Name: "Edit", Arguments: map[string]any{"path": "alias.txt", "old": "original", "new": "blocked"}},
	} {
		if _, err := executor.Execute(context.Background(), req); err == nil {
			t.Fatalf("%s through an in-workspace symlink unexpectedly succeeded", req.Name)
		}
	}
	data, err := os.ReadFile(realPath)
	if err != nil {
		t.Fatalf("read fixture after rejected calls: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("symlink target changed after rejected calls: %q", string(data))
	}
}

func TestFileToolsAtomicWritesLeaveNoTemporaryFiles(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(root)
	path := "atomic/file.txt"

	for _, content := range []string{"first\n", strings.Repeat("second\n", 2048)} {
		if _, err := executor.Execute(context.Background(), ToolRequest{
			Name:      "Write",
			Arguments: map[string]any{"path": path, "content": content},
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		entries, err := os.ReadDir(filepath.Join(root, "atomic"))
		if err != nil {
			t.Fatalf("read atomic directory: %v", err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".code-agent-write-") {
				t.Fatalf("temporary write file left behind: %s", entry.Name())
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatalf("read final file: %v", err)
	}
	if string(data) != strings.Repeat("second\n", 2048) {
		t.Fatalf("final file content was not atomically replaced")
	}
}
