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
	cfg         config.Config
	input       *InputBuffer
	renderer    *StreamRenderer
	status      *StatusLine
	metrics     *metrics.Collector
	session     *session.Manager
	memory      *memory.Manager
	permissions *permission.Controller
	orchestrator *orchestrator.Client
	hooks       *hooks.Engine
	executor    *tools.Executor
	safety      *safety.Analyzer
	mcp         *mcp.Manager
	worktree    *worktree.Manager
	undo        *undo.Manager
	recovery    *recovery.Engine
	skills      *skills.Manager
	prompts     *prompts.Builder
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

	return &App{
		cfg:          cfg,
		input:        NewInputBuffer(stdin, stdout),
		renderer:     NewStreamRenderer(stdout),
		status:       NewStatusLine(),
		metrics:      metrics.NewCollector(),
		session:      session.NewManager(nil),
		memory:       memory.NewManager(cfg.MemoryDir),
		permissions:  permission.NewControllerWithRules(levels, allowlist, denylist),
		orchestrator: orchestratorClient,
		hooks:        hooks.NewEngine(),
		executor:     tools.NewExecutor(cfg.ProjectRoot),
		safety:       safety.NewAnalyzer(),
		mcp:          mcp.NewManager(),
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
		"instructions: " + fmt.Sprint(len(a.instructions)),
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
	analysis := a.safety.AnalyzeCommand(input)
	if !analysis.Allowed {
		return "[blocked] " + analysis.Reason
	}

	if a.orchestrator != nil {
		reply, err := a.orchestrator.Converse(ctx, input, a.handleToolCall)
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

	result, err := a.executor.Execute(ctx, tools.ToolRequest{
		Name:      call.Name,
		Arguments: params,
	})
	a.metrics.RecordToolCall()
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

func (a *App) handleSlashCommand(_ context.Context, raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return true
	}

	switch fields[0] {
	case "/help":
		a.renderer.PrintBlock("help", []string{
			"/help show this help",
			"/plan enter planning mode",
			"/compact compress context",
			"/clear reset the current conversation",
			"/config show loaded configuration",
			"/memory show memory status",
			"/tasks show task status",
			"/undo revert the last recorded change set",
			"/diff show the current session diff summary",
			"/resume resume the last session",
		})
	case "/plan":
		a.renderer.PrintLine("planning mode placeholder")
	case "/compact":
		a.renderer.PrintLine("compaction placeholder")
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
			"memory dir: " + a.cfg.MemoryDir,
			"mcp config: " + a.cfg.MCPConfig,
		})
	case "/memory":
		a.renderer.PrintLine(a.memorySummary())
	case "/tasks":
		a.renderer.PrintLine("task list placeholder")
	case "/undo":
		if entry, ok := a.undo.RevertLast(); ok {
			a.renderer.PrintLine("reverted: " + entry.Description)
		} else {
			a.renderer.PrintLine("nothing to undo")
		}
	case "/diff":
		a.renderer.PrintLine("diff placeholder")
	case "/resume":
		a.renderer.PrintLine("session resume placeholder")
	default:
		return false
	}
	return true
}

func (a *App) memorySummary() string {
	stats := a.memory.Snapshot()
	return fmt.Sprintf("memories: %d | dir: %s", stats.Count, stats.Dir)
}
