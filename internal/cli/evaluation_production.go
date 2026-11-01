package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/memory"
	"code-agent/internal/orchestrator"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
)

type evaluationProduction struct {
	ledger          *session.SQLiteEventLog
	tasks           *session.TaskWorkspaces
	runner          *sandbox.OpenSandboxRunner
	owner           uint
	id              string
	source          string
	proof           session.TaskWorkspaceEvidence
	identity        sandbox.OpenSandboxIdentity
	memoryNamespace string
}

// A failed bootstrap still owns a retained preparation. Preserve its identity
// and verified references without inventing a run or exporting error text.
func (p *evaluationProduction) refuse(ctx context.Context, cfg config.Config, request EvaluationRequest, expected session.EvaluationBinding, output io.Writer, code string, diagnostic map[string]string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	proof, proofErr := p.tasks.Evidence(ctx, p.owner, p.id)
	if proofErr != nil {
		proof = p.proof
		proof.Verified = false
		proof.AdmissionReady = false
	}
	state := "blocked"
	if code == "run_not_admitted" {
		state = "infra_error"
		view, err := p.tasks.Inspect(ctx, p.owner, p.id)
		switch {
		case err != nil:
			code = "production_workspace_evidence_unavailable"
		case !view.TestAvailable:
			code = "production_runtime_unavailable"
		case view.LeaseExpiresAt != nil && !view.LeaseExpiresAt.After(time.Now()):
			code = "production_workspace_lease_expired"
		case view.State != "prepared":
			code = "production_workspace_integrity_unconfirmed"
		case !proof.AdmissionReady:
			code = "production_workspace_outcomes_unconfirmed"
		}
		diagnostic = map[string]string{"stage": "workspace_admission"}
	}
	if code == "orchestrator_startup_unavailable" {
		state = "infra_error"
	}
	snapshot, snapshotErr := session.ReadVerifiedSnapshot(ctx, p.ledger, p.id)
	usage, usageErr := evaluationUsage(ctx, p.ledger, p.id)
	if usageErr != nil {
		usage = EvaluationUsage{State: "unknown", CostStatus: "unknown"}
	}
	head, headID := "", ""
	if len(snapshot.Events) > 0 {
		last := snapshot.Events[len(snapshot.Events)-1]
		head, headID = last.Checksum, last.EventID
	}
	if err := json.NewEncoder(output).Encode(map[string]any{
		"type": "receipt", "schema_version": 1, "run_id": request.RunID, "task_id": request.TaskID, "session_id": p.id, "product_run_id": "", "trace_id": "", "pid": os.Getpid(),
		"runtime_contract": "production_opensandbox", "product_path": "opensandbox-production", "execution_path": "go-session-runner-grpc", "source_workspace": p.source, "workspace": p.proof.WorkingDir,
		"lifecycle": request.Lifecycle, "turn_id": request.TurnID, "model": request.Model, "budget": request.Budget, "policy": request.Policy, "effective_policy": request.Policy,
		"product_runtime": expected.Runtime, "permissions_sha256": expected.PermissionsSHA256, "opensandbox": p.identity, "task_workspace": proof, "usage": usage,
		"ledger":     map[string]any{"path": cfg.SessionDBPath, "session_id": p.id, "event_count": len(snapshot.Events), "head_event_id": headID, "head_checksum": head, "verified": snapshotErr == nil},
		"task_state": state, "error_code": code, "diagnostic": diagnostic, "admission_finalized": false,
	}); err != nil {
		return err
	}
	return errors.New(code)
}

func evaluationSandboxConfig(cfg config.Config) sandbox.Config {
	return sandbox.Config{Backend: cfg.Sandbox.Backend, TrustRoot: cfg.Sandbox.TrustRoot, Image: cfg.Sandbox.Image, Toolchain: cfg.Sandbox.Toolchain, AllowWorkspaceWrite: cfg.Sandbox.AllowWorkspaceWrite, MemoryLimit: cfg.Sandbox.MemoryLimit, CPULimit: cfg.Sandbox.CPULimit, PidsLimit: cfg.Sandbox.PidsLimit, TmpfsSize: cfg.Sandbox.TmpfsSize, OpenSandboxManifest: cfg.Sandbox.OpenSandboxManifest, OpenSandboxManifestSHA256: cfg.Sandbox.OpenSandboxManifestSHA256}
}

// Preparation precedes application bootstrap so discovery manifests and every
// model-facing file operation are confined to the retained product copy.
func prepareEvaluationProduction(ctx context.Context, cfg config.Config, request EvaluationRequest, source string) (*evaluationProduction, error) {
	root, err := filepath.EvalSymlinks(cfg.Sandbox.TrustRoot)
	if err != nil || !filepath.IsAbs(cfg.Sandbox.TrustRoot) || !strings.EqualFold(filepath.Clean(root), filepath.Clean(cfg.Sandbox.TrustRoot)) || strings.EqualFold(filepath.Clean(root), filepath.Clean(source)) || cfg.Sandbox.AllowWorkspaceWrite {
		return nil, errors.New("production_runtime_required")
	}
	runner := sandbox.NewOpenSandboxRunner(evaluationSandboxConfig(cfg))
	profile, err := runner.ApprovedIdentity(ctx)
	if err != nil {
		return nil, errors.New("production_runtime_unavailable")
	}
	base := configuredCLIActor(cfg)
	actor, err := base.BindSession("evaluation-owner")
	if err != nil {
		return nil, errors.New("actor_unavailable")
	}
	owner, err := memory.OwnerForActor(actor)
	if err != nil {
		return nil, errors.New("actor_unavailable")
	}
	ledger, err := session.OpenSQLiteEventLog(cfg.SessionDBPath)
	if err != nil {
		return nil, errors.New("canonical_ledger_unavailable")
	}
	prod := &evaluationProduction{ledger: ledger, runner: runner, owner: owner, source: source, identity: profile}
	defer func() {
		if prod.id == "" {
			_ = ledger.Close()
		}
	}()
	w := session.NewWorkbench(ledger, nil)
	var current session.SessionView
	if request.SessionID == "" {
		if request.Operation == "continue" || request.Operation == "inspect" || request.Operation == "finalize" {
			return nil, errors.New("session_required")
		}
		current, err = w.CreateWithWorkingDir(ctx, owner, "evaluation", request.TaskID, request.Message, source)
	} else {
		current, err = w.Get(ctx, owner, request.SessionID)
	}
	if err != nil || !strings.EqualFold(filepath.Clean(current.WorkingDir), filepath.Clean(source)) {
		return nil, errors.New("session_scope_mismatch")
	}
	prod.id = current.ID
	budget := admission.NewBudget(ledger)
	budgetOwner := admission.ActorOwner(actor)
	prod.tasks = session.NewTaskWorkspaces(w, func(context.Context) (uint, error) { return owner, nil }, source, root).WithBaselineRuntime(session.TaskBaselineRuntime{
		Toolchain: cfg.Sandbox.Toolchain, Runner: runner, Ready: runner.Ready,
		Acquire: func(ctx context.Context, callerOwner uint, id string) error {
			if callerOwner != owner {
				return session.ErrSessionOwnerRequired
			}
			_, err := budget.AcquireTask(ctx, budgetOwner, id)
			return err
		},
		Finish: func(ctx context.Context, callerOwner uint, id string) error {
			if callerOwner != owner {
				return session.ErrSessionOwnerRequired
			}
			_, err := budget.FinishTask(ctx, budgetOwner, id)
			return err
		},
	})
	if request.SessionID == "" {
		view, err := prod.tasks.Prepare(ctx, owner, current.ID, int64(current.EventCount), current.ID+":evaluation-prepare")
		if err != nil || view.State != "prepared" || !view.TestAvailable {
			return prod, errors.New("production_workspace_unavailable")
		}
	}
	prod.proof, err = prod.tasks.Evidence(ctx, owner, current.ID)
	if err != nil || !prod.proof.Verified {
		return prod, errors.New("production_workspace_unavailable")
	}
	snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, current.ID)
	if err != nil {
		return prod, errors.New("evidence_unavailable")
	}
	bound, err := session.ReadEvaluation(snapshot.Events)
	if err != nil {
		return prod, errors.New("policy_binding_mismatch")
	}
	prod.memoryNamespace = current.ID
	if bound != nil {
		prod.memoryNamespace = bound.MemoryNamespace
	}
	if request.Operation != "inspect" && request.Operation != "continue" && request.Operation != "finalize" && request.Operation != "grade" && !prod.proof.AdmissionReady {
		return prod, errors.New("production_workspace_not_ready")
	}
	return prod, nil
}

func (p *evaluationProduction) modelWorkspace(ctx context.Context, owner uint, id string) (session.TaskModelWorkspace, error) {
	if owner != p.owner {
		return session.TaskModelWorkspace{}, session.ErrSessionNotFound
	}
	if id != p.id {
		return p.tasks.ReadOnlyAgentModelWorkspace(ctx, owner, p.id, id)
	}
	return p.tasks.ModelWorkspace(ctx, owner, id)
}

func (p *evaluationProduction) toolWorkspace(ctx context.Context, actor identity.Actor, id, dir string, call orchestrator.ToolCall) (session.TaskModelWorkspace, error) {
	if actor.SessionID != id {
		return session.TaskModelWorkspace{}, session.ErrSessionNotFound
	}
	var workspace session.TaskModelWorkspace
	var err error
	if id != p.id {
		workspace, err = p.tasks.ReadOnlyAgentToolWorkspace(ctx, p.owner, p.id, actor, call)
	} else {
		workspace, err = p.tasks.ToolWorkspace(ctx, p.owner, id, call.ID)
	}
	if err != nil || !strings.EqualFold(filepath.Clean(workspace.WorkingDir), filepath.Clean(dir)) {
		return session.TaskModelWorkspace{}, session.ErrSessionStateConflict
	}
	return workspace, nil
}

func (p *evaluationProduction) agentWorkspace(ctx context.Context, actor identity.Actor, id string, call orchestrator.ToolCall) (session.TaskModelWorkspace, error) {
	if actor.SessionID != id {
		return session.TaskModelWorkspace{}, session.ErrSessionNotFound
	}
	return p.tasks.ReadOnlyAgentToolWorkspace(ctx, p.owner, p.id, actor, call)
}
