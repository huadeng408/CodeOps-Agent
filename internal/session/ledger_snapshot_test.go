package session

import (
	"context"
	"errors"
	"testing"
)

type singleReadLedger struct {
	EventLog
	reads int
}

func (l *singleReadLedger) Events(ctx context.Context, sessionID string) ([]Event, error) {
	l.reads++
	return l.EventLog.Events(ctx, sessionID)
}

func (l *singleReadLedger) Verify(context.Context, string) error {
	return errors.New("separate verifier must not be used")
}

func (l *singleReadLedger) Surface(context.Context, string) ([]Event, error) {
	return nil, errors.New("separate surface read must not be used")
}

func TestLedgerSnapshotVerifiesAndProjectsOneRead(t *testing.T) {
	ledger := openWorkbenchTestLedger(t)
	ctx := context.Background()
	if _, err := ledger.AppendSurface(ctx, "snapshot", 0, "user/message", map[string]any{"content": "one snapshot"}, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	wrapper := &singleReadLedger{EventLog: ledger}
	snapshot, err := ReadVerifiedSnapshot(ctx, wrapper, "snapshot")
	if err != nil || wrapper.reads != 1 || len(snapshot.Events) != 1 || len(snapshot.Surface) != 1 {
		t.Fatalf("snapshot = %+v, reads=%d, err=%v", snapshot, wrapper.reads, err)
	}
}

type duplicateIdentityLedger struct {
	EventLog
}

func (l duplicateIdentityLedger) Events(ctx context.Context, sessionID string) ([]Event, error) {
	events, err := l.EventLog.Events(ctx, sessionID)
	if err == nil && len(events) >= 2 {
		events[1].EventID = events[0].EventID
		events[1].Checksum = checksumEvent(events[1])
	}
	return events, err
}

func TestLedgerSnapshotRejectsDuplicateEventIdentity(t *testing.T) {
	ledger := openWorkbenchTestLedger(t)
	ctx := context.Background()
	for seq := int64(0); seq < 2; seq++ {
		if _, err := ledger.Append(ctx, "duplicate", seq, "fixture/fact", map[string]any{"seq": seq}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ReadVerifiedSnapshot(ctx, duplicateIdentityLedger{EventLog: ledger}, "duplicate"); !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("duplicate event identity accepted: %v", err)
	}
}
