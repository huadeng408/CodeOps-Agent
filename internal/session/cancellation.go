package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"code-agent/internal/identity"
)

const runCancelRequestedEventType = "session/run-cancel-requested"
const runCancelUnconfirmedEventType = "session/run-cancel-unconfirmed"
const RunCanceling RunStatus = "canceling"

var ErrRunCancelRequested = errors.New("session run cancellation requested")

type CancelRunCommand struct {
	RequestID string
	SessionID string
	RunID     string
	OwnerID   uint
	Actor     identity.Actor
}

type RunCancellationModule interface {
	CancelRun(context.Context, CancelRunCommand) (RunView, error)
}

type runCancelPayload struct {
	RunID        string         `json:"run_id"`
	RequestID    string         `json:"request_id"`
	Actor        identity.Actor `json:"actor"`
	CleanupOwner string         `json:"cleanup_owner,omitempty"`
}

// CancelRun persists intent before interrupting the owned worker. Stopped is
// projected only after that worker has returned and released its resources.
func (r *SessionRunner) CancelRun(ctx context.Context, command CancelRunCommand) (RunView, error) {
	if r == nil || r.workbench == nil {
		return RunView{}, ErrContinuationUnavailable
	}
	command.RequestID, command.SessionID, command.RunID = strings.TrimSpace(command.RequestID), strings.TrimSpace(command.SessionID), strings.TrimSpace(command.RunID)
	run, err := r.requestRunCancellation(ctx, command)
	if err != nil || run.terminal {
		return run.view, err
	}
	command.Actor = run.actor
	key := runKey{sessionID: command.SessionID, runID: command.RunID}
	r.workbench.signal(command.SessionID)
	if !r.interruptRun(key) {
		if run.view.Attempt == 0 {
			r.finishCancellation(key)
		} else {
			r.enqueue(ctx, key, time.Time{})
		}
	}
	r.stopModelCalls(command)
	return r.runView(ctx, command.SessionID, command.RunID)
}

func (r *SessionRunner) requestRunCancellation(ctx context.Context, command CancelRunCommand) (runProjection, error) {
	r.lifecycleMu.RLock()
	defer r.lifecycleMu.RUnlock()
	if r.closed {
		return runProjection{}, ErrSessionRunnerClosed
	}
	if r.cancelOwnerID == "" {
		return runProjection{}, ErrContinuationUnavailable
	}
	if command.RequestID == "" || command.SessionID == "" || command.RunID == "" {
		return runProjection{}, ErrInvalidSessionInput
	}
	if _, err := r.workbench.Get(ctx, command.OwnerID, command.SessionID); err != nil {
		return runProjection{}, err
	}
	actor, err := command.Actor.BindSession(command.SessionID)
	if err != nil {
		return runProjection{}, ErrInvalidSessionInput
	}
	command.Actor = actor
	for attempt := 0; attempt < 16; attempt++ {
		snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, command.SessionID)
		if err != nil {
			return runProjection{}, err
		}
		runs, err := projectRuns(snapshot.Events)
		if err != nil {
			return runProjection{}, err
		}
		for id, previous := range runs {
			if previous.cancelRequest != nil && previous.cancelRequest.RequestID == command.RequestID && id != command.RunID {
				return runProjection{}, ErrSessionStateConflict
			}
		}
		run, err := projectRun(snapshot.Events, command.RunID)
		if err != nil {
			return runProjection{}, err
		}
		if run.actor.ScopeKey() != actor.ScopeKey() {
			return runProjection{}, ErrSessionStateConflict
		}
		if run.cancelRequest != nil {
			return run, nil
		}
		if run.terminal {
			return runProjection{}, ErrSessionStateConflict
		}
		payload := runCancelPayload{RunID: command.RunID, RequestID: command.RequestID, Actor: actor, CleanupOwner: r.cancelOwnerID}
		_, err = r.workbench.ledger.Append(ctx, command.SessionID, int64(len(snapshot.Events)), runCancelRequestedEventType, payload)
		if errors.Is(err, ErrSequenceConflict) {
			continue
		}
		if err != nil {
			return runProjection{}, err
		}
		run.cancelRequest = &payload
		return run, nil
	}
	return runProjection{}, ErrSequenceConflict
}

func (r *SessionRunner) stopModelCalls(command CancelRunCommand) error {
	if r.options.CancelModelCalls == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return r.options.CancelModelCalls(ctx, command)
}

func (r *SessionRunner) interruptRun(key runKey) bool {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	if execution := r.activeAgentRuns[key]; execution != nil {
		execution.stop()
		return true
	}
	return false
}

func (r *SessionRunner) finishCancellation(key runKey) {
	r.agentMu.Lock()
	if r.cancelingRuns[key] {
		r.agentMu.Unlock()
		return
	}
	if r.cancelingRuns == nil {
		r.cancelingRuns = map[runKey]bool{}
	}
	r.cancelingRuns[key] = true
	r.agentMu.Unlock()
	defer func() {
		r.agentMu.Lock()
		delete(r.cancelingRuns, key)
		r.agentMu.Unlock()
	}()
	state := "stopped"
	readCtx, stopRead := context.WithTimeout(context.Background(), 5*time.Second)
	snapshot, err := ReadVerifiedSnapshot(readCtx, r.workbench.ledger, key.sessionID)
	stopRead()
	if err != nil {
		return
	}
	run, err := projectRun(snapshot.Events, key.runID)
	if err != nil || run.terminal || run.cancelRequest == nil {
		return
	}
	r.agentMu.Lock()
	execution := r.activeAgentRuns[key]
	// A leased run's cleanup belongs to its own leases. A queued run can still
	// inherit Session resources, so retain its earlier-lease ownership fence.
	priorLease, allOwned := false, true
	for _, event := range snapshot.Events {
		if event.Type == runLeasedEventType {
			var lease runLeasePayload
			if json.Unmarshal(event.Payload, &lease) != nil {
				allOwned = false
				continue
			}
			if run.view.Attempt > 0 && lease.RunID != key.runID {
				// Another Run cannot own this worker's finalizer, but may
				// retain Session resources that this process cannot stop.
				if !r.ownedLeases[lease.LeaseID] {
					state = "unknown"
				}
				continue
			}
			priorLease = true
			if !r.ownedLeases[lease.LeaseID] {
				allOwned = false
			}
		}
	}
	owned := allOwned && (run.view.Attempt == 0 && (priorLease || run.cancelRequest.CleanupOwner == r.cancelOwnerID) || execution != nil && execution.leaseID != "" && execution.leaseID == run.leaseID)
	r.agentMu.Unlock()
	if !owned {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if run.view.CancellationState != "unknown" {
			_ = r.appendLeasedFact(ctx, key, run.leaseID, runCancelUnconfirmedEventType, *run.cancelRequest)
		}
		return
	}
	childCtx, stopChildren := context.WithTimeout(context.Background(), 30*time.Second)
	if r.tasks.stopLinkedAgents(childCtx, key.sessionID, projectionOwner(snapshot.Events), run.cancelRequest.RequestID, map[string]bool{}) != nil {
		state = "unknown"
	}
	stopChildren()
	if unconfirmedToolStops(snapshot.Events) {
		state = "unknown"
	}
	if r.stopModelCalls(CancelRunCommand{RequestID: run.cancelRequest.RequestID, SessionID: key.sessionID, RunID: key.runID, OwnerID: projectionOwner(snapshot.Events), Actor: run.cancelRequest.Actor}) != nil {
		state = "unknown"
	}
	if releaser, ok := r.tools.(sessionResourceReleaser); ok {
		if err := releaser.ReleaseSession(key.sessionID); err != nil {
			state = "unknown"
		}
	}
	taskCtx, stopTask := context.WithTimeout(context.Background(), 5*time.Second)
	if r.tasks.cancelRunTask(taskCtx, snapshot.Events, run.cancelRequest.RequestID) != nil {
		state = "unknown"
	}
	stopTask()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.closeCanceledApprovals(ctx, key, run.leaseID); err != nil {
		state = "unknown"
	}
	for attempt := 0; attempt < 16; attempt++ {
		events, err := r.workbench.ledger.Events(ctx, key.sessionID)
		if err != nil {
			return
		}
		run, err := projectRun(events, key.runID)
		if err != nil || run.terminal || run.cancelRequest == nil {
			return
		}
		payload := runTerminalPayload{RunID: key.runID, RequestID: run.view.RequestID, LeaseID: run.leaseID, Attempt: run.view.Attempt, CancellationState: state, TraceContext: run.traceContext}
		if _, err := r.workbench.ledger.Append(ctx, key.sessionID, int64(len(events)), runCanceledEventType, payload); errors.Is(err, ErrSequenceConflict) {
			continue
		} else if err != nil {
			return
		}
		r.workbench.signal(key.sessionID)
		return
	}
}

// The owned worker has returned; cancellation denies its still-unapproved calls.
func (r *SessionRunner) closeCanceledApprovals(ctx context.Context, key runKey, leaseID string) error {
	events, err := r.workbench.ledger.Events(ctx, key.sessionID)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.Type != approvalPendingEventType {
			continue
		}
		var pending toolApprovalPayload
		if json.Unmarshal(event.Payload, &pending) != nil {
			return ErrEventIntegrity
		}
		if pending.RunID != key.runID {
			continue
		}
		approval, err := projectToolApproval(events, key.runID, pending.ToolCallID)
		if err != nil {
			return err
		}
		if approval.pendingEvent == nil || approval.pendingEvent.EventID != event.EventID {
			continue
		}
		invocation := invocationProjection(events, key.runID, pending.ToolCallID)
		if approval.decision != nil || invocation.dispatched != nil || invocation.result != nil {
			continue
		}
		if invocation.prepared == nil || !toolApprovalMatchesCall(pending, *invocation.prepared) {
			return ErrEventIntegrity
		}
		for _, fact := range events {
			if fact.Type != commitPendingEventType && fact.Type != toolUnknownEventType {
				continue
			}
			var identity struct {
				RunID      string `json:"run_id"`
				ToolCallID string `json:"tool_call_id"`
			}
			if json.Unmarshal(fact.Payload, &identity) != nil {
				return ErrEventIntegrity
			}
			if identity.RunID == key.runID && identity.ToolCallID == pending.ToolCallID {
				return ErrEventIntegrity
			}
		}
		pending.Decision = ApprovalDenied
		if err := r.appendLeasedFact(ctx, key, leaseID, approvalDeniedEventType, pending); err != nil {
			return err
		}
		result := toolResultPayload{RunID: key.runID, ToolCallID: pending.ToolCallID, ToolName: pending.ToolName, Error: "tool canceled before approval", ExitCode: 1}
		if err := r.appendLeasedSurface(ctx, key, runLeasePayload{LeaseID: leaseID}, "tool/result", result); err != nil {
			return err
		}
	}
	return nil
}

func unconfirmedToolStops(events []Event) bool {
	for _, event := range events {
		if event.Type != toolDispatchedType {
			continue
		}
		var call toolCallPayload
		if json.Unmarshal(event.Payload, &call) != nil {
			return true
		}
		result := invocationProjection(events, call.RunID, call.ToolCallID).result
		if result == nil || (result.OutcomeUnknown || result.RuntimeID != "") && !result.Stopped {
			return true
		}
	}
	return false
}
