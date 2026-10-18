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
	// Earlier leases can retain MCP or job resources in another Go process.
	priorLease, allOwned := false, true
	for _, event := range snapshot.Events {
		if event.Type == runLeasedEventType {
			var lease runLeasePayload
			priorLease = true
			if json.Unmarshal(event.Payload, &lease) != nil || !r.ownedLeases[lease.LeaseID] {
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
	if r.stopLinkedAgents(childCtx, key.sessionID, projectionOwner(snapshot.Events), run.cancelRequest.RequestID, map[string]bool{}) != nil {
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
	task, _, taskErr := projectAgentTask(snapshot.Events)
	if taskErr != nil {
		state = "unknown"
	} else if task != nil {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		if task.Status != "canceled" {
			taskErr = r.appendAgentFact(ctx, key.sessionID, "agent/task-canceled", agentTaskMutation{RequestID: "run-cancel:" + run.cancelRequest.RequestID}, agentMutationExists("agent/task-canceled", "run-cancel:"+run.cancelRequest.RequestID))
		}
		if taskErr == nil {
			task.Status = "canceled"
			taskErr = r.reconcileAgentWorkspaceLifecycle(ctx, task)
		}
		stop()
		if taskErr != nil {
			state = "unknown"
		}
	}
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
		payload := runTerminalPayload{RunID: key.runID, RequestID: run.view.RequestID, LeaseID: run.leaseID, Attempt: run.view.Attempt, CancellationState: state}
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

func (r *SessionRunner) stopLinkedAgents(ctx context.Context, parent string, owner uint, requestID string, seen map[string]bool) error {
	children := map[string][]runKey{}
	var stopErrors []error
	stopErrors = append(stopErrors, r.fenceLinkedAgents(ctx, parent, owner, requestID, seen, children))
	for childID, keys := range children {
		r.agentMu.Lock()
		stopping := []*runExecution{}
		for key, execution := range r.activeAgentRuns {
			if key.sessionID == childID {
				execution.stop()
				stopping = append(stopping, execution)
			}
		}
		r.agentMu.Unlock()
		for _, execution := range stopping {
			select {
			case <-execution.done:
			case <-ctx.Done():
				stopErrors = append(stopErrors, ctx.Err())
			}
		}
		child, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, childID)
		if err != nil {
			stopErrors = append(stopErrors, err)
			continue
		}
		runs, err := projectRuns(child.Events)
		if err != nil {
			stopErrors = append(stopErrors, err)
			continue
		}
		for _, key := range keys {
			if run := runs[key.runID]; !run.terminal && run.view.Attempt == 0 {
				r.finishCancellation(key)
			}
		}
		child, err = ReadVerifiedSnapshot(ctx, r.workbench.ledger, childID)
		if err != nil {
			stopErrors = append(stopErrors, err)
			continue
		}
		runs, err = projectRuns(child.Events)
		if err != nil {
			stopErrors = append(stopErrors, err)
			continue
		}
		for _, run := range runs {
			if !run.terminal || run.view.CancellationState == "unknown" {
				stopErrors = append(stopErrors, errors.New("linked agent resource stop unconfirmed"))
			}
		}
		if unconfirmedToolStops(child.Events) {
			stopErrors = append(stopErrors, errors.New("linked agent tool stop unconfirmed"))
		}
	}
	return errors.Join(stopErrors...)
}

func (r *SessionRunner) fenceLinkedAgents(ctx context.Context, parent string, owner uint, requestID string, seen map[string]bool, children map[string][]runKey) error {
	if seen[parent] {
		return ErrEventIntegrity
	}
	seen[parent] = true
	snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, parent)
	if err != nil {
		return err
	}
	var stopErrors []error
	for _, event := range snapshot.Events {
		if event.Type != "agent/task-linked" {
			continue
		}
		stopErr := func() error {
			var link agentTaskLink
			if json.Unmarshal(event.Payload, &link) != nil {
				return ErrEventIntegrity
			}
			task, created, err := r.readAgentTask(ctx, owner, link.TaskID, parent)
			if err != nil {
				return err
			}
			child, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, task.ChildSessionId)
			if err != nil {
				return err
			}
			runs, err := projectRuns(child.Events)
			if err != nil {
				return err
			}
			children[task.ChildSessionId] = nil
			for id, run := range runs {
				if !run.terminal {
					if _, err := r.requestRunCancellation(ctx, CancelRunCommand{RequestID: "root-cancel:" + requestID + ":" + id, SessionID: task.ChildSessionId, RunID: id, OwnerID: owner, Actor: created.Actor}); err != nil {
						return err
					}
					key := runKey{sessionID: task.ChildSessionId, runID: id}
					children[task.ChildSessionId] = append(children[task.ChildSessionId], key)
					r.workbench.signal(task.ChildSessionId)
					if !r.interruptRun(key) {
						r.enqueue(ctx, key, time.Time{})
					}
				}
			}
			if len(children[task.ChildSessionId]) == 0 && !taskTerminal(task.Status) {
				if err := r.cancelAgentTask(ctx, task, "root-cancel:"+requestID); err != nil {
					return err
				}
			}
			return r.fenceLinkedAgents(ctx, task.ChildSessionId, owner, requestID, seen, children)
		}()
		stopErrors = append(stopErrors, stopErr)
	}
	return errors.Join(stopErrors...)
}
