package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	workspaceRestoreIntentEventType    = "workspace/restore-intent"
	workspaceRestoreCompletedEventType = "workspace/restore-completed"
	workspaceRestoreFailedEventType    = "workspace/restore-failed"
)

// CodeFileTransition is the backend-only content transition that a controlled
// workspace adapter must verify before restoring a completed run.
type CodeFileTransition struct {
	Path   string
	Before string
	After  string
}

type workspaceRestorePayload struct {
	CheckpointHash   string  `json:"checkpoint_hash"`
	RunID            string  `json:"run_id"`
	ModificationSeqs []int64 `json:"modification_seqs"`
	IntentEventID    string  `json:"intent_event_id,omitempty"`
}

// RestoreCodeChanges is the session-scoped restore module. It binds a
// controlled filesystem action to immutable code/modified receipts from one
// terminal run, then appends intent and outcome facts to the same ledger.
func (w *Workbench) RestoreCodeChanges(ctx context.Context, ownerID uint, sessionID, checkpointHash, runID string, expectedSeq int64, apply func([]CodeFileTransition) error) (EventView, error) {
	if apply == nil {
		return EventView{}, fmt.Errorf("%w: workspace restore adapter is required", ErrInvalidSessionInput)
	}
	if _, err := w.Get(ctx, ownerID, sessionID); err != nil {
		return EventView{}, err
	}
	checkpointHash = strings.TrimSpace(checkpointHash)
	runID = strings.TrimSpace(runID)
	if checkpointHash == "" || runID == "" || expectedSeq < 0 {
		return EventView{}, ErrSessionNotFound
	}
	events, err := w.ledger.Events(ctx, sessionID)
	if err != nil {
		return EventView{}, err
	}
	checkpoint, _, err := selectCheckpoint(events, checkpointHash)
	if err != nil {
		return EventView{}, ErrSessionNotFound
	}
	run, err := projectRun(events, runID)
	if err != nil || run.view.CheckpointHash != checkpoint.Checksum || !run.terminal {
		return EventView{}, ErrSessionNotFound
	}
	transitions, modificationSeqs, err := codeTransitionsForRun(events, runID, checkpoint.Checksum, run.order)
	if err != nil {
		return EventView{}, err
	}
	if len(transitions) == 0 {
		return EventView{}, fmt.Errorf("%w: no restorable code modifications", ErrSessionStateConflict)
	}
	intentPayload := workspaceRestorePayload{CheckpointHash: checkpoint.Checksum, RunID: runID, ModificationSeqs: modificationSeqs}
	intent, err := w.ledger.Append(ctx, sessionID, expectedSeq, workspaceRestoreIntentEventType, intentPayload)
	if err != nil {
		return EventView{}, err
	}
	w.signal(sessionID)
	if err := apply(transitions); err != nil {
		outcome, outcomeErr := w.appendWorkspaceRestoreOutcome(ctx, sessionID, intent, intentPayload, workspaceRestoreFailedEventType)
		if outcomeErr != nil {
			return EventView{}, fmt.Errorf("record workspace restore failure: %w", outcomeErr)
		}
		view, viewErr := eventToView(outcome)
		if viewErr != nil {
			return EventView{}, viewErr
		}
		return view, fmt.Errorf("%w: workspace restore conflict", ErrSessionStateConflict)
	}
	outcome, err := w.appendWorkspaceRestoreOutcome(ctx, sessionID, intent, intentPayload, workspaceRestoreCompletedEventType)
	if err != nil {
		return EventView{}, err
	}
	view, err := eventToView(outcome)
	if err != nil {
		return EventView{}, err
	}
	return view, nil
}

func (w *Workbench) appendWorkspaceRestoreOutcome(ctx context.Context, sessionID string, intent Event, payload workspaceRestorePayload, eventType string) (Event, error) {
	payload.IntentEventID = intent.EventID
	for attempt := 0; attempt < 16; attempt++ {
		events, err := w.ledger.Events(ctx, sessionID)
		if err != nil {
			return Event{}, err
		}
		outcome, appendErr := w.ledger.Append(ctx, sessionID, int64(len(events)), eventType, payload)
		if appendErr == nil {
			w.signal(sessionID)
			return outcome, nil
		} else if !errors.Is(appendErr, ErrSequenceConflict) {
			return Event{}, appendErr
		}
	}
	return Event{}, ErrSequenceConflict
}

func codeTransitionsForRun(events []Event, runID, checkpointHash string, terminalSeq int64) ([]CodeFileTransition, []int64, error) {
	continuationSeq := int64(-1)
	for _, event := range events {
		if event.Type != continuationEventType {
			continue
		}
		payload, err := decodeContinuationPayload(event.Payload)
		if err != nil {
			return nil, nil, err
		}
		if payload.RunID == runID && payload.CheckpointHash == checkpointHash {
			continuationSeq = event.Seq
			break
		}
	}
	if continuationSeq < 0 || terminalSeq <= continuationSeq {
		return nil, nil, ErrSessionNotFound
	}
	transitions := make([]CodeFileTransition, 0)
	modificationSeqs := make([]int64, 0)
	byPath := make(map[string]int)
	for _, event := range events {
		if event.Type != codeModifiedEventType {
			continue
		}
		var payload codeModificationPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil || validateCodeModificationPayload(payload) != nil {
			return nil, nil, fmt.Errorf("%w: invalid code modification payload at seq %d", ErrEventIntegrity, event.Seq)
		}
		if payload.RunID != runID {
			continue
		}
		if event.Seq <= continuationSeq || event.Seq >= terminalSeq {
			return nil, nil, fmt.Errorf("%w: code modification lies outside run lifecycle", ErrEventIntegrity)
		}
		if payload.BeforeSHA256 != hashCodeState(payload.Before) || payload.AfterSHA256 != hashCodeState(payload.After) || payload.DiffSHA256 != hashCodeTransition(payload.Before, payload.After) {
			return nil, nil, fmt.Errorf("%w: code modification contents do not match digests", ErrEventIntegrity)
		}
		if index, exists := byPath[payload.Path]; exists {
			if transitions[index].After != payload.Before {
				return nil, nil, fmt.Errorf("%w: non-contiguous code modification transition", ErrEventIntegrity)
			}
			transitions[index].After = payload.After
		} else {
			byPath[payload.Path] = len(transitions)
			transitions = append(transitions, CodeFileTransition{Path: payload.Path, Before: payload.Before, After: payload.After})
		}
		modificationSeqs = append(modificationSeqs, event.Seq)
	}
	return transitions, modificationSeqs, nil
}
