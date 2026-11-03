package session_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"code-agent/internal/session"
)

func lockedReadOnlyLedger(t *testing.T) (*session.SQLiteEventLog, session.LedgerSnapshot, string, func()) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "locked.sqlite")
	writer, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := session.NewWorkbench(writer, nil).Create(ctx, 7, "fixture", "locked read", "")
	if err != nil {
		writer.Close()
		t.Fatal(err)
	}
	before, err := session.ReadVerifiedSnapshot(ctx, writer, created.ID)
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	lockerDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lockerDB.Close() })
	locker, err := lockerDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { locker.Close() })
	var journal string
	if err := locker.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&journal); err != nil || journal != "delete" {
		t.Fatalf("temporary rollback journal: %q, %v", journal, err)
	}
	reader, err := session.OpenReadOnlySQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	if _, err := locker.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	var released sync.Once
	release := func() {
		released.Do(func() {
			if _, err := locker.ExecContext(ctx, "ROLLBACK"); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(release)
	return reader, before, created.ID, release
}

func TestReadOnlyLedgerWaitsForShortExclusiveLock(t *testing.T) {
	reader, before, id, release := lockedReadOnlyLedger(t)
	type result struct {
		snapshot session.LedgerSnapshot
		err      error
	}
	completed := make(chan result, 1)
	go func() {
		snapshot, err := session.ReadVerifiedSnapshot(context.Background(), reader, id)
		completed <- result{snapshot, err}
	}()
	select {
	case got := <-completed:
		t.Fatalf("native read returned before the exclusive lock was released: %v", got.err)
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case got := <-completed:
		if got.err != nil || !reflect.DeepEqual(got.snapshot, before) {
			t.Fatalf("released lock did not restore the exact verified boundary: %v", got.err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("native read did not finish after lock release")
	}
}

func TestReadOnlyLedgerRejectsPersistentExclusiveLock(t *testing.T) {
	reader, _, id, release := lockedReadOnlyLedger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		snapshot session.LedgerSnapshot
		err      error
	}
	completed := make(chan result, 1)
	go func() {
		snapshot, err := session.ReadVerifiedSnapshot(ctx, reader, id)
		completed <- result{snapshot, err}
	}()
	var got result
	select {
	case got = <-completed:
	case <-time.After(8 * time.Second):
		release()
		cancel()
		select {
		case <-completed:
		case <-time.After(8 * time.Second):
			t.Error("locked read worker did not stop after rollback and cancellation")
		}
		t.Fatal("native held-lock read exceeded its independent watchdog")
	}
	var sqliteError interface{ Code() int }
	if !errors.As(got.err, &sqliteError) || sqliteError.Code()&255 != 5 {
		t.Fatalf("held lock must fail with bounded SQLITE_BUSY: %v", got.err)
	}
	if !reflect.DeepEqual(got.snapshot, session.LedgerSnapshot{}) {
		t.Fatal("failed locked read returned a partial boundary")
	}
}

func TestReadOnlyLedgerCancellationUnderLockPreservesReader(t *testing.T) {
	reader, before, id, release := lockedReadOnlyLedger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		snapshot session.LedgerSnapshot
		err      error
	}
	completed := make(chan result, 1)
	go func() {
		snapshot, err := session.ReadVerifiedSnapshot(ctx, reader, id)
		completed <- result{snapshot, err}
	}()
	select {
	case got := <-completed:
		t.Fatalf("native read returned before lock cancellation: %v", got.err)
	case <-time.After(150 * time.Millisecond):
	}
	cancel()
	select {
	case got := <-completed:
		if !errors.Is(got.err, context.Canceled) || !reflect.DeepEqual(got.snapshot, session.LedgerSnapshot{}) {
			t.Fatalf("canceled read returned a boundary or lost cancellation: %v", got.err)
		}
	case <-time.After(8 * time.Second):
		release()
		t.Fatal("native read did not finish after cancellation")
	}
	release()
	after, err := session.ReadVerifiedSnapshot(context.Background(), reader, id)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("canceled read poisoned the verified reader: %v", err)
	}
}

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
