package session

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"code-agent/internal/identity"
)

const toolRuntimeStoppedEventType = "tool/runtime-stopped"

type runtimeStopReference struct {
	EventID   string `json:"event_id"`
	Checksum  string `json:"checksum"`
	RuntimeID string `json:"runtime_id"`
}

type runtimeStopsPayload struct {
	SchemaVersion int                    `json:"schema_version"`
	Results       []runtimeStopReference `json:"results"`
}

func runtimeStopDispatched(events []Event, original Event, result toolResultPayload) bool {
	if result.ToolCallID == "" || result.ToolName == "" {
		return false
	}
	count := 0
	for _, event := range events {
		if event.Seq >= original.Seq {
			break
		}
		if event.Type != toolDispatchedType {
			continue
		}
		var call toolCallPayload
		if json.Unmarshal(event.Payload, &call) != nil {
			return false
		}
		if call.RunID == result.RunID && call.ToolCallID == result.ToolCallID {
			if call.ToolName != result.ToolName {
				return false
			}
			count++
		}
	}
	return count == 1
}

func confirmedRuntimeStops(events []Event, actor identity.Actor) (map[string]bool, error) {
	results, stopped := map[string]Event{}, map[string]bool{}
	for index, event := range events {
		if event.Type == "tool/result" {
			results[event.EventID] = event
		}
		if event.Type != toolRuntimeStoppedEventType {
			continue
		}
		var proof runtimeStopsPayload
		if json.Unmarshal(event.Payload, &proof) != nil || proof.SchemaVersion != 1 || len(proof.Results) == 0 {
			return nil, ErrEventIntegrity
		}
		runs, err := projectRuns(events[:index])
		if err != nil {
			return nil, err
		}
		for _, ref := range proof.Results {
			original, exists := results[ref.EventID]
			var result toolResultPayload
			if !exists || stopped[ref.EventID] || original.Checksum != ref.Checksum ||
				json.Unmarshal(original.Payload, &result) != nil || result.Stopped || result.RuntimeID == "" || result.RuntimeID != ref.RuntimeID {
				return nil, ErrEventIntegrity
			}
			run, exists := runs[result.RunID]
			if !exists || !run.terminal || run.actor.ScopeKey() != actor.ScopeKey() || !runtimeStopDispatched(events[:index], original, result) {
				return nil, ErrEventIntegrity
			}
			stopped[ref.EventID] = true
		}
	}
	return stopped, nil
}

// ConfirmTaskRuntimeStops records physical absence only. Original command
// outcomes remain unknown and cannot authorize continuation or a proposal.
func ConfirmTaskRuntimeStops(ctx context.Context, ledger EventLog, actor identity.Actor, verify func(context.Context, string) error) error {
	root, err := ModelTaskRoot(ctx, ledger, actor)
	if err != nil {
		return err
	}
	if root != actor.SessionID || verify == nil {
		return ErrInvalidSessionInput
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for attempt := 0; attempt < 8; attempt++ {
		snapshot, err := ReadVerifiedSnapshot(ctx, ledger, root)
		if err != nil {
			return err
		}
		runs, err := projectRuns(snapshot.Events)
		if err != nil {
			return err
		}
		for _, run := range runs {
			if !run.terminal {
				return ErrSessionStateConflict
			}
		}
		confirmed, err := confirmedRuntimeStops(snapshot.Events, actor)
		if err != nil {
			return err
		}
		proof := runtimeStopsPayload{SchemaVersion: 1}
		for _, event := range snapshot.Events {
			if event.Type != "tool/result" || confirmed[event.EventID] {
				continue
			}
			var result toolResultPayload
			if json.Unmarshal(event.Payload, &result) != nil {
				return ErrEventIntegrity
			}
			if result.Stopped || result.RuntimeID == "" {
				continue
			}
			run, exists := runs[result.RunID]
			if !exists || !run.terminal || run.actor.ScopeKey() != actor.ScopeKey() || !runtimeStopDispatched(snapshot.Events, event, result) {
				return ErrSessionStateConflict
			}
			if err := verify(ctx, result.RuntimeID); err != nil {
				return err
			}
			proof.Results = append(proof.Results, runtimeStopReference{EventID: event.EventID, Checksum: event.Checksum, RuntimeID: result.RuntimeID})
		}
		if len(proof.Results) == 0 {
			return nil
		}
		_, err = ledger.Append(ctx, root, int64(len(snapshot.Events)), toolRuntimeStoppedEventType, proof)
		if errors.Is(err, ErrSequenceConflict) {
			continue
		}
		return err
	}
	return ErrSequenceConflict
}
