package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	codeagentpb "code-agent/gen/codeagentpb"
)

const compactionEventType = "context/compaction"

type canonicalSource struct {
	EventID  string `json:"event_id"`
	Checksum string `json:"checksum"`
}

type compactionPayload struct {
	RunID           string            `json:"run_id"`
	Summary         string            `json:"summary"`
	Sources         []canonicalSource `json:"sources"`
	ReportedSources []canonicalSource `json:"reported_sources"`
	InputEventID    string            `json:"input_event_id"`
}

func eventSources(event Event) ([]canonicalSource, error) {
	if event.Type != compactionEventType {
		return []canonicalSource{{event.EventID, event.Checksum}}, nil
	}
	var payload compactionPayload
	if json.Unmarshal(event.Payload, &payload) != nil || len(payload.ReportedSources) == 0 {
		return nil, ErrEventIntegrity
	}
	return payload.ReportedSources, nil
}

// Resolve only a complete, contiguous prefix. Counts cannot authorize a cut.
func compactionPrefix(surface []Event, reported []canonicalSource, inputID string) (int, error) {
	if len(reported) == 0 {
		return 0, fmt.Errorf("%w: compaction sources missing", ErrEventIntegrity)
	}
	seen := map[string]bool{}
	for _, source := range reported {
		if source.EventID == "" || source.Checksum == "" || seen[source.EventID] {
			return 0, ErrEventIntegrity
		}
		seen[source.EventID] = true
	}
	offset := 0
	openCalls := map[string]bool{}
	for i, event := range surface {
		if event.EventID == inputID {
			return 0, fmt.Errorf("%w: compaction includes active input", ErrEventIntegrity)
		}
		refs, err := eventSources(event)
		if offset < len(reported) && reported[offset] == (canonicalSource{event.EventID, event.Checksum}) {
			refs = []canonicalSource{reported[offset]}
		}
		if err != nil || offset+len(refs) > len(reported) {
			return 0, ErrEventIntegrity
		}
		for _, ref := range refs {
			if ref != reported[offset] {
				return 0, fmt.Errorf("%w: compaction source mismatch", ErrEventIntegrity)
			}
			offset++
		}
		switch event.Type {
		case "tool/call":
			var payload toolCallPayload
			if json.Unmarshal(event.Payload, &payload) != nil {
				return 0, ErrEventIntegrity
			}
			key := invocationHistoryKey(payload.RunID, payload.ToolCallID)
			if openCalls[key] {
				return 0, ErrEventIntegrity
			}
			openCalls[key] = true
		case "tool/result":
			var payload toolResultPayload
			if json.Unmarshal(event.Payload, &payload) != nil {
				return 0, ErrEventIntegrity
			}
			key := invocationHistoryKey(payload.RunID, payload.ToolCallID)
			if !openCalls[key] {
				return 0, fmt.Errorf("%w: orphan compacted tool result", ErrEventIntegrity)
			}
			delete(openCalls, key)
		case userMessageEventType, "assistant/message", compactionEventType:
		default:
			return 0, ErrEventIntegrity
		}
		if offset == len(reported) {
			if len(openCalls) != 0 || i+1 >= len(surface) {
				return 0, fmt.Errorf("%w: unsafe compaction boundary", ErrEventIntegrity)
			}
			return i + 1, nil
		}
	}
	return 0, ErrEventIntegrity
}

func applyCompaction(surface []Event, event Event) ([]Event, error) {
	var payload compactionPayload
	if json.Unmarshal(event.Payload, &payload) != nil || payload.RunID == "" || strings.TrimSpace(payload.Summary) == "" || len(payload.Sources) == 0 {
		return nil, ErrEventIntegrity
	}
	count, err := compactionPrefix(surface, payload.ReportedSources, payload.InputEventID)
	if err != nil || count != len(payload.Sources) {
		return nil, ErrEventIntegrity
	}
	for i, source := range payload.Sources {
		if source != (canonicalSource{surface[i].EventID, surface[i].Checksum}) {
			return nil, ErrEventIntegrity
		}
	}
	return append([]Event{event}, surface[count:]...), nil
}

func (r *SessionRunner) persistCompaction(ctx context.Context, key runKey, lease runLeasePayload, update *codeagentpb.CompactionUpdate) error {
	if update == nil || strings.TrimSpace(update.Summary) == "" || update.RemovedMessages < 1 {
		return ErrEventIntegrity
	}
	reported := make([]canonicalSource, 0, len(update.SourceEvents))
	for _, ref := range update.SourceEvents {
		reported = append(reported, canonicalSource{ref.GetEventId(), ref.GetChecksum()})
	}
	for attempt := 0; attempt < 16; attempt++ {
		events, err := r.workbench.ledger.Events(ctx, key.sessionID)
		if err != nil {
			return err
		}
		view, err := reduceSessionView(events)
		if err != nil {
			return err
		}
		if view.Status == "deleted" {
			return ErrSessionNotFound
		}
		run, err := projectRun(events, key.runID)
		if err != nil {
			return err
		}
		if run.terminal || run.leaseID != lease.LeaseID {
			return ErrLeaseLost
		}
		surface, err := projectSurface(events)
		if err != nil {
			return err
		}
		if len(surface) > 0 && surface[0].Type == compactionEventType {
			var previous compactionPayload
			if json.Unmarshal(surface[0].Payload, &previous) == nil && previous.RunID == key.runID && slices.Equal(previous.ReportedSources, reported) && previous.Summary == update.Summary {
				return nil
			}
		}
		count, err := compactionPrefix(surface, reported, run.inputEvent)
		if err != nil {
			return err
		}
		if count != int(update.RemovedMessages) {
			return fmt.Errorf("%w: compaction includes uncommitted messages", ErrEventIntegrity)
		}
		payload := compactionPayload{RunID: key.runID, Summary: update.Summary, ReportedSources: reported, InputEventID: run.inputEvent}
		for _, event := range surface[:count] {
			payload.Sources = append(payload.Sources, canonicalSource{event.EventID, event.Checksum})
		}
		_, err = r.workbench.ledger.AppendSurface(ctx, key.sessionID, int64(len(events)), compactionEventType, payload, SurfaceOperation{Op: "compact"})
		if err == nil {
			r.workbench.signal(key.sessionID)
			return nil
		}
		if !errors.Is(err, ErrSequenceConflict) {
			return err
		}
	}
	return ErrSequenceConflict
}

// Expand only current-run replacements, leaving the immutable checkpoint's
// own summaries intact. The existing suffix validator then checks run ownership.
func expandContinuationCompactions(checkpoint, current, events []Event, runIDs ...string) ([]Event, error) {
	baseline := map[string]bool{}
	for _, event := range checkpoint {
		baseline[event.EventID] = true
	}
	byID := map[string]Event{}
	for _, event := range events {
		byID[event.EventID] = event
	}
	allowed := map[string]bool{}
	for _, id := range runIDs {
		allowed[id] = true
	}
	var expand func(Event) ([]Event, error)
	expand = func(event Event) ([]Event, error) {
		if event.Type != compactionEventType || baseline[event.EventID] {
			return []Event{event}, nil
		}
		var payload compactionPayload
		if json.Unmarshal(event.Payload, &payload) != nil || !allowed[payload.RunID] || len(payload.Sources) == 0 {
			return nil, ErrEventIntegrity
		}
		var restored []Event
		for _, ref := range payload.Sources {
			source, ok := byID[ref.EventID]
			if !ok || source.Checksum != ref.Checksum || source.Seq >= event.Seq {
				return nil, ErrEventIntegrity
			}
			nested, err := expand(source)
			if err != nil {
				return nil, err
			}
			restored = append(restored, nested...)
		}
		return restored, nil
	}
	var result []Event
	for _, event := range current {
		restored, err := expand(event)
		if err != nil {
			return nil, err
		}
		result = append(result, restored...)
	}
	return result, nil
}
