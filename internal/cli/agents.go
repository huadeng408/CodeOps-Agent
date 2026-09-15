package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
	"code-agent/internal/skills"
	"code-agent/internal/tools"
	"google.golang.org/protobuf/encoding/protojson"
)

// Approval commands are parsed only from human CLI input, not model tools.
func (a *App) handleAgentsCommand(ctx context.Context, fields []string) {
	if err := a.memory.Err(); err != nil || a.agentRunner == nil {
		a.renderer.PrintLine("independent agents unavailable")
		return
	}
	current := a.session.Current()
	result := a.agentRunner.ExecuteAgentTool(ctx, current.Actor, current.ID, orchestrator.ToolCall{ID: "cli-agent-list", Name: "AgentTask", ParametersJSON: `{"action":"list"}`})
	if result.Error != "" {
		a.renderer.PrintLine(result.Error)
		return
	}
	var encoded []json.RawMessage
	if json.Unmarshal([]byte(result.Output), &encoded) != nil {
		a.renderer.PrintLine("agent catalog unavailable")
		return
	}
	lines := []string{}
	for _, body := range encoded {
		task := &pb.AgentTask{}
		if protojson.Unmarshal(body, task) != nil {
			a.renderer.PrintLine("agent task unavailable")
			return
		}
		lines = append(lines, fmt.Sprintf("%s | %s | artifacts: %d", task.Id, task.Status, len(task.Artifacts)))
		for _, pending := range task.PendingApprovals {
			lines = append(lines, fmt.Sprintf("pending: %s %s %s | %s", task.Id, pending.RunId, pending.ToolCallId, pending.ToolName))
			if len(fields) != 5 || (fields[1] != "approve" && fields[1] != "deny") || fields[2] != task.Id || fields[3] != pending.RunId || fields[4] != pending.ToolCallId {
				continue
			}
			decision := session.ApprovalDenied
			if fields[1] == "approve" {
				approved, err := a.confirmToolApproval(ctx, orchestrator.ToolCall{ID: pending.ToolCallId, Name: pending.ToolName, ParametersJSON: pending.ArgumentsJson}, nil)
				if err != nil {
					a.renderer.PrintLine("approval input unavailable")
					return
				}
				if approved {
					decision = session.ApprovalApproved
				}
			}
			_, err := a.agentRunner.DecideAgentToolApproval(ctx, current.Actor, task.Id, session.ToolApprovalDecisionCommand{
				RunID: pending.RunId, ToolCallID: pending.ToolCallId, PendingEventID: pending.PendingEventId, PendingSeq: pending.PendingSeq, Decision: decision,
			})
			if err != nil {
				a.renderer.PrintLine("agent approval rejected")
				return
			}
			a.renderer.PrintLine("agent tool " + string(decision))
			return
		}
	}
	if len(fields) > 1 && fields[1] != "list" {
		a.renderer.PrintLine("usage: /agents <approve|deny> <task-id> <run-id> <tool-call-id>")
		return
	}
	if len(lines) == 0 {
		lines = append(lines, "no independent agents")
	}
	a.renderer.PrintBlock("agents", lines)
}

type cliAgentTools struct {
	cfg   config.Config
	mu    sync.Mutex
	items map[string]*tools.Executor
}

func (a *cliAgentTools) Execute(ctx context.Context, actor identity.Actor, sessionID string, call orchestrator.ToolCall) orchestrator.ToolResult {
	return a.ExecuteInWorkingDir(ctx, actor, sessionID, a.cfg.WorkingDir, call)
}

func (a *cliAgentTools) ExecuteInWorkingDir(ctx context.Context, _ identity.Actor, sessionID, dir string, call orchestrator.ToolCall) orchestrator.ToolResult {
	a.mu.Lock()
	executor := a.items[sessionID]
	if executor == nil {
		executor = tools.NewExecutor(dir)
		manager := skills.NewManager()
		user, _ := os.UserHomeDir()
		_ = manager.Discover(skills.DiscoveryOptions{ProjectDSHDir: filepath.Join(dir, ".dsh", "skills"), ProjectAgentsDir: filepath.Join(dir, ".agents", "skills"), ProjectDir: filepath.Join(dir, ".agent", "skills"),
			Directories: a.cfg.SkillDirectories, UserDSHDir: filepath.Join(user, ".dsh", "skills"), UserAgentsDir: filepath.Join(user, ".agents", "skills"), GlobalDir: filepath.Join(user, ".agent", "skills")})
		executor.SetSkillsManager(manager)
		if a.cfg.Sandbox.Enabled {
			executor.SetSandbox(sandbox.NewSandboxRunner(sandbox.Config{Backend: a.cfg.Sandbox.Backend, WSLDistro: a.cfg.Sandbox.WSLDistro, TrustRoot: dir, Image: a.cfg.Sandbox.Image, AllowWorkspaceWrite: a.cfg.Sandbox.AllowWorkspaceWrite,
				MemoryLimit: a.cfg.Sandbox.MemoryLimit, CPULimit: a.cfg.Sandbox.CPULimit, PidsLimit: a.cfg.Sandbox.PidsLimit, TmpfsSize: a.cfg.Sandbox.TmpfsSize}))
		}
		a.items[sessionID] = executor
	}
	a.mu.Unlock()
	if call.Name == "Bash" && !executor.HasSandbox() {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "independent agent shell requires a configured sandbox", ExitCode: 1}
	}
	var arguments map[string]any
	if json.Unmarshal([]byte(call.ParametersJSON), &arguments) != nil {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "invalid child tool parameters", ExitCode: 1}
	}
	result, err := executor.Execute(ctx, tools.ToolRequest{Name: call.Name, Arguments: arguments, OwnerSessionID: sessionID})
	out := orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Output: result.Output, Error: result.Error, ExitCode: int32(result.ExitCode), Truncated: result.Truncated}
	for _, change := range result.Changes {
		out.Changes = append(out.Changes, orchestrator.CodeChange{Path: change.Path, Before: change.Before, After: change.After})
	}
	if err != nil {
		out.Error = "child tool execution failed"
		if out.ExitCode == 0 {
			out.ExitCode = 1
		}
	}
	return out
}

func (a *cliAgentTools) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var first error
	for _, executor := range a.items {
		if err := executor.Close(); err != nil && first == nil {
			first = err
		}
	}
	a.items = make(map[string]*tools.Executor)
	return first
}
