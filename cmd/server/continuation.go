package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/skills"
	"code-agent/internal/tools"
)

// configureContinuationSkills installs the Harness-side Skill catalog on the
// executor used by browser continuations. Discovery is deliberately separate
// from process startup so callers can surface a degraded catalog while the
// built-in skills remain usable.
func configureContinuationSkills(executor *tools.Executor, root string) error {
	if executor == nil {
		return os.ErrInvalid
	}
	manager := skills.NewManager()
	globalDir := ""
	userDSHDir := ""
	userAgentsDir := ""
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		globalDir = filepath.Join(home, ".agent", "skills")
		userDSHDir = filepath.Join(home, ".dsh", "skills")
		userAgentsDir = filepath.Join(home, ".agents", "skills")
	}
	err := manager.Discover(skills.DiscoveryOptions{
		GlobalDir:        globalDir,
		UserDSHDir:       userDSHDir,
		UserAgentsDir:    userAgentsDir,
		ProjectDSHDir:    filepath.Join(root, ".dsh", "skills"),
		ProjectAgentsDir: filepath.Join(root, ".agents", "skills"),
		ProjectDir:       filepath.Join(root, ".agent", "skills"),
	})
	executor.SetSkillsManager(manager)
	return err
}

// continuationToolExecutors keeps mutable Executor cwd/job state scoped to a
// durable Session instead of sharing one process-global working directory.
type continuationToolExecutors struct {
	root  string
	mu    sync.Mutex
	items map[string]*tools.Executor
}

func newContinuationToolExecutors(root string) *continuationToolExecutors {
	return &continuationToolExecutors{root: root, items: make(map[string]*tools.Executor)}
}

func (m *continuationToolExecutors) Execute(ctx context.Context, actor identity.Actor, sessionID string, call orchestrator.ToolCall) orchestrator.ToolResult {
	return m.ExecuteInWorkingDir(ctx, actor, sessionID, "", call)
}

func (m *continuationToolExecutors) ExecuteInWorkingDir(ctx context.Context, _ identity.Actor, sessionID, workingDir string, call orchestrator.ToolCall) orchestrator.ToolResult {
	executor, err := m.executor(sessionID, workingDir)
	if err != nil {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: err.Error(), ExitCode: 1}
	}
	if strings.HasPrefix(sessionID, "agent-") && call.Name == "Bash" && !executor.HasSandbox() {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "independent agent shell requires a configured sandbox", ExitCode: 1}
	}
	var arguments map[string]any
	if strings.TrimSpace(call.ParametersJSON) != "" {
		if err := json.Unmarshal([]byte(call.ParametersJSON), &arguments); err != nil {
			return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "invalid tool parameters", ExitCode: 1}
		}
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	result, execErr := executor.Execute(ctx, tools.ToolRequest{Name: call.Name, Arguments: arguments, OwnerSessionID: sessionID})
	out := orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Output: result.Output, Error: result.Error, ExitCode: int32(result.ExitCode), Truncated: result.Truncated}
	for _, change := range result.Changes {
		out.Changes = append(out.Changes, orchestrator.CodeChange{Path: change.Path, Before: change.Before, After: change.After})
	}
	if execErr != nil && out.Error == "" {
		out.Error = execErr.Error()
	}
	if execErr != nil && out.ExitCode == 0 {
		out.ExitCode = 1
	}
	return out
}

func (m *continuationToolExecutors) executor(sessionID, workingDir string) (*tools.Executor, error) {
	key := strings.TrimSpace(sessionID) + "\x00" + filepath.Clean(strings.TrimSpace(workingDir))
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.items[key]; existing != nil {
		return existing, nil
	}
	dir := strings.TrimSpace(workingDir)
	if dir == "" {
		dir = m.root
	}
	trustRoot := m.root
	if strings.HasPrefix(sessionID, "agent-") {
		trustRoot = dir
	}
	executor := tools.NewExecutor(trustRoot)
	if err := executor.SetWorkingDir(dir); err != nil {
		_ = executor.Close()
		return nil, fmt.Errorf("set session working directory: %w", err)
	}
	if err := configureContinuationSkills(executor, dir); err != nil {
		// Built-ins remain available; discovery errors are intentionally not
		// fatal because the executor is already safely scoped.
	}
	m.items[key] = executor
	return executor, nil
}

func (m *continuationToolExecutors) Close() error {
	m.mu.Lock()
	items := make([]*tools.Executor, 0, len(m.items))
	for _, executor := range m.items {
		items = append(items, executor)
	}
	m.items = make(map[string]*tools.Executor)
	m.mu.Unlock()
	var firstErr error
	for _, executor := range items {
		if err := executor.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
