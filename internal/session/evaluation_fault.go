package session

import (
	"context"
	"encoding/json"
	"time"

	"code-agent/internal/orchestrator"
	"code-agent/internal/safety"
)

type EvaluationFaultReference struct {
	SessionID string `json:"session_id"`
	EventID   string `json:"event_id"`
	Seq       int64  `json:"seq"`
	Checksum  string `json:"checksum"`
}

// EvaluationBoundary identifies the durable, still-held tool window.
type EvaluationBoundary struct {
	SchemaVersion int                      `json:"schema_version"`
	Window        string                   `json:"window"`
	SessionID     string                   `json:"session_id"`
	RunID         string                   `json:"product_run_id"`
	ToolCallID    string                   `json:"tool_call_id"`
	ToolName      string                   `json:"tool_name"`
	Ready         EvaluationFaultReference `json:"ready"`
	Process       safety.ProcessIdentity   `json:"process"`
	HoldDeadline  int64                    `json:"hold_deadline_unix_nano"`
}

type evaluationFaultReady struct {
	SchemaVersion int                    `json:"schema_version"`
	RunID         string                 `json:"run_id"`
	Window        string                 `json:"window"`
	ToolCallID    string                 `json:"tool_call_id"`
	ToolName      string                 `json:"tool_name"`
	Process       safety.ProcessIdentity `json:"process"`
	HoldDeadline  int64                  `json:"hold_deadline_unix_nano"`
}

type evaluationFaultReleased struct {
	SchemaVersion int                      `json:"schema_version"`
	RunID         string                   `json:"run_id"`
	ToolCallID    string                   `json:"tool_call_id"`
	Window        string                   `json:"window"`
	Ready         EvaluationFaultReference `json:"ready"`
}

type EvaluationFaultEvidence struct {
	EvaluationBoundary
	Verified            bool                      `json:"verified"`
	Released            bool                      `json:"released"`
	Release             *EvaluationFaultReference `json:"release,omitempty"`
	RunTerminal         bool                      `json:"run_terminal"`
	CancelRequested     bool                      `json:"cancel_requested"`
	ResultOrderVerified bool                      `json:"result_order_verified"`
	ProcessStopped      bool                      `json:"process_stopped"`
}

func evaluationFaultRef(event Event) EvaluationFaultReference {
	return EvaluationFaultReference{event.SessionID, event.EventID, event.Seq, event.Checksum}
}

func evaluationFaultBoundary(event Event, fact evaluationFaultReady) EvaluationBoundary {
	return EvaluationBoundary{SchemaVersion: fact.SchemaVersion, Window: fact.Window, SessionID: event.SessionID, RunID: fact.RunID, ToolCallID: fact.ToolCallID, ToolName: fact.ToolName, Ready: evaluationFaultRef(event), Process: fact.Process, HoldDeadline: fact.HoldDeadline}
}

func (r *SessionRunner) evaluationBoundary(ctx context.Context, key runKey, call orchestrator.ToolCall, binding *EvaluationBinding, window string) error {
	if r.options.EvaluationObserver == nil || binding == nil || binding.Policy.FaultWindow != window || binding.Policy.FaultTool != "" && binding.Policy.FaultTool != call.Name {
		return nil
	}
	events, err := r.workbench.ledger.Events(ctx, key.sessionID)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.Type == "evaluation/fault-ready" {
			return nil
		}
	}
	deadline, ok := ctx.Deadline()
	if binding.SchemaVersion != 3 || !ok || !deadline.After(time.Now()) {
		return ErrSessionStateConflict
	}
	process, err := safety.CurrentProcessIdentity()
	if err != nil {
		return err
	}
	run, err := projectRun(events, key.runID)
	if err != nil {
		return err
	}
	fact := evaluationFaultReady{1, key.runID, window, call.ID, call.Name, *process, deadline.UnixNano()}
	ready, err := r.appendLeasedFactEvent(ctx, key, run.leaseID, "evaluation/fault-ready", fact)
	if err != nil {
		return err
	}
	r.options.EvaluationObserver(ctx, evaluationFaultBoundary(ready, fact))
	// The release must be durable before effects, result recording or acknowledgement advance.
	audit, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err = r.appendLeasedFactEvent(audit, key, run.leaseID, "evaluation/fault-released", evaluationFaultReleased{1, key.runID, call.ID, window, evaluationFaultRef(ready)})
	if err != nil {
		<-ctx.Done()
		return err
	}
	return ctx.Err()
}

func readEvaluationFaultEvidence(events []Event) ([]EvaluationFaultEvidence, error) {
	proofs := []EvaluationFaultEvidence{}
	matchedReleases := map[string]bool{}
	binding, err := ReadEvaluation(events)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		if event.Type != "evaluation/fault-ready" {
			continue
		}
		var fact evaluationFaultReady
		if json.Unmarshal(event.Payload, &fact) != nil {
			return nil, ErrEventIntegrity
		}
		proof := EvaluationFaultEvidence{EvaluationBoundary: evaluationFaultBoundary(event, fact)}
		if fact.SchemaVersion == 0 {
			if binding != nil && binding.SchemaVersion == 3 {
				return nil, ErrEventIntegrity
			}
			proofs = append(proofs, proof)
			continue
		}
		if len(proofs) != 0 || binding == nil || binding.SchemaVersion != 3 || binding.Policy.FaultWindow != fact.Window || binding.Policy.FaultTool != "" && binding.Policy.FaultTool != fact.ToolName || fact.SchemaVersion != 1 || fact.RunID == "" || fact.ToolCallID == "" || fact.ToolName == "" || !fact.Process.Valid() || fact.HoldDeadline <= event.CreatedAt.UnixNano() || fact.Window != "before_effect" && fact.Window != "after_effect" && fact.Window != "after_record" {
			return nil, ErrEventIntegrity
		}
		run, err := projectRun(events, fact.RunID)
		if err != nil {
			return nil, err
		}
		proof.Verified, proof.RunTerminal, proof.CancelRequested = true, run.terminal, run.cancelRequest != nil
		proof.ProcessStopped = fact.Process.ConfirmedStopped()
		dispatches, results, ordered := 0, 0, true
		for _, candidate := range events {
			if candidate.Type == "evaluation/fault-released" {
				var release evaluationFaultReleased
				if json.Unmarshal(candidate.Payload, &release) != nil || release.SchemaVersion != 1 {
					return nil, ErrEventIntegrity
				}
				if release.Ready != proof.Ready {
					continue
				}
				if proof.Released || release.RunID != fact.RunID || release.ToolCallID != fact.ToolCallID || release.Window != fact.Window || candidate.Seq <= event.Seq {
					return nil, ErrEventIntegrity
				}
				ref := evaluationFaultRef(candidate)
				proof.Released, proof.Release = true, &ref
				matchedReleases[candidate.EventID] = true
			}
			if candidate.Type != toolDispatchedType && candidate.Type != "tool/result" {
				continue
			}
			var tool struct {
				RunID      string `json:"run_id"`
				ToolCallID string `json:"tool_call_id"`
				ToolName   string `json:"tool_name"`
			}
			if json.Unmarshal(candidate.Payload, &tool) != nil {
				return nil, ErrEventIntegrity
			}
			if tool.RunID != fact.RunID || tool.ToolCallID != fact.ToolCallID {
				continue
			}
			ordered = ordered && candidate.Seq < event.Seq && tool.ToolName == fact.ToolName
			if candidate.Type == toolDispatchedType {
				dispatches++
			} else {
				results++
			}
		}
		proof.ResultOrderVerified = ordered && dispatches == 1 && (fact.Window == "after_record" && results == 1 || fact.Window != "after_record" && results == 0)
		proofs = append(proofs, proof)
	}
	for _, event := range events {
		if event.Type == "evaluation/fault-released" && !matchedReleases[event.EventID] {
			return nil, ErrEventIntegrity
		}
	}
	return proofs, nil
}
