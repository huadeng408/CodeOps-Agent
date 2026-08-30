package tools

import (
	"context"
	"strings"
	"testing"

	"code-agent/internal/sandbox"
)

type recordingSandbox struct {
	requests []sandbox.Request
}

func (s *recordingSandbox) Run(_ context.Context, request sandbox.Request) (sandbox.Result, error) {
	s.requests = append(s.requests, request)
	return sandbox.Result{Output: "sandboxed output", ExitCode: 0}, nil
}

func TestExecutorBashUsesConfiguredSandboxInsteadOfHostShell(t *testing.T) {
	executor := NewExecutor(t.TempDir())
	runner := &recordingSandbox{}
	executor.SetSandbox(runner)

	result, err := executor.Execute(context.Background(), ToolRequest{
		Name:      "Bash",
		Arguments: map[string]any{"command": "this-command-must-not-run-on-the-host"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Output != "sandboxed output" || result.ExitCode != 0 {
		t.Fatalf("Execute() result = %+v, want sandbox result", result)
	}
	if len(runner.requests) != 1 || runner.requests[0].Command != "this-command-must-not-run-on-the-host" {
		t.Fatalf("sandbox requests = %#v", runner.requests)
	}
}

func TestExecutorGitUsesConfiguredSandboxWithoutShellConcatenation(t *testing.T) {
	executor := NewExecutor(t.TempDir())
	runner := &recordingSandbox{}
	executor.SetSandbox(runner)

	result, err := executor.Execute(context.Background(), ToolRequest{
		Name:      "Git",
		Arguments: map[string]any{"command": "status"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Output != "sandboxed output" || result.ExitCode != 0 {
		t.Fatalf("Execute() result = %+v, want sandbox result", result)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("sandbox requests = %#v", runner.requests)
	}
	request := runner.requests[0]
	if request.Program != "git" || len(request.Args) == 0 || request.Args[len(request.Args)-1] != "status" {
		t.Fatalf("git request = %#v", request)
	}
	if strings.Contains(request.Command, "git") {
		t.Fatalf("git must use program arguments, not a shell command: %#v", request)
	}
}
