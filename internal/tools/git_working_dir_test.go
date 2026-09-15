package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/sandbox"
)

type gitArgsCaptureRunner struct {
	request sandbox.Request
}

func (r *gitArgsCaptureRunner) Run(_ context.Context, request sandbox.Request) (sandbox.Result, error) {
	r.request = request
	return sandbox.Result{Output: "ok"}, nil
}

func TestGitSandboxUsesSessionWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	workingDir := filepath.Join(root, "nested", "repo")
	runner := &gitArgsCaptureRunner{}
	executor := NewExecutor(root)
	defer executor.Close()
	executor.SetSandbox(runner)
	if err := executor.SetWorkingDir(workingDir); err != nil {
		t.Fatalf("set working directory: %v", err)
	}

	result, err := executor.Execute(context.Background(), ToolRequest{
		Name: "Git", Arguments: map[string]any{"command": "status"},
	})
	if err != nil {
		t.Fatalf("Git: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("Git result error: %s", result.Error)
	}
	joined := strings.Join(runner.request.Args, "\x00")
	if !strings.Contains(joined, "-C\x00/workspace/nested/repo") {
		t.Fatalf("sandbox git args = %q, want session container directory", joined)
	}
	if runner.request.WorkingDir != workingDir {
		t.Fatalf("sandbox working directory = %q, want %q", runner.request.WorkingDir, workingDir)
	}
}
