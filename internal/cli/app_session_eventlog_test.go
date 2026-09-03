package cli

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/config"
	"code-agent/internal/session"
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
