package hooks_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/hooks"
	"code-agent/internal/sandbox"
)

func TestCommandHookCannotEscapeTheSelectedUnapprovedSandbox(t *testing.T) {
	root := t.TempDir()
	engine := hooks.NewEngine()
	engine.RegisterCommandHook(hooks.CommandHook{Phase: hooks.PhasePreTool, Command: "echo bypassed > escaped.txt", WorkDir: root})
	engine.SetSandbox(sandbox.NewOpenSandboxRunner(sandbox.Config{TrustRoot: root}), root)
	results, err := engine.Run(context.Background(), hooks.PhasePreTool, hooks.Context{SessionID: "session", ToolName: "Bash"})
	if err == nil || len(results) != 1 || !results[0].Cancel || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("unapproved Hook did not report the selected boundary: %+v %v", results, err)
	}
	if _, err := os.Stat(filepath.Join(root, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("Hook ran on the host: %v", err)
	}
}

func TestCommandHookKeepsStructuredArgumentsLiteral(t *testing.T) {
	if os.Getenv("CODEOPS_HOOK_ARGV_FIXTURE") == "1" {
		data, _ := json.Marshal(os.Args[len(os.Args)-1])
		fmt.Println(string(data))
		os.Exit(0)
	}
	t.Setenv("CODEOPS_HOOK_ARGV_FIXTURE", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	literal := "spaces ; $(never-execute) & quotes \"literal\""
	engine := hooks.NewEngine()
	engine.RegisterCommandHook(hooks.CommandHook{Phase: hooks.PhasePreTool, Program: executable, Args: []string{"-test.run=TestCommandHookKeepsStructuredArgumentsLiteral", "--", "${VALUE}"}})
	results, err := engine.Run(context.Background(), hooks.PhasePreTool, hooks.Context{Payload: map[string]any{"value": literal}})
	if err != nil || len(results) != 1 {
		t.Fatalf("structured Hook failed: %+v %v", results, err)
	}
	var got string
	if json.Unmarshal([]byte(results[0].Message), &got) != nil || got != literal {
		t.Fatalf("Hook interpreted arguments through a shell: %q", results[0].Message)
	}
}

func TestCommandHookDoesNotReinterpretInsertedArguments(t *testing.T) {
	if os.Getenv("CODEOPS_HOOK_TEMPLATE_FIXTURE") == "1" {
		data, _ := json.Marshal(os.Args[len(os.Args)-1])
		fmt.Println(string(data))
		os.Exit(0)
	}
	t.Setenv("CODEOPS_HOOK_TEMPLATE_FIXTURE", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	engine := hooks.NewEngine()
	engine.RegisterCommandHook(hooks.CommandHook{Phase: hooks.PhasePreTool, Program: executable, Args: []string{"-test.run=TestCommandHookDoesNotReinterpretInsertedArguments", "--", "${VALUE}|${OTHER}"}})
	results, err := engine.Run(context.Background(), hooks.PhasePreTool, hooks.Context{Payload: map[string]any{"value": "${OTHER}", "other": "${VALUE}"}})
	if err != nil || len(results) != 1 {
		t.Fatalf("structured Hook failed: %+v %v", results, err)
	}
	var got string
	if json.Unmarshal([]byte(results[0].Message), &got) != nil || got != "${OTHER}|${VALUE}" {
		t.Fatalf("Hook recursively interpreted inserted data: %q", results[0].Message)
	}
}
