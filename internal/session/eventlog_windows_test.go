package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanceledLedgerReadsReleaseWindowsDatabaseHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "canceled-reads.sqlite")
	ledger, err := OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	if _, err := ledger.Append(context.Background(), "canceled-reads", 0, "session/created", map[string]any{"owner_id": 7}); err != nil {
		t.Fatal(err)
	}
	for range 1000 {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { cancel(); close(done) }()
		_, err := ledger.Events(ctx, "canceled-reads")
		<-done
		if err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "interrupted (9)") {
			t.Fatal(err)
		}
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("database handle remains after canceled reads and Close (connections=%d): %v", ledger.db.Stats().OpenConnections, err)
	}
}
