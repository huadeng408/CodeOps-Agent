package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/config"
	"code-agent/internal/hooks"
	"code-agent/internal/identity"
	"code-agent/internal/mcp"
	"code-agent/internal/memory"
	"code-agent/internal/metrics"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/prompts"
	"code-agent/internal/rag"
	"code-agent/internal/recovery"
	"code-agent/internal/safety"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
	"code-agent/internal/skills"
	"code-agent/internal/telemetry/genai"
	"code-agent/internal/todo"
	"code-agent/internal/tools"
	"code-agent/internal/undo"
	"code-agent/internal/worktree"
)

type Options struct {
	Config config.Config
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type App struct {
	cfg            config.Config
	input          *InputBuffer
	renderer       *StreamRenderer
	status         *StatusLine
	metrics        *metrics.Collector
	session        *session.Manager
	memory         *memory.Manager
	todos          *todo.Manager
	permissions    *permission.Controller
	actor          identity.Actor
	orchestrator   *orchestrator.Client
	orchestratorPM *orchestrator.ProcessManager
	hooks          *hooks.Engine
	executor       *tools.Executor
	ragClient      *rag.Client
	ragIngester    rag.OpenFileIngester
	safety         *safety.Analyzer
	mcp            *mcp.Manager
	worktree       *worktree.Manager
	undo           *undo.Manager
	recovery       *recovery.Engine
	skills         *skills.Manager
	prompts        *prompts.Builder
	planMode       bool
	instructions   []config.InstructionSource
	interruptMu    sync.Mutex
	telemetry      genai.Tracer
	currentCancel  context.CancelFunc
	interrupts     int
	lastInterrupt  time.Time
}

func NewApp(cfg config.Config, stdin io.Reader, stdout io.Writer, stderr io.Writer) *App {
	instructions, _ := config.LoadInstructions(cfg.ProjectRoot, cfg.WorkingDir)
	allowlist := make([]permission.AllowRule, 0, len(cfg.Permissions.Allow))
	for _, rule := range cfg.Permissions.Allow {
		allowlist = append(allowlist, permission.AllowRule{Tool: rule.Tool, Pattern: rule.Pattern})
	}
	denylist := make([]permission.AllowRule, 0, len(cfg.Permissions.Deny))
	for _, rule := range cfg.Permissions.Deny {
		denylist = append(denylist, permission.AllowRule{Tool: rule.Tool, Pattern: rule.Pattern})
	}
	levels := map[string]permission.Level{}
	for tool, level := range permission.DefaultPermissions {
		levels[tool] = level
	}

	orchestratorManager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address:                cfg.OrchestratorAddr,
		AutoStart:              cfg.OrchestratorAutoStart,
		Command:                cfg.OrchestratorCommand,
		Args:                   cfg.OrchestratorArgs,
		ProjectRoot:            cfg.ProjectRoot,
		WorkingDir:             cfg.WorkingDir,
		MemoryDir:              cfg.MemoryDir,
		MaxTokens:              cfg.MaxTokensPerSession,
		MaxCost:                cfg.MaxCostPerSession,
		ModelFast:              cfg.ModelFast,
		RequireHarnessWorktree: true,
		StartupTimeout:         time.Duration(cfg.OrchestratorStartupTimeout) * time.Second,
		ConversationTimeout:    time.Duration(cfg.OrchestratorConversationTimeout) * time.Second,
	})
	orchestratorClient, _ := orchestratorManager.Client(context.Background())
	mcpManager := mcp.NewManager()
	_ = mcpManager.LoadConfigFile(resolveConfigPath(cfg.ProjectRoot, cfg.MCPConfig))
	_ = mcpManager.StartAll(context.Background())
	_ = mcpManager.WriteToolsManifest(filepath.Join(cfg.ProjectRoot, ".agent", "mcp-tools.json"))
	hookEngine := hooks.NewEngine()
	for _, hookConfig := range cfg.Hooks {
		hookEngine.RegisterCommandHook(hooks.CommandHook{
			Phase:   hooks.Phase(hookConfig.Type),
			Matcher: hookConfig.Matcher,
			Command: hookConfig.Command,
			WorkDir: cfg.ProjectRoot,
			Timeout: hookConfig.Timeout,
		})
	}

	executor := tools.NewExecutor(cfg.ProjectRoot)
	_ = executor.SetWorkingDir(cfg.WorkingDir)
	if cfg.Sandbox.Enabled {
		trustRoot := cfg.Sandbox.TrustRoot
		if strings.TrimSpace(trustRoot) == "" {
			trustRoot = cfg.ProjectRoot
		}
		executor.SetSandbox(sandbox.NewSandboxRunner(sandbox.Config{
			Backend:             cfg.Sandbox.Backend,
			WSLDistro:           cfg.Sandbox.WSLDistro,
			TrustRoot:           trustRoot,
			Image:               cfg.Sandbox.Image,
			AllowWorkspaceWrite: cfg.Sandbox.AllowWorkspaceWrite,
			MemoryLimit:         cfg.Sandbox.MemoryLimit,
			CPULimit:            cfg.Sandbox.CPULimit,
			PidsLimit:           cfg.Sandbox.PidsLimit,
			TmpfsSize:           cfg.Sandbox.TmpfsSize,
		}))
	}
	executor.SetMCPManager(mcpManager)
	ragClient := rag.NewClient(rag.Config{
		Enabled:       cfg.RAGEnabled,
		BaseURL:       cfg.RAGServerURL,
		InternalToken: cfg.RAGInternalSecret,
		UserID:        cfg.RAGUserID,
		OrgTag:        cfg.RAGOrgTag,
		IngestPublic:  cfg.RAGIngestPublic,
		IngestProvenance: rag.IngestProvenanceConfig{
			SourceID:         cfg.RAGSourceID,
			SourcePathPrefix: cfg.RAGSourcePathPrefix,
			SourceURL:        cfg.RAGSourceURL,
			SourceCommit:     cfg.RAGSourceCommit,
			TargetIndex:      cfg.RAGTargetIndex,
			CorpusGeneration: cfg.RAGCorpusGeneration,
			RunID:            cfg.RAGIngestRunID,
		},
	})
	executor.SetRAGSearcher(ragClient)
	skillsManager := skills.NewManager()
	globalSkillsDir := ""
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		globalSkillsDir = filepath.Join(home, ".agent", "skills")
	}
	if err := skillsManager.Discover(skills.DiscoveryOptions{
		GlobalDir:   globalSkillsDir,
		Directories: cfg.SkillDirectories,
		ProjectDir:  filepath.Join(cfg.ProjectRoot, ".agent", "skills"),
	}); err != nil && stderr != nil {
		fmt.Fprintf(stderr, "skills discovery failed: %v\n", err)
	}
	if err := skillsManager.WriteManifest(filepath.Join(cfg.ProjectRoot, ".agent", "skills.json")); err != nil && stderr != nil {
		fmt.Fprintf(stderr, "skills manifest failed: %v\n", err)
	}
	executor.SetSkillsManager(skillsManager)

	telemetry := genai.NewTelemetry(context.Background())
	orchestratorClient.SetTracer(telemetry)
	actor := identity.Actor{
		SchemaVersion: identity.SchemaVersion,
		ActorID:       cfg.ActorID,
		Subject:       cfg.ActorSubject,
		TenantID:      cfg.ActorTenantID,
		Roles:         append([]string(nil), cfg.ActorRoles...),
	}
	if actor.ActorID == "" {
		actor = identity.Default()
	}
	if orchestratorClient != nil {
		if err := orchestratorClient.SetActor(actor); err != nil && stderr != nil {
			fmt.Fprintf(stderr, "actor configuration failed: %v\n", err)
		}
	}
	executor.SetTracer(telemetry)

	app := &App{
		cfg:            cfg,
		input:          NewInputBuffer(stdin, stdout),
		renderer:       NewStreamRenderer(stdout),
		status:         NewStatusLine(),
		metrics:        metrics.NewCollector(),
		session:        session.NewManager(session.NewSQLiteEventStore(cfg.SessionDBPath)),
		memory:         memory.NewManager(cfg.MemoryDir),
		todos:          todo.NewManager(),
		permissions:    permission.NewControllerWithRules(levels, allowlist, denylist),
		actor:          actor,
		orchestrator:   orchestratorClient,
		orchestratorPM: orchestratorManager,
		hooks:          hookEngine,
		executor:       executor,
		ragClient:      ragClient,
		ragIngester:    ragClient,
		safety:         safety.NewAnalyzer(),
		mcp:            mcpManager,
		worktree:       worktree.NewManager(cfg.ProjectRoot, cfg.WorktreeBaseRef),
		undo:           undo.NewManager(),
		recovery:       recovery.NewEngine(),
		skills:         skillsManager,
		prompts:        prompts.NewBuilder(),
		instructions:   instructions,
		telemetry:      telemetry,
	}
	app.input.SetInterruptHandler(func() bool {
		return app.handleInterrupt(time.Now(), func() {})
	})
	if app.orchestrator != nil {
		app.orchestrator.OnCompaction = app.handleCompactionUpdate
		app.orchestrator.OnPlanTodoUpdate = app.handlePlanTodoUpdate
		app.orchestrator.OnAgentSpawn = app.handleAgentSpawn
		app.orchestrator.OnAgentLifecycle = app.handleAgentLifecycle
	}
	app.input.SetWorkspaceDir(cfg.WorkingDir)
	return app
}

func (a *App) Run(ctx context.Context) error {
	defer func() { _ = a.session.Close() }()
	defer func() {
		if a.executor != nil {
			_ = a.executor.Close()
		}
	}()
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	if a.orchestratorPM != nil {
		defer a.orchestratorPM.Stop()
		// Supervise the orchestrator process: auto-restart on crash so the
		// session survives an unexpected orchestrator exit between turns.
		go a.orchestratorPM.Monitor(runCtx)
	}
	cleanupSignals := a.setupSignalHandling(stop)
	defer cleanupSignals()

	// Flush pending OTel spans on exit so Phoenix receives the
	// invoke_agent span and all instrumented children. Use a fresh
	// context.Background() with a timeout — runCtx is already cancelled
	// by the signal handler on SIGTERM/Ctrl-C exit, and passing a cancelled
	// context makes the OTel batch processor return context.Canceled
	// without synchronously flushing, dropping the final spans.
	if a.telemetry != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = a.telemetry.Shutdown(shutdownCtx)
		}()
	}

	created := a.session.NewSession(a.cfg.WorkingDir)
	if err := a.bindSessionActor(created); err != nil {
		return fmt.Errorf("bind initial session actor: %w", err)
	}
	a.session.SetMode("chat")
	a.renderBootstrap()

	// Wire up streaming text rendering so each orchestrator text delta
	// is printed incrementally instead of accumulating into a single panel.
	if a.orchestrator != nil {
		a.orchestrator.OnTextDelta = func(delta string) {
			a.renderer.AppendAssistantText(delta)
		}
	}

	for {
		if err := runCtx.Err(); err != nil {
			return err
		}

		turnCtx, turnCancel := context.WithCancel(runCtx)
		a.setCurrentCancel(turnCancel)
		line, err := a.input.ReadLine(turnCtx)
		if err != nil {
			a.clearCurrentCancel()
			turnCancel()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if errors.Is(err, context.Canceled) && runCtx.Err() == nil {
				continue
			}
			if runCtx.Err() != nil {
				return runCtx.Err()
			}
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			a.clearCurrentCancel()
			turnCancel()
			continue
		}

		if strings.HasPrefix(line, "/") {
			if handled := a.handleSlashCommand(turnCtx, line); handled {
				a.clearCurrentCancel()
				turnCancel()
				continue
			}
		}

		a.metrics.BeginTurn()
		a.session.Append(session.RoleUser, line)
		reply := a.handleUserInput(turnCtx, line)
		a.metrics.EndTurn()
		if errors.Is(turnCtx.Err(), context.Canceled) && runCtx.Err() == nil {
			a.clearCurrentCancel()
			turnCancel()
			continue
		}
		a.clearCurrentCancel()
		turnCancel()
		a.session.Append(session.RoleAssistant, reply)
		// Streaming text was already rendered via OnTextDelta callbacks
		// during handleUserInput. Close the panel and fall back to full
		// panel rendering only when streaming produced no text.
		a.renderer.EndAssistantPanel()
		if strings.HasPrefix(reply, "[orchestrator") || strings.HasPrefix(reply, "[blocked") {
			a.renderer.PrintAssistant(reply)
		}
		a.renderer.PrintStatus(a.status.FormatWidth(a.metrics.Snapshot(), "status", a.renderer.Width()))
	}
}

func (a *App) setupSignalHandling(stop context.CancelFunc) func() {
	sigCh := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		for {
			select {
			case <-done:
				return
			case sig := <-sigCh:
				if sig == syscall.SIGTERM {
					stop()
					return
				}
				if a.handleInterrupt(time.Now(), stop) {
					return
				}
			}
		}
	}()
	return func() {
		signal.Stop(sigCh)
		close(done)
	}
}

func (a *App) setCurrentCancel(cancel context.CancelFunc) {
	a.interruptMu.Lock()
	defer a.interruptMu.Unlock()
	a.currentCancel = cancel
}

func (a *App) clearCurrentCancel() {
	a.interruptMu.Lock()
	defer a.interruptMu.Unlock()
	a.currentCancel = nil
}

func (a *App) handleInterrupt(now time.Time, stop context.CancelFunc) bool {
	a.interruptMu.Lock()
	if a.lastInterrupt.IsZero() || now.Sub(a.lastInterrupt) > time.Second {
		a.interrupts = 0
	}
	a.interrupts++
	a.lastInterrupt = now
	interrupts := a.interrupts
	cancel := a.currentCancel
	a.interruptMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if interrupts >= 3 {
		stop()
		return true
	}
	if a.renderer != nil {
		a.renderer.PrintInterrupted()
	}
	return false
}

func (a *App) renderBootstrap() {
	a.renderer.PrintBootstrap(BootstrapView{
		Version:   "v0.1",
		Branch:    currentBranch(a.cfg.WorkingDir),
		Workspace: a.cfg.WorkingDir,
		Mode:      "chat",
		Model:     a.cfg.Model,
	})
	a.renderer.PrintLine("/help | /ingest | /diff | /plan")
}

func currentBranch(root string) string {
	branch, err := runGit(context.Background(), root, "branch", "--show-current")
	if err != nil || strings.TrimSpace(branch) == "" {
		return "-"
	}
	return strings.TrimSpace(branch)
}

func (a *App) handleUserInput(ctx context.Context, input string) string {
	if a.planMode {
		input = "[plan mode] " + input
	}
	analysis := a.safety.AnalyzeCommand(input)
	if !analysis.Allowed {
		return "[blocked] " + analysis.Reason
	}

	if a.orchestrator == nil {
		return "[orchestrator stub] " + input
	}

	// Root invoke_agent span for the turn so every child span
	// (tools, inference on the Python side) nests under it.
	teleCtx, span := a.telemetry.StartSpan(ctx, "invoke_agent code-agent", genai.OperationInvokeAgent, genai.SystemGenAI)
	span.SetAttributes(genai.AgentNameKV("code-agent"))
	defer span.End()

	reply, err := a.converse(teleCtx, input)
	if err == nil && strings.TrimSpace(reply) != "" {
		return reply
	}
	if err == nil {
		return "[orchestrator stub] " + input
	}

	// Graceful degradation: on a connection-level failure (the orchestrator
	// process died or the stream broke mid-turn), restart the process and
	// retry the turn once. Session state is persisted in SQLite, so the retry
	// replays the same session id + history without losing context.
	if orchestrator.IsConnectionError(err) && a.orchestratorPM != nil {
		a.renderer.PrintLine("[Orchestrator connection lost; restarting...]")
		if client, rerr := a.restartOrchestrator(); rerr == nil && client != nil {
			a.orchestrator = client
			a.orchestrator.OnTextDelta = func(delta string) {
				a.renderer.AppendAssistantText(delta)
			}
			a.orchestrator.SetTracer(a.telemetry)
			a.orchestrator.OnCompaction = a.handleCompactionUpdate
			a.orchestrator.OnPlanTodoUpdate = a.handlePlanTodoUpdate
			a.renderer.PrintLine("[Orchestrator restarted. Session preserved.]")
			reply2, err2 := a.converse(teleCtx, input)
			if err2 == nil && strings.TrimSpace(reply2) != "" {
				return reply2
			}
			if err2 != nil {
				// Surface the most recent failure so the user sees why the
				// retried turn did not succeed.
				err = err2
			}
		}
	}

	span.RecordError(err)
	return "[orchestrator error] " + err.Error()
}

func (a *App) handleCompactionUpdate(update *codeagentpb.CompactionUpdate) error {
	if update == nil || a.session == nil {
		return nil
	}
	_, _, ok := a.session.ReplaceMessages(update.GetSummary(), int(update.GetKeepRecentMessages()))
	if !ok {
		return errors.New("session persistence failed")
	}
	return nil
}

// converse runs a single orchestrator turn using the persisted session.
func (a *App) converse(ctx context.Context, input string) (string, error) {
	current := a.session.Current()
	if err := a.bindSessionActor(current); err != nil {
		return "", err
	}
	current = a.session.Current()
	return a.orchestrator.ConverseWithHistoryAndState(
		ctx, input, current.ID,
		orchestratorHistory(current.Messages, input),
		planTodoSnapshot(current),
		a.handleOrchestratorEvent, a.handleAskUserRequest, a.handleToolCall,
	)
}

func planTodoSnapshot(current session.Session) *codeagentpb.PlanTodoSnapshot {
	mode := current.Plan.Mode
	if strings.TrimSpace(mode) == "" {
		mode = "chat"
	}
	items := make([]*codeagentpb.TodoItem, 0, len(current.Todos))
	for _, item := range current.Todos {
		items = append(items, &codeagentpb.TodoItem{
			Content:    item.Content,
			ActiveForm: item.ActiveForm,
			Status:     item.Status,
		})
	}
	return &codeagentpb.PlanTodoSnapshot{
		SchemaVersion: 1,
		Revision:      current.PlanTodoRevision,
		Plan: &codeagentpb.PlanUpdate{
			Steps:        append([]string(nil), current.Plan.Steps...),
			CurrentIndex: int32(current.Plan.CurrentIndex),
			Mode:         mode,
			Revision:     current.PlanTodoRevision,
		},
		Todos: items,
	}
}

// handlePlanTodoUpdate is the durable Harness boundary for Python's
// versioned state events. Plan and Todo are applied as one projection so a
// stale or partially written update cannot advance only half of the state.
func (a *App) handlePlanTodoUpdate(planUpdate *codeagentpb.PlanUpdate, todoUpdate *codeagentpb.TodoUpdate) error {
	if a == nil || a.session == nil {
		return errors.New("session is not configured")
	}
	current := a.session.Current()
	plan := current.Plan
	todos := append([]session.TodoItem(nil), current.Todos...)
	var revision uint64
	if planUpdate != nil {
		plan = session.PlanState{
			Steps:        append([]string(nil), planUpdate.GetSteps()...),
			CurrentIndex: int(planUpdate.GetCurrentIndex()),
			Mode:         planUpdate.GetMode(),
		}
		revision = planUpdate.GetRevision()
	}
	if todoUpdate != nil {
		todos = make([]session.TodoItem, 0, len(todoUpdate.GetTodos()))
		for _, item := range todoUpdate.GetTodos() {
			if item == nil {
				continue
			}
			todos = append(todos, session.TodoItem{
				Content:    item.GetContent(),
				ActiveForm: item.GetActiveForm(),
				Status:     item.GetStatus(),
			})
		}
		revision = todoUpdate.GetRevision()
	}
	if revision == 0 {
		return errors.New("plan/todo update has no revision")
	}
	if _, ok := a.session.ApplyPlanTodoState(plan, todos, revision); !ok {
		return fmt.Errorf("stale plan/todo revision %d", revision)
	}
	return nil
}

// handleAgentSpawn is the Harness-side gate for Python SpawnAgent events. It
// creates the isolated checkout and persists its lease before the streaming
// generator can resume and launch the child process.
func (a *App) handleAgentSpawn(ctx context.Context, spawn *codeagentpb.AgentSpawn) error {
	if a == nil || a.session == nil || a.worktree == nil {
		return errors.New("agent worktree manager is not configured")
	}
	if spawn == nil {
		return errors.New("agent spawn payload is required")
	}
	if strings.TrimSpace(spawn.GetIsolation()) != "worktree" {
		return errors.New("agent spawn must declare worktree isolation")
	}
	current := a.session.Current()
	if current.ID == "" || strings.TrimSpace(spawn.GetParentSessionId()) != current.ID {
		return errors.New("agent spawn parent session does not match active session")
	}
	tree, err := a.worktree.SpawnAgent(ctx, worktree.AgentSpawnRequest{
		RequestID:       spawn.GetRequestId(),
		ParentSessionID: spawn.GetParentSessionId(),
		ChildSessionID:  spawn.GetChildSessionId(),
		WorktreeName:    spawn.GetWorktreeName(),
		BaseRef:         spawn.GetBaseRef(),
	})
	if err != nil {
		return err
	}
	persisted := a.session.SetWorktrees(sessionWorktrees(a.worktree.List()))
	if !containsAgentWorktree(persisted.Worktrees, tree.RequestID) {
		_ = a.worktree.CleanupAgent(context.Background(), tree.RequestID, true, "session-persist-failed")
		return errors.New("persist agent worktree lease failed")
	}
	a.session.AppendWorktreeLifecycle(session.WorktreeLifecycle{
		Name:            tree.Name,
		Path:            tree.Path,
		BaseRef:         tree.BaseRef,
		RequestID:       tree.RequestID,
		ParentSessionID: tree.ParentSessionID,
		ChildSessionID:  tree.ChildSessionID,
		LeaseID:         tree.LeaseID,
		Status:          worktree.AgentWorktreeActive,
		Reason:          "spawned",
	})
	return nil
}

// handleAgentLifecycle validates and records a terminal child state before
// removing the Harness-owned checkout. Completed children use guarded cleanup;
// failed/cancelled children use discard semantics so crash recovery cannot be
// blocked by partial files.
func (a *App) handleAgentLifecycle(ctx context.Context, lifecycle *codeagentpb.AgentLifecycle) error {
	if a == nil || a.session == nil || a.worktree == nil || lifecycle == nil {
		return errors.New("agent lifecycle manager is not configured")
	}
	tree, ok := a.worktree.FindAgent(lifecycle.GetRequestId())
	if !ok {
		return nil
	}
	if leaseID := strings.TrimSpace(lifecycle.GetLeaseId()); leaseID != "" && leaseID != tree.LeaseID {
		return errors.New("agent lifecycle lease does not match active worktree")
	}
	if childID := strings.TrimSpace(lifecycle.GetChildSessionId()); childID != "" && childID != tree.ChildSessionID {
		return errors.New("agent lifecycle child session does not match active worktree")
	}
	status := strings.ToLower(strings.TrimSpace(lifecycle.GetStatus()))
	if status == "ok" {
		status = worktree.AgentWorktreeReleased
	}
	if status != "completed" && status != "failed" && status != "cancelled" && status != "reaped" {
		return errors.New("unsupported agent lifecycle status")
	}
	discard := status != "completed"
	if err := a.worktree.CleanupAgent(ctx, tree.RequestID, discard, lifecycle.GetReason()); err != nil {
		return err
	}
	persisted := a.session.SetWorktrees(sessionWorktrees(a.worktree.List()))
	if len(persisted.Worktrees) != 0 {
		return errors.New("persist agent worktree cleanup failed")
	}
	a.session.AppendWorktreeLifecycle(session.WorktreeLifecycle{
		Name:            tree.Name,
		Path:            tree.Path,
		BaseRef:         tree.BaseRef,
		RequestID:       tree.RequestID,
		ParentSessionID: tree.ParentSessionID,
		ChildSessionID:  tree.ChildSessionID,
		LeaseID:         tree.LeaseID,
		Status:          status,
		Reason:          lifecycle.GetReason(),
	})
	return nil
}

func containsAgentWorktree(trees []session.WorktreeState, requestID string) bool {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return false
	}
	for _, tree := range trees {
		if tree.RequestID == requestID && tree.Status == worktree.AgentWorktreeActive && tree.LeaseID != "" {
			return true
		}
	}
	return false
}

// restartOrchestrator asks the process manager to tear down and relaunch the
// orchestrator, returning a fresh client. It uses a dedicated context so a
// cancelled turn cannot abort recovery.
func (a *App) restartOrchestrator() (*orchestrator.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.orchestrator.ConversationTimeout())
	defer cancel()
	return a.orchestratorPM.Restart(ctx)
}

func (a *App) handleToolCall(ctx context.Context, call orchestrator.ToolCall) orchestrator.ToolResult {
	params := map[string]any{}
	if strings.TrimSpace(call.ParametersJSON) != "" {
		if err := json.Unmarshal([]byte(call.ParametersJSON), &params); err != nil {
			return orchestrator.ToolResult{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Error:      "invalid tool parameters: " + err.Error(),
				ExitCode:   1,
			}
		}
	}

	if decision := a.permissions.Check(call.Name, params); decision != permission.Approve {
		if decision == permission.Deny {
			return orchestrator.ToolResult{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Error:      "permission denied by policy",
				ExitCode:   1,
			}
		}
		approved, err := a.confirmToolApproval(ctx, call, params)
		if err != nil {
			return orchestrator.ToolResult{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Error:      "permission prompt failed: " + err.Error(),
				ExitCode:   1,
			}
		}
		if !approved {
			return orchestrator.ToolResult{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Error:      "permission denied by user",
				ExitCode:   1,
			}
		}
		// Keep an auditable, parameter-generalized record for every explicit
		// user approval. AlwaysAsk tools remain one-shot because the permission
		// controller deliberately ignores sessionApproved for that level.
		a.permissions.RecordApproval(call.Name, params)
		a.session.SetApprovalHistory(a.permissions.ApprovalHistory())
		if a.permissions.Level(call.Name) == permission.AskSession {
			a.session.SetApprovedTools(a.permissions.ApprovedTools())
		}
	}

	current := a.session.Current()
	hookCtx := hooks.Context{
		SessionID: current.ID,
		ToolName:  call.Name,
		Payload:   params,
		Metadata: map[string]string{
			"phase": "pre_tool",
		},
	}
	preResults, err := a.hooks.Run(ctx, hooks.PhasePreTool, hookCtx)
	if err != nil || hooksCancelled(preResults) {
		if err == nil {
			err = errors.New("hook blocked tool execution")
		}
		return orchestrator.ToolResult{
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Output:     hookMessages(preResults),
			Error:      err.Error(),
			ExitCode:   1,
		}
	}

	var result tools.ToolResult
	if call.Name == "SessionFork" || call.Name == "SessionRewind" {
		result = a.executeSessionControl(ctx, call.Name, params)
	} else {
		result, err = a.executor.Execute(ctx, tools.ToolRequest{
			Name:           call.Name,
			Arguments:      params,
			OwnerSessionID: current.ID,
		})
	}
	a.metrics.RecordToolCall()
	postCtx := hookCtx
	if active := a.session.Current(); active.ID != "" {
		postCtx.SessionID = active.ID
	}
	postCtx.Metadata = map[string]string{
		"phase":      "post_tool",
		"exit_code":  fmt.Sprint(result.ExitCode),
		"truncated":  fmt.Sprint(result.Truncated),
		"tool_error": result.Error,
	}
	if postResults, hookErr := a.hooks.Run(ctx, hooks.PhasePostTool, postCtx); (hookErr != nil || hooksCancelled(postResults)) && result.Error == "" {
		if hookErr != nil {
			result.Error = hookErr.Error()
		} else {
			result.Error = "post hook blocked tool execution"
		}
		result.ExitCode = 1
		result.Output = strings.TrimSpace(result.Output + "\n" + hookMessages(postResults))
	}
	a.recordUndo(call, result)
	a.recordToolResult(ctx, call, result)
	a.recordToolWorkingDir(call, result)
	if err != nil {
		return orchestrator.ToolResult{
			ToolCallID:    call.ID,
			ToolName:      call.Name,
			Output:        result.Output,
			Error:         result.Error,
			ExitCode:      int32(result.ExitCode),
			Truncated:     result.Truncated,
			Spill:         orchestratorSpillRef(result.Spill),
			ContentBlocks: orchestratorContentBlocks(result.ContentBlocks),
		}
	}
	return orchestrator.ToolResult{
		ToolCallID:    call.ID,
		ToolName:      call.Name,
		Output:        result.Output,
		Error:         result.Error,
		ExitCode:      int32(result.ExitCode),
		Truncated:     result.Truncated,
		Spill:         orchestratorSpillRef(result.Spill),
		ContentBlocks: orchestratorContentBlocks(result.ContentBlocks),
	}
}

func orchestratorSpillRef(ref *tools.SpillRef) *orchestrator.SpillRef {
	if ref == nil {
		return nil
	}
	return &orchestrator.SpillRef{Locator: ref.Locator, SHA256: ref.SHA256, Bytes: ref.Bytes}
}

func orchestratorContentBlocks(blocks []tools.ContentBlock) []orchestrator.ContentBlock {
	converted := make([]orchestrator.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		converted = append(converted, orchestrator.ContentBlock{
			Text:      block.Text,
			ImageBlob: block.ImageBlob,
			MIME:      block.MIME,
		})
	}
	return converted
}

func (a *App) confirmToolApproval(ctx context.Context, call orchestrator.ToolCall, params map[string]any) (bool, error) {
	if a.input == nil {
		return false, errors.New("input is not available")
	}

	parametersJSON := strings.TrimSpace(call.ParametersJSON)
	if parametersJSON == "" && len(params) > 0 {
		if data, err := json.Marshal(params); err == nil {
			parametersJSON = string(data)
		}
	}
	if parametersJSON != "" {
		parametersJSON = truncateForMetadata(parametersJSON, 360)
	}
	if a.renderer != nil {
		a.renderer.PrintPermission(PermissionView{Tool: call.Name, Reason: "requires approval", Parameters: parametersJSON, AllowSession: a.permissions.Level(call.Name) == permission.AskSession})
	}

	answer, err := a.input.ReadLine(ctx)
	if err != nil {
		return false, err
	}
	return isApprovalAnswer(answer), nil
}

func isApprovalAnswer(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y":
		return true
	default:
		return false
	}
}

func hookMessages(results []hooks.Result) string {
	parts := make([]string, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.Message) != "" {
			parts = append(parts, result.Message)
		}
	}
	return strings.Join(parts, "\n")
}

func hooksCancelled(results []hooks.Result) bool {
	for _, result := range results {
		if result.Cancel {
			return true
		}
	}
	return false
}

func (a *App) handleOrchestratorEvent(ctx context.Context, event orchestrator.Event) {
	_ = ctx
	if event.ToolProgress != nil {
		progress := event.ToolProgress
		if strings.EqualFold(progress.Phase, "start") || strings.EqualFold(progress.Phase, "started") || strings.EqualFold(progress.Phase, "running") {
			a.renderer.ToolStartedWithID(progress.ToolCallID, progress.ToolName, "")
		} else {
			a.renderer.ToolCompleted(ToolEvent{ToolCallID: progress.ToolCallID, Name: progress.ToolName, ExitCode: int(progress.ExitCode), Error: progress.Error, Detail: progress.Error, Truncated: progress.Truncated})
		}
	}

	if event.SessionMeta != nil {
		cachedTokens := int(event.SessionMeta.GetCachedTokens())
		a.metrics.RecordLLMUsage(
			event.SessionMeta.GetModel(),
			int(event.SessionMeta.GetTokensIn()),
			int(event.SessionMeta.GetTokensOut()),
			event.SessionMeta.GetCost(),
		)
		a.metrics.RecordCachedTokens(cachedTokens)
		a.session.AddLLMUsage(
			int(event.SessionMeta.GetTokensIn()),
			int(event.SessionMeta.GetTokensOut()),
			event.SessionMeta.GetCost(),
		)
		a.session.AddCachedTokens(cachedTokens)
		a.session.MergeMetadata(map[string]string{
			"last_turn":          fmt.Sprint(event.SessionMeta.GetTurn()),
			"last_tokens_in":     fmt.Sprint(event.SessionMeta.GetTokensIn()),
			"last_tokens_out":    fmt.Sprint(event.SessionMeta.GetTokensOut()),
			"last_cost":          fmt.Sprintf("%.6f", event.SessionMeta.GetCost()),
			"last_model":         event.SessionMeta.GetModel(),
			"last_cached_tokens": fmt.Sprint(event.SessionMeta.GetCachedTokens()),
		})
	}

	if event.AgentSpawn != nil {
		a.session.AppendAgentSpawn(session.AgentSpawnRecord{
			Kind:            event.AgentSpawn.GetKind(),
			Task:            event.AgentSpawn.GetTask(),
			ContextJSON:     event.AgentSpawn.GetContextJson(),
			Parallel:        event.AgentSpawn.GetParallel(),
			ProtocolVersion: event.AgentSpawn.GetProtocolVersion(),
			RequestID:       event.AgentSpawn.GetRequestId(),
			ParentSessionID: event.AgentSpawn.GetParentSessionId(),
			ChildSessionID:  event.AgentSpawn.GetChildSessionId(),
		})
		lines := []string{
			"kind: " + event.AgentSpawn.GetKind(),
			"task: " + event.AgentSpawn.GetTask(),
			"parallel: " + fmt.Sprint(event.AgentSpawn.GetParallel()),
		}
		if protocol := strings.TrimSpace(event.AgentSpawn.GetProtocolVersion()); protocol != "" {
			lines = append(lines, "protocol: "+protocol)
		}
		contextJSON := strings.TrimSpace(event.AgentSpawn.GetContextJson())
		if contextJSON != "" {
			if len(contextJSON) > 160 {
				contextJSON = contextJSON[:157] + "..."
			}
			lines = append(lines, "context: "+contextJSON)
		}
		a.renderer.PrintBlock("agent", lines)
	}

	if event.PlanUpdate != nil {
		plan := session.PlanState{
			Steps:        append([]string(nil), event.PlanUpdate.GetSteps()...),
			CurrentIndex: int(event.PlanUpdate.GetCurrentIndex()),
			Mode:         event.PlanUpdate.GetMode(),
		}
		a.renderer.PrintBlock("plan", formatPlanLines(plan))
	}

	if event.TodoUpdate == nil {
		return
	}

	items := make([]todo.Item, 0, len(event.TodoUpdate.GetTodos()))
	for _, item := range event.TodoUpdate.GetTodos() {
		if item == nil {
			continue
		}
		todoItem := todo.Item{
			Content:    item.GetContent(),
			ActiveForm: item.GetActiveForm(),
			Status:     item.GetStatus(),
		}
		items = append(items, todoItem)
	}
	a.todos.Update(items)
}

func (a *App) handleSlashCommand(ctx context.Context, raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return true
	}

	switch fields[0] {
	case "/help":
		a.renderer.PrintBlock("help", []string{
			"/help show this help",
			"/plan toggle planning mode",
			"/compact compress the current session",
			"/clear reset the current conversation",
			"/config show loaded configuration",
			"/budget show token and cost budget usage",
			"/memory manage persistent memories (add/list/find/show/delete)",
			"/sessions list recent saved sessions",
			"/tasks show task status",
			"/undo revert the last recorded change set",
			"/diff show the current session diff summary",
			"/worktree manage worktree state (list/create/switch/cleanup)",
			"/resume [session-id] resume a saved session",
			"/fork <session-id> <event-seq> fork the current session history",
			"/rewind <event-seq> rewind the current session to an event",
			"/skills list available skills",
			"/skill <name> [args] run a named skill",
			"/init [instructions] run the init skill",
			"/review [focus] run the review skill",
			"/security-review [focus] run the security review skill",
			"/commit suggest a conventional commit message from the current diff",
			"/ingest <path> ingest a workspace file into RAG knowledge",
		})
	case "/plan":
		a.planMode = !a.planMode
		mode := "off"
		if a.planMode {
			mode = "on"
		}
		a.session.SetMode(map[bool]string{true: "plan", false: "chat"}[a.planMode])
		a.renderer.PrintLine("planning mode " + mode)
	case "/compact":
		if a.orchestrator == nil {
			a.renderer.PrintLine("[compaction error] orchestrator unavailable")
			return true
		}
		current := a.session.Current()
		update, err := a.orchestrator.Compact(
			ctx,
			current.ID,
			orchestratorHistory(current.Messages, ""),
		)
		if err != nil {
			a.renderer.PrintLine("[compaction error] " + err.Error())
			return true
		}
		if update == nil || strings.TrimSpace(update.GetSummary()) == "" {
			a.renderer.PrintLine("nothing to compact")
			return true
		}
		a.renderer.PrintLine(fmt.Sprintf(
			"compacted session %s, removed %d messages",
			current.ID,
			update.GetRemovedMessages(),
		))
		a.renderer.PrintBlock("summary", strings.Split(update.GetSummary(), "\n"))
	case "/clear":
		cleared := a.session.Reset()
		if err := a.bindSessionActor(cleared); err != nil {
			a.renderer.PrintLine("clear actor failed: " + err.Error())
		}
		a.renderer.PrintLine("session cleared")
	case "/config":
		a.renderer.PrintBlock("config", []string{
			"model: " + a.cfg.Model,
			"fast model: " + a.cfg.ModelFast,
			"context window: " + fmt.Sprint(a.cfg.ContextWindow),
			"max tokens per session: " + fmt.Sprint(a.cfg.MaxTokensPerSession),
			"max cost per session: $" + fmt.Sprintf("%.2f", a.cfg.MaxCostPerSession),
			"orchestrator: " + a.cfg.OrchestratorAddr,
			"orchestrator auto-start: " + fmt.Sprint(a.cfg.OrchestratorAutoStart),
			"orchestrator command: " + strings.Join(append([]string{a.cfg.OrchestratorCommand}, a.cfg.OrchestratorArgs...), " "),
			"orchestrator conversation timeout: " + fmt.Sprint(a.cfg.OrchestratorConversationTimeout) + "s",
			"session db: " + a.cfg.SessionDBPath,
			"memory dir: " + a.cfg.MemoryDir,
			"mcp config: " + a.cfg.MCPConfig,
			"mcp servers: " + fmt.Sprint(len(a.mcp.Snapshot())),
			"rag enabled: " + fmt.Sprint(a.cfg.RAGEnabled),
			"rag server: " + a.cfg.RAGServerURL,
			"rag user: " + fmt.Sprint(a.cfg.RAGUserID),
			"rag org: " + a.cfg.RAGOrgTag,
			"rag ingest public: " + fmt.Sprint(a.cfg.RAGIngestPublic),
			"planning mode: " + map[bool]string{true: "on", false: "off"}[a.planMode],
		})
	case "/budget":
		a.renderer.PrintBlock("budget", a.budgetLines())
	case "/memory":
		a.handleMemoryCommand(raw, fields)
	case "/sessions":
		a.handleSessionsCommand(ctx, fields)
	case "/tasks":
		a.renderer.PrintBlock("tasks", a.todos.Lines())
	case "/undo":
		entry, ok := a.undo.Latest()
		if !ok {
			a.renderer.PrintLine("nothing to undo")
			return true
		}
		if err := undo.ApplyEntry(a.cfg.ProjectRoot, entry); err != nil {
			a.renderer.PrintLine("undo failed: " + err.Error())
			return true
		}
		a.undo.RevertLast()
		a.session.SetUndo(sessionUndoEntries(a.undo.List()))
		a.renderer.PrintLine("reverted: " + entry.Description)
	case "/diff":
		summary, err := a.gatherDiffSummary(ctx)
		if err != nil {
			a.renderer.PrintLine("diff failed: " + err.Error())
			return true
		}
		a.renderer.PrintDiff(summary)
	case "/worktree":
		a.handleWorktreeCommand(fields)
	case "/resume":
		if len(fields) > 1 {
			resumed, err := a.session.Resume(ctx, fields[1])
			if err != nil {
				a.renderer.PrintLine("resume failed: " + err.Error())
				return true
			}
			a.restoreWorkingDir(resumed)
			a.restoreMetrics(resumed)
			a.restoreMode(resumed)
			a.restorePermissions(resumed)
			a.restoreUndo(resumed)
			a.restoreWorktrees(resumed)
			a.restoreTodos(resumed)
			a.restorePlan(resumed)
			a.renderer.PrintLine(fmt.Sprintf("resumed session %s with %d messages", resumed.ID, len(resumed.Messages)))
			return true
		}

		resumed, ok, err := a.session.ResumeLatest(ctx)
		if err != nil {
			a.renderer.PrintLine("resume failed: " + err.Error())
			return true
		}
		if !ok {
			a.renderer.PrintLine("no previous session found")
			return true
		}
		a.restoreWorkingDir(resumed)
		a.restoreMetrics(resumed)
		a.restoreMode(resumed)
		a.restorePermissions(resumed)
		a.restoreUndo(resumed)
		a.restoreWorktrees(resumed)
		a.restoreTodos(resumed)
		a.restorePlan(resumed)
		a.renderer.PrintLine(fmt.Sprintf("resumed session %s with %d messages", resumed.ID, len(resumed.Messages)))
	case "/fork":
		if len(fields) < 3 {
			a.renderer.PrintLine("usage: /fork <session-id> <event-seq>")
			return true
		}
		targetSeq, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || targetSeq < 0 {
			a.renderer.PrintLine("fork failed: event-seq must be a non-negative integer")
			return true
		}
		forked, err := a.session.Fork(ctx, fields[1], targetSeq)
		if err != nil {
			a.renderer.PrintLine("fork failed: " + err.Error())
			return true
		}
		a.restoreWorkingDir(forked)
		a.restoreMetrics(forked)
		a.restoreMode(forked)
		a.restorePermissions(forked)
		a.restoreUndo(forked)
		a.restoreWorktrees(forked)
		a.restoreTodos(forked)
		a.restorePlan(forked)
		a.renderer.PrintLine(fmt.Sprintf("forked session %s from event %d", forked.ID, targetSeq))
	case "/rewind":
		if len(fields) < 2 {
			a.renderer.PrintLine("usage: /rewind <event-seq>")
			return true
		}
		targetSeq, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || targetSeq < 0 {
			a.renderer.PrintLine("rewind failed: event-seq must be a non-negative integer")
			return true
		}
		rewound, err := a.session.Rewind(ctx, targetSeq)
		if err != nil {
			a.renderer.PrintLine("rewind failed: " + err.Error())
			return true
		}
		a.restoreWorkingDir(rewound)
		a.restoreMetrics(rewound)
		a.restoreMode(rewound)
		a.restorePermissions(rewound)
		a.restoreUndo(rewound)
		a.restoreWorktrees(rewound)
		a.restoreTodos(rewound)
		a.restorePlan(rewound)
		a.renderer.PrintLine(fmt.Sprintf("rewound session %s to event %d", rewound.ID, targetSeq))
	case "/skills":
		a.renderer.PrintBlock("skills", a.skillLines())
	case "/skill":
		if len(fields) < 2 {
			a.renderer.PrintLine("usage: /skill <name> [args]")
			return true
		}
		a.runSkillCommand(ctx, "/skill "+fields[1], fields[1], slashArgs(raw, 2))
	case "/init":
		a.runSkillCommand(ctx, "/init", "init", slashArgs(raw, 1))
	case "/review":
		a.runSkillCommand(ctx, "/review", "review", slashArgs(raw, 1))
	case "/security-review":
		a.runSkillCommand(ctx, "/security-review", "security", slashArgs(raw, 1))
	case "/commit":
		a.runCommitCommand(ctx)
	case "/ingest":
		a.handleIngestCommand(ctx, raw)
	default:
		return false
	}
	return true
}

func (a *App) gatherDiffSummary(ctx context.Context) (DiffSummary, error) {
	root := strings.TrimSpace(a.cfg.ProjectRoot)
	if root == "" {
		root = strings.TrimSpace(a.cfg.WorkingDir)
	}
	if root == "" {
		root = "."
	}

	numstat, err := runGit(ctx, root, "diff", "HEAD", "--numstat", "--")
	if err != nil {
		return DiffSummary{}, err
	}
	status, err := runGit(ctx, root, "status", "--short", "--untracked-files=all")
	if err != nil {
		return DiffSummary{}, err
	}

	summary := DiffSummary{}
	files := map[string]struct{}{}
	for _, line := range strings.Split(strings.TrimSpace(numstat), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "\t", 3)
		if len(fields) != 3 {
			continue
		}
		if added, parseErr := strconv.Atoi(fields[0]); parseErr == nil {
			summary.Added += added
		}
		if removed, parseErr := strconv.Atoi(fields[1]); parseErr == nil {
			summary.Removed += removed
		}
		files[fields[2]] = struct{}{}
	}

	statusLines := strings.Split(strings.TrimRight(status, "\r\n"), "\n")
	for _, line := range statusLines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) > 3 {
			files[strings.TrimSpace(line[3:])] = struct{}{}
		}
		summary.Lines = append(summary.Lines, line)
	}
	sort.Strings(summary.Lines)
	summary.Files = len(files)
	return summary, nil
}

func (a *App) runSkillCommand(ctx context.Context, command, name, args string) {
	if a.skills == nil {
		a.renderer.PrintLine("skills are not available")
		return
	}
	skill, ok, err := a.skills.Load(name)
	if err != nil {
		a.renderer.PrintLine(err.Error())
		return
	}
	if !ok {
		a.renderer.PrintLine("skill not found: " + name)
		return
	}
	input := buildSkillInput(skill, args)
	a.metrics.BeginTurn()
	a.session.Append(session.RoleUser, strings.TrimSpace(command+" "+strings.TrimSpace(args)))
	reply := a.handleUserInput(ctx, input)
	a.metrics.EndTurn()
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	a.session.Append(session.RoleAssistant, reply)
	// Streaming text was already rendered via OnTextDelta callbacks
	// during handleUserInput. Close the panel and fall back to full
	// panel rendering only when streaming produced no text.
	a.renderer.EndAssistantPanel()
	if strings.HasPrefix(reply, "[orchestrator") || strings.HasPrefix(reply, "[blocked") {
		a.renderer.PrintAssistant(reply)
	}
	a.renderer.PrintStatus(a.status.FormatWidth(a.metrics.Snapshot(), "status", a.renderer.Width()))
}

func buildSkillInput(skill skills.Skill, args string) string {
	lines := []string{
		"Run the " + skill.Name + " skill.",
		"",
		"Skill instructions:",
		strings.TrimSpace(skill.Prompt),
	}
	if len(skill.Tools) > 0 {
		lines = append(lines, "", "Preferred tools: "+strings.Join(skill.Tools, ", "))
	}
	if strings.TrimSpace(args) != "" {
		lines = append(lines, "", "User focus:", strings.TrimSpace(args))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// commitDiffByteLimit 限制发送给模型的完整 diff 体量，避免单条 prompt 过大。
const commitDiffByteLimit = 8192

// runCommitCommand 实现 /commit：收集当前工作区的暂存+未暂存改动，调用 commit 技能
// 让模型起草一条 conventional commit 提交信息，并以建议形式呈现给用户复制。它不会
// 自动执行 git commit。当 orchestrator 不可用时，回退到基于 diff --stat 的启发式提示。
func (a *App) runCommitCommand(ctx context.Context) {
	stat, body, fileCount, hasChanges, err := a.gatherCommitDiff(ctx)
	if err != nil {
		a.renderer.PrintLine("commit diff failed: " + err.Error())
		return
	}
	if !hasChanges {
		a.renderer.PrintLine("No uncommitted changes to summarize.")
		return
	}

	// 没有可用的 orchestrator（或未注册 commit 技能）时，直接给出本地启发式建议。
	skill, ok := a.skills.Get("commit")
	if !ok || a.orchestrator == nil {
		a.renderCommitSuggestion(heuristicCommitMessage(fileCount), true)
		return
	}

	a.renderer.PrintLine("Generating commit message suggestion (not auto-committed)...")

	input := buildCommitInput(skill, stat, body)
	a.metrics.BeginTurn()
	a.session.Append(session.RoleUser, "/commit")
	reply := a.handleUserInput(ctx, input)
	a.metrics.EndTurn()
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	a.session.Append(session.RoleAssistant, reply)
	// 流式文本已通过 OnTextDelta 回调在 handleUserInput 期间渲染；这里关闭面板，
	// 仅在没有产生流式输出（orchestrator 不可用/被阻塞）时回退到启发式建议。
	a.renderer.EndAssistantPanel()
	if strings.HasPrefix(reply, "[orchestrator") || strings.HasPrefix(reply, "[blocked") {
		a.renderCommitSuggestion(heuristicCommitMessage(fileCount), true)
		return
	}
	a.renderer.PrintLine("Suggestion only — review the message and run `git commit` manually.")
	a.renderer.PrintStatus(a.status.FormatWidth(a.metrics.Snapshot(), "status", a.renderer.Width()))
}

// gatherCommitDiff 收集提交信息所需的素材：status 用于判定是否存在改动并统计文件数，
// diff HEAD --stat 与 diff HEAD 同时覆盖暂存与未暂存改动（相对最近一次提交）。
// 返回 (stat, body, fileCount, hasChanges, err)，body 为送入模型的 diff 正文。
func (a *App) gatherCommitDiff(ctx context.Context) (stat, body string, fileCount int, hasChanges bool, err error) {
	root := strings.TrimSpace(a.cfg.ProjectRoot)
	if root == "" {
		root = "."
	}

	// 安全检查：diff 属于只读 git 操作，但仍走一遍 analyzer 与 executeGit 保持一致。
	if a.safety != nil {
		if analysis := a.safety.AnalyzeGit([]string{"diff", "HEAD", "--stat"}); !analysis.Allowed {
			err = fmt.Errorf("commit diff blocked by safety analyzer: %s", analysis.Reason)
			return
		}
	}

	status, err := runGit(ctx, root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return "", "", 0, false, nil
	}
	hasChanges = true
	fileCount = len(strings.Split(status, "\n"))

	stat, err = runGit(ctx, root, "diff", "HEAD", "--stat", "--")
	if err != nil {
		return
	}
	stat = strings.TrimSpace(stat)

	diff, err := runGit(ctx, root, "diff", "HEAD", "--")
	if err != nil {
		return
	}
	body = strings.TrimSpace(diff)
	if len(body) > commitDiffByteLimit {
		body = body[:commitDiffByteLimit] + fmt.Sprintf("\n[diff truncated at %d bytes]", commitDiffByteLimit)
	}
	// 只有未跟踪文件时 diff HEAD 为空，回退到 status 列表让模型仍有上下文可用。
	if body == "" {
		body = "No tracked diff (untracked files only):\n" + status
	}
	return stat, body, fileCount, hasChanges, nil
}

// buildCommitInput 组装送入 orchestrator 的 commit 技能 prompt：技能说明 + diff --stat + diff 正文。
func buildCommitInput(skill skills.Skill, stat, body string) string {
	lines := []string{
		"Run the " + skill.Name + " skill.",
		"",
		"Skill instructions:",
		strings.TrimSpace(skill.Prompt),
		"",
		"Diff --stat:",
		strings.TrimSpace(stat),
		"",
		"Diff:",
		strings.TrimSpace(body),
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// heuristicCommitMessage 在 orchestrator 不可用时，依据改动文件数给出兜底的提交信息。
func heuristicCommitMessage(fileCount int) string {
	if fileCount > 0 {
		noun := "files"
		if fileCount == 1 {
			noun = "file"
		}
		return fmt.Sprintf("chore: update %d %s", fileCount, noun)
	}
	return "chore: update working tree"
}

// renderCommitSuggestion 把建议以面板形式输出；local=true 表示由本地启发式生成。
func (a *App) renderCommitSuggestion(message string, local bool) {
	lines := []string{strings.TrimSpace(message)}
	if local {
		lines = append(lines, "", "(orchestrator unavailable; generated locally from the diff)")
	}
	a.renderer.PrintBlock("commit message suggestion", lines)
}

// runGit 在 root 目录下执行只读 git 子命令并返回合并后的输出。
func runGit(ctx context.Context, root string, args ...string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("git command is required")
	}
	cmdArgs := safety.HardenedGitArgs(root, args[0], args[1:])
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = safety.ScrubGitEnvironment(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (a *App) skillLines() []string {
	if a.skills == nil {
		return []string{"skills are not available"}
	}
	registered := a.skills.List()
	if len(registered) == 0 {
		return []string{"no skills registered"}
	}
	lines := make([]string, 0, len(registered))
	for _, skill := range registered {
		command := skillCommand(skill.Name)
		tools := "none"
		if len(skill.Tools) > 0 {
			tools = strings.Join(skill.Tools, ", ")
		}
		lines = append(lines, fmt.Sprintf("%s | %s | tools: %s", command, skill.Description, tools))
	}
	return lines
}

func skillCommand(name string) string {
	switch name {
	case "init", "review", "commit":
		return "/" + name
	case "security":
		return "/security-review"
	default:
		return "/skill " + name
	}
}

func (a *App) recordUndo(call orchestrator.ToolCall, result tools.ToolResult) {
	if result.ExitCode != 0 || strings.TrimSpace(result.Error) != "" || len(result.Changes) == 0 {
		return
	}
	changes := make([]undo.Change, 0, len(result.Changes))
	for _, change := range result.Changes {
		changes = append(changes, undo.Change{
			Path:   change.Path,
			Before: change.Before,
			After:  change.After,
		})
	}
	a.undo.Record(call.Name, changes)
	a.session.SetUndo(sessionUndoEntries(a.undo.List()))
}

func (a *App) recordToolResult(ctx context.Context, call orchestrator.ToolCall, result tools.ToolResult) {
	modifiedFiles := modifiedFilesFromToolCall(call, result)
	a.session.AppendToolResult(session.ToolResultRecord{
		Name:          call.Name,
		ExitCode:      result.ExitCode,
		Error:         result.Error,
		Output:        result.Output,
		Truncated:     result.Truncated,
		SpillLocator:  spillLocator(result.Spill),
		SpillSHA256:   spillSHA256(result.Spill),
		SpillBytes:    spillBytes(result.Spill),
		ModifiedFiles: modifiedFiles,
	})

	values := map[string]string{
		"last_tool":           call.Name,
		"last_tool_exit_code": fmt.Sprint(result.ExitCode),
		"last_tool_truncated": fmt.Sprint(result.Truncated),
	}
	if strings.TrimSpace(result.Error) != "" {
		values["last_tool_error"] = truncateForMetadata(result.Error, 240)
	}
	if strings.TrimSpace(result.Output) != "" {
		values["last_tool_output"] = truncateForMetadata(firstMemoryLine(result.Output), 240)
	}
	a.session.MergeMetadata(values)
	if err := a.session.AutoSave(ctx); err != nil {
		a.renderer.PrintLine("autosave failed: " + err.Error())
	}
}

func spillLocator(ref *tools.SpillRef) string {
	if ref == nil {
		return ""
	}
	return ref.Locator
}

func spillSHA256(ref *tools.SpillRef) string {
	if ref == nil {
		return ""
	}
	return ref.SHA256
}

func spillBytes(ref *tools.SpillRef) int64 {
	if ref == nil {
		return 0
	}
	return ref.Bytes
}

func formatToolProgress(progress *orchestrator.ToolProgress) string {
	if progress == nil {
		return ""
	}
	name := strings.TrimSpace(progress.ToolName)
	if name == "" {
		name = "tool"
	}
	position := ""
	if progress.Total > 1 {
		position = fmt.Sprintf(" %d/%d", progress.Index, progress.Total)
	}
	switch progress.Phase {
	case "start":
		return fmt.Sprintf("tool%s %s started", position, name)
	case "finish":
		status := fmt.Sprintf("tool%s %s finished exit=%d", position, name, progress.ExitCode)
		if progress.Truncated {
			status += " truncated=true"
		}
		if strings.TrimSpace(progress.Error) != "" {
			status += " error=" + truncateForMetadata(progress.Error, 120)
		}
		return status
	default:
		return fmt.Sprintf("tool%s %s %s", position, name, strings.TrimSpace(progress.Phase))
	}
}

func (a *App) recordToolWorkingDir(call orchestrator.ToolCall, result tools.ToolResult) {
	if call.Name != "Bash" || result.ExitCode != 0 || strings.TrimSpace(result.Error) != "" {
		return
	}
	a.session.SetWorkingDir(a.executor.WorkingDir())
}

func (a *App) budgetLines() []string {
	snapshot := a.metrics.Snapshot()
	usedTokens := snapshot.TotalTokensIn + snapshot.TotalTokensOut
	maxTokens := a.cfg.MaxTokensPerSession
	remainingTokens := maxTokens - usedTokens
	if remainingTokens < 0 {
		remainingTokens = 0
	}
	remainingCost := a.cfg.MaxCostPerSession - snapshot.TotalCost
	if remainingCost < 0 {
		remainingCost = 0
	}

	tokenLimit := "unlimited"
	tokenRemaining := "unlimited"
	if maxTokens > 0 {
		tokenLimit = fmt.Sprint(maxTokens)
		tokenRemaining = fmt.Sprint(remainingTokens)
	}
	costLimit := "unlimited"
	costRemaining := "unlimited"
	if a.cfg.MaxCostPerSession > 0 {
		costLimit = fmt.Sprintf("$%.6f", a.cfg.MaxCostPerSession)
		costRemaining = fmt.Sprintf("$%.6f", remainingCost)
	}

	return []string{
		fmt.Sprintf("tokens: %d used / %s limit / %s remaining", usedTokens, tokenLimit, tokenRemaining),
		fmt.Sprintf("cost: $%.6f used / %s limit / %s remaining", snapshot.TotalCost, costLimit, costRemaining),
		fmt.Sprintf("turns: %d | tools: %d | errors: %d", snapshot.Turns, snapshot.ToolCalls, snapshot.Errors),
	}
}

func (a *App) handleSessionsCommand(ctx context.Context, fields []string) {
	limit := 10
	if len(fields) > 1 {
		if parsed, err := strconv.Atoi(fields[1]); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	sessions, err := a.session.ListRecent(ctx, limit)
	if err != nil {
		a.renderer.PrintLine("list sessions failed: " + err.Error())
		return
	}
	if len(sessions) == 0 {
		a.renderer.PrintBlock("sessions", []string{"no sessions saved"})
		return
	}

	lines := make([]string, 0, len(sessions))
	for _, item := range sessions {
		line := fmt.Sprintf("%s | %s | mode: %s | messages: %d | tools: %d | cost: $%.6f | %s",
			item.ID,
			item.UpdatedAt.Format("2006-01-02 15:04:05"),
			item.Mode,
			item.MessageCount,
			item.Metrics.ToolCalls,
			item.Metrics.TotalCost,
			item.WorkingDir,
		)
		if strings.TrimSpace(item.LastMessage) != "" {
			line += " | " + item.LastMessage
		}
		if len(item.Metrics.FilesModified) > 0 {
			line += fmt.Sprintf(" | files: %d", len(item.Metrics.FilesModified))
		}
		if item.TodoCount > 0 {
			line += fmt.Sprintf(" | todos: %d", item.TodoCount)
		}
		if item.PlanSteps > 0 {
			line += fmt.Sprintf(" | plan: %d", item.PlanSteps)
		}
		if item.AgentCount > 0 {
			line += fmt.Sprintf(" | agents: %d", item.AgentCount)
		}
		if item.UndoCount > 0 {
			line += fmt.Sprintf(" | undo: %d", item.UndoCount)
		}
		if item.ApprovedToolCount > 0 {
			line += fmt.Sprintf(" | approved: %d", item.ApprovedToolCount)
		}
		if item.WorktreeCount > 0 {
			line += fmt.Sprintf(" | worktrees: %d", item.WorktreeCount)
		}
		lines = append(lines, line)
	}
	a.renderer.PrintBlock("sessions", lines)
}

func (a *App) handleWorktreeCommand(fields []string) {
	if len(fields) == 1 || fields[1] == "list" {
		a.renderer.PrintBlock("worktrees", formatWorktrees(a.worktree.List()))
		return
	}
	if len(fields) < 3 {
		a.renderer.PrintLine("usage: /worktree <create|switch|cleanup> <name> [--discard]")
		return
	}
	name := fields[2]
	switch fields[1] {
	case "create":
		tree, err := a.worktree.Create(name)
		if err != nil {
			a.renderer.PrintLine("worktree create failed: " + err.Error())
			return
		}
		a.session.SetWorktrees(sessionWorktrees(a.worktree.List()))
		a.renderer.PrintLine("created worktree: " + tree.Name)
	case "switch":
		tree, err := a.worktree.Switch(name)
		if err != nil {
			a.renderer.PrintLine("worktree switch failed: " + err.Error())
			return
		}
		a.session.SetWorktrees(sessionWorktrees(a.worktree.List()))
		a.renderer.PrintLine("switched worktree: " + tree.Name)
	case "cleanup":
		force := len(fields) > 3 && (fields[3] == "--discard" || fields[3] == "--force")
		var err error
		if force {
			err = a.worktree.CleanupDiscard(name)
		} else {
			err = a.worktree.Cleanup(name)
		}
		if err != nil {
			a.renderer.PrintLine("worktree cleanup failed: " + err.Error())
			return
		}
		a.session.SetWorktrees(sessionWorktrees(a.worktree.List()))
		a.renderer.PrintLine("cleaned worktree: " + name)
	default:
		a.renderer.PrintLine("usage: /worktree <list|create|switch|cleanup> [name] [--discard]")
	}
}

func formatWorktrees(trees []worktree.Worktree) []string {
	if len(trees) == 0 {
		return []string{"no worktrees"}
	}
	lines := make([]string, 0, len(trees))
	for _, tree := range trees {
		state := "inactive"
		if tree.Active {
			state = "active"
		}
		lines = append(lines, fmt.Sprintf("%s | %s | base: %s | %s", tree.Name, state, tree.BaseRef, tree.Path))
	}
	return lines
}

func (a *App) restoreMode(restored session.Session) {
	a.planMode = restored.Mode == "plan"
	if restored.Mode == "" && restored.Metadata != nil {
		a.planMode = restored.Metadata["mode"] == "plan"
	}
}

func (a *App) restoreWorkingDir(restored session.Session) {
	if strings.TrimSpace(restored.WorkingDir) == "" {
		return
	}
	if err := a.executor.SetWorkingDir(restored.WorkingDir); err != nil {
		a.renderer.PrintLine("restore working dir failed: " + err.Error())
	}
}

func (a *App) restoreMetrics(restored session.Session) {
	a.metrics.Hydrate(metrics.SessionMetrics{
		StartTime:         restored.CreatedAt,
		TotalTokensIn:     restored.Metrics.TotalTokensIn,
		TotalTokensOut:    restored.Metrics.TotalTokensOut,
		TotalCachedTokens: restored.Metrics.TotalCachedTokens,
		TotalCost:         restored.Metrics.TotalCost,
		ToolCalls:         restored.Metrics.ToolCalls,
	})
}

func (a *App) restoreUndo(restored session.Session) {
	a.undo.Restore(undoEntries(restored.Undo))
}

func (a *App) restorePermissions(restored session.Session) {
	if restored.ID != "" || restored.Actor.ActorID != "" {
		if err := a.bindSessionActor(restored); err != nil {
			a.renderer.PrintLine("restore actor failed: " + err.Error())
			return
		}
		restored = a.session.Current()
	}
	a.permissions.RestoreApprovedTools(restored.ApprovedTools)
	a.permissions.RestoreApprovalHistory(restored.ApprovalHistory)
}

func (a *App) bindSessionActor(current session.Session) error {
	if a == nil || a.session == nil || current.ID == "" {
		return errors.New("session is not configured")
	}
	expected, err := a.actor.BindSession(current.ID)
	if err != nil {
		return fmt.Errorf("bind configured actor to session: %w", err)
	}
	actor := current.Actor
	if actor.ActorID == "" {
		actor = expected
		if updated := a.session.SetActor(actor); updated.Actor.ScopeKey() != actor.ScopeKey() {
			return errors.New("persist session actor failed")
		}
	} else {
		if _, err := actor.BindSession(current.ID); err != nil {
			return fmt.Errorf("invalid session actor: %w", err)
		}
		if actor.ScopeKey() != expected.ScopeKey() {
			return errors.New("session actor does not match configured actor")
		}
	}
	if err := a.permissions.SetScope(actor); err != nil {
		return fmt.Errorf("set permission scope: %w", err)
	}
	if a.orchestrator != nil {
		if err := a.orchestrator.SetActor(actor); err != nil {
			return fmt.Errorf("set orchestrator actor: %w", err)
		}
	}
	return nil
}

func (a *App) restoreWorktrees(restored session.Session) {
	if err := a.worktree.RestoreChecked(worktrees(restored.Worktrees)); err != nil {
		a.renderer.PrintLine("restore worktrees failed: " + err.Error())
		return
	}
	reaped, err := a.worktree.ReapExpired(context.Background(), time.Now())
	if err != nil {
		a.renderer.PrintLine("reap expired worktrees failed: " + err.Error())
		return
	}
	if len(reaped) == 0 {
		return
	}
	a.session.SetWorktrees(sessionWorktrees(a.worktree.List()))
	for _, tree := range reaped {
		a.session.AppendWorktreeLifecycle(session.WorktreeLifecycle{
			Name:            tree.Name,
			Path:            tree.Path,
			BaseRef:         tree.BaseRef,
			RequestID:       tree.RequestID,
			ParentSessionID: tree.ParentSessionID,
			ChildSessionID:  tree.ChildSessionID,
			LeaseID:         tree.LeaseID,
			Status:          worktree.AgentWorktreeReaped,
			Reason:          "expired lease recovered",
		})
	}
}

func (a *App) restoreTodos(restored session.Session) {
	items := make([]todo.Item, 0, len(restored.Todos))
	for _, item := range restored.Todos {
		items = append(items, todo.Item{
			Content:    item.Content,
			ActiveForm: item.ActiveForm,
			Status:     item.Status,
		})
	}
	a.todos.Update(items)
}

func (a *App) restorePlan(restored session.Session) {
	if len(restored.Plan.Steps) == 0 {
		return
	}
	a.renderer.PrintBlock("restored plan", formatPlanLines(restored.Plan))
}

func formatPlanLines(plan session.PlanState) []string {
	lines := make([]string, 0, len(plan.Steps)+1)
	mode := strings.TrimSpace(plan.Mode)
	if mode == "" {
		mode = "plan"
	}
	lines = append(lines, fmt.Sprintf("mode: %s | current: %d", mode, plan.CurrentIndex))
	for idx, step := range plan.Steps {
		prefix := "[ ]"
		if idx < plan.CurrentIndex {
			prefix = "[x]"
		} else if idx == plan.CurrentIndex {
			prefix = "[~]"
		}
		lines = append(lines, fmt.Sprintf("%d. %s %s", idx+1, prefix, step))
	}
	return lines
}

func orchestratorHistory(messages []session.Message, currentInput string) []orchestrator.ConversationMessage {
	currentInput = strings.TrimSpace(currentInput)
	out := make([]orchestrator.ConversationMessage, 0, len(messages))
	for idx, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		if idx == len(messages)-1 && message.Role == session.RoleUser && content == currentInput {
			continue
		}
		out = append(out, orchestrator.ConversationMessage{
			Role:      string(message.Role),
			Content:   message.Content,
			CreatedAt: message.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return out
}

func modifiedFilesFromToolCall(call orchestrator.ToolCall, result tools.ToolResult) []string {
	if result.ExitCode != 0 || strings.TrimSpace(result.Error) != "" {
		return nil
	}
	switch call.Name {
	case "Edit", "Write":
	default:
		return nil
	}

	params := map[string]any{}
	if strings.TrimSpace(call.ParametersJSON) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(call.ParametersJSON), &params); err != nil {
		return nil
	}
	for _, key := range []string{"path", "file"} {
		value, ok := params[key]
		if !ok {
			continue
		}
		if path, ok := value.(string); ok && strings.TrimSpace(path) != "" {
			return []string{filepath.ToSlash(strings.TrimSpace(path))}
		}
	}
	return nil
}

func (a *App) memorySummary() string {
	stats := a.memory.Snapshot()
	return fmt.Sprintf("memories: %d | dir: %s | index: %s", stats.Count, stats.Dir, stats.File)
}

func (a *App) handleMemoryCommand(raw string, fields []string) {
	if err := a.memory.Err(); err != nil {
		a.renderer.PrintLine("memory unavailable: " + err.Error())
		return
	}
	if len(fields) == 1 {
		lines := []string{a.memorySummary()}
		items := a.memory.List()
		if len(items) == 0 {
			lines = append(lines, "no memories saved")
		} else {
			lines = append(lines, "recent:")
			lines = append(lines, formatMemoryItems(items, 5)...)
		}
		a.renderer.PrintBlock("memory", lines)
		return
	}

	subcommand := fields[1]
	args := slashArgs(raw, 2)
	switch subcommand {
	case "add":
		content, tags := extractInlineTags(args)
		if strings.TrimSpace(content) == "" {
			a.renderer.PrintLine("usage: /memory add <content> [#tag...]")
			return
		}
		item, err := a.memory.Add(content, tags...)
		if err != nil {
			a.renderer.PrintLine("memory rejected: " + err.Error())
			return
		}
		a.renderer.PrintLine("saved memory: " + item.Name)
	case "list":
		items := a.memory.List()
		a.renderer.PrintBlock("memory", formatMemoryItems(items, 0))
	case "find":
		if strings.TrimSpace(args) == "" {
			a.renderer.PrintLine("usage: /memory find <query>")
			return
		}
		items := a.memory.LoadRelevant(args)
		a.renderer.PrintBlock("memory matches", formatMemoryItems(items, 0))
	case "show":
		if strings.TrimSpace(args) == "" {
			a.renderer.PrintLine("usage: /memory show <name>")
			return
		}
		item, ok := a.memory.Get(args)
		if !ok {
			a.renderer.PrintLine("memory not found: " + args)
			return
		}
		a.renderer.PrintBlock("memory "+item.Name, formatMemoryDetail(item))
	case "delete":
		if strings.TrimSpace(args) == "" {
			a.renderer.PrintLine("usage: /memory delete <name>")
			return
		}
		if err := a.memory.Delete(args); err != nil {
			a.renderer.PrintLine("delete failed: " + err.Error())
			return
		}
		a.renderer.PrintLine("deleted memory: " + args)
	default:
		a.renderer.PrintBlock("memory", []string{
			a.memorySummary(),
			"commands: add, list, find, show, delete",
		})
	}
}

func formatMemoryItems(items []memory.Memory, limit int) []string {
	if len(items) == 0 {
		return []string{"no memories found"}
	}
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		tags := strings.Join(item.Tags, ", ")
		if tags == "" {
			tags = "none"
		}
		lines = append(lines, fmt.Sprintf("%s | tags: %s | %s", item.Name, tags, firstMemoryLine(item.Content)))
	}
	return lines
}

func formatMemoryDetail(item memory.Memory) []string {
	tags := strings.Join(item.Tags, ", ")
	if tags == "" {
		tags = "none"
	}
	lines := []string{
		"id: " + item.ID,
		"tags: " + tags,
		"created: " + item.CreatedAt.Format("2006-01-02 15:04:05"),
		"updated: " + item.UpdatedAt.Format("2006-01-02 15:04:05"),
		"content:",
	}
	for _, line := range strings.Split(item.Content, "\n") {
		lines = append(lines, line)
	}
	return lines
}

func slashArgs(raw string, skip int) string {
	value := strings.TrimSpace(raw)
	for i := 0; i < skip; i++ {
		_, rest, ok := strings.Cut(value, " ")
		if !ok {
			return ""
		}
		value = strings.TrimSpace(rest)
	}
	return value
}

func extractInlineTags(value string) (string, []string) {
	fields := strings.Fields(value)
	tags := []string{}
	content := []string{}
	for _, field := range fields {
		if strings.HasPrefix(field, "#") && len(field) > 1 {
			tags = append(tags, strings.TrimPrefix(field, "#"))
			continue
		}
		content = append(content, field)
	}
	return strings.Join(content, " "), tags
}

func firstMemoryLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if len(line) > 80 {
				return strings.TrimSpace(line[:77]) + "..."
			}
			return line
		}
	}
	return ""
}

func truncateForMetadata(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit-3]) + "..."
}

func sessionUndoEntries(entries []undo.Entry) []session.UndoEntry {
	out := make([]session.UndoEntry, 0, len(entries))
	for _, entry := range entries {
		changes := make([]session.UndoChange, 0, len(entry.Changes))
		for _, change := range entry.Changes {
			changes = append(changes, session.UndoChange{
				Path:   change.Path,
				Before: change.Before,
				After:  change.After,
			})
		}
		out = append(out, session.UndoEntry{
			ID:          entry.ID,
			Description: entry.Description,
			Changes:     changes,
			CreatedAt:   entry.CreatedAt,
		})
	}
	return out
}

func undoEntries(entries []session.UndoEntry) []undo.Entry {
	out := make([]undo.Entry, 0, len(entries))
	for _, entry := range entries {
		changes := make([]undo.Change, 0, len(entry.Changes))
		for _, change := range entry.Changes {
			changes = append(changes, undo.Change{
				Path:   change.Path,
				Before: change.Before,
				After:  change.After,
			})
		}
		out = append(out, undo.Entry{
			ID:          entry.ID,
			Description: entry.Description,
			Changes:     changes,
			CreatedAt:   entry.CreatedAt,
		})
	}
	return out
}

func sessionWorktrees(trees []worktree.Worktree) []session.WorktreeState {
	out := make([]session.WorktreeState, 0, len(trees))
	for _, tree := range trees {
		out = append(out, session.WorktreeState{
			Name:            tree.Name,
			Path:            tree.Path,
			BaseRef:         tree.BaseRef,
			Active:          tree.Active,
			RequestID:       tree.RequestID,
			ParentSessionID: tree.ParentSessionID,
			ChildSessionID:  tree.ChildSessionID,
			LeaseID:         tree.LeaseID,
			LeaseExpiresAt:  tree.LeaseExpiresAt,
			Status:          tree.Status,
		})
	}
	return out
}

func worktrees(trees []session.WorktreeState) []worktree.Worktree {
	out := make([]worktree.Worktree, 0, len(trees))
	for _, tree := range trees {
		out = append(out, worktree.Worktree{
			Name:            tree.Name,
			Path:            tree.Path,
			BaseRef:         tree.BaseRef,
			Active:          tree.Active,
			RequestID:       tree.RequestID,
			ParentSessionID: tree.ParentSessionID,
			ChildSessionID:  tree.ChildSessionID,
			LeaseID:         tree.LeaseID,
			LeaseExpiresAt:  tree.LeaseExpiresAt,
			Status:          tree.Status,
		})
	}
	return out
}

func resolveConfigPath(projectRoot, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(projectRoot, path)
}
