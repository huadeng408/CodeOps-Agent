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

func TestFileToolsResolveRelativePathsFromWorkingDir(t *testing.T) {
	root := t.TempDir()
	workingDir := filepath.Join(root, "nested", "repo")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatalf("create working directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("nested repository"), 0o644); err != nil {
		t.Fatalf("write nested fixture: %v", err)
	}
	executor := NewExecutor(root)
	defer executor.Close()
	if err := executor.SetWorkingDir(workingDir); err != nil {
		t.Fatalf("set working directory: %v", err)
	}

	read, err := executor.Execute(context.Background(), ToolRequest{
		Name: "Read", Arguments: map[string]any{"path": "README.md"},
	})
	if err != nil {
		t.Fatalf("Read from working directory: %v", err)
	}
	if !strings.Contains(read.Output, "nested repository") {
		t.Fatalf("Read output = %q, want nested fixture", read.Output)
	}

	if _, err := executor.Execute(context.Background(), ToolRequest{
		Name: "Write", Arguments: map[string]any{"path": "generated.txt", "content": "created in nested repo"},
	}); err != nil {
		t.Fatalf("Write from working directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workingDir, "generated.txt")); err != nil {
		t.Fatalf("nested write missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "generated.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("write escaped working directory: %v", err)
	}

	glob, err := executor.Execute(context.Background(), ToolRequest{
		Name: "Glob", Arguments: map[string]any{"pattern": "*.md"},
	})
	if err != nil {
		t.Fatalf("Glob from working directory: %v", err)
	}
	if strings.TrimSpace(glob.Output) != "README.md" {
		t.Fatalf("Glob output = %q, want README.md from session directory", glob.Output)
	}

	grep, err := executor.Execute(context.Background(), ToolRequest{
		Name: "Grep", Arguments: map[string]any{"pattern": "nested repository", "path": ".", "output_mode": "content"},
	})
	if err != nil {
		t.Fatalf("Grep from working directory: %v", err)
	}
	if !strings.Contains(grep.Output, "README.md:1:nested repository") {
		t.Fatalf("Grep output = %q, want nested repository match", grep.Output)
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

func TestNotebookEditRejectsSymlinkAliasInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	realPath := filepath.Join(root, "real.ipynb")
	original := `{"cells":[{"cell_type":"code","id":"cell-1","source":["before\n"],"metadata":{},"outputs":[],"execution_count":null}],"metadata":{},"nbformat":4,"nbformat_minor":5}`
	if err := os.WriteFile(realPath, []byte(original), 0o644); err != nil {
		t.Fatalf("write notebook fixture: %v", err)
	}
	alias := filepath.Join(root, "alias.ipynb")
	if err := os.Symlink(realPath, alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating Windows symlink requires an enabled symlink policy: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}

	_, err := NewExecutor(root).Execute(context.Background(), ToolRequest{
		Name: "NotebookEdit",
		Arguments: map[string]any{
			"path":       "alias.ipynb",
			"edit_mode":  "replace",
			"cell_index": 0,
			"source":     "blocked\n",
		},
	})
	if err == nil {
		t.Fatal("NotebookEdit through an in-workspace symlink unexpectedly succeeded")
	}
	data, readErr := os.ReadFile(realPath)
	if readErr != nil {
		t.Fatalf("read notebook after rejected edit: %v", readErr)
	}
	if string(data) != original {
		t.Fatalf("symlink target changed after rejected NotebookEdit: %q", string(data))
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
