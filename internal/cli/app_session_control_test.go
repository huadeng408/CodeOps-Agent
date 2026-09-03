package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/config"
	"code-agent/internal/hooks"
	"code-agent/internal/metrics"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/session"
	"code-agent/internal/todo"
	"code-agent/internal/tools"
	"code-agent/internal/undo"
	"code-agent/internal/worktree"
)

func TestHandleToolCallExecutesVersionedSessionControls(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := session.NewSQLiteEventStore(filepath.Join(root, "sessions.sqlite"))
	manager := session.NewManager(store)
	parent := manager.NewSession(root)
	manager.Append(session.RoleUser, "first")
	manager.Append(session.RoleAssistant, "second")
	app := newSessionControlTestApp(root, manager)
	t.Cleanup(func() { _ = manager.Close() })
	var postHookSessionID string
	app.hooks.Register(hooks.PhasePostTool, func(_ context.Context, hookCtx hooks.Context) (hooks.Result, error) {
		postHookSessionID = hookCtx.SessionID
		return hooks.Result{}, nil
	})

	fork := app.handleToolCall(ctx, orchestrator.ToolCall{
		Name:           "SessionFork",
		ParametersJSON: `{"api_version":"v1","operation":"fork","target_session_id":"child","target_seq":2}`,
	})
	if fork.ExitCode != 0 || fork.Error != "" {
		t.Fatalf("session fork failed: %+v", fork)
	}
	var forkReceipt map[string]any
	if err := json.Unmarshal([]byte(fork.Output), &forkReceipt); err != nil {
		t.Fatalf("fork output is not JSON: %v", err)
	}
	if forkReceipt["operation"] != "fork" || forkReceipt["session_id"] != "child" {
		t.Fatalf("unexpected fork receipt: %s", fork.Output)
	}
	if current := manager.Current(); current.ID != "child" || len(current.Messages) < 2 || current.Messages[0].Content != "first" || current.Messages[1].Content != "second" {
		t.Fatalf("fork did not switch to child projection: %+v", current)
	}
	if postHookSessionID != "child" {
		t.Fatalf("post-hook session attribution = %q, want child", postHookSessionID)
	}

	rewind := app.handleToolCall(ctx, orchestrator.ToolCall{
		Name:           "SessionRewind",
		ParametersJSON: `{"api_version":"v1","operation":"rewind","target_seq":1}`,
	})
	if rewind.ExitCode != 0 || rewind.Error != "" {
		t.Fatalf("session rewind failed: %+v", rewind)
	}
	var rewindReceipt map[string]any
	if err := json.Unmarshal([]byte(rewind.Output), &rewindReceipt); err != nil {
		t.Fatalf("rewind output is not JSON: %v", err)
	}
	if rewindReceipt["operation"] != "rewind" || rewindReceipt["session_id"] != "child" {
		t.Fatalf("unexpected rewind receipt: %s", rewind.Output)
	}
	if current := manager.Current(); current.ID != "child" || len(current.Messages) < 1 || current.Messages[0].Content != "first" {
		t.Fatalf("rewind did not switch to target projection: %+v", current)
	}
	loadedParent, err := store.Load(ctx, parent.ID)
	if err != nil {
		t.Fatalf("load parent after child controls: %v", err)
	}
	if loadedParent == nil || len(loadedParent.Messages) != 2 {
		t.Fatalf("parent session changed after child controls: %+v", loadedParent)
	}
}

func TestHandleToolCallRejectsInvalidSessionControlVersion(t *testing.T) {
	root := t.TempDir()
	manager := session.NewManager(session.NewSQLiteEventStore(filepath.Join(root, "sessions.sqlite")))
	manager.NewSession(root)
	manager.Append(session.RoleUser, "keep")
	app := newSessionControlTestApp(root, manager)
	t.Cleanup(func() { _ = manager.Close() })

	result := app.handleToolCall(context.Background(), orchestrator.ToolCall{
		Name:           "SessionRewind",
		ParametersJSON: `{"api_version":"v0","target_seq":1}`,
	})
	if result.ExitCode == 0 || !strings.Contains(result.Error, "unsupported session control api_version") {
		t.Fatalf("invalid version was not rejected: %+v", result)
	}
	if current := manager.Current(); len(current.Messages) < 1 || current.Messages[0].Content != "keep" {
		t.Fatalf("invalid control mutated session: %+v", current)
	}
}

func TestHandleToolCallRejectsMismatchedSessionControlOperation(t *testing.T) {
	root := t.TempDir()
	manager := session.NewManager(session.NewSQLiteEventStore(filepath.Join(root, "sessions.sqlite")))
	manager.NewSession(root)
	app := newSessionControlTestApp(root, manager)
	t.Cleanup(func() { _ = manager.Close() })

	result := app.handleToolCall(context.Background(), orchestrator.ToolCall{
		Name:           "SessionFork",
		ParametersJSON: `{"api_version":"v1","operation":"rewind","target_session_id":"child","target_seq":0}`,
	})
	if result.ExitCode == 0 || !strings.Contains(result.Error, `operation must be "fork"`) {
		t.Fatalf("mismatched operation was not rejected: %+v", result)
	}
}

func TestHandleToolCallDeniesSessionControlByPolicy(t *testing.T) {
	root := t.TempDir()
	manager := session.NewManager(session.NewSQLiteEventStore(filepath.Join(root, "sessions.sqlite")))
	manager.NewSession(root)
	app := newSessionControlTestApp(root, manager)
	app.permissions = permission.NewControllerWithRules(
		map[string]permission.Level{"SessionFork": permission.AskSession},
		nil,
		[]permission.AllowRule{{Tool: "SessionFork"}},
	)
	t.Cleanup(func() { _ = manager.Close() })

	result := app.handleToolCall(context.Background(), orchestrator.ToolCall{
		Name:           "SessionFork",
		ParametersJSON: `{"api_version":"v1","operation":"fork","target_session_id":"child","target_seq":0}`,
	})
	if result.ExitCode == 0 || result.Error != "permission denied by policy" {
		t.Fatalf("policy denial was not enforced: %+v", result)
	}
	if current := manager.Current(); current.ID == "child" {
		t.Fatal("denied fork switched the current session")
	}
}

func TestHandleToolCallDeniesSessionControlWhenUserDeclines(t *testing.T) {
	root := t.TempDir()
	manager := session.NewManager(session.NewSQLiteEventStore(filepath.Join(root, "sessions.sqlite")))
	manager.NewSession(root)
	output := &strings.Builder{}
	app := newSessionControlTestApp(root, manager)
	app.input = NewInputBuffer(strings.NewReader("n\n"), output)
	app.permissions = permission.NewController(nil, nil)
	t.Cleanup(func() { _ = manager.Close() })

	result := app.handleToolCall(context.Background(), orchestrator.ToolCall{
		Name:           "SessionFork",
		ParametersJSON: `{"api_version":"v1","operation":"fork","target_session_id":"child","target_seq":0}`,
	})
	if result.ExitCode == 0 || result.Error != "permission denied by user" {
		t.Fatalf("user denial was not enforced: %+v", result)
	}
	if current := manager.Current(); current.ID == "child" {
		t.Fatal("declined fork switched the current session")
	}
}

func newSessionControlTestApp(root string, manager *session.Manager) *App {
	return &App{
		cfg:         config.Config{ProjectRoot: root, WorkingDir: root},
		input:       NewInputBuffer(strings.NewReader(""), &strings.Builder{}),
		renderer:    NewStreamRenderer(&strings.Builder{}),
		session:     manager,
		permissions: permission.NewController(map[string]permission.Level{"SessionFork": permission.AutoAllow, "SessionRewind": permission.AutoAllow}, nil),
		executor:    tools.NewExecutor(root),
		metrics:     metrics.NewCollector(),
		hooks:       hooks.NewEngine(),
		undo:        undo.NewManager(),
		worktree:    worktree.NewManager(root, "HEAD"),
		todos:       todo.NewManager(),
	}
}
