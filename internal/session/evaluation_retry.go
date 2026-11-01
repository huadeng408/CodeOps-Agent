package session

import (
	"context"
	"encoding/json"
	"errors"

	"code-agent/internal/orchestrator"
)

// EvaluationRetryEvidence projects the admitted visible-validation attempts;
// it never treats a missing or unknown result as a failed validation.
type EvaluationRetryEvidence struct {
	AttemptCount  int                      `json:"attempt_count"`
	RetryObserved bool                     `json:"retry_observed"`
	BoundEnforced bool                     `json:"bound_enforced"`
	Attempts      []EvaluationRetryAttempt `json:"attempts"`
}

type EvaluationRetryAttempt struct {
	SessionID        string `json:"session_id"`
	RunID            string `json:"run_id"`
	ToolCallID       string `json:"tool_call_id"`
	CommandSHA256    string `json:"command_sha256"`
	PendingEventID   string `json:"pending_event_id"`
	PendingChecksum  string `json:"pending_checksum"`
	DispatchEventID  string `json:"dispatch_event_id"`
	DispatchChecksum string `json:"dispatch_checksum"`
	ResultEventID    string `json:"result_event_id"`
	ResultChecksum   string `json:"result_checksum"`
	State            string `json:"state"`
	ExitCode         *int32 `json:"exit_code"`
}

type evaluationRetryProjection struct {
	evidence       EvaluationRetryEvidence
	pendingChanges map[string]bool
	unknownChange  bool
}

func evaluationRetryCommand(binding *EvaluationBinding, call toolCallPayload) (string, bool) {
	if call.ToolName != "Bash" {
		return "", false
	}
	var parameters struct {
		Command    string `json:"command"`
		CWD        string `json:"cwd"`
		WorkingDir string `json:"working_dir"`
	}
	if json.Unmarshal([]byte(call.ArgumentsJSON), &parameters) != nil || parameters.CWD != "" && parameters.CWD != "." || parameters.WorkingDir != "" && parameters.WorkingDir != "." {
		return "", false
	}
	for _, command := range binding.Runtime.VisibleVerificationCommands {
		if command != "" && parameters.Command == command {
			return command, true
		}
	}
	return "", false
}

func evaluationRetryChangesCandidate(name string) bool {
	if isReadOnlyTool(name) {
		return false
	}
	switch name {
	case "SearchKnowledge", "WebSearch", "WebFetch", "AskUser", "ManageMemory", "AgentTask", "PublishArtifact":
		return false
	default:
		return true
	}
}

func (p *evaluationRetryProjection) allows(verification bool, name string) bool {
	if !p.evidence.BoundEnforced {
		return false
	}
	if !verification && !evaluationRetryChangesCandidate(name) {
		return true
	}
	if p.unknownChange {
		return false
	}
	attempts := p.evidence.Attempts
	if verification {
		return len(p.pendingChanges) == 0 && (len(attempts) == 0 || len(attempts) == 1 && attempts[0].State == "failed")
	}
	return len(attempts) == 0 || len(attempts) == 1 && attempts[0].State == "failed"
}

// Only approved pending records reserve an attempt. A model's concurrent,
// unapproved tool/call records cannot reserve or hide validation capacity.
func projectEvaluationRetry(binding *EvaluationBinding, events []Event) (*evaluationRetryProjection, error) {
	if binding == nil || binding.Policy.ExecutionStrategy != "verify_retry" {
		return nil, nil
	}
	p := &evaluationRetryProjection{evidence: EvaluationRetryEvidence{BoundEnforced: len(binding.Runtime.VisibleVerificationCommands) > 0, Attempts: []EvaluationRetryAttempt{}}, pendingChanges: map[string]bool{}}
	calls := map[string]toolCallPayload{}
	indices := map[string]int{}
	unknownAttempts := map[string]bool{}
	for _, event := range events {
		switch event.Type {
		case "tool/call", toolDispatchedType, toolUnknownEventType:
			var call toolCallPayload
			if json.Unmarshal(event.Payload, &call) != nil || call.RunID == "" || call.ToolCallID == "" {
				return nil, ErrEventIntegrity
			}
			key := invocationHistoryKey(call.RunID, call.ToolCallID)
			if event.Type == "tool/call" {
				if _, exists := calls[key]; exists {
					return nil, ErrEventIntegrity
				}
				calls[key] = call
			} else if index, exists := indices[key]; exists {
				attempt := &p.evidence.Attempts[index]
				if event.Type == toolUnknownEventType {
					unknownAttempts[key] = true
					attempt.State, attempt.ExitCode = "unknown", nil
				} else {
					prepared := calls[key]
					if attempt.DispatchEventID != "" || prepared.ToolName != call.ToolName || prepared.ArgumentsJSON != call.ArgumentsJSON {
						return nil, ErrEventIntegrity
					}
					attempt.DispatchEventID, attempt.DispatchChecksum = event.EventID, event.Checksum
				}
			} else if event.Type == toolUnknownEventType && evaluationRetryChangesCandidate(call.ToolName) {
				p.unknownChange = true
			}
		case commitPendingEventType:
			var pending struct {
				commitPayload
				Bound bool `json:"evaluation_retry_bound"`
			}
			if json.Unmarshal(event.Payload, &pending) != nil || pending.RunID == "" || pending.ToolCallID == "" {
				return nil, ErrEventIntegrity
			}
			key := invocationHistoryKey(pending.RunID, pending.ToolCallID)
			call, exists := calls[key]
			if !exists || call.ToolName != pending.ToolName || call.ArgumentsJSON != pending.ArgumentsJSON {
				return nil, ErrEventIntegrity
			}
			command, verification := evaluationRetryCommand(binding, call)
			if verification {
				if _, exists := indices[key]; exists {
					return nil, ErrEventIntegrity
				}
				if !p.allows(true, call.ToolName) || !pending.Bound {
					p.evidence.BoundEnforced = false
				}
				indices[key] = len(p.evidence.Attempts)
				p.evidence.Attempts = append(p.evidence.Attempts, EvaluationRetryAttempt{SessionID: event.SessionID, RunID: call.RunID, ToolCallID: call.ToolCallID, CommandSHA256: hashCodeState(command), PendingEventID: event.EventID, PendingChecksum: event.Checksum, State: "pending"})
			} else if evaluationRetryChangesCandidate(call.ToolName) {
				if !p.allows(false, call.ToolName) || !pending.Bound || call.ToolName == "Bash" || p.pendingChanges[key] {
					p.evidence.BoundEnforced = false
				}
				p.pendingChanges[key] = true
			}
		case "tool/result":
			var result toolResultPayload
			if json.Unmarshal(event.Payload, &result) != nil || result.RunID == "" || result.ToolCallID == "" {
				return nil, ErrEventIntegrity
			}
			key := invocationHistoryKey(result.RunID, result.ToolCallID)
			if index, exists := indices[key]; exists {
				attempt := &p.evidence.Attempts[index]
				if attempt.ResultEventID != "" || result.ToolName != "Bash" {
					return nil, ErrEventIntegrity
				}
				attempt.ResultEventID, attempt.ResultChecksum = event.EventID, event.Checksum
				attempt.State = "unknown"
				if !unknownAttempts[key] && attempt.DispatchEventID != "" && result.Error == "" && !result.Truncated && !result.OutcomeUnknown && result.RuntimeID != "" && result.Stopped {
					code := result.ExitCode
					attempt.ExitCode, attempt.State = &code, "failed"
					if code == 0 {
						attempt.State = "passed"
					}
				}
			} else if p.pendingChanges[key] {
				delete(p.pendingChanges, key)
				if result.OutcomeUnknown || result.RuntimeID != "" && !result.Stopped {
					p.unknownChange = true
				}
			}
		}
	}
	p.evidence.AttemptCount = len(p.evidence.Attempts)
	p.evidence.RetryObserved = len(p.evidence.Attempts) >= 2 && p.evidence.Attempts[0].State == "failed" && p.evidence.Attempts[1].DispatchEventID != ""
	return p, nil
}

// The policy check and pending append share one CAS-protected admission region.
// Execution and result recording retain the ordinary Harness paths.
func (r *SessionRunner) appendEvaluationToolPending(ctx context.Context, key runKey, lease runLeasePayload, binding *EvaluationBinding, call orchestrator.ToolCall, pending commitPayload) (Event, error) {
	if binding == nil || binding.Policy.ExecutionStrategy != "verify_retry" {
		return r.appendLeasedFactEvent(ctx, key, lease.LeaseID, commitPendingEventType, pending)
	}
	r.appendMu.Lock()
	defer r.appendMu.Unlock()
	for attempt := 0; attempt < 16; attempt++ {
		snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, key.sessionID)
		if err != nil {
			return Event{}, err
		}
		view, viewErr := reduceSessionView(snapshot.Events)
		if viewErr != nil {
			return Event{}, viewErr
		}
		if view.Status == "deleted" {
			return Event{}, ErrSessionNotFound
		}
		run, err := projectRun(snapshot.Events, key.runID)
		if err != nil || run.terminal || run.leaseID != lease.LeaseID {
			return Event{}, ErrLeaseLost
		}
		if run.cancelRequest != nil {
			return Event{}, ErrRunCancelRequested
		}
		current, err := ReadEvaluation(snapshot.Events)
		if err != nil || current == nil || current.Policy.ExecutionStrategy != "verify_retry" {
			return Event{}, ErrSessionStateConflict
		}
		projection, err := projectEvaluationRetry(current, snapshot.Events)
		if err != nil {
			return Event{}, err
		}
		_, verification := evaluationRetryCommand(current, toolCallPayload{ToolName: call.Name, ArgumentsJSON: call.ParametersJSON})
		if call.Name == "Bash" && !verification || !projection.allows(verification, call.Name) {
			return Event{}, errors.New("evaluation bounded verification/retry policy denies this tool attempt")
		}
		payload := struct {
			commitPayload
			Bound bool `json:"evaluation_retry_bound"`
		}{pending, true}
		event, err := r.workbench.ledger.Append(ctx, key.sessionID, int64(len(snapshot.Events)), commitPendingEventType, payload)
		if err == nil {
			r.workbench.signal(key.sessionID)
			return event, nil
		}
		if !errors.Is(err, ErrSequenceConflict) {
			return Event{}, err
		}
	}
	return Event{}, ErrSequenceConflict
}

func (r *SessionRunner) completeEvaluationRetry(ctx context.Context, key runKey) error {
	r.appendMu.Lock()
	defer r.appendMu.Unlock()
	snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, key.sessionID)
	if err != nil {
		return err
	}
	binding, err := ReadEvaluation(snapshot.Events)
	if err != nil || binding == nil || binding.Policy.ExecutionStrategy != "verify_retry" {
		return err
	}
	projection, err := projectEvaluationRetry(binding, snapshot.Events)
	if err != nil {
		return err
	}
	attempts := projection.evidence.Attempts
	if !projection.evidence.BoundEnforced || projection.unknownChange || len(projection.pendingChanges) != 0 ||
		!(len(attempts) == 1 && attempts[0].State == "passed" || len(attempts) == 2 && attempts[0].State == "failed" && (attempts[1].State == "passed" || attempts[1].State == "failed")) {
		return errors.New("evaluation bounded verification/retry strategy is incomplete or unknown")
	}
	return nil
}
