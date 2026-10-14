package cli

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/config"
	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
	"code-agent/internal/tools"
	"code-agent/internal/undo"
)

func TestCLIUndoLifecycleFenceSurvivesLedgerReopen(t *testing.T) {
	for _, tool := range []string{"Rename", "Delete unknown", "Rename undo save failed"} {
		t.Run(tool, func(t *testing.T) {
			root := t.TempDir()
			database := filepath.Join(root, "sessions.sqlite")
			manager := session.NewManager(session.NewSQLiteEventStore(database))
			created := manager.NewSession(root)
			app := &App{session: manager, undo: undo.NewManager()}
			app.undo.Record("Write", []undo.Change{{Path: "a.txt", Before: "", After: "before"}})
			manager.SetUndo(sessionUndoEntries(app.undo.List()))
			result := tools.ToolResult{Changes: []tools.Change{{Path: "a.txt", Destination: "b.txt", Before: "before", After: "before"}}}
			name := "Rename"
			if tool == "Delete unknown" {
				name, result.ExitCode, result.Error, result.OutcomeUnknown = "Delete", 1, "unknown outcome", true
			}
			if _, err := manager.BeginInvocation("lifecycle-1", name, `{}`); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.MarkInvocationDispatched("lifecycle-1"); err != nil {
				t.Fatal(err)
			}
			if tool == "Rename undo save failed" {
				db, err := sql.Open("sqlite", database)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`CREATE TRIGGER fail_undo_snapshot BEFORE INSERT ON session_events WHEN NEW.type = 'session/state' BEGIN SELECT RAISE(ABORT, 'fixture undo save failure'); END;`); err != nil {
					t.Fatal(err)
				}
				app.recordUndo(orchestrator.ToolCall{Name: name}, result)
				if len(manager.Current().Undo) != 1 {
					t.Fatal("undo save failure was not exercised")
				}
				if _, err := db.Exec(`DROP TRIGGER fail_undo_snapshot`); err != nil {
					t.Fatal(err)
				}
				db.Close()
			} else {
				app.recordUndo(orchestrator.ToolCall{Name: name}, result)
			}
			if _, err := manager.CommitInvocation(session.InvocationResult{ID: "lifecycle-1", Name: name, ExitCode: result.ExitCode, Error: result.Error, OutcomeUnknown: result.OutcomeUnknown}); err != nil {
				t.Fatal(err)
			}
			if err := manager.Close(); err != nil {
				t.Fatal(err)
			}
			manager = session.NewManager(session.NewSQLiteEventStore(database))
			defer manager.Close()
			retained, err := manager.Load(context.Background(), created.ID)
			if err != nil {
				t.Fatal(err)
			}
			output := &strings.Builder{}
			app = &App{cfg: config.Config{ProjectRoot: root}, session: manager, undo: undo.NewManager(), renderer: NewStreamRenderer(output)}
			app.undo.Restore(undoEntries(retained.Undo))
			for _, name := range []string{"a.txt", "b.txt"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("user replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			app.handleSlashCommand(context.Background(), "/undo")
			if len(app.undo.List()) != len(retained.Undo) || !strings.Contains(output.String(), "reconcile") || strings.Contains(output.String(), "reverted:") {
				t.Fatalf("persisted undo fence missing: %s", output)
			}
			for _, name := range []string{"a.txt", "b.txt"} {
				data, err := os.ReadFile(filepath.Join(root, name))
				if err != nil || string(data) != "user replacement" {
					t.Fatal("unsupported undo changed user contents")
				}
			}
		})
	}
}
