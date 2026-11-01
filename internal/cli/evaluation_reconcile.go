package cli

import (
	"context"
	"encoding/json"
	"errors"

	"code-agent/internal/identity"
	"code-agent/internal/session"
)

func reconcileEvaluationWrites(ctx context.Context, app *App, actor identity.Actor, runID string) ([]session.EvaluationWriteReconciliation, error) {
	results := []session.EvaluationWriteReconciliation{}
	snapshot, err := session.ReadVerifiedSnapshot(ctx, app.ledger, actor.SessionID)
	if err != nil {
		return results, err
	}
	faultCalls := map[string]bool{}
	for _, event := range snapshot.Events {
		if event.Type != "evaluation/fault-ready" {
			continue
		}
		var fault struct {
			Window     string `json:"window"`
			ToolCallID string `json:"tool_call_id"`
		}
		if json.Unmarshal(event.Payload, &fault) != nil {
			return results, session.ErrEventIntegrity
		}
		if fault.Window == "before_effect" || fault.Window == "after_effect" {
			faultCalls[fault.ToolCallID] = true
		}
	}
	for _, event := range snapshot.Events {
		if event.Type != "evaluation/write-preimage" {
			continue
		}
		var call struct {
			RunID      string `json:"run_id"`
			ToolCallID string `json:"tool_call_id"`
		}
		if json.Unmarshal(event.Payload, &call) != nil {
			return results, session.ErrEventIntegrity
		}
		if call.RunID != runID || !faultCalls[call.ToolCallID] {
			continue
		}
		current, err := session.ReadVerifiedSnapshot(ctx, app.ledger, actor.SessionID)
		if err != nil || len(current.Events) == 0 {
			return results, session.ErrEventIntegrity
		}
		result, err := app.agentRunner.ReconcileEvaluationWrite(ctx, session.EvaluationWriteReconcileCommand{Actor: actor, SessionID: actor.SessionID, RunID: runID, ToolCallID: call.ToolCallID, ExpectedSessionHeadChecksum: current.Events[len(current.Events)-1].Checksum})
		results = append(results, result)
		if err != nil {
			return results, err
		}
		if !result.EffectKnown {
			return results, errors.New("evaluation Write outcome remains unknown")
		}
	}
	return results, nil
}
