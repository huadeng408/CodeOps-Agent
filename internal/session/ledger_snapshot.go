package session

import (
	"context"
	"errors"
)

// LedgerSnapshot is an ephemeral read projection, not a second fact store.
type LedgerSnapshot struct {
	Events  []Event
	Surface []Event
}

// Run projects a durable Run from this same immutable read boundary.
func (s LedgerSnapshot) Run(runID string) (RunView, error) {
	run, err := projectRun(s.Events, runID)
	return run.view, err
}

// Prefix replays an earlier boundary of this verified read without racing a
// second database query. endSeq is exclusive.
func (s LedgerSnapshot) Prefix(endSeq int64) (LedgerSnapshot, error) {
	if endSeq < 0 || endSeq > int64(len(s.Events)) {
		return LedgerSnapshot{}, ErrEventIntegrity
	}
	events := s.Events[:endSeq]
	surface, err := projectSurface(events)
	if err != nil {
		return LedgerSnapshot{}, err
	}
	return LedgerSnapshot{Events: events, Surface: surface}, nil
}

// ReadVerifiedSnapshot verifies and replays the very same immutable read.
// Separate Verify/Surface/Events calls can observe different append boundaries.
func ReadVerifiedSnapshot(ctx context.Context, ledger EventLog, sessionID string) (LedgerSnapshot, error) {
	snapshot, err := readVerifiedMetadataSnapshot(ctx, ledger, sessionID)
	if err != nil {
		return LedgerSnapshot{}, err
	}
	snapshot.Surface, err = projectSurface(snapshot.Events)
	if err != nil {
		return LedgerSnapshot{}, err
	}
	return snapshot, nil
}

func readVerifiedMetadataSnapshot(ctx context.Context, ledger EventLog, sessionID string) (LedgerSnapshot, error) {
	if ledger == nil {
		return LedgerSnapshot{}, errors.New("session ledger is required")
	}
	events, err := ledger.Events(ctx, sessionID)
	if err != nil {
		return LedgerSnapshot{}, err
	}
	if err := verifyLedgerEvents(events, sessionID); err != nil {
		return LedgerSnapshot{}, err
	}
	return LedgerSnapshot{Events: events}, nil
}
