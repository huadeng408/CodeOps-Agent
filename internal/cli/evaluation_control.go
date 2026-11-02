package cli

import (
	"context"
	"errors"

	"code-agent/internal/admission"
	"code-agent/internal/config"
	"code-agent/internal/memory"
	"code-agent/internal/permission"
	"code-agent/internal/session"
	"code-agent/internal/telemetry/genai"
)

// Active abandonment still needs the existing runner's cancellation and reconciliation.
func newEvaluationControlApp(ctx context.Context, cfg config.Config, request EvaluationRequest, prod *evaluationProduction) (*App, error) {
	if request.Operation != "inspect" && request.Operation != "finalize" {
		return nil, nil
	}
	if request.SessionID == "" {
		return nil, errors.New("session_required")
	}
	store := session.NewSQLiteEventStore(cfg.SessionDBPath)
	retained := false
	defer func() {
		if !retained {
			_ = store.Close()
		}
	}()
	ledger, err := store.Ledger()
	if err != nil {
		return nil, errors.New("canonical_ledger_unavailable")
	}
	actor := configuredCLIActor(cfg)
	ownerActor, err := actor.BindSession("evaluation-owner")
	if err != nil {
		return nil, errors.New("actor_unavailable")
	}
	owner, err := memory.OwnerForActor(ownerActor)
	if err != nil {
		return nil, errors.New("actor_unavailable")
	}
	current, err := session.NewWorkbench(ledger, nil).Get(ctx, owner, request.SessionID)
	if err != nil {
		return nil, errors.New("session_scope_mismatch")
	}
	if request.Operation == "finalize" && (current.Run == nil || !evaluationTerminalRun(*current.Run)) {
		return nil, nil
	}
	if _, err := memory.NewFrozenView(memory.NewLedgerMemory(ledger), request.Policy.FrozenMemory, cfg.WorkingDir, owner); err != nil {
		return nil, errors.New("product_prerequisites_missing")
	}
	allow := make([]permission.AllowRule, 0, len(cfg.Permissions.Allow))
	for _, rule := range cfg.Permissions.Allow {
		allow = append(allow, permission.AllowRule{Tool: rule.Tool, Pattern: rule.Pattern})
	}
	deny := make([]permission.AllowRule, 0, len(cfg.Permissions.Deny))
	for _, rule := range cfg.Permissions.Deny {
		deny = append(deny, permission.AllowRule{Tool: rule.Tool, Pattern: rule.Pattern})
	}
	app := &App{
		cfg: cfg, actor: actor, ledger: ledger, session: session.NewManager(store),
		permissions: permission.NewControllerWithRules(nil, allow, deny),
		tokenBudget: admission.NewBudget(ledger), telemetry: genai.NewTelemetry(ctx),
	}
	app.modelGateway = admission.NewGateway(app.tokenBudget, admission.ProviderFromEnv())
	if prod != nil {
		app.modelGateway.SetRuntimeStopVerifier(prod.runner.ConfirmStopped)
	}
	retained = true
	return app, nil
}

func evaluationTerminalRun(run session.RunView) bool {
	return run.Status == session.RunCompleted || run.Status == session.RunFailed || run.Status == session.RunCanceled
}
