package codeagent_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"code-agent/internal/tools"
)

func TestExecutorReadWriteAndGlob(t *testing.T) {
	root := t.TempDir()
	executor := tools.NewExecutor(root)

	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Write",
		Arguments: map[string]any{
			"path":    "notes/demo.txt",
			"content": "hello skeleton",
		},
	}); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	writeAgain, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Write",
		Arguments: map[string]any{
			"path":    "notes/demo.txt",
			"content": "hello updated",
		},
	})
	if err != nil {
		t.Fatalf("second write failed: %v", err)
	}
	if len(writeAgain.Changes) != 1 || writeAgain.Changes[0].Before != "hello skeleton" || writeAgain.Changes[0].After != "hello updated" {
		t.Fatalf("unexpected write changes: %#v", writeAgain.Changes)
	}

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Read",
		Arguments: map[string]any{
			"path": "notes/demo.txt",
		},
	})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(result.Output, "1\thello updated") {
		t.Fatalf("unexpected read output: %q", result.Output)
	}

	glob, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Glob",
		Arguments: map[string]any{
			"pattern": "**/*.txt",
		},
	})
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if !strings.Contains(glob.Output, filepath.ToSlash("notes/demo.txt")) {
		t.Fatalf("glob output missing file: %q", glob.Output)
	}
}

func TestExecutorReadSupportsLineRanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("alpha\nbeta\ngamma\ndelta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(root)

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Read",
		Arguments: map[string]any{
			"path":   "sample.txt",
			"offset": 1,
			"limit":  2,
		},
	})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(result.Output, "[Range read]") {
		t.Fatalf("range metadata missing: %q", result.Output)
	}
	if !strings.Contains(result.Output, "2\tbeta") || !strings.Contains(result.Output, "3\tgamma") {
		t.Fatalf("expected ranged lines with line numbers, got %q", result.Output)
	}
	if strings.Contains(result.Output, "1\talpha") || strings.Contains(result.Output, "4\tdelta") {
		t.Fatalf("range included unexpected lines: %q", result.Output)
	}
}

func TestExecutorReadRejectsInvalidArguments(t *testing.T) {
	executor := tools.NewExecutor(t.TempDir())

	if _, err := executor.Execute(context.Background(), tools.ToolRequest{Name: "Read", Arguments: map[string]any{}}); err == nil {
		t.Fatal("expected missing path to fail")
	}

	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Read",
		Arguments: map[string]any{"path": "../outside.txt"},
	}); err == nil || !strings.Contains(err.Error(), "path escapes workspace") {
		t.Fatalf("expected workspace escape failure, got %v", err)
	}

	if err := os.WriteFile(filepath.Join(executor.Root, "sample.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Read",
		Arguments: map[string]any{"path": "sample.txt", "limit": 0},
	}); err == nil || !strings.Contains(err.Error(), "limit must be positive") {
		t.Fatalf("expected invalid limit failure, got %v", err)
	}
}

func TestExecutorEditReturnsUndoChange(t *testing.T) {
	root := t.TempDir()
	executor := tools.NewExecutor(root)
	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Write",
		Arguments: map[string]any{
			"path":    "notes/demo.txt",
			"content": "alpha beta",
		},
	}); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Edit",
		Arguments: map[string]any{
			"path": "notes/demo.txt",
			"old":  "beta",
			"new":  "gamma",
		},
	})
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	if len(result.Changes) != 1 || result.Changes[0].Before != "alpha beta" || result.Changes[0].After != "alpha gamma" {
		t.Fatalf("unexpected edit changes: %#v", result.Changes)
	}
}

func TestExecutorBashRunsSafeCommand(t *testing.T) {
	root := t.TempDir()
	executor := tools.NewExecutor(root)
	command := "printf ok"
	if runtime.GOOS == "windows" {
		command = "Write-Output ok"
	}

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Bash",
		Arguments: map[string]any{
			"command": command,
		},
	})
	if err != nil {
		t.Fatalf("bash failed: %v output=%q", err, result.Output)
	}
	if !strings.Contains(result.Output, "ok") {
		t.Fatalf("unexpected bash output: %q", result.Output)
	}
}

func TestExecutorBashBlocksDangerousCommand(t *testing.T) {
	executor := tools.NewExecutor(t.TempDir())

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Bash",
		Arguments: map[string]any{
			"command": "rm -rf /",
		},
	})
	if err == nil {
		t.Fatal("expected dangerous command to be blocked")
	}
	if !strings.Contains(result.Error, "blocked command") {
		t.Fatalf("unexpected error: %q", result.Error)
	}
}

func TestExecutorBashPersistsWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(root)

	if result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Bash",
		Arguments: map[string]any{"command": "cd nested"},
	}); err != nil {
		t.Fatalf("cd failed: %v output=%q", err, result.Output)
	}
	if got := executor.WorkingDir(); got != filepath.Join(root, "nested") {
		t.Fatalf("unexpected working dir: %s", got)
	}

	command := "pwd"
	if runtime.GOOS == "windows" {
		command = "(Get-Location).Path"
	}
	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Bash",
		Arguments: map[string]any{"command": command},
	})
	if err != nil {
		t.Fatalf("pwd failed: %v output=%q", err, result.Output)
	}
	if !strings.Contains(filepath.Clean(result.Output), filepath.Join(root, "nested")) {
		t.Fatalf("bash did not use persisted working dir: %q", result.Output)
	}
}

func TestExecutorBashRejectsWorkingDirectoryEscape(t *testing.T) {
	executor := tools.NewExecutor(t.TempDir())

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Bash",
		Arguments: map[string]any{"command": "cd .."},
	})
	if err == nil {
		t.Fatal("expected cd outside workspace to fail")
	}
	if !strings.Contains(result.Error, "path escapes workspace") {
		t.Fatalf("unexpected error: %q", result.Error)
	}
}
