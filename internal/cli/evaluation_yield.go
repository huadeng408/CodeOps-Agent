package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"code-agent/internal/admission"
	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/session"
)

func runEvaluationYield(ctx context.Context, cfg config.Config, request EvaluationRequest, output io.Writer) error {
	refuse := func(code string) error { return evaluationRefusal(output, request, code) }
	if os.Getenv("CODE_AGENT_EVAL_ALLOW_TASK_YIELD") != "1" {
		return refuse("task_yield_not_enabled")
	}
	if request.SessionID == "" || len(request.ExpectedSessionHeadChecksum) != 64 || !request.DryRun && len(request.ExpectedAdmissionHeadChecksum) != 64 {
		return refuse("task_yield_pin_required")
	}
	info, err := os.Stat(cfg.SessionDBPath)
	if err != nil || info.IsDir() {
		return refuse("canonical_ledger_unavailable")
	}
	actor := identity.Actor{SchemaVersion: identity.SchemaVersion, ActorID: cfg.ActorID, Subject: cfg.ActorSubject, TenantID: cfg.ActorTenantID, Roles: append([]string(nil), cfg.ActorRoles...)}
	if actor.ActorID == "" {
		actor = identity.Default()
	}
	if raw := os.Getenv("CODE_AGENT_EVAL_YIELD_ACTOR_JSON"); raw != "" {
		if len(raw) > 8192 {
			return refuse("task_yield_actor_invalid")
		}
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		actor = identity.Actor{}
		if decoder.Decode(&actor) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			return refuse("task_yield_actor_invalid")
		}
	}
	actor, err = actor.BindSession(request.SessionID)
	if err != nil {
		return refuse("task_yield_actor_invalid")
	}
	openLedger := session.OpenSQLiteEventLog
	if request.DryRun {
		openLedger = session.OpenReadOnlySQLiteEventLog
	}
	ledger, err := openLedger(cfg.SessionDBPath)
	if err != nil {
		return refuse("canonical_ledger_unavailable")
	}
	defer ledger.Close()
	before, err := session.ReadVerifiedSnapshot(ctx, ledger, request.SessionID)
	if err != nil || len(before.Events) == 0 || before.Events[0].Type != "session/created" {
		return refuse("task_yield_source_unavailable")
	}
	if before.Events[len(before.Events)-1].Checksum != request.ExpectedSessionHeadChecksum {
		return refuse("task_yield_session_changed")
	}
	var created struct {
		WorkingDir string `json:"working_dir"`
	}
	if json.Unmarshal(before.Events[0].Payload, &created) != nil || !strings.EqualFold(filepath.Clean(created.WorkingDir), filepath.Clean(request.Workspace)) {
		return refuse("task_yield_workspace_mismatch")
	}
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	if gateway.CheckTaskYield(ctx, actor) != nil {
		return refuse("task_yield_not_quiescent_or_authorized")
	}
	admissionBefore, err := session.ReadVerifiedSnapshot(ctx, ledger, session.VerificationAdmissionLedgerID)
	if err != nil || len(admissionBefore.Events) == 0 {
		return refuse("task_yield_evidence_unavailable")
	}
	if request.ExpectedAdmissionHeadChecksum != "" && admissionBefore.Events[len(admissionBefore.Events)-1].Checksum != request.ExpectedAdmissionHeadChecksum {
		return refuse("task_yield_admission_changed")
	}
	batchBefore, err := budget.Inspect(ctx, admission.ActorOwner(actor))
	if err != nil {
		return refuse("task_yield_evidence_unavailable")
	}
	if !request.DryRun {
		if err := gateway.YieldTask(ctx, actor); err != nil {
			return refuse("task_yield_refused")
		}
	}
	after, sessionErr := session.ReadVerifiedSnapshot(ctx, ledger, request.SessionID)
	admissionAfter, admissionErr := session.ReadVerifiedSnapshot(ctx, ledger, session.VerificationAdmissionLedgerID)
	batchAfter, batchErr := budget.Inspect(ctx, admission.ActorOwner(actor))
	unchanged := sessionErr == nil && len(before.Events) == len(after.Events) && len(after.Events) > 0 && before.Events[len(before.Events)-1].Checksum == after.Events[len(after.Events)-1].Checksum
	accountingRetained := batchErr == nil && batchAfter.ID == batchBefore.ID && batchAfter.UsedTokens == batchBefore.UsedTokens && batchAfter.ReservedTokens == batchBefore.ReservedTokens && batchAfter.UnknownUsage == batchBefore.UnknownUsage && batchAfter.ActiveCalls == batchBefore.ActiveCalls
	admissionBoundaryValid := admissionErr == nil && len(admissionAfter.Events) > 0
	if request.DryRun {
		admissionBoundaryValid = admissionBoundaryValid && len(admissionAfter.Events) == len(admissionBefore.Events) && admissionAfter.Events[len(admissionAfter.Events)-1].Checksum == admissionBefore.Events[len(admissionBefore.Events)-1].Checksum
	} else {
		admissionBoundaryValid = admissionBoundaryValid && len(admissionAfter.Events) == len(admissionBefore.Events)+1 && admissionAfter.Events[len(admissionAfter.Events)-1].Type == "admission/task_yielded" && batchAfter.TaskID == ""
	}
	state, code := "yielded", ""
	if request.DryRun {
		state = "yieldable"
	}
	if !unchanged || !accountingRetained || !admissionBoundaryValid {
		state, code = "unknown", "task_yield_evidence_changed"
	}
	boundary := func(snapshot session.LedgerSnapshot) map[string]any {
		head, eventID, id := "", "", ""
		if len(snapshot.Events) > 0 {
			last := snapshot.Events[len(snapshot.Events)-1]
			head, eventID = last.Checksum, last.EventID
			id = last.SessionID
		}
		return map[string]any{"session_id": id, "event_count": len(snapshot.Events), "head_checksum": head, "head_event_id": eventID}
	}
	if err := json.NewEncoder(output).Encode(map[string]any{
		"type": "receipt", "schema_version": 1, "operation": "yield", "dry_run": request.DryRun, "run_id": request.RunID, "task_id": request.TaskID,
		"session_id": request.SessionID, "task_state": state, "error_code": code, "execution_path": "go-admission-operator",
		"session_before": boundary(before), "session_after": boundary(after), "session_unchanged": unchanged,
		"admission_before": boundary(admissionBefore), "admission_after": boundary(admissionAfter), "accounting_retained": accountingRetained,
		"admission_boundary_verified": admissionBoundaryValid,
		"batch":                       map[string]any{"id": batchAfter.ID, "task_id": batchAfter.TaskID, "used_tokens": batchAfter.UsedTokens, "reserved_tokens": batchAfter.ReservedTokens, "unknown_usage": batchAfter.UnknownUsage, "active_calls": batchAfter.ActiveCalls},
		"would_append":                "admission/task_yielded", "requires_updated_reader": true,
	}); err != nil {
		return err
	}
	if code != "" {
		return errors.New(code)
	}
	return nil
}
