package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"code-agent/internal/admission"
	"code-agent/internal/config"
	"code-agent/internal/session"
)

// This operator-only control path never bootstraps Python or sends a model request.
func runEvaluationGrade(ctx context.Context, cfg config.Config, request EvaluationRequest, prod *evaluationProduction, expected session.EvaluationBinding, output io.Writer) error {
	if prod == nil || prod.tasks == nil || prod.runner == nil || prod.id != request.SessionID || request.Lifecycle != "hold" || request.ExpectedSessionHeadChecksum == "" {
		return evaluationRefusal(output, request, "invalid_grade_scope")
	}
	snapshot, err := session.ReadVerifiedSnapshot(ctx, prod.ledger, prod.id)
	if err != nil || len(snapshot.Events) == 0 {
		return evaluationRefusal(output, request, "evidence_unavailable")
	}
	binding, err := session.ReadEvaluation(snapshot.Events)
	if err != nil || binding == nil {
		return evaluationRefusal(output, request, "policy_binding_mismatch")
	}
	actualJSON, _ := json.Marshal(binding)
	expectedJSON, _ := json.Marshal(expected)
	if string(actualJSON) != string(expectedJSON) {
		return evaluationRefusal(output, request, "policy_binding_mismatch")
	}
	actor, err := configuredCLIActor(cfg).BindSession(prod.id)
	if err != nil {
		return evaluationRefusal(output, request, "actor_unavailable")
	}
	root, err := session.ModelTaskRoot(ctx, prod.ledger, actor)
	if err != nil || root != prod.id {
		return evaluationRefusal(output, request, "session_scope_mismatch")
	}
	workbench := session.NewWorkbench(prod.ledger, nil)
	current, err := workbench.Get(ctx, prod.owner, prod.id)
	if err != nil || current.Run == nil || current.Run.Status != session.RunCompleted {
		return evaluationRefusal(output, request, "completed_model_candidate_required")
	}
	retained, err := session.ReadEvaluationGrade(snapshot.Events)
	if err != nil {
		return evaluationRefusal(output, request, "evidence_unavailable")
	}
	gradeID := sha256.Sum256([]byte("evaluation-grade-request/v1\x00" + prod.id + "\x00" + request.GradeChecksSHA256))
	command := session.EvaluationGradeCommand{OwnerID: prod.owner, Actor: actor, SessionID: prod.id, RequestID: hex.EncodeToString(gradeID[:]), ExpectedSessionHeadChecksum: request.ExpectedSessionHeadChecksum, ChecksPath: request.GradeChecksPath, ChecksSHA256: request.GradeChecksSHA256}
	if retained == nil {
		if err := prod.tasks.ValidateEvaluationGradeChecks(ctx, request.GradeChecksPath, request.GradeChecksSHA256); err != nil {
			return evaluationRefusal(output, request, "grade_checks_unavailable")
		}
		finalized, readErr := session.ReadEvaluationFinalization(snapshot.Events)
		if readErr != nil || finalized != nil || snapshot.Events[len(snapshot.Events)-1].Checksum != request.ExpectedSessionHeadChecksum {
			return evaluationRefusal(output, request, "evaluation_session_changed")
		}
		budget := admission.NewBudget(prod.ledger)
		batch, budgetErr := budget.Inspect(ctx, admission.ActorOwner(actor))
		if budgetErr != nil || batch.ActiveCalls != 0 || batch.TaskID != prod.id || batch.TaskOwnerID != admission.ActorOwner(actor) {
			return evaluationRefusal(output, request, "grader_admission_unavailable")
		}
		gateway := admission.NewGateway(budget, admission.ProviderFromEnv())
		if err := gateway.Start(); err != nil {
			return evaluationRefusal(output, request, "grader_admission_unavailable")
		}
		defer gateway.Close()
		gateway.SetRuntimeStopVerifier(prod.runner.ConfirmStopped)
		prod.tasks.WithProposalScope(func(scopeCtx context.Context, owner uint, id string) (func() error, error) {
			if owner != prod.owner || id != prod.id {
				return nil, session.ErrSessionNotFound
			}
			return gateway.BindExistingTask(scopeCtx, admission.ActorOwner(actor), id)
		})
	}
	grade, gradeErr := prod.tasks.GradeEvaluation(ctx, command)
	snapshot, snapshotErr := session.ReadVerifiedSnapshot(ctx, prod.ledger, prod.id)
	proof, proofErr := prod.tasks.Evidence(ctx, prod.owner, prod.id)
	usage, usageErr := evaluationUsage(ctx, prod.ledger, prod.id)
	state, code := "graded", ""
	var diagnostic map[string]string
	if grade.Reason != "" {
		diagnostic = map[string]string{"stage": grade.Reason}
	}
	if gradeErr != nil || grade.State != "completed" {
		state, code = "scorer_error", "grader_unavailable"
		if gradeErr != nil && grade.IntentEventID == "" {
			code = "grader_preflight_error"
		} else if grade.State == "unknown" {
			code = "grader_outcome_unknown"
		}
	}
	if snapshotErr != nil || proofErr != nil || usageErr != nil || len(snapshot.Events) == 0 {
		state, code = "unknown", "evidence_unavailable"
	}
	head, headID := "", ""
	if len(snapshot.Events) > 0 {
		head, headID = snapshot.Events[len(snapshot.Events)-1].Checksum, snapshot.Events[len(snapshot.Events)-1].EventID
	}
	receipt := map[string]any{
		"type": "receipt", "schema_version": 1, "operation": "grade", "run_id": request.RunID, "task_id": request.TaskID, "session_id": prod.id, "product_run_id": current.Run.RunID,
		"pid": os.Getpid(), "task_state": state, "run_state": string(current.Run.Status), "error_code": code, "diagnostic": diagnostic, "grade": grade,
		"workspace": prod.proof.WorkingDir, "source_workspace": prod.source, "runtime_contract": request.RuntimeContract, "product_path": "opensandbox-production", "execution_path": "go-session-runner-grpc",
		"policy": request.Policy, "effective_policy": request.Policy, "permissions_sha256": binding.PermissionsSHA256, "budget": request.Budget, "model": request.Model, "product_runtime": binding.Runtime,
		"task_workspace": proof, "lifecycle": "hold", "turn_id": request.TurnID, "admission_finalized": false, "usage": usage, "opensandbox": prod.identity,
		"ledger": map[string]any{"path": cfg.SessionDBPath, "session_id": prod.id, "event_count": len(snapshot.Events), "head_event_id": headID, "head_checksum": head, "verified": snapshotErr == nil},
	}
	if err := json.NewEncoder(output).Encode(receipt); err != nil {
		return err
	}
	if code != "" || strings.TrimSpace(head) == "" {
		return errors.New("evaluation grading is not complete")
	}
	return nil
}
