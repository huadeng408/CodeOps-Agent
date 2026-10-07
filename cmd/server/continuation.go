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
	"code-agent/internal/mcp"
	"code-agent/internal/orchestrator"
	"code-agent/internal/sandbox"
	"code-agent/internal/skills"
	"code-agent/internal/telemetry/genai"
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
	root    string
	mu      sync.Mutex
	items   map[string]*tools.Executor
	sandbox *lazySandboxRunner
	tracer  genai.Tracer
	mcp     *mcp.Manager
	mcps    map[string]*mcp.Manager
}

// lazySandboxRunner keeps server startup and read-only sessions responsive
// while preserving the sandbox package's fail-closed behaviour. It never
// executes a host process when backend selection fails.
type lazySandboxRunner struct {
	config sandbox.Config
	once   sync.Once
	mu     sync.RWMutex
	runner *sandbox.RoutingRunner
}

func newLazySandboxRunner(config sandbox.Config) *lazySandboxRunner {
	return &lazySandboxRunner{config: config}
}

func (r *lazySandboxRunner) init() *sandbox.RoutingRunner {
	r.once.Do(func() {
		r.mu.Lock()
		r.runner = sandbox.NewSandboxRunner(r.config)
		r.mu.Unlock()
	})
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.runner
}

func (r *lazySandboxRunner) Run(ctx context.Context, request sandbox.Request) (sandbox.Result, error) {
	return r.init().Run(ctx, request)
}

func (r *lazySandboxRunner) Start(ctx context.Context, request sandbox.Request) (sandbox.Process, error) {
	return r.init().Start(ctx, request)
}

func (r *lazySandboxRunner) Capability() (string, string) {
	r.mu.RLock()
	runner := r.runner
	r.mu.RUnlock()
	if runner == nil {
		return "unknown", "sandbox backend has not been probed"
	}
	if backend := runner.Backend(); backend != "" && backend != "unavailable" {
		return "ok", "sandbox backend: " + string(backend)
	}
	return "blocked", "no isolated sandbox backend is available"
}

func newContinuationToolExecutors(root string) *continuationToolExecutors {
	config := sandbox.DefaultConfig()
	config.TrustRoot = root
	return &continuationToolExecutors{root: root, items: make(map[string]*tools.Executor), sandbox: newLazySandboxRunner(config), mcps: make(map[string]*mcp.Manager)}
}

func (m *continuationToolExecutors) SetTracer(tracer genai.Tracer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tracer = tracer
	for _, executor := range m.items {
		executor.SetTracer(tracer)
	}
}

func (m *continuationToolExecutors) SetMCPManager(manager *mcp.Manager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mcp = manager
	for key, executor := range m.items {
		if m.mcps[key] == nil {
			executor.SetMCPManager(manager)
		}
	}
}

func (m *continuationToolExecutors) Execute(ctx context.Context, actor identity.Actor, sessionID string, call orchestrator.ToolCall) orchestrator.ToolResult {
	return m.ExecuteInWorkingDir(ctx, actor, sessionID, "", call)
}

func (m *continuationToolExecutors) ExecuteInWorkingDir(ctx context.Context, _ identity.Actor, sessionID, workingDir string, call orchestrator.ToolCall) orchestrator.ToolResult {
	executor, err := m.executor(ctx, sessionID, workingDir)
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
	result, execErr := executor.Execute(ctx, tools.ToolRequest{
		Name: call.Name, Arguments: arguments, OwnerSessionID: sessionID,
		MCPServer: call.MCPServer, MCPInputSchemaSHA256: call.MCPInputSchemaSHA256, MCPServerConfigSHA256: call.MCPServerConfigSHA256,
	})
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

func (m *continuationToolExecutors) executor(ctx context.Context, sessionID, workingDir string) (*tools.Executor, error) {
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
	// All continuation executors share the same lazy, isolated runner. The
	// runner's trust root is the parent workspace, so child worktrees remain
	// confined without probing Docker once per session.
	executor.SetSandbox(m.sandbox)
	executor.SetTracer(m.tracer)
	manager := m.mcp
	if strings.HasPrefix(sessionID, "agent-") && manager != nil {
		var cloneErr error
		manager, cloneErr = manager.CloneForWorkingDir(ctx, dir)
		if cloneErr != nil {
			_ = executor.Close()
			return nil, fmt.Errorf("start child MCP manager: %w", cloneErr)
		}
		m.mcps[key] = manager
	}
	executor.SetMCPManager(manager)
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

func (m *continuationToolExecutors) ReleaseSession(sessionID string) error {
	prefix := strings.TrimSpace(sessionID) + "\x00"
	if prefix == "\x00" {
		return nil
	}
	m.mu.Lock()
	items := make([]*tools.Executor, 0, 1)
	managers := make([]*mcp.Manager, 0, 1)
	for key, executor := range m.items {
		if strings.HasPrefix(key, prefix) {
			items = append(items, executor)
			delete(m.items, key)
		}
	}
	for key, manager := range m.mcps {
		if strings.HasPrefix(key, prefix) {
			managers = append(managers, manager)
			delete(m.mcps, key)
		}
	}
	m.mu.Unlock()
	var first error
	for _, executor := range items {
		if err := executor.Close(); err != nil && first == nil {
			first = err
		}
	}
	for _, manager := range managers {
		if err := manager.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (m *continuationToolExecutors) Close() error {
	m.mu.Lock()
	items := make([]*tools.Executor, 0, len(m.items))
	for _, executor := range m.items {
		items = append(items, executor)
	}
	m.items = make(map[string]*tools.Executor)
	managers := make([]*mcp.Manager, 0, len(m.mcps))
	for _, manager := range m.mcps {
		managers = append(managers, manager)
	}
	m.mcps = make(map[string]*mcp.Manager)
	m.mu.Unlock()
	var firstErr error
	for _, executor := range items {
		if err := executor.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, manager := range managers {
		if err := manager.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
