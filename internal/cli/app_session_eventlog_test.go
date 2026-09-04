package cli

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/metrics"
	"code-agent/internal/permission"
	"code-agent/internal/session"
	"code-agent/internal/todo"
	"code-agent/internal/tools"
	"code-agent/internal/undo"
	"code-agent/internal/worktree"
	_ "modernc.org/sqlite"
)

func TestNewAppPersistsSessionInEventLedger(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "sessions.sqlite")
	cfg := config.Default(root)
	cfg.ProjectRoot = root
	cfg.WorkingDir = root
	cfg.SessionDBPath = databasePath
	cfg.OrchestratorAddr = "127.0.0.1:1"
	cfg.OrchestratorAutoStart = false

	app := NewApp(cfg, strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	if app == nil {
		t.Fatal("NewApp returned nil")
	}
	defer cleanupIntegrationApp(t, app)
	created := app.session.NewSession(root)
	app.session.Append(session.RoleUser, "event-backed app message")
	if err := app.session.Close(); err != nil {
		t.Fatalf("close app session store: %v", err)
	}

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open session database: %v", err)
	}
	defer db.Close()
	var eventTables, legacyTables int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_events'`).Scan(&eventTables); err != nil {
		t.Fatalf("inspect event table: %v", err)
	}
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&legacyTables); err != nil {
		t.Fatalf("inspect legacy table: %v", err)
	}
	if eventTables != 1 || legacyTables != 0 {
		t.Fatalf("app storage tables = event:%d legacy:%d", eventTables, legacyTables)
	}
	var messageCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM session_events WHERE session_id = ? AND type = 'session/state'`, created.ID).Scan(&messageCount); err != nil {
		t.Fatalf("count state events: %v", err)
	}
	if messageCount < 2 {
		t.Fatalf("state event count = %d, want at least 2", messageCount)
	}
}

func TestRestoredSessionActorMustMatchConfiguredHarnessActor(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.ProjectRoot = root
	cfg.WorkingDir = root
	cfg.SessionDBPath = filepath.Join(root, "sessions.sqlite")
	cfg.OrchestratorAddr = "127.0.0.1:1"
	cfg.OrchestratorAutoStart = false
	app := NewApp(cfg, strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	t.Cleanup(func() { cleanupIntegrationApp(t, app) })

	created := app.session.NewSession(root)
	foreign, err := (identity.Actor{
		SchemaVersion: identity.SchemaVersion,
		ActorID:       "user:other",
		Subject:       "other",
		TenantID:      "tenant:local",
		Roles:         []string{"LOCAL"},
	}).BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	app.session.SetActor(foreign)

	if err := app.bindSessionActor(app.session.Current()); err == nil {
		t.Fatal("restored session with a foreign actor must fail closed")
	}
}

func TestBindSessionActorAllowsUnavailableOrchestrator(t *testing.T) {
	root := t.TempDir()
	manager := session.NewManager(session.NewMemoryStore())
	manager.NewSession(root)
	app := &App{
		cfg:         config.Config{ProjectRoot: root, WorkingDir: root},
		session:     manager,
		permissions: permission.NewController(nil, nil),
		actor:       identity.Default(),
		// A local/degraded App may not have a live orchestrator client. The
		// Harness still owns identity and permission scope in this path.
	}
	t.Cleanup(func() { _ = manager.Close() })

	if err := app.bindSessionActor(manager.Current()); err != nil {
		t.Fatalf("binding identity must not require an orchestrator: %v", err)
	}
	current := manager.Current()
	if current.Actor.ActorID != "actor:local" || current.Actor.SessionID != current.ID {
		t.Fatalf("session actor = %+v, want local actor bound to session", current.Actor)
	}
	app.permissions.ApproveSessionFor(current.Actor, "Edit")
	if got := app.permissions.Check("Edit", nil); got != permission.Approve {
		t.Fatalf("permission scope was not bound to session actor: decision=%v", got)
	}
}

func TestForkAndRewindCommandsUseDurableSessionHistory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := session.NewSQLiteEventStore(filepath.Join(root, "sessions.sqlite"))
	manager := session.NewManager(store)
	source := manager.NewSession(root)
	manager.Append(session.RoleUser, "first")
	manager.Append(session.RoleAssistant, "second")

	out := &strings.Builder{}
	app := &App{
		cfg:         config.Config{ProjectRoot: root, WorkingDir: root},
		renderer:    NewStreamRenderer(out),
		session:     manager,
		executor:    tools.NewExecutor(root),
		permissions: permission.NewController(nil, nil),
		undo:        undo.NewManager(),
		worktree:    worktree.NewManager(root, "HEAD"),
		todos:       todo.NewManager(),
		metrics:     metrics.NewCollector(),
	}
	t.Cleanup(func() { _ = manager.Close() })

	if !app.handleSlashCommand(ctx, "/fork child-session 2") {
		t.Fatal("/fork should be handled")
	}
	child := manager.Current()
	if child.ID != "child-session" || len(child.Messages) != 2 {
		t.Fatalf("fork current session = %+v, want child with two messages", child)
	}
	if !strings.Contains(out.String(), "forked session child-session") {
		t.Fatalf("fork output = %q", out.String())
	}

	if !app.handleSlashCommand(ctx, "/rewind 1") {
		t.Fatal("/rewind should be handled")
	}
	rewound := manager.Current()
	if rewound.ID != "child-session" || len(rewound.Messages) != 1 || rewound.Messages[0].Content != "first" {
		t.Fatalf("rewound session = %+v, want only first message", rewound)
	}

	reloadedSource, err := store.Load(ctx, source.ID)
	if err != nil {
		t.Fatalf("load source after child rewind: %v", err)
	}
	if reloadedSource == nil || len(reloadedSource.Messages) != 2 {
		t.Fatalf("source changed after child rewind: %+v", reloadedSource)
	}
	if !strings.Contains(out.String(), "rewound session child-session") {
		t.Fatalf("rewind output = %q", out.String())
	}
}

func TestForkAndRewindCommandsFailClosedForInvalidTargets(t *testing.T) {
	root := t.TempDir()
	manager := session.NewManager(session.NewMemoryStore())
	manager.NewSession(root)
	out := &strings.Builder{}
	app := &App{
		cfg:         config.Config{ProjectRoot: root, WorkingDir: root},
		renderer:    NewStreamRenderer(out),
		session:     manager,
		executor:    tools.NewExecutor(root),
		permissions: permission.NewController(nil, nil),
		undo:        undo.NewManager(),
		worktree:    worktree.NewManager(root, "HEAD"),
		todos:       todo.NewManager(),
		metrics:     metrics.NewCollector(),
	}

	if !app.handleSlashCommand(context.Background(), "/fork child -1") {
		t.Fatal("invalid /fork should be handled")
	}
	if !strings.Contains(out.String(), "event-seq must be a non-negative integer") {
		t.Fatalf("invalid fork output = %q", out.String())
	}
	out.Reset()
	if !app.handleSlashCommand(context.Background(), "/rewind") {
		t.Fatal("missing /rewind argument should be handled")
	}
	if !strings.Contains(out.String(), "usage: /rewind <event-seq>") {
		t.Fatalf("missing rewind output = %q", out.String())
	}
	out.Reset()
	if !app.handleSlashCommand(context.Background(), "/fork child 0") {
		t.Fatal("unsupported /fork should be handled")
	}
	if !strings.Contains(out.String(), "session history operations unsupported") {
		t.Fatalf("unsupported fork output = %q", out.String())
	}
}
