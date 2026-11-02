package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/config"
	"code-agent/internal/memory"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
	"code-agent/internal/telemetry/genai"
	"code-agent/internal/tools"
	"go.opentelemetry.io/otel/trace"
)

type EvaluationBudget struct {
	MaxTokens        int64   `json:"max_tokens"`
	WallClockSeconds float64 `json:"wall_clock_seconds"`
}

type EvaluationRequest struct {
	RuntimeContract               string                   `json:"runtime_contract,omitempty"`
	GradeChecksPath               string                   `json:"grade_checks_path,omitempty"`
	GradeChecksSHA256             string                   `json:"grade_checks_sha256,omitempty"`
	SchemaVersion                 int                      `json:"schema_version"`
	RunID                         string                   `json:"run_id"`
	TaskID                        string                   `json:"task_id"`
	Workspace                     string                   `json:"workspace"`
	Message                       string                   `json:"message"`
	Model                         string                   `json:"model"`
	Budget                        EvaluationBudget         `json:"budget"`
	SessionID                     string                   `json:"session_id,omitempty"`
	Operation                     string                   `json:"operation,omitempty"`
	Lifecycle                     string                   `json:"lifecycle,omitempty"`
	TurnID                        string                   `json:"turn_id,omitempty"`
	FinalizeMode                  string                   `json:"finalize_mode,omitempty"`
	DryRun                        bool                     `json:"dry_run,omitempty"`
	ExpectedSessionHeadChecksum   string                   `json:"expected_session_head_checksum,omitempty"`
	ExpectedAdmissionHeadChecksum string                   `json:"expected_admission_head_checksum,omitempty"`
	Policy                        session.EvaluationPolicy `json:"policy"`
}

// RunEvaluation is the non-interactive product entry point; stdout is NDJSON only.
func RunEvaluation(ctx context.Context, cfg config.Config, input io.Reader, output, stderr io.Writer) error {
	var request EvaluationRequest
	decoder := json.NewDecoder(io.LimitReader(input, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return evaluationRefusal(output, request, "invalid_request")
	}
	existingSession := request.SessionID != ""
	if path := strings.TrimSpace(os.Getenv("CODE_AGENT_SESSION_LEDGER_PATH")); path != "" {
		if !filepath.IsAbs(path) {
			return evaluationRefusal(output, request, "invalid_canonical_ledger_path")
		}
		cfg.SessionDBPath = filepath.Clean(path)
	}
	if request.Model == "" || request.Model != admission.ProviderFromEnv().Model {
		return evaluationRefusal(output, request, "model_mismatch")
	}
	production := strings.EqualFold(strings.TrimSpace(string(cfg.Sandbox.Backend)), string(sandbox.BackendOpenSandbox))
	if request.RuntimeContract != "" && request.RuntimeContract != "production_opensandbox" {
		return evaluationRefusal(output, request, "invalid_runtime_contract")
	}
	if request.RuntimeContract == "production_opensandbox" && !production {
		return evaluationRefusal(output, request, "production_runtime_required")
	}
	if production {
		request.RuntimeContract = "production_opensandbox"
	}
	if request.Policy.ContextStrategy == "" {
		request.Policy.ContextStrategy = "metadata_on_demand"
	}
	if request.Policy.ExecutionStrategy == "" {
		request.Policy.ExecutionStrategy = "single"
	}
	if request.Policy.MaxToolRounds == 0 {
		request.Policy.MaxToolRounds = 16
	}
	if request.Policy.ContextMaxBytes == 0 {
		request.Policy.ContextMaxBytes = 12000
	}
	if request.Policy.FrozenMemory == nil {
		request.Policy.FrozenMemory = []session.FrozenMemoryReference{}
	}
	if request.Policy.Validate() != nil {
		return evaluationRefusal(output, request, "invalid_policy")
	}
	if request.Lifecycle != "" && request.Lifecycle != "hold" || request.TurnID != "" && !genai.ValidEvalJoinID(request.TurnID) || request.FinalizeMode != "" && (request.Operation != "finalize" || request.FinalizeMode != "finish" && request.FinalizeMode != "abandon") {
		return evaluationRefusal(output, request, "invalid_lifecycle")
	}
	if request.SchemaVersion != 1 || !genai.ValidEvalJoinID(request.RunID) || !genai.ValidEvalJoinID(request.TaskID) || strings.TrimSpace(request.Message) == "" || request.Budget.MaxTokens <= 0 || request.Budget.MaxTokens > admission.VerificationTokenLimit || request.Budget.WallClockSeconds <= 0 || request.Budget.WallClockSeconds > 86400 || math.IsNaN(request.Budget.WallClockSeconds) || math.IsInf(request.Budget.WallClockSeconds, 0) {
		return evaluationRefusal(output, request, "invalid_request")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return evaluationRefusal(output, request, "invalid_request")
	}
	workspace, err := filepath.EvalSymlinks(request.Workspace)
	if err != nil || !filepath.IsAbs(request.Workspace) || !strings.EqualFold(filepath.Clean(workspace), filepath.Clean(request.Workspace)) || request.Operation != "yield" && strings.EqualFold(filepath.Clean(workspace), filepath.Clean(cfg.ProjectRoot)) {
		return evaluationRefusal(output, request, "workspace_not_isolated")
	}
	root, err := exec.CommandContext(ctx, "git", "-C", workspace, "rev-parse", "--show-toplevel").Output()
	if err != nil || !strings.EqualFold(filepath.Clean(strings.TrimSpace(string(root))), filepath.Clean(workspace)) {
		return evaluationRefusal(output, request, "workspace_not_git_root")
	}
	if request.Operation != "" && request.Operation != "run" && request.Operation != "continue" && request.Operation != "inspect" && request.Operation != "yield" && request.Operation != "finalize" && request.Operation != "grade" {
		return evaluationRefusal(output, request, "invalid_operation")
	}
	if request.Operation == "yield" {
		return runEvaluationYield(ctx, cfg, request, output)
	}
	if request.DryRun || request.ExpectedSessionHeadChecksum != "" && request.Operation != "continue" && request.Operation != "finalize" && request.Operation != "grade" || request.ExpectedAdmissionHeadChecksum != "" || request.Operation != "grade" && (request.GradeChecksPath != "" || request.GradeChecksSHA256 != "") {
		return evaluationRefusal(output, request, "invalid_operation_fields")
	}
	if request.Operation == "grade" && (request.SessionID == "" || !production || request.GradeChecksPath == "" || len(request.GradeChecksSHA256) != 64 || request.ExpectedSessionHeadChecksum == "") {
		return evaluationRefusal(output, request, "invalid_grader_request")
	}
	moduleRoot := cfg.ProjectRoot
	for _, setting := range []struct {
		name    string
		target  *int
		maximum int
	}{
		{"CODE_AGENT_EVAL_ORCHESTRATOR_STARTUP_SECONDS", &cfg.OrchestratorStartupTimeout, 300},
		{"CODE_AGENT_EVAL_CONTEXT_WINDOW", &cfg.ContextWindow, 2_000_000},
	} {
		if raw, exists := os.LookupEnv(setting.name); exists {
			value, parseErr := strconv.Atoi(raw)
			if parseErr != nil || value <= 0 || value > setting.maximum {
				return evaluationRefusal(output, request, "invalid_evaluation_runtime")
			}
			*setting.target = value
		}
	}
	for i, arg := range cfg.OrchestratorArgs {
		raw := ""
		if strings.HasPrefix(arg, "--context-window=") {
			raw = strings.TrimPrefix(arg, "--context-window=")
		}
		if arg == "--context-window" && i+1 < len(cfg.OrchestratorArgs) {
			raw = cfg.OrchestratorArgs[i+1]
		}
		if raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value <= 0 {
				return evaluationRefusal(output, request, "invalid_evaluation_runtime")
			}
			if _, exists := os.LookupEnv("CODE_AGENT_EVAL_CONTEXT_WINDOW"); exists && value != cfg.ContextWindow {
				return evaluationRefusal(output, request, "context_window_configuration_conflict")
			}
			cfg.ContextWindow = value
		}
	}
	command, lookupErr := exec.LookPath(cfg.OrchestratorCommand)
	if lookupErr != nil {
		return evaluationRefusal(output, request, "orchestrator_command_unavailable")
	}
	runtimeConfig := session.EvaluationRuntime{OrchestratorCommand: command, StartupSeconds: cfg.OrchestratorStartupTimeout, ConversationSeconds: cfg.OrchestratorConversationTimeout, ContextWindow: cfg.ContextWindow, VisibleVerificationCommands: []string{}}
	providerProfile := admission.ProviderFromEnv()
	if providerProfile.ValidateResponseProfile() != nil {
		return evaluationRefusal(output, request, "invalid_model_response_profile")
	}
	if providerProfile.ValidateReasoningProfile() != nil {
		return evaluationRefusal(output, request, "invalid_model_reasoning_profile")
	}
	if providerProfile.ValidateIdentityProfile() != nil {
		return evaluationRefusal(output, request, "invalid_model_identity_profile")
	}
	if providerProfile.ValidateRoutingProfile() != nil {
		return evaluationRefusal(output, request, "invalid_model_routing_profile")
	}
	runtimeConfig.ModelProfile = providerProfile.EvaluationProfile()
	if cfg.WorktreeBaseRef == "" || cfg.WorktreeBaseRef == "fresh" {
		cfg.WorktreeBaseRef = "HEAD"
	}
	baseCommit, baseErr := exec.CommandContext(ctx, "git", "-C", workspace, "rev-parse", "--verify", cfg.WorktreeBaseRef+"^{commit}").Output()
	if baseErr != nil {
		return evaluationRefusal(output, request, "agent_baseline_unavailable")
	}
	runtimeConfig.AgentBaseRef, runtimeConfig.AgentBaseCommit = cfg.WorktreeBaseRef, strings.TrimSpace(string(baseCommit))
	if raw := os.Getenv("CODE_AGENT_EVAL_VISIBLE_VERIFICATION_COMMANDS_JSON"); raw != "" {
		if json.Unmarshal([]byte(raw), &runtimeConfig.VisibleVerificationCommands) != nil || len(runtimeConfig.VisibleVerificationCommands) > 8 {
			return evaluationRefusal(output, request, "invalid_visible_verification_commands")
		}
		for _, command := range runtimeConfig.VisibleVerificationCommands {
			if strings.TrimSpace(command) != command || command == "" || len(command) > 512 || strings.ContainsAny(command, "\x00\r\n") || tools.RedactSensitive(command) != command {
				return evaluationRefusal(output, request, "invalid_visible_verification_commands")
			}
		}
	}
	if runtimeConfig.VisibleVerificationCommands == nil {
		runtimeConfig.VisibleVerificationCommands = []string{}
	}
	if request.Policy.ExecutionStrategy == "verify_retry" && len(runtimeConfig.VisibleVerificationCommands) == 0 {
		return evaluationRefusal(output, request, "visible_verification_required")
	}
	permissionBytes, permissionErr := configureEvaluationPermissions(&cfg)
	if permissionErr != nil {
		return evaluationRefusal(output, request, "invalid_evaluation_permissions")
	}
	permissionHash := sha256.Sum256(permissionBytes)
	cfg.ProjectRoot, cfg.WorkingDir = workspace, workspace
	cfg.Model = request.Model
	cfg.MemoryDir = filepath.Join(workspace, ".agent", "evaluation-memory")
	if !production {
		cfg.Sandbox.TrustRoot = workspace
	}
	sandboxBytes, _ := json.Marshal(cfg.Sandbox)
	sandboxHash := sha256.Sum256(sandboxBytes)
	runtimeConfig.SandboxEnabled, runtimeConfig.SandboxBackend, runtimeConfig.SandboxImage, runtimeConfig.SandboxToolchain, runtimeConfig.SandboxAllowWorkspaceWrite = cfg.Sandbox.Enabled, string(cfg.Sandbox.Backend), cfg.Sandbox.Image, cfg.Sandbox.Toolchain, cfg.Sandbox.AllowWorkspaceWrite
	runtimeConfig.SandboxConfigSHA256 = hex.EncodeToString(sandboxHash[:])
	executionWorkspace := workspace
	var productionRuntime *evaluationProduction
	if production {
		productionRuntime, err = prepareEvaluationProduction(ctx, cfg, request, workspace)
		if productionRuntime != nil {
			defer productionRuntime.ledger.Close()
			request.SessionID = productionRuntime.id
			runtimeConfig.OpenSandbox = &productionRuntime.identity
			runtimeConfig.TaskWorkspaceRoot = cfg.Sandbox.TrustRoot
		}
		if err != nil {
			if productionRuntime != nil {
				return productionRuntime.refuse(ctx, cfg, request, session.EvaluationBinding{Runtime: runtimeConfig, PermissionsSHA256: hex.EncodeToString(permissionHash[:])}, output, err.Error(), map[string]string{"stage": "workspace_preparation"})
			}
			return evaluationRefusal(output, request, err.Error())
		}
		executionWorkspace = productionRuntime.proof.WorkingDir
		cfg.ProjectRoot, cfg.WorkingDir = executionWorkspace, executionWorkspace
		cfg.MemoryDir = filepath.Join(cfg.Sandbox.TrustRoot, ".runtime", "evaluation-memory", productionRuntime.id)
	}
	sourceWorkspace := ""
	if production {
		sourceWorkspace = workspace
	}
	bindingVersion := 1
	if providerProfile.ResponseMode == "sse" {
		bindingVersion = 2
	}
	if request.Policy.FaultWindow != "" {
		bindingVersion = 3
	}
	expectedBinding := session.EvaluationBinding{RuntimeContract: request.RuntimeContract, SourceWorkspace: sourceWorkspace, SchemaVersion: bindingVersion, RunID: request.RunID, TaskID: request.TaskID, Workspace: executionWorkspace, Model: request.Model, MaxTokens: request.Budget.MaxTokens, WallClockSeconds: request.Budget.WallClockSeconds, Policy: request.Policy, PermissionsSHA256: hex.EncodeToString(permissionHash[:]), Runtime: runtimeConfig, Lifecycle: request.Lifecycle}
	if productionRuntime != nil {
		expectedBinding.MemoryNamespace = productionRuntime.memoryNamespace
	}
	refuse := func(code string) error {
		if productionRuntime != nil {
			return productionRuntime.refuse(ctx, cfg, request, expectedBinding, output, code, nil)
		}
		return evaluationRefusal(output, request, code)
	}
	if request.Operation == "grade" {
		return runEvaluationGrade(ctx, cfg, request, productionRuntime, expectedBinding, output)
	}
	app, controlErr := newEvaluationControlApp(ctx, cfg, request, productionRuntime)
	if controlErr != nil {
		return refuse(controlErr.Error())
	}
	controlOnly := app != nil
	if !controlOnly {
		if cfg.OrchestratorAutoStart {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return refuse("orchestrator_address_unavailable")
			}
			cfg.OrchestratorAddr = listener.Addr().String()
			_ = listener.Close()
		}
		priorPythonPath, present := os.LookupEnv("PYTHONPATH")
		_ = os.Setenv("PYTHONPATH", moduleRoot+string(os.PathListSeparator)+priorPythonPath)
		defer func() {
			if present {
				_ = os.Setenv("PYTHONPATH", priorPythonPath)
			} else {
				_ = os.Unsetenv("PYTHONPATH")
			}
		}()
		app = newAppWithTaskInventory(cfg, strings.NewReader(""), io.Discard, stderr, false, production)
	}
	defer app.closeProductRuntime()
	app.evaluationPolicy = &request.Policy
	app.evaluationProduction = productionRuntime
	var outputMu sync.Mutex
	emit := func(value any) error {
		outputMu.Lock()
		defer outputMu.Unlock()
		return json.NewEncoder(output).Encode(value)
	}
	started := make(chan struct{})
	app.evaluationObserver = func(ctx context.Context, boundary session.EvaluationBoundary) {
		select {
		case <-started:
		case <-ctx.Done():
			return
		}
		_ = emit(map[string]any{"type": "fault_ready", "schema_version": 1, "run_id": request.RunID, "task_id": request.TaskID, "pid": os.Getpid(), "window": boundary.Window, "session_id": boundary.SessionID, "product_run_id": boundary.RunID, "tool_call_id": boundary.ToolCallID, "tool_name": boundary.ToolName, "fault_boundary": boundary})
		<-ctx.Done()
	}
	if !controlOnly {
		if _, err := app.initializeMemory(); err != nil || app.agentRunner == nil {
			if app.orchestratorErr != nil {
				diagnostic := orchestrator.FailureDetails(app.orchestratorErr)
				diagnostic["stage"] = "orchestrator_startup"
				if productionRuntime != nil {
					return productionRuntime.refuse(ctx, cfg, request, expectedBinding, output, "orchestrator_startup_unavailable", diagnostic)
				}
				return evaluationStartupRefusal(output, request, diagnostic, runtimeConfig)
			}
			return refuse("product_prerequisites_missing")
		}
	}
	workbench := session.NewWorkbench(app.ledger, nil)
	baseActor := app.actor
	ownerActor, err := baseActor.BindSession("evaluation-owner")
	if err != nil {
		return refuse("actor_unavailable")
	}
	owner, err := memory.OwnerForActor(ownerActor)
	if err != nil {
		return refuse("actor_unavailable")
	}
	var current session.SessionView
	if request.SessionID == "" {
		if request.Operation == "continue" || request.Operation == "inspect" || request.Operation == "finalize" {
			return refuse("session_required")
		}
		current, err = workbench.CreateWithWorkingDir(ctx, owner, "evaluation", request.TaskID, request.Message, workspace)
	} else {
		current, err = workbench.Get(ctx, owner, request.SessionID)
		if err == nil && !strings.EqualFold(filepath.Clean(current.WorkingDir), workspace) {
			err = session.ErrSessionStateConflict
		}
	}
	if err != nil {
		return refuse("session_scope_mismatch")
	}
	prebind, err := session.ReadVerifiedSnapshot(ctx, app.ledger, current.ID)
	if err != nil {
		return refuse("session_read_unavailable")
	}
	previousPolicy, err := session.ReadEvaluation(prebind.Events)
	if err != nil {
		return refuse("policy_binding_mismatch")
	}
	expectedBinding.MemoryNamespace = current.ID
	if previousPolicy != nil {
		expectedBinding.MemoryNamespace = previousPolicy.MemoryNamespace
	}
	if err := session.BindEvaluation(ctx, app.ledger, current.ID, expectedBinding); err != nil {
		return refuse("policy_binding_mismatch")
	}
	if current, err = workbench.Get(ctx, owner, current.ID); err != nil {
		return refuse("session_read_unavailable")
	}
	boundSnapshot, err := session.ReadVerifiedSnapshot(ctx, app.ledger, current.ID)
	if err != nil {
		return refuse("session_read_unavailable")
	}
	finalization, err := session.ReadEvaluationFinalization(boundSnapshot.Events)
	if err != nil {
		return refuse("evidence_unavailable")
	}
	grade, err := session.ReadEvaluationGrade(boundSnapshot.Events)
	if err != nil {
		return refuse("evidence_unavailable")
	}
	if grade != nil && request.Operation != "inspect" && request.Operation != "finalize" {
		return refuse("trial_already_graded")
	}
	if finalization != nil && request.Operation != "inspect" && request.Operation != "finalize" {
		return refuse("trial_finalized")
	}
	if existingSession && (request.Operation == "" || request.Operation == "run") && (request.Lifecycle != "hold" || request.TurnID == "") {
		return refuse("multi_turn_requires_hold_and_turn_id")
	}
	actor, err := baseActor.BindSession(current.ID)
	if err != nil {
		return refuse("actor_unavailable")
	}
	if request.ExpectedSessionHeadChecksum != "" && (len(boundSnapshot.Events) == 0 || boundSnapshot.Events[len(boundSnapshot.Events)-1].Checksum != request.ExpectedSessionHeadChecksum) {
		return refuse("evaluation_session_changed")
	}
	if err := app.permissions.SetScope(actor); err != nil {
		return refuse("permissions_unavailable")
	}
	trialContext, trialSpan := app.telemetry.StartSpan(genai.WithEvalJoinBaggage(ctx, request.RunID, current.ID), "eval.run", genai.OperationInvokeAgent, genai.SystemGenAI)
	defer trialSpan.End()
	traceID := trace.SpanContextFromContext(trialContext).TraceID().String()
	if !trace.SpanContextFromContext(trialContext).IsValid() {
		traceID = ""
	}
	duration := time.Duration(request.Budget.WallClockSeconds * float64(time.Second))
	deadline := current.CreatedAt.Add(duration)
	if request.Operation == "inspect" || request.Operation == "finalize" {
		deadline = time.Now().Add(10 * time.Second)
	}
	trialContext, cancel := context.WithDeadline(trialContext, deadline)
	defer cancel()
	app.modelGateway.SetTaskAdmission(func(callContext context.Context, taskID, sessionID string) error {
		if taskID != current.ID {
			return errors.New("evaluation task scope rejected")
		}
		if productionRuntime != nil {
			if _, err := productionRuntime.modelWorkspace(callContext, owner, sessionID); err != nil {
				return err
			}
		}
		return trialContext.Err()
	})
	var run session.RunView
	reconciliations := []session.EvaluationWriteReconciliation{}
	recoveryBlocked := false
	if request.Operation == "inspect" || request.Operation == "finalize" {
		if current.Run != nil {
			run = *current.Run
		}
		if controlOnly && request.Operation == "finalize" && !evaluationTerminalRun(run) {
			return refuse("evaluation_session_changed")
		}
	} else if request.Operation == "continue" {
		if current.Run == nil {
			return refuse("continuation_unavailable")
		}
		if current.Run.Status == session.RunRunning || current.Run.Status == session.RunQueued || current.Run.Status == session.RunCanceling {
			if err := app.tokenBudget.RecoverStoppedScopes(trialContext, admission.ActorOwner(actor), current.ID); err != nil {
				return refuse("recovery_process_stop_unconfirmed")
			}
			reconciliations, err = reconcileEvaluationWrites(trialContext, app, actor, current.Run.RunID)
			if err != nil {
				recoveryBlocked, err = true, nil
			} else {
				err = app.agentRunner.RecoverSession(trialContext, owner, current.ID)
			}
			run = *current.Run
		} else {
			requestID := sha256.Sum256([]byte(request.RunID + ":continue:" + current.Run.RunID + ":" + current.Run.CheckpointHash))
			run, err = app.agentRunner.RequestContinuation(trialContext, session.ContinueCommand{RequestID: hex.EncodeToString(requestID[:]), SessionID: current.ID, CheckpointHash: current.Run.CheckpointHash, OwnerID: owner, ExpectedSeq: int64(current.EventCount), Actor: actor})
		}
	} else {
		identity := sha256.Sum256([]byte(request.RunID + "\x00" + request.TaskID + "\x00" + request.TurnID + "\x00" + request.Message))
		run, err = app.agentRunner.SubmitMessage(trialContext, session.SubmitMessageCommand{RequestID: hex.EncodeToString(identity[:]), SessionID: current.ID, OwnerID: owner, ExpectedSeq: int64(current.EventCount), Content: request.Message, Actor: actor})
	}
	if err != nil {
		return refuse("run_not_admitted")
	}
	base := map[string]any{"schema_version": 1, "run_id": request.RunID, "task_id": request.TaskID, "session_id": current.ID, "product_run_id": run.RunID, "trace_id": traceID, "pid": os.Getpid(), "workspace": executionWorkspace, "source_workspace": workspace, "runtime_contract": request.RuntimeContract, "product_path": "cli-file-tools", "execution_path": "go-session-runner-grpc", "policy": request.Policy, "effective_policy": request.Policy, "budget": request.Budget, "model": request.Model}
	base["memory_namespace"] = expectedBinding.MemoryNamespace
	if productionRuntime != nil {
		base["product_path"] = "opensandbox-production"
		base["task_workspace"] = productionRuntime.proof
		base["opensandbox"] = productionRuntime.identity
	}
	base["permissions"] = json.RawMessage(permissionBytes)
	base["product_runtime"] = runtimeConfig
	base["lifecycle"], base["turn_id"] = request.Lifecycle, request.TurnID
	base["permissions_sha256"] = hex.EncodeToString(permissionHash[:])
	base["type"] = "started"
	if err := emit(base); err != nil {
		return err
	}
	close(started)
	if request.Operation == "finalize" && request.FinalizeMode == "abandon" && finalization == nil && current.Run != nil && run.Status != session.RunCompleted && run.Status != session.RunFailed && run.Status != session.RunCanceled {
		if err := app.tokenBudget.RecoverStoppedScopes(trialContext, admission.ActorOwner(actor), current.ID); err != nil {
			return evaluationRefusal(output, request, "recovery_process_stop_unconfirmed")
		}
		reconciliations, err = reconcileEvaluationWrites(trialContext, app, actor, run.RunID)
		if err != nil {
			recoveryBlocked, err = true, nil
		} else {
			run, err = app.agentRunner.CancelRun(trialContext, session.CancelRunCommand{RequestID: request.RunID + ":abandon", SessionID: current.ID, RunID: run.RunID, OwnerID: owner, Actor: actor})
		}
	}
	if !recoveryBlocked && request.Operation != "inspect" && (request.Operation != "finalize" || request.FinalizeMode == "abandon") {
		for run.Status != session.RunCompleted && run.Status != session.RunFailed && run.Status != session.RunCanceled {
			select {
			case <-trialContext.Done():
				cancelCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				run, _ = app.agentRunner.CancelRun(cancelCtx, session.CancelRunCommand{RequestID: request.RunID + ":deadline", SessionID: current.ID, RunID: run.RunID, OwnerID: owner, Actor: actor})
				stop()
				goto finished
			case <-time.After(20 * time.Millisecond):
				run, err = app.agentRunner.Run(trialContext, current.ID, run.RunID)
				if err != nil {
					goto finished
				}
			}
		}
	}
finished:
	// Drain memory reflection and all admitted scopes before deriving the final usage.
	if app.agentRunner != nil {
		_ = app.agentRunner.Close()
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finishCancel()
	var finalizationErr error
	if request.Operation == "finalize" || request.Operation != "inspect" && request.Lifecycle != "hold" {
		finalizationErr = app.modelGateway.FinishTask(finishCtx, actor)
		if finalizationErr != nil && request.Operation == "finalize" && request.FinalizeMode == "abandon" {
			finalizationErr = app.modelGateway.AbandonCanceledTask(finishCtx, actor)
		}
		if finalizationErr == nil {
			finalizationErr = session.FinalizeEvaluation(finishCtx, app.ledger, current.ID, request.FinalizeMode)
		}
	}
	snapshot, snapshotErr := session.ReadVerifiedSnapshot(finishCtx, app.ledger, current.ID)
	if snapshotErr == nil {
		finalization, snapshotErr = session.ReadEvaluationFinalization(snapshot.Events)
	}
	finalized := snapshotErr == nil && finalization != nil
	usage, usageErr := evaluationUsage(finishCtx, app.ledger, current.ID)
	state := string(run.Status)
	if state == "" {
		state = "unknown"
	}
	code := ""
	if recoveryBlocked {
		code, state = "tool_outcome_unknown", "infra_error"
	}
	if finalizationErr != nil && (request.Operation == "finalize" || run.Status == session.RunCompleted) {
		code, state = "admission_finalization_unavailable", "infra_error"
	}
	var diagnostic map[string]string
	var runDiagnostic map[string]string
	runErrorCode := ""
	if run.Status == session.RunFailed {
		for i := len(snapshot.Events) - 1; i >= 0; i-- {
			event := snapshot.Events[i]
			if event.Type != "execution_result" {
				continue
			}
			var failure struct {
				RunID      string            `json:"run_id"`
				Code       string            `json:"error_code"`
				Diagnostic map[string]string `json:"diagnostic"`
			}
			if json.Unmarshal(event.Payload, &failure) == nil && (failure.RunID == "" || failure.RunID == run.RunID) {
				runErrorCode, runDiagnostic = failure.Code, failure.Diagnostic
				break
			}
		}
		if usage.CallCount == 0 && usageErr == nil {
			if request.Operation != "finalize" {
				state = "infra_error"
			}
			if runErrorCode == "" || runErrorCode == "continuation_runtime_error" || runErrorCode == "orchestrator_terminal_unknown" {
				runErrorCode = "orchestrator_pre_dispatch_failed"
			}
		}
		if request.Operation != "inspect" && request.Operation != "finalize" {
			code, diagnostic = runErrorCode, runDiagnostic
		}
	}
	if code == "token_budget_exhausted" {
		code = "token_budget_exceeded"
		state = "budget_exceeded"
	}
	if trialContext.Err() != nil && request.Operation != "inspect" && request.Operation != "finalize" {
		code = "wall_clock_budget_exhausted"
	}
	if code == "wall_clock_budget_exhausted" {
		state = "budget_exceeded"
	}
	if finalized && finalizationErr == nil && (request.Operation == "inspect" || request.Operation == "finalize") {
		state = "finalized"
	}
	if err != nil && state != "finalized" {
		code = "run_read_unavailable"
	}
	if snapshotErr != nil || usageErr != nil {
		code = "evidence_unavailable"
		state = "unknown"
	}
	head, headEventID := "", ""
	if len(snapshot.Events) > 0 {
		head = snapshot.Events[len(snapshot.Events)-1].Checksum
		headEventID = snapshot.Events[len(snapshot.Events)-1].EventID
	}
	base["type"] = "receipt"
	base["task_state"] = state
	base["run_state"] = string(run.Status)
	base["run_error_code"], base["run_diagnostic"] = runErrorCode, runDiagnostic
	base["reconciliations"] = reconciliations
	base["admission_finalized"] = finalized
	base["checkpoint_hash"] = run.CheckpointHash
	base["usage"] = usage
	toolCount := 0
	for _, event := range snapshot.Events {
		if event.Type == "tool/call" {
			toolCount++
		}
	}
	base["ledger"] = map[string]any{"path": cfg.SessionDBPath, "session_id": current.ID, "event_count": len(snapshot.Events), "head_event_id": headEventID, "head_checksum": head, "verified": snapshotErr == nil}
	if productionRuntime != nil {
		proof, err := productionRuntime.tasks.Evidence(finishCtx, owner, current.ID)
		base["task_workspace"] = proof
		if err != nil {
			state, code = "unknown", "production_workspace_evidence_unavailable"
			base["task_state"] = state
		}
	}
	base["tool_call_count"] = toolCount
	evidence, evidenceErr := session.ReadEvaluationExecutionEvidence(finishCtx, app.ledger, actor, runtimeConfig.VisibleVerificationCommands)
	base["execution_evidence"] = evidence
	var childCalls *int
	if evidenceErr == nil && usageErr == nil && (len(evidence.ChildSessionIDs) == 0 || usage.UnattributedCalls == 0) {
		count := 0
		for _, id := range evidence.ChildSessionIDs {
			count += usage.SessionCallCounts[id]
		}
		childCalls = &count
	}
	base["strategy_evidence"] = map[string]any{"verified": evidenceErr == nil, "child_session_ids": evidence.ChildSessionIDs, "child_model_call_count": childCalls, "visible_verification_count": evidence.VisibleVerificationCount, "retry_evidence": evidence.RetryEvidence}
	if evidenceErr != nil {
		state, code = "unknown", "execution_evidence_unavailable"
		base["task_state"] = state
	}
	reportedModels := map[string]bool{}
	revisions := map[string]bool{}
	profiles := []session.EvaluationModelProfile{}
	profileKeys := map[string]bool{}
	observations := 0
	for _, event := range snapshot.Events {
		if event.Type != "evaluation/model-response" {
			continue
		}
		var identity struct {
			Model    string                         `json:"reported_model"`
			Revision string                         `json:"system_fingerprint"`
			Profile  session.EvaluationModelProfile `json:"effective_request_profile"`
		}
		if json.Unmarshal(event.Payload, &identity) != nil {
			continue
		}
		observations++
		reportedModels[identity.Model] = true
		revisions[identity.Revision] = true
		if identity.Profile.Protocol != "" {
			key, _ := json.Marshal(identity.Profile)
			if !profileKeys[string(key)] {
				profileKeys[string(key)] = true
				profiles = append(profiles, identity.Profile)
			}
		}
	}
	models := make([]string, 0, len(reportedModels))
	for model := range reportedModels {
		models = append(models, model)
	}
	sort.Strings(models)
	fingerprints := make([]string, 0, len(revisions))
	for revision := range revisions {
		fingerprints = append(fingerprints, revision)
	}
	sort.Strings(fingerprints)
	revisionState := "unknown"
	if observations == usage.CallCount && observations > 0 && len(models) == 1 && models[0] == runtimeConfig.ModelProfile.ExpectedReportedModel && len(fingerprints) == 1 && fingerprints[0] != "" {
		revisionState = "verified"
	}
	base["model_identity"] = map[string]any{"requested_model": request.Model, "expected_reported_model": runtimeConfig.ModelProfile.ExpectedReportedModel, "reported_models": models, "system_fingerprints": fingerprints, "revision_status": revisionState, "observations": observations, "effective_request_profiles": profiles}
	base["error_code"] = code
	base["diagnostic"] = diagnostic
	if err := emit(base); err != nil {
		return err
	}
	if code != "" || request.Operation != "inspect" && state != "completed" && state != "finalized" {
		return errors.New("evaluation did not complete")
	}
	return nil
}

func configureEvaluationPermissions(cfg *config.Config) ([]byte, error) {
	cfg.Permissions.Allow = append([]config.PermissionRule(nil), cfg.Permissions.Allow...)
	cfg.Permissions.Deny = append([]config.PermissionRule(nil), cfg.Permissions.Deny...)
	allowed := map[string]bool{}
	for _, rule := range cfg.Permissions.Allow {
		allowed[rule.Tool] = true
	}
	for _, name := range strings.Split(os.Getenv("CODE_AGENT_EVAL_ALLOW_TOOLS"), ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		switch name {
		case "Read", "ReadSpill", "Glob", "Grep", "Write", "Edit", "RecallMemory", "ManageMemory", "SpawnAgent", "AgentTask", "PublishArtifact", "Bash", "Git":
		default:
			return nil, errors.New("unsupported evaluation tool approval")
		}
		if !allowed[name] {
			cfg.Permissions.Allow = append(cfg.Permissions.Allow, config.PermissionRule{Tool: name})
			allowed[name] = true
		}
	}
	for name, level := range permission.DefaultPermissions {
		if level != permission.AutoAllow && !allowed[name] {
			cfg.Permissions.Deny = append(cfg.Permissions.Deny, config.PermissionRule{Tool: name})
		}
	}
	sort.Slice(cfg.Permissions.Allow, func(i, j int) bool {
		return cfg.Permissions.Allow[i].Tool+cfg.Permissions.Allow[i].Pattern < cfg.Permissions.Allow[j].Tool+cfg.Permissions.Allow[j].Pattern
	})
	sort.Slice(cfg.Permissions.Deny, func(i, j int) bool {
		return cfg.Permissions.Deny[i].Tool+cfg.Permissions.Deny[i].Pattern < cfg.Permissions.Deny[j].Tool+cfg.Permissions.Deny[j].Pattern
	})
	return json.Marshal(cfg.Permissions)
}

type EvaluationUsage struct {
	State             string         `json:"state"`
	InputTokens       int64          `json:"input_tokens"` // Includes cached input tokens exactly once.
	OutputTokens      int64          `json:"output_tokens"`
	CachedInputTokens int64          `json:"cached_input_tokens"`
	ConfirmedTokens   int64          `json:"confirmed_tokens"`
	ReservedTokens    int64          `json:"reserved_tokens"`
	UnknownCalls      int            `json:"unknown_calls"`
	CallCount         int            `json:"call_count"`
	CostStatus        string         `json:"cost_status"`
	SessionCallCounts map[string]int `json:"-"`
	UnattributedCalls int            `json:"-"`
}

func evaluationUsage(ctx context.Context, ledger session.EventLog, taskID string) (EvaluationUsage, error) {
	usage := EvaluationUsage{State: "known", CostStatus: "unknown", SessionCallCounts: map[string]int{}}
	if _, err := admission.NewBudget(ledger).Inspect(ctx, 1); err != nil {
		return usage, err
	}
	snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, session.VerificationAdmissionLedgerID)
	if err != nil {
		return usage, err
	}
	type accounting struct {
		reserved int64
		settled  bool
		unknown  bool
	}
	calls := map[string]accounting{}
	for _, event := range snapshot.Events {
		var fact struct {
			TaskID    string `json:"task_id"`
			SessionID string `json:"session_id"`
			CallID    string `json:"call_id"`
			Tokens    int64  `json:"tokens"`
			Input     int64  `json:"input_tokens"`
			Output    int64  `json:"output_tokens"`
			Cached    int64  `json:"cached_tokens"`
		}
		if json.Unmarshal(event.Payload, &fact) != nil {
			return usage, session.ErrEventIntegrity
		}
		switch event.Type {
		case "admission/call_reserved":
			if fact.TaskID == taskID {
				calls[fact.CallID] = accounting{reserved: fact.Tokens}
				usage.CallCount++
				if fact.SessionID == "" {
					usage.UnattributedCalls++
				} else {
					usage.SessionCallCounts[fact.SessionID]++
				}
			}
		case "admission/call_settled":
			if call, ok := calls[fact.CallID]; ok && !call.settled {
				call.settled = true
				call.unknown = false
				calls[fact.CallID] = call
				usage.InputTokens += fact.Input
				usage.OutputTokens += fact.Output
				usage.CachedInputTokens += fact.Cached
				usage.ConfirmedTokens += fact.Tokens
			}
		case "admission/call_unknown":
			if call, ok := calls[fact.CallID]; ok {
				call.unknown = true
				calls[fact.CallID] = call
			}
		}
	}
	for _, call := range calls {
		if !call.settled {
			usage.ReservedTokens += call.reserved
			usage.UnknownCalls++
			usage.State = "unknown"
		}
	}
	if usage.CallCount == 0 {
		usage.State = "unknown"
	}
	return usage, nil
}

func (a *App) closeProductRuntime() {
	if a.agentRunner != nil {
		_ = a.agentRunner.Close()
	}
	if a.agentTools != nil {
		_ = a.agentTools.Close()
	}
	if a.modelGateway != nil {
		a.modelGateway.Close()
	}
	if a.orchestratorPM != nil {
		a.orchestratorPM.Stop()
	}
	if a.executor != nil {
		_ = a.executor.Close()
	}
	if a.mcp != nil {
		_ = a.mcp.Close()
	}
	if a.session != nil {
		_ = a.session.Close()
	}
	if a.telemetry != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.telemetry.Shutdown(ctx)
	}
}

func evaluationRefusal(output io.Writer, request EvaluationRequest, code string) error {
	return evaluationRefusalDetails(output, request, code, nil)
}

func evaluationStartupRefusal(output io.Writer, request EvaluationRequest, diagnostic map[string]string, runtime session.EvaluationRuntime) error {
	if err := json.NewEncoder(output).Encode(map[string]any{
		"type": "receipt", "schema_version": 1, "run_id": request.RunID, "task_id": request.TaskID,
		"session_id": "", "task_state": "infra_error", "error_code": "orchestrator_startup_unavailable",
		"execution_path": "go-session-runner-grpc", "budget": request.Budget, "policy": request.Policy,
		"model": request.Model, "diagnostic": diagnostic, "product_runtime": runtime,
	}); err != nil {
		return err
	}
	return errors.New("orchestrator_startup_unavailable")
}

func evaluationRefusalDetails(output io.Writer, request EvaluationRequest, code string, diagnostic map[string]string) error {
	if err := json.NewEncoder(output).Encode(map[string]any{
		"type": "receipt", "schema_version": 1, "run_id": request.RunID, "task_id": request.TaskID,
		"session_id": "", "task_state": "blocked", "error_code": code,
		"execution_path": "go-session-runner-grpc",
		"budget":         request.Budget, "policy": request.Policy, "model": request.Model,
		"diagnostic": diagnostic,
	}); err != nil {
		return err
	}
	return errors.New(code)
}
