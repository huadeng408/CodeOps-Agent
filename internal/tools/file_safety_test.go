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

func TestFileToolsEditLFExcerptInCRLFFile(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(root)
	defer executor.Close()
	path := filepath.Join(root, "source.go")
	before := "// untouched\r\nfunc value() int {\r\n\treturn 1\r\n}\r\n// tail\r\n"
	want := "// untouched\r\nfunc value() int {\r\n\treturn 2\r\n}\r\n// tail\r\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	read, err := executor.Execute(context.Background(), ToolRequest{
		Name: "Read", Arguments: map[string]any{"path": "source.go"},
	})
	if err != nil || !strings.Contains(read.Output, "return 1") || strings.Contains(read.Output, "\r") {
		t.Fatalf("Read should expose the LF excerpt: output=%q, error=%v", read.Output, err)
	}
	edited, err := executor.Execute(context.Background(), ToolRequest{
		Name: "Edit", Arguments: map[string]any{
			"path": "source.go",
			"old":  "func value() int {\n\treturn 1\n}",
			"new":  "func value() int {\n\treturn 2\n}",
		},
	})
	if err != nil {
		t.Fatalf("Edit the displayed LF excerpt: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("Edit should preserve CRLF and outside content: data=%q, error=%v", data, err)
	}
	if len(edited.Changes) != 1 || edited.Changes[0].Before != before || edited.Changes[0].After != want {
		t.Fatalf("Edit changes must retain the exact file bytes: %+v", edited.Changes)
	}
	finalInfo, err := os.Stat(path)
	if err != nil || finalInfo.Mode().Perm() != originalInfo.Mode().Perm() {
		t.Fatalf("Edit changed file permissions: %v", err)
	}
}

func TestFileToolsEditNewlineMappingKeepsExactMatchGuards(t *testing.T) {
	for _, tc := range []struct {
		name       string
		before     string
		old        string
		new        string
		want       string
		replaceAll bool
		wantErr    bool
	}{
		{
			name: "LF exact match", before: "one\ntwo\n", old: "one\ntwo", new: "ONE\nTWO",
			want: "ONE\nTWO\n",
		},
		{
			name: "explicit CRLF exact match", before: "one\r\ntwo\r\n", old: "one\r\ntwo", new: "ONE\r\nTWO",
			want: "ONE\r\nTWO\r\n",
		},
		{
			name: "explicit replacement CRLF is not doubled", before: "one\r\ntwo\r\n", old: "one\ntwo", new: "ONE\r\nTWO",
			want: "ONE\r\nTWO\r\n",
		},
		{
			name: "final newline remains absent", before: "one\r\ntwo", old: "one\ntwo", new: "ONE\nTWO",
			want: "ONE\r\nTWO",
		},
		{
			name: "nonunique excerpt refuses", before: "one\r\ntwo\r\none\r\ntwo\r\n", old: "one\ntwo", new: "ONE\nTWO",
			wantErr: true,
		},
		{
			name: "explicit replace all", before: "one\r\ntwo\r\none\r\ntwo\r\n", old: "one\ntwo", new: "ONE\nTWO",
			want: "ONE\r\nTWO\r\nONE\r\nTWO\r\n", replaceAll: true,
		},
		{
			name: "changed indentation refuses", before: "one\r\n\ttwo\r\n", old: "one\n  two", new: "ONE\nTWO",
			wantErr: true,
		},
		{
			name: "mixed newlines have no inferred mapping", before: "one\r\ntwo\nthree\r\n", old: "one\ntwo", new: "ONE\nTWO",
			wantErr: true,
		},
		{
			name: "standalone CR has no inferred mapping", before: "head\rone\r\ntwo\r\n", old: "one\ntwo", new: "ONE\nTWO",
			wantErr: true,
		},
		{
			name: "raw exact match in mixed file wins", before: "head\r\none\ntwo\nfoot\r\n", old: "one\ntwo", new: "ONE\nTWO",
			want: "head\r\nONE\nTWO\nfoot\r\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			executor := NewExecutor(root)
			defer executor.Close()
			path := filepath.Join(root, "file.txt")
			if err := os.WriteFile(path, []byte(tc.before), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := executor.Execute(context.Background(), ToolRequest{
				Name: "Edit", Arguments: map[string]any{
					"path": "file.txt", "old": tc.old, "new": tc.new, "replace_all": tc.replaceAll,
				},
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("Edit error=%v, wantErr=%t", err, tc.wantErr)
			}
			want := tc.want
			if tc.wantErr {
				want = tc.before
				if len(result.Changes) != 0 {
					t.Fatalf("refused edit reported changes: %+v", result.Changes)
				}
			} else if len(result.Changes) != 1 || result.Changes[0].Before != tc.before || result.Changes[0].After != want {
				t.Fatalf("Edit changes differ from file bytes: %+v", result.Changes)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != want {
				t.Fatalf("file bytes=%q, want=%q, error=%v", data, want, err)
			}
		})
	}
}

func TestFileToolsNormalizeAbsoluteChangePaths(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(root)
	path := filepath.Join(root, "nested", "file.txt")

	written, err := executor.Execute(context.Background(), ToolRequest{
		Name: "Write", Arguments: map[string]any{"path": path, "content": "before"},
	})
	if err != nil {
		t.Fatalf("absolute Write: %v", err)
	}
	if len(written.Changes) != 1 || written.Changes[0].Path != "nested/file.txt" {
		t.Fatalf("absolute Write changes = %+v", written.Changes)
	}

	edited, err := executor.Execute(context.Background(), ToolRequest{
		Name: "Edit", Arguments: map[string]any{"path": path, "old": "before", "new": "after"},
	})
	if err != nil {
		t.Fatalf("absolute Edit: %v", err)
	}
	if len(edited.Changes) != 1 || edited.Changes[0].Path != "nested/file.txt" {
		t.Fatalf("absolute Edit changes = %+v", edited.Changes)
	}

	notebookPath := filepath.Join(root, "nested", "analysis.ipynb")
	notebook := `{"cells":[{"cell_type":"code","id":"cell-1","source":["before\n"],"metadata":{},"outputs":[],"execution_count":null}],"metadata":{},"nbformat":4,"nbformat_minor":5}`
	if err := os.WriteFile(notebookPath, []byte(notebook), 0o644); err != nil {
		t.Fatalf("write notebook fixture: %v", err)
	}
	notebookResult, err := executor.Execute(context.Background(), ToolRequest{
		Name: "NotebookEdit", Arguments: map[string]any{
			"path": notebookPath, "edit_mode": "replace", "cell_index": 0, "source": "after\n",
		},
	})
	if err != nil {
		t.Fatalf("absolute NotebookEdit: %v", err)
	}
	if len(notebookResult.Changes) != 1 || notebookResult.Changes[0].Path != "nested/analysis.ipynb" {
		t.Fatalf("absolute NotebookEdit changes = %+v", notebookResult.Changes)
	}

	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if _, err := executor.Execute(context.Background(), ToolRequest{
		Name: "Write", Arguments: map[string]any{"path": outside, "content": "blocked"},
	}); err == nil {
		t.Fatal("absolute path outside workspace unexpectedly succeeded")
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
