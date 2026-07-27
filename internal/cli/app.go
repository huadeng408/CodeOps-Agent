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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"code-agent/internal/config"
	"code-agent/internal/hooks"
	"code-agent/internal/mcp"
	"code-agent/internal/memory"
	"code-agent/internal/metrics"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/prompts"
	"code-agent/internal/recovery"
	"code-agent/internal/safety"
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
	orchestrator   *orchestrator.Client
	orchestratorPM *orchestrator.ProcessManager
	hooks          *hooks.Engine
	executor       *tools.Executor
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
	_ = stderr

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
		Address:             cfg.OrchestratorAddr,
		AutoStart:           cfg.OrchestratorAutoStart,
		Command:             cfg.OrchestratorCommand,
		Args:                cfg.OrchestratorArgs,
		ProjectRoot:         cfg.ProjectRoot,
		WorkingDir:          cfg.WorkingDir,
		MemoryDir:           cfg.MemoryDir,
		MaxTokens:           cfg.MaxTokensPerSession,
		MaxCost:             cfg.MaxCostPerSession,
		ModelFast:           cfg.ModelFast,
		StartupTimeout:      time.Duration(cfg.OrchestratorStartupTimeout) * time.Second,
		ConversationTimeout: time.Duration(cfg.OrchestratorConversationTimeout) * time.Second,
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
	executor.SetMCPManager(mcpManager)
	skillsManager := skills.NewManager()
	executor.SetSkillsManager(skillsManager)

	telemetry := genai.NewTelemetry(context.Background())
	orchestratorClient.SetTracer(telemetry)

	return &App{
		cfg:            cfg,
		input:          NewInputBuffer(stdin, stdout),
		renderer:       NewStreamRenderer(stdout),
		status:         NewStatusLine(),
		metrics:        metrics.NewCollector(),
		session:        session.NewManager(session.NewSQLiteStore(cfg.SessionDBPath)),
		memory:         memory.NewManager(cfg.MemoryDir),
		todos:          todo.NewManager(),
		permissions:    permission.NewControllerWithRules(levels, allowlist, denylist),
		orchestrator:   orchestratorClient,
		orchestratorPM: orchestratorManager,
		hooks:          hookEngine,
		executor:       executor,
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
}

func (a *App) Run(ctx context.Context) error {
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

	a.session.NewSession(a.cfg.WorkingDir)
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
		a.renderer.PrintStatus(a.status.Format(a.metrics.Snapshot()))
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
		a.renderer.PrintLine("")
		a.renderer.PrintLine("[Interrupted. You can give new instructions.]")
	}
	return false
}

func (a *App) renderBootstrap() {
	a.renderer.Separator()
	a.renderer.PrintBlock("code-agent", []string{
		"model: " + a.cfg.Model,
		"context window: " + fmt.Sprint(a.cfg.ContextWindow),
		"workspace: " + a.cfg.WorkingDir,
		"orchestrator: " + a.cfg.OrchestratorAddr,
		"instructions: " + fmt.Sprint(len(a.instructions)),
		"mcp servers: " + fmt.Sprint(len(a.mcp.Snapshot())),
	})
	if len(a.instructions) > 0 {
		lines := make([]string, 0, len(a.instructions))
		for _, instruction := range a.instructions {
			lines = append(lines, filepath.Base(instruction.Path))
		}
		a.renderer.PrintBlock("loaded AGENT.md", lines)
	}
	a.renderer.PrintBlock("commands", []string{
		"/help", "/plan", "/compact", "/clear", "/config", "/budget", "/memory", "/sessions", "/tasks", "/undo", "/diff", "/worktree", "/resume", "/skills", "/init", "/review", "/security-review", "/commit",
	})
	a.renderer.Separator()
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

// converse runs a single orchestrator turn using the persisted session.
func (a *App) converse(ctx context.Context, input string) (string, error) {
	current := a.session.Current()
	return a.orchestrator.ConverseWithHistoryAndPrompts(
		ctx, input, current.ID,
		orchestratorHistory(current.Messages, input),
		a.handleOrchestratorEvent, a.handleAskUserRequest, a.handleToolCall,
	)
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
		if a.permissions.Level(call.Name) == permission.AskSession {
			a.permissions.ApproveSession(call.Name)
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

	result, err := a.executor.Execute(ctx, tools.ToolRequest{
		Name:      call.Name,
		Arguments: params,
	})
	a.metrics.RecordToolCall()
	postCtx := hookCtx
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
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Output:     result.Output,
			Error:      result.Error,
			ExitCode:   int32(result.ExitCode),
			Truncated:  result.Truncated,
		}
	}
	return orchestrator.ToolResult{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Output:     result.Output,
		Error:      result.Error,
		ExitCode:   int32(result.ExitCode),
		Truncated:  result.Truncated,
	}
}

func (a *App) confirmToolApproval(ctx context.Context, call orchestrator.ToolCall, params map[string]any) (bool, error) {
	if a.input == nil {
		return false, errors.New("input is not available")
	}

	lines := []string{
		"tool: " + call.Name,
		"required permission: " + fmt.Sprint(call.RequiredPermission),
	}
	parametersJSON := strings.TrimSpace(call.ParametersJSON)
	if parametersJSON == "" && len(params) > 0 {
		if data, err := json.Marshal(params); err == nil {
			parametersJSON = string(data)
		}
	}
	if parametersJSON != "" {
		lines = append(lines, "parameters: "+truncateForMetadata(parametersJSON, 360))
	}
	lines = append(lines, "approve this tool call? [y/N]")
	if a.renderer != nil {
		a.renderer.PrintBlock("permission required", lines)
	}

	answer, err := a.input.ReadLine(ctx)
	if err != nil {
		return false, err
	}
	return isApprovalAnswer(answer), nil
}

func isApprovalAnswer(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes", "allow", "approve", "ok":
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
		a.renderer.PrintLine(formatToolProgress(event.ToolProgress))
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
			"last_turn":         fmt.Sprint(event.SessionMeta.GetTurn()),
			"last_tokens_in":    fmt.Sprint(event.SessionMeta.GetTokensIn()),
			"last_tokens_out":   fmt.Sprint(event.SessionMeta.GetTokensOut()),
			"last_cost":         fmt.Sprintf("%.6f", event.SessionMeta.GetCost()),
			"last_model":        event.SessionMeta.GetModel(),
			"last_cached_tokens": fmt.Sprint(event.SessionMeta.GetCachedTokens()),
		})
	}

	if event.AgentSpawn != nil {
		a.session.AppendAgentSpawn(session.AgentSpawnRecord{
			Kind:        event.AgentSpawn.GetKind(),
			Task:        event.AgentSpawn.GetTask(),
			ContextJSON: event.AgentSpawn.GetContextJson(),
			Parallel:    event.AgentSpawn.GetParallel(),
		})
		lines := []string{
			"kind: " + event.AgentSpawn.GetKind(),
			"task: " + event.AgentSpawn.GetTask(),
			"parallel: " + fmt.Sprint(event.AgentSpawn.GetParallel()),
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
		a.session.SetPlan(plan)
		a.renderer.PrintBlock("plan", formatPlanLines(plan))
	}

	if event.TodoUpdate == nil {
		return
	}

	items := make([]todo.Item, 0, len(event.TodoUpdate.GetTodos()))
	sessionTodos := make([]session.TodoItem, 0, len(event.TodoUpdate.GetTodos()))
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
		sessionTodos = append(sessionTodos, session.TodoItem{
			Content:    todoItem.Content,
			ActiveForm: todoItem.ActiveForm,
			Status:     todoItem.Status,
		})
	}
	a.todos.Update(items)
	a.session.SetTodos(sessionTodos)
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
			"/skills list available skills",
			"/init [instructions] run the init skill",
			"/review [focus] run the review skill",
			"/security-review [focus] run the security review skill",
			"/commit suggest a conventional commit message from the current diff",
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
		compacted, removed, summary := a.session.Compact(12)
		if removed == 0 {
			a.renderer.PrintLine("nothing to compact")
			return true
		}
		a.renderer.PrintLine(fmt.Sprintf("compacted session %s, removed %d messages", compacted.ID, removed))
		if strings.TrimSpace(summary) != "" {
			a.renderer.PrintBlock("summary", strings.Split(summary, "\n"))
		}
	case "/clear":
		a.session.Reset()
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
		lines, err := a.worktree.DiffLines(ctx)
		if err != nil {
			a.renderer.PrintLine("diff failed: " + err.Error())
			return true
		}
		a.renderer.PrintBlock("diff", lines)
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
	case "/skills":
		a.renderer.PrintBlock("skills", a.skillLines())
	case "/init":
		a.runSkillCommand(ctx, "/init", "init", slashArgs(raw, 1))
	case "/review":
		a.runSkillCommand(ctx, "/review", "review", slashArgs(raw, 1))
	case "/security-review":
		a.runSkillCommand(ctx, "/security-review", "security", slashArgs(raw, 1))
	case "/commit":
		a.runCommitCommand(ctx)
	default:
		return false
	}
	return true
}

func (a *App) runSkillCommand(ctx context.Context, command, name, args string) {
	if a.skills == nil {
		a.renderer.PrintLine("skills are not available")
		return
	}
	skill, ok := a.skills.Get(name)
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
	a.renderer.PrintStatus(a.status.Format(a.metrics.Snapshot()))
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
	a.renderer.PrintStatus(a.status.Format(a.metrics.Snapshot()))
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
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
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
		command := "/" + skill.Name
		if skill.Name == "security" {
			command = "/security-review"
		}
		tools := "none"
		if len(skill.Tools) > 0 {
			tools = strings.Join(skill.Tools, ", ")
		}
		lines = append(lines, fmt.Sprintf("%s | %s | tools: %s", command, skill.Description, tools))
	}
	return lines
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
	a.permissions.RestoreApprovedTools(restored.ApprovedTools)
}

func (a *App) restoreWorktrees(restored session.Session) {
	a.worktree.Restore(worktrees(restored.Worktrees))
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
		item := a.memory.Add(content, tags...)
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
			Name:    tree.Name,
			Path:    tree.Path,
			BaseRef: tree.BaseRef,
			Active:  tree.Active,
		})
	}
	return out
}

func worktrees(trees []session.WorktreeState) []worktree.Worktree {
	out := make([]worktree.Worktree, 0, len(trees))
	for _, tree := range trees {
		out = append(out, worktree.Worktree{
			Name:    tree.Name,
			Path:    tree.Path,
			BaseRef: tree.BaseRef,
			Active:  tree.Active,
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
