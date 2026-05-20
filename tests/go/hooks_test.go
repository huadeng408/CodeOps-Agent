package codeagent_test

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"code-agent/internal/hooks"
)

func TestCommandHookMatchesAndExpandsContext(t *testing.T) {
	engine := hooks.NewEngine()
	command := "printf '%s' '${TOOL_NAME}|${FILE_PATH}|${TOOL_PARAMS}'"
	if runtime.GOOS == "windows" {
		command = "Write-Output '${TOOL_NAME}|${FILE_PATH}|${TOOL_PARAMS}'"
	}
	engine.RegisterCommandHook(hooks.CommandHook{
		Phase:   hooks.PhasePreTool,
		Matcher: "Edit",
		Command: command,
	})

	results, err := engine.Run(context.Background(), hooks.PhasePreTool, hooks.Context{
		SessionID: "session-1",
		ToolName:  "Edit",
		Payload: map[string]any{
			"file_path": "src/main.go",
			"mode":      "check",
		},
	})
	if err != nil {
		t.Fatalf("hook failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one hook result, got %d", len(results))
	}
	output := results[0].Message
	for _, want := range []string{"Edit", "src/main.go", `"file_path":"src/main.go"`, `"mode":"check"`} {
		if !strings.Contains(output, want) {
			t.Fatalf("hook output %q missing %q", output, want)
		}
	}
}

func TestCommandHookSkipsNonMatchingTool(t *testing.T) {
	engine := hooks.NewEngine()
	engine.RegisterCommandHook(hooks.CommandHook{
		Phase:   hooks.PhasePreTool,
		Matcher: "Write",
		Command: "exit 7",
	})

	results, err := engine.Run(context.Background(), hooks.PhasePreTool, hooks.Context{
		ToolName: "Read",
	})
	if err != nil {
		t.Fatalf("non-matching hook should not run: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected registered hook to return an empty result, got %d", len(results))
	}
	if results[0].Message != "" || results[0].Cancel {
		t.Fatalf("expected empty result for non-matching hook, got %+v", results[0])
	}
}

func TestCommandHookFailureReturnsBlockingResult(t *testing.T) {
	engine := hooks.NewEngine()
	command := "printf blocked; exit 3"
	if runtime.GOOS == "windows" {
		command = "Write-Output blocked; exit 3"
	}
	engine.RegisterCommandHook(hooks.CommandHook{
		Phase:   hooks.PhasePreTool,
		Matcher: "Bash",
		Command: command,
	})

	results, err := engine.Run(context.Background(), hooks.PhasePreTool, hooks.Context{
		ToolName: "Bash",
	})
	if err == nil {
		t.Fatal("expected failing command hook to return an error")
	}
	if len(results) != 1 {
		t.Fatalf("expected failed hook result to be returned, got %d", len(results))
	}
	if !results[0].Cancel {
		t.Fatalf("expected failed hook to cancel execution: %+v", results[0])
	}
	if !strings.Contains(results[0].Message, "blocked") {
		t.Fatalf("expected failed hook output to be preserved, got %q", results[0].Message)
	}
}

func TestHookRunStopsOnCancelResult(t *testing.T) {
	engine := hooks.NewEngine()
	calledSecond := false
	engine.Register(hooks.PhasePreTool, func(context.Context, hooks.Context) (hooks.Result, error) {
		return hooks.Result{Cancel: true, Message: "stop"}, nil
	})
	engine.Register(hooks.PhasePreTool, func(context.Context, hooks.Context) (hooks.Result, error) {
		calledSecond = true
		return hooks.Result{}, nil
	})

	results, err := engine.Run(context.Background(), hooks.PhasePreTool, hooks.Context{})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if calledSecond {
		t.Fatal("engine should stop when a hook returns Cancel")
	}
	if len(results) != 1 || results[0].Message != "stop" {
		t.Fatalf("unexpected results: %+v", results)
	}
}
