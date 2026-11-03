package session_test

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"code-agent/internal/session"
)

func TestLedgerSnapshotBoundsUnusedSurfaceCopies(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "isolated.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	created, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "surface", "retained branches", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 512; i++ {
		if _, err := ledger.AppendSurface(ctx, created.ID, int64(i+1), "user/message", map[string]string{"author": "user", "content": strings.Repeat("generic observation ", 16)}, session.SurfaceOperation{Op: "append"}); err != nil {
			t.Fatal(err)
		}
	}
	original, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstRewind, err := ledger.Rewind(ctx, created.ID, 128)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.AppendSurface(ctx, created.ID, firstRewind.Seq+1, "user/message", map[string]string{"author": "user", "content": "temporary branch"}, session.SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Rewind(ctx, created.ID, 384); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("surface snapshot allocated=%d bytes; canonical_events=%d; targets=2", allocated, len(snapshot.Events))
	if allocated > 8<<20 {
		t.Errorf("unused Surface copies allocated %d bytes; budget is 8 MiB", allocated)
	}
	if len(snapshot.Surface) != 384 {
		t.Fatalf("restored Surface length=%d, want 384", len(snapshot.Surface))
	}
	for i, event := range snapshot.Surface {
		if event.EventID != original[i+1].EventID || event.Checksum != original[i+1].Checksum {
			t.Fatalf("hidden-branch replay changed canonical source %d", i)
		}
	}
}
