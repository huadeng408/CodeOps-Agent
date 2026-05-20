package codeagent_test

import (
	"context"
	"path/filepath"
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
