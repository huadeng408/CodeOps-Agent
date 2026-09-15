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

// ReadVerifiedSnapshot verifies and replays the very same immutable read.
// Separate Verify/Surface/Events calls can observe different append boundaries.
func ReadVerifiedSnapshot(ctx context.Context, ledger EventLog, sessionID string) (LedgerSnapshot, error) {
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
	surface, err := projectSurface(events)
	if err != nil {
		return LedgerSnapshot{}, err
	}
	return LedgerSnapshot{Events: events, Surface: surface}, nil
}
