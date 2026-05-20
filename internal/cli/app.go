package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

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
	cfg          config.Config
	input        *InputBuffer
	renderer     *StreamRenderer
	status       *StatusLine
	metrics      *metrics.Collector
	session      *session.Manager
	memory       *memory.Manager
	todos        *todo.Manager
	permissions  *permission.Controller
	orchestrator *orchestrator.Client
	hooks        *hooks.Engine
	executor     *tools.Executor
	safety       *safety.Analyzer
	mcp          *mcp.Manager
	worktree     *worktree.Manager
	undo         *undo.Manager
	recovery     *recovery.Engine
	skills       *skills.Manager
	prompts      *prompts.Builder
	planMode     bool
	instructions []config.InstructionSource
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

	orchestratorClient, _ := orchestrator.NewClient(cfg.OrchestratorAddr)
	mcpManager := mcp.NewManager()
	_ = mcpManager.LoadConfigFile(resolveConfigPath(cfg.ProjectRoot, cfg.MCPConfig))
	_ = mcpManager.StartAll(context.Background())
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

	return &App{
		cfg:          cfg,
		input:        NewInputBuffer(stdin, stdout),
		renderer:     NewStreamRenderer(stdout),
		status:       NewStatusLine(),
		metrics:      metrics.NewCollector(),
		session:      session.NewManager(session.NewSQLiteStore(cfg.SessionDBPath)),
		memory:       memory.NewManager(cfg.MemoryDir),
		todos:        todo.NewManager(),
		permissions:  permission.NewControllerWithRules(levels, allowlist, denylist),
		orchestrator: orchestratorClient,
		hooks:        hookEngine,
		executor:     tools.NewExecutor(cfg.ProjectRoot),
		safety:       safety.NewAnalyzer(),
		mcp:          mcpManager,
		worktree:     worktree.NewManager(cfg.ProjectRoot, cfg.WorktreeBaseRef),
		undo:         undo.NewManager(),
		recovery:     recovery.NewEngine(),
		skills:       skills.NewManager(),
		prompts:      prompts.NewBuilder(),
		instructions: instructions,
	}
}

func (a *App) Run(ctx context.Context) error {
	a.session.NewSession(a.cfg.WorkingDir)
	a.renderBootstrap()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		line, err := a.input.ReadLine(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "/") {
			if handled := a.handleSlashCommand(ctx, line); handled {
				continue
			}
		}

		a.metrics.BeginTurn()
		a.session.Append(session.RoleUser, line)
		a.metrics.RecordInput(line)
		reply := a.handleUserInput(ctx, line)
		a.session.Append(session.RoleAssistant, reply)
		a.metrics.RecordOutput(reply)
		a.metrics.EndTurn()
		a.renderer.PrintLine(reply)
		a.renderer.PrintLine(a.status.Format(a.metrics.Snapshot()))
	}
}

func (a *App) renderBootstrap() {
	a.renderer.Separator()
	a.renderer.PrintLine("code-agent skeleton")
	a.renderer.PrintBlock("workspace", []string{
		"root: " + a.cfg.ProjectRoot,
		"working dir: " + a.cfg.WorkingDir,
		"model: " + a.cfg.Model,
		"orchestrator: " + a.cfg.OrchestratorAddr,
		"session db: " + a.cfg.SessionDBPath,
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
	a.renderer.PrintBlock("available commands", []string{
		"/help", "/plan", "/compact", "/clear", "/config", "/memory", "/tasks", "/undo", "/diff", "/resume",
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

	if a.orchestrator != nil {
		reply, err := a.orchestrator.ConverseWithEvents(ctx, input, a.handleOrchestratorEvent, a.handleToolCall)
		if err == nil && strings.TrimSpace(reply) != "" {
			return reply
		}
	}

	return "[orchestrator stub] " + input
}

func (a *App) handleToolCall(ctx context.Context, call orchestrator.ToolCall) orchestrator.ToolResult {
	params := map[string]any{}
	if strings.TrimSpace(call.ParametersJSON) != "" {
		if err := json.Unmarshal([]byte(call.ParametersJSON), &params); err != nil {
			return orchestrator.ToolResult{
				ToolName: call.Name,
				Error:    "invalid tool parameters: " + err.Error(),
				ExitCode: 1,
			}
		}
	}

	if decision := a.permissions.Check(call.Name, params); decision != permission.Approve {
		reason := "permission required"
		if decision == permission.Deny {
			reason = "permission denied by policy"
		}
		return orchestrator.ToolResult{
			ToolName: call.Name,
			Error:    reason,
			ExitCode: 1,
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
			ToolName: call.Name,
			Output:   hookMessages(preResults),
			Error:    err.Error(),
			ExitCode: 1,
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
	if err != nil {
		return orchestrator.ToolResult{
			ToolName:  call.Name,
			Output:    result.Output,
			Error:     result.Error,
			ExitCode:  int32(result.ExitCode),
			Truncated: result.Truncated,
		}
	}
	return orchestrator.ToolResult{
		ToolName:  call.Name,
		Output:    result.Output,
		Error:     result.Error,
		ExitCode:  int32(result.ExitCode),
		Truncated: result.Truncated,
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
	if event.SessionMeta != nil {
		a.session.MergeMetadata(map[string]string{
			"last_turn":       fmt.Sprint(event.SessionMeta.GetTurn()),
			"last_tokens_in":  fmt.Sprint(event.SessionMeta.GetTokensIn()),
			"last_tokens_out": fmt.Sprint(event.SessionMeta.GetTokensOut()),
			"last_cost":       fmt.Sprintf("%.6f", event.SessionMeta.GetCost()),
			"last_model":      event.SessionMeta.GetModel(),
		})
	}

	if event.AgentSpawn != nil {
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
		lines := make([]string, 0, len(event.PlanUpdate.GetSteps())+1)
		lines = append(lines, fmt.Sprintf("mode: %s | current: %d", event.PlanUpdate.GetMode(), event.PlanUpdate.GetCurrentIndex()))
		for idx, step := range event.PlanUpdate.GetSteps() {
			prefix := "[ ]"
			if idx < int(event.PlanUpdate.GetCurrentIndex()) {
				prefix = "[x]"
			} else if idx == int(event.PlanUpdate.GetCurrentIndex()) {
				prefix = "[~]"
			}
			lines = append(lines, fmt.Sprintf("%d. %s %s", idx+1, prefix, step))
		}
		a.renderer.PrintBlock("plan", lines)
	}

	if event.TodoUpdate == nil {
		return
	}

	items := make([]todo.Item, 0, len(event.TodoUpdate.GetTodos()))
	for _, item := range event.TodoUpdate.GetTodos() {
		if item == nil {
			continue
		}
		items = append(items, todo.Item{
			Content:    item.GetContent(),
			ActiveForm: item.GetActiveForm(),
			Status:     item.GetStatus(),
		})
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
			"/memory manage persistent memories (add/list/find/show/delete)",
			"/tasks show task status",
			"/undo revert the last recorded change set",
			"/diff show the current session diff summary",
			"/resume resume the last session",
		})
	case "/plan":
		a.planMode = !a.planMode
		mode := "off"
		if a.planMode {
			mode = "on"
		}
		_ = a.session.SetMetadata("mode", map[bool]string{true: "plan", false: "chat"}[a.planMode])
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
			"max cost: " + fmt.Sprintf("%.2f", a.cfg.MaxCostPerSession),
			"orchestrator: " + a.cfg.OrchestratorAddr,
			"session db: " + a.cfg.SessionDBPath,
			"memory dir: " + a.cfg.MemoryDir,
			"mcp config: " + a.cfg.MCPConfig,
			"mcp servers: " + fmt.Sprint(len(a.mcp.Snapshot())),
			"planning mode: " + map[bool]string{true: "on", false: "off"}[a.planMode],
		})
	case "/memory":
		a.handleMemoryCommand(raw, fields)
	case "/tasks":
		a.renderer.PrintBlock("tasks", a.todos.Lines())
	case "/undo":
		if entry, ok := a.undo.RevertLast(); ok {
			a.renderer.PrintLine("reverted: " + entry.Description)
		} else {
			a.renderer.PrintLine("nothing to undo")
		}
	case "/diff":
		lines, err := a.worktree.DiffLines(ctx)
		if err != nil {
			a.renderer.PrintLine("diff failed: " + err.Error())
			return true
		}
		a.renderer.PrintBlock("diff", lines)
	case "/resume":
		resumed, ok, err := a.session.ResumeLatest(ctx)
		if err != nil {
			a.renderer.PrintLine("resume failed: " + err.Error())
			return true
		}
		if !ok {
			a.renderer.PrintLine("no previous session found")
			return true
		}
		a.renderer.PrintLine(fmt.Sprintf("resumed session %s with %d messages", resumed.ID, len(resumed.Messages)))
	default:
		return false
	}
	return true
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

func resolveConfigPath(projectRoot, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(projectRoot, path)
}
