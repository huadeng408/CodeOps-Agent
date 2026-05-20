package codeagent_test

import (
	"context"
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

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Read",
		Arguments: map[string]any{
			"path": "notes/demo.txt",
		},
	})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(result.Output, "hello skeleton") {
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
