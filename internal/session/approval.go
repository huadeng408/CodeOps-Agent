package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	approvalPendingEventType  = "approval/pending"
	approvalApprovedEventType = "approval/approved"
	approvalDeniedEventType   = "approval/denied"
)

type ApprovalDecision string

const (
	ApprovalPending  ApprovalDecision = "pending"
	ApprovalApproved ApprovalDecision = "approved"
	ApprovalDenied   ApprovalDecision = "denied"
)

// ToolApprovalView is the browser-safe projection of one durable decision.
// The run and tool-call identity are immutable after the pending fact exists.
type ToolApprovalView struct {
	RunID         string           `json:"runId"`
	ToolCallID    string           `json:"toolCallId"`
	ToolName      string           `json:"toolName"`
	ArgumentsJSON string           `json:"argumentsJson"`
	Decision      ApprovalDecision `json:"decision"`
}

// ToolApprovalDecisionCommand identifies the immutable pending fact being
// decided. PendingEventID/PendingSeq are the entity CAS; unrelated ledger
// appends do not invalidate the command.
type ToolApprovalDecisionCommand struct {
	RunID          string
	ToolCallID     string
	PendingEventID string
	PendingSeq     int64
	Decision       ApprovalDecision
}

type toolApprovalPayload struct {
	RunID         string           `json:"run_id"`
	ToolCallID    string           `json:"tool_call_id"`
	ToolName      string           `json:"tool_name"`
	ArgumentsJSON string           `json:"arguments_json"`
	Decision      ApprovalDecision `json:"decision"`
}

type toolApprovalProjection struct {
	pending       *toolApprovalPayload
	pendingEvent  *Event
	decision      *toolApprovalPayload
	decisionEvent *Event
}

// DecideToolApproval appends one owner-scoped decision under an entity CAS.
// Tool identity and arguments are copied from the pending fact so the browser
// cannot swap the operation while approving it. Ledger-head races are hidden
// inside this module because callers do not own heartbeat timing.
func (w *Workbench) DecideToolApproval(ctx context.Context, ownerID uint, sessionID string, command ToolApprovalDecisionCommand) (EventView, error) {
	if _, err := w.Get(ctx, ownerID, sessionID); err != nil {
		return EventView{}, err
	}
	command.RunID = strings.TrimSpace(command.RunID)
	command.ToolCallID = strings.TrimSpace(command.ToolCallID)
	command.PendingEventID = strings.TrimSpace(command.PendingEventID)
	if command.PendingSeq < 0 || command.RunID == "" || command.ToolCallID == "" || command.PendingEventID == "" || (command.Decision != ApprovalApproved && command.Decision != ApprovalDenied) {
		return EventView{}, fmt.Errorf("%w: approval identity, pending event, and decision are required", ErrInvalidSessionInput)
	}
	for attempt := 0; attempt < 32; attempt++ {
		events, err := w.ledger.Events(ctx, sessionID)
		if err != nil {
			return EventView{}, err
		}
		view, err := reduceSessionView(events)
		if err != nil {
			return EventView{}, err
		}
		if view.Status == "deleted" {
			return EventView{}, ErrSessionNotFound
		}
		run, err := projectRun(events, command.RunID)
		if err != nil {
			return EventView{}, ErrSessionNotFound
		}
		invocation := invocationProjection(events, command.RunID, command.ToolCallID)
		approval, err := projectToolApproval(events, command.RunID, command.ToolCallID)
		if err != nil {
			return EventView{}, err
		}
		if invocation.prepared == nil || approval.pending == nil || approval.pendingEvent == nil {
			return EventView{}, ErrSessionNotFound
		}
		if approval.pendingEvent.EventID != command.PendingEventID || approval.pendingEvent.Seq != command.PendingSeq {
			return EventView{}, fmt.Errorf("%w: pending approval identity changed", ErrSessionStateConflict)
		}
		if !toolApprovalMatchesCall(*approval.pending, *invocation.prepared) {
			return EventView{}, fmt.Errorf("%w: approval does not match prepared tool call", ErrEventIntegrity)
		}
		if approval.decision != nil && approval.decisionEvent != nil {
			if approval.decision.Decision != command.Decision {
				return EventView{}, fmt.Errorf("%w: tool approval was already decided differently", ErrSessionStateConflict)
			}
			return eventToView(*approval.decisionEvent)
		}
		if run.terminal {
			return EventView{}, fmt.Errorf("%w: run is already terminal", ErrSessionStateConflict)
		}
		payload := *approval.pending
		payload.Decision = command.Decision
		eventType := approvalApprovedEventType
		if command.Decision == ApprovalDenied {
			eventType = approvalDeniedEventType
		}
		event, err := w.ledger.Append(ctx, sessionID, int64(len(events)), eventType, payload)
		if err == nil {
			w.signal(sessionID)
			return eventToView(event)
		}
		if !errors.Is(err, ErrSequenceConflict) {
			return EventView{}, err
		}
	}
	return EventView{}, ErrSequenceConflict
}

func projectToolApproval(events []Event, runID, toolCallID string) (toolApprovalProjection, error) {
	var projection toolApprovalProjection
	for _, event := range events {
		if event.Type != approvalPendingEventType && event.Type != approvalApprovedEventType && event.Type != approvalDeniedEventType {
			continue
		}
		var payload toolApprovalPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return toolApprovalProjection{}, fmt.Errorf("%w: invalid approval payload at seq %d", ErrEventIntegrity, event.Seq)
		}
		if payload.RunID != runID || payload.ToolCallID != toolCallID {
			continue
		}
		if err := validateToolApprovalPayload(event.Type, payload); err != nil {
			return toolApprovalProjection{}, fmt.Errorf("%w: invalid approval payload at seq %d", ErrEventIntegrity, event.Seq)
		}
		copy := payload
		eventCopy := event
		if event.Type == approvalPendingEventType {
			if projection.pending != nil && !sameToolApprovalIdentity(*projection.pending, payload) {
				return toolApprovalProjection{}, fmt.Errorf("%w: conflicting pending approval", ErrEventIntegrity)
			}
			projection.pending = &copy
			projection.pendingEvent = &eventCopy
			continue
		}
		if projection.decision != nil {
			return toolApprovalProjection{}, fmt.Errorf("%w: duplicate approval decision", ErrEventIntegrity)
		}
		projection.decision = &copy
		projection.decisionEvent = &eventCopy
	}
	if projection.decision != nil && projection.pending == nil {
		return toolApprovalProjection{}, fmt.Errorf("%w: approval decision has no pending fact", ErrEventIntegrity)
	}
	if projection.pending != nil && projection.decision != nil && !sameToolApprovalIdentity(*projection.pending, *projection.decision) {
		return toolApprovalProjection{}, fmt.Errorf("%w: approval decision identity changed", ErrEventIntegrity)
	}
	return projection, nil
}

func approvalEventView(event Event) (ToolApprovalView, error) {
	var payload toolApprovalPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil || validateToolApprovalPayload(event.Type, payload) != nil {
		return ToolApprovalView{}, fmt.Errorf("%w: invalid approval payload at seq %d", ErrEventIntegrity, event.Seq)
	}
	return ToolApprovalView{
		RunID: payload.RunID, ToolCallID: payload.ToolCallID, ToolName: payload.ToolName,
		ArgumentsJSON: payload.ArgumentsJSON, Decision: payload.Decision,
	}, nil
}

func validateToolApprovalPayload(eventType string, payload toolApprovalPayload) error {
	want := ApprovalPending
	switch eventType {
	case approvalApprovedEventType:
		want = ApprovalApproved
	case approvalDeniedEventType:
		want = ApprovalDenied
	case approvalPendingEventType:
	default:
		return errors.New("unsupported approval event")
	}
	if strings.TrimSpace(payload.RunID) == "" || strings.TrimSpace(payload.ToolCallID) == "" || strings.TrimSpace(payload.ToolName) == "" || payload.Decision != want {
		return errors.New("invalid approval identity")
	}
	if _, err := canonicalToolArguments(payload.ArgumentsJSON); err != nil {
		return errors.New("invalid approval arguments")
	}
	return nil
}

func sameToolApprovalIdentity(left, right toolApprovalPayload) bool {
	return left.RunID == right.RunID && left.ToolCallID == right.ToolCallID && left.ToolName == right.ToolName && left.ArgumentsJSON == right.ArgumentsJSON
}

func toolApprovalMatchesCall(approval toolApprovalPayload, call toolCallPayload) bool {
	return approval.RunID == call.RunID && approval.ToolCallID == call.ToolCallID && approval.ToolName == call.ToolName && approval.ArgumentsJSON == call.ArgumentsJSON
}

func approvalContent(approval ToolApprovalView) string {
	switch approval.Decision {
	case ApprovalApproved:
		return approval.ToolName + " approved"
	case ApprovalDenied:
		return approval.ToolName + " denied"
	default:
		return approval.ToolName + " awaiting approval"
	}
}
