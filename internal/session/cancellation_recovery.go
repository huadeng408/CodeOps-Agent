package session

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"

	"code-agent/internal/identity"
)

type CanceledRunRecoveryCommand struct {
	CancelRunCommand
	ExpectedSeq          int64
	ExpectedHeadChecksum string
	ProofDigest          string
}

type canceledRunRecoveryPayload struct {
	runTerminalPayload
	CancellationRequestID string         `json:"cancellation_request_id"`
	ProofDigest           string         `json:"proof_digest"`
	RecoveryActor         identity.Actor `json:"recovery_actor"`
}

// Recovery reuses pendingCommits -> commitPayload -> commit_done, but may close
// only one uniquely recorded, known invocation under an actual physical proof.
func knownRecoveryCommit(events []Event, pending commitPayload) bool {
	if pending.PendingSeq < 0 || pending.PendingSeq >= int64(len(events)) || pending.CommitID != commitIDFor(pending.RunID, pending.ToolCallID) {
		return false
	}
	original := events[pending.PendingSeq]
	var intent commitPayload
	if original.Type != commitPendingEventType || original.Checksum != pending.PendingChecksum || json.Unmarshal(original.Payload, &intent) != nil {
		return false
	}
	intent.PendingSeq, intent.PendingChecksum = original.Seq, original.Checksum
	if intent != pending {
		return false
	}
	facts := map[string]Event{}
	for _, event := range events {
		if event.Type != "tool/call" && event.Type != toolDispatchedType && event.Type != "tool/result" && event.Type != commitPendingEventType {
			continue
		}
		var identity struct {
			RunID      string `json:"run_id"`
			ToolCallID string `json:"tool_call_id"`
		}
		if json.Unmarshal(event.Payload, &identity) != nil {
			return false
		}
		if identity.RunID != pending.RunID || identity.ToolCallID != pending.ToolCallID {
			continue
		}
		if _, exists := facts[event.Type]; exists {
			return false
		}
		facts[event.Type] = event
	}
	prepared, dispatched, result := facts["tool/call"], facts[toolDispatchedType], facts["tool/result"]
	if prepared.EventID == "" || dispatched.EventID == "" || result.EventID == "" || facts[commitPendingEventType].EventID != original.EventID || prepared.SurfaceOp == nil || prepared.SurfaceOp.Op != "append" || !(prepared.Seq < original.Seq && original.Seq < dispatched.Seq && dispatched.Seq < result.Seq) {
		return false
	}
	var call, dispatch toolCallPayload
	var outcome toolResultPayload
	if json.Unmarshal(prepared.Payload, &call) != nil || json.Unmarshal(dispatched.Payload, &dispatch) != nil || json.Unmarshal(result.Payload, &outcome) != nil {
		return false
	}
	return call.RunID == pending.RunID && call.ToolCallID == pending.ToolCallID && call.ToolName == pending.ToolName && call.ArgumentsJSON == pending.ArgumentsJSON &&
		dispatch.RunID == call.RunID && dispatch.ToolCallID == call.ToolCallID && dispatch.ToolName == call.ToolName && dispatch.ArgumentsJSON == call.ArgumentsJSON && dispatch.OperationID == call.OperationID && call.MCPBinding == nil && dispatch.MCPBinding == nil &&
		outcome.RunID == call.RunID && outcome.ToolCallID == call.ToolCallID && outcome.ToolName == call.ToolName && !outcome.OutcomeUnknown && (outcome.RuntimeID == "" || outcome.Stopped)
}

// RecoverCanceledRun is a local Harness seam. The trusted Go verifier establishes
// physical quiescence; legacy leases never acquire fabricated ownership.
func (w *Workbench) RecoverCanceledRun(ctx context.Context, command CanceledRunRecoveryCommand, verifyStopped func(context.Context) error) (RunView, error) {
	if w == nil || w.ledger == nil || verifyStopped == nil || command.ExpectedSeq < 1 || command.OwnerID == 0 || command.RequestID == "" || command.RunID == "" || command.SessionID == "" {
		return RunView{}, ErrInvalidSessionInput
	}
	for _, digest := range []string{command.ExpectedHeadChecksum, command.ProofDigest} {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != 32 || digest != strings.ToLower(digest) {
			return RunView{}, ErrInvalidSessionInput
		}
	}
	actor, err := command.Actor.BindSession(command.SessionID)
	if err != nil {
		return RunView{}, ErrInvalidSessionInput
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, w.ledger, command.SessionID)
	if err != nil {
		return RunView{}, err
	}
	view, err := reduceSessionView(snapshot.Events)
	if err != nil {
		return RunView{}, err
	}
	if view.UserID != command.OwnerID || view.Status == "deleted" {
		return RunView{}, ErrSessionNotFound
	}
	if int64(len(snapshot.Events)) < command.ExpectedSeq || snapshot.Events[command.ExpectedSeq-1].Checksum != command.ExpectedHeadChecksum {
		return RunView{}, ErrSequenceConflict
	}
	base, err := snapshot.Prefix(command.ExpectedSeq)
	if err != nil {
		return RunView{}, err
	}
	runs, err := projectRuns(base.Events)
	if err != nil {
		return RunView{}, err
	}
	run, exists := runs[command.RunID]
	if !exists || run.terminal || run.view.Status != RunCanceling || run.actor.ScopeKey() != actor.ScopeKey() || run.cancelRequest == nil || run.cancelRequest.RequestID != command.RequestID || run.cancelRequest.Actor.ScopeKey() != actor.ScopeKey() || run.view.Attempt < 1 {
		return RunView{}, ErrSessionStateConflict
	}
	closures := pendingCommits(base.Events, command.RunID)
	for runID, other := range runs {
		if runID != command.RunID && (!other.terminal || other.view.Status == RunCanceled && other.view.CancellationState == "unknown" || len(pendingCommits(base.Events, runID)) != 0) {
			return RunView{}, ErrSessionStateConflict
		}
	}
	for _, pending := range closures {
		if !knownRecoveryCommit(base.Events, pending) {
			return RunView{}, ErrSessionStateConflict
		}
	}
	if unconfirmedToolStops(base.Events) {
		return RunView{}, ErrSessionStateConflict
	}
	for _, event := range base.Events {
		if strings.HasPrefix(event.Type, "agent/task-") || event.Type == toolUnknownEventType {
			return RunView{}, ErrSessionStateConflict
		}
		if event.Type == "tool/call" {
			var call toolCallPayload
			if json.Unmarshal(event.Payload, &call) != nil {
				return RunView{}, ErrEventIntegrity
			}
			invocation := invocationProjection(base.Events, call.RunID, call.ToolCallID)
			if invocation.dispatched == nil || invocation.result == nil || invocation.result.ToolName != call.ToolName || invocation.result.OutcomeUnknown {
				return RunView{}, ErrSessionStateConflict
			}
		}
		if event.Type == approvalPendingEventType {
			var pending toolApprovalPayload
			if json.Unmarshal(event.Payload, &pending) != nil {
				return RunView{}, ErrEventIntegrity
			}
			approval, err := projectToolApproval(base.Events, pending.RunID, pending.ToolCallID)
			if err != nil {
				return RunView{}, err
			}
			if approval.decision == nil {
				return RunView{}, ErrSessionStateConflict
			}
		}
	}
	terminal := canceledRunRecoveryPayload{runTerminalPayload: runTerminalPayload{RunID: command.RunID, RequestID: run.view.RequestID, LeaseID: run.leaseID, Attempt: run.view.Attempt, CancellationState: "stopped"}, CancellationRequestID: command.RequestID, ProofDigest: command.ProofDigest, RecoveryActor: actor}
	for {
		progress := len(snapshot.Events) - int(command.ExpectedSeq)
		if progress > len(closures)+1 {
			return RunView{}, ErrSequenceConflict
		}
		for offset, event := range snapshot.Events[command.ExpectedSeq:] {
			if offset < len(closures) {
				var done commitPayload
				if event.Type != commitDoneEventType || json.Unmarshal(event.Payload, &done) != nil || done != closures[offset] {
					return RunView{}, ErrSequenceConflict
				}
			} else {
				var previous canceledRunRecoveryPayload
				if event.Type != runCanceledEventType || json.Unmarshal(event.Payload, &previous) != nil || previous.runTerminalPayload != terminal.runTerminalPayload || previous.CancellationRequestID != command.RequestID || previous.ProofDigest != command.ProofDigest || previous.RecoveryActor.ScopeKey() != actor.ScopeKey() {
					return RunView{}, ErrSequenceConflict
				}
			}
		}
		if progress == len(closures)+1 {
			result, err := projectRun(snapshot.Events, command.RunID)
			return result.view, err
		}
		if err := verifyStopped(ctx); err != nil {
			return RunView{}, err
		}
		current, err := ReadVerifiedSnapshot(ctx, w.ledger, command.SessionID)
		if err != nil {
			return RunView{}, err
		}
		if len(current.Events) != len(snapshot.Events) || current.Events[len(current.Events)-1].Checksum != snapshot.Events[len(snapshot.Events)-1].Checksum {
			return RunView{}, ErrSequenceConflict
		}
		eventType := runCanceledEventType
		var payload any = terminal
		if progress < len(closures) {
			eventType, payload = commitDoneEventType, closures[progress]
		}
		appended, err := w.ledger.Append(ctx, command.SessionID, int64(len(current.Events)), eventType, payload)
		if err != nil {
			return RunView{}, err
		}
		w.signal(command.SessionID)
		snapshot, err = ReadVerifiedSnapshot(ctx, w.ledger, command.SessionID)
		if err != nil {
			return RunView{}, err
		}
		if len(snapshot.Events) != len(current.Events)+1 || snapshot.Events[len(snapshot.Events)-1].Checksum != appended.Checksum {
			return RunView{}, ErrSequenceConflict
		}
	}
}
