package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/config"
	"code-agent/internal/hooks"
	"code-agent/internal/metrics"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/session"
	"code-agent/internal/tools"
	"code-agent/internal/undo"
)

func TestHandleToolCallPromptsAndApprovesAskSessionTool(t *testing.T) {
	root := t.TempDir()
	app, out := newPermissionTestApp(root, "y\n")

	result := app.handleToolCall(context.Background(), orchestrator.ToolCall{
		Name:               "Write",
		RequiredPermission: 2,
		ParametersJSON:     `{"path":"notes.txt","content":"approved"}`,
	})

	if result.ExitCode != 0 || result.Error != "" {
		t.Fatalf("expected write to run after approval, got result=%+v", result)
	}
	data, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "approved" {
		t.Fatalf("unexpected file content: %q", string(data))
	}
	if got := app.permissions.Check("Write", nil); got != permission.Approve {
		t.Fatalf("Write should be approved for the session, got %v", got)
	}
	if !strings.Contains(out.String(), "permission required") {
		t.Fatalf("permission prompt was not rendered: %q", out.String())
	}
}

func TestHandleToolCallDeniesWhenUserDeclines(t *testing.T) {
	root := t.TempDir()
	app, _ := newPermissionTestApp(root, "n\n")

	result := app.handleToolCall(context.Background(), orchestrator.ToolCall{
		Name:               "Write",
		RequiredPermission: 2,
		ParametersJSON:     `{"path":"notes.txt","content":"denied"}`,
	})

	if result.ExitCode == 0 || result.Error != "permission denied by user" {
		t.Fatalf("expected user denial, got result=%+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied write should not create file, stat err=%v", err)
	}
}

func newPermissionTestApp(root, input string) (*App, *bytes.Buffer) {
	out := &bytes.Buffer{}
	sessions := session.NewManager(session.NewMemoryStore())
	sessions.NewSession(root)

	return &App{
		cfg:         config.Config{ProjectRoot: root, WorkingDir: root},
		input:       NewInputBuffer(strings.NewReader(input), out),
		renderer:    NewStreamRenderer(out),
		metrics:     metrics.NewCollector(),
		session:     sessions,
		permissions: permission.NewController(nil, nil),
		hooks:       hooks.NewEngine(),
		executor:    tools.NewExecutor(root),
		undo:        undo.NewManager(),
	}, out
}
