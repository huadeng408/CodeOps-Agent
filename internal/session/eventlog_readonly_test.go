package session_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"code-agent/internal/session"
)

func TestReadOnlyLedgerRefusesMutationAndMissingDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	writer, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := session.NewWorkbench(writer, nil).Create(ctx, 7, "fixture", "read-only audit", "")
	if err != nil {
		t.Fatal(err)
	}
	before, err := session.ReadVerifiedSnapshot(ctx, writer, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := session.OpenReadOnlySQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Append(ctx, created.ID, 1, "test/forbidden", map[string]bool{"changed": true}); err == nil {
		t.Fatal("read-only Ledger accepted a fact")
	}
	after, err := session.ReadVerifiedSnapshot(ctx, reader, created.ID)
	if err != nil || len(after.Events) != len(before.Events) || after.Events[0].Checksum != before.Events[0].Checksum {
		t.Fatal("read-only audit changed the source boundary")
	}
	missing := filepath.Join(t.TempDir(), "missing.sqlite")
	if log, err := session.OpenReadOnlySQLiteEventLog(missing); err == nil {
		log.Close()
		t.Fatal("read-only Ledger created a missing source")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("read-only open left a database behind")
	}
}
