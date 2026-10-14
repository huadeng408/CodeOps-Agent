package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/safety"

	"github.com/google/jsonschema-go/jsonschema"
)

type ServerState string

const (
	ServerStopped ServerState = "stopped"
	ServerRunning ServerState = "running"
)

type Server struct {
	Config      ServerConfig
	State       ServerState
	LastError   string
	StartedAt   time.Time
	catalog     map[string]ToolBinding
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      *bufio.Reader
	unpin       func()
	nextID      int64
	requestMu   sync.Mutex
	lifecycleMu sync.Mutex
	// A failed stream is stopped; independent calls must revalidate its pinned
	// identity and catalog before starting a replacement process.
	poisoned bool
}

type Manager struct {
	mu        sync.Mutex
	servers   map[string]*Server
	tools     map[string]ToolDefinition
	authority *Manager
}

type ConfigFile struct {
	Servers map[string]ServerConfig `json:"servers"`
}

var reservedToolNames = map[string]struct{}{
	"agenttask": {}, "askuser": {}, "attachment": {}, "bash": {}, "coderuntime": {},
	"edit": {}, "extension": {}, "git": {}, "glob": {}, "grep": {}, "jobkill": {},
	"joblist": {}, "joboutput": {}, "jobstart": {}, "jobwait": {}, "jobwrite": {},
	"lsp": {}, "managememory": {}, "multiedit": {}, "notebookedit": {}, "planwrite": {},
	"publishartifact": {}, "read": {}, "readspill": {}, "recallmemory": {}, "runworkflow": {},
	"searchknowledge": {}, "sessionfork": {}, "sessionrewind": {}, "skill": {}, "spawnagent": {},
	"todowrite": {}, "webfetch": {}, "websearch": {}, "write": {}, "job_kill": {},
	"delete": {}, "rename": {},
	"job_list": {}, "job_output": {}, "job_start": {}, "job_wait": {}, "job_write": {},
}

func NewManager() *Manager {
	return &Manager{
		servers: make(map[string]*Server),
		tools:   make(map[string]ToolDefinition),
	}
}

func (m *Manager) RegisterServer(cfg ServerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.Name == "" {
		return
	}
	m.servers[cfg.Name] = &Server{Config: cloneServerConfig(cfg), State: ServerStopped}
}

func (m *Manager) LoadConfigFile(path string) error {
	data, resolved, err := readConfigFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}

	var cfg ConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse mcp config %s: %w", resolved, err)
	}
	for name, server := range cfg.Servers {
		if server.Name == "" {
			server.Name = name
		}
		if server.WorkingDir != "" && !filepath.IsAbs(server.WorkingDir) {
			server.WorkingDir = filepath.Join(filepath.Dir(resolved), server.WorkingDir)
		}
		m.RegisterServer(server)
	}
	return nil
}

func (m *Manager) Start(ctx context.Context, name string) error {
	return m.StartContext(ctx, name)
}

func (m *Manager) StartContext(ctx context.Context, name string) error {
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	m.mu.Lock()
	server, ok := m.servers[name]
	m.mu.Unlock()
	if !ok {
		return errors.New("mcp server not registered")
	}
	server.lifecycleMu.Lock()
	defer server.lifecycleMu.Unlock()
	m.mu.Lock()
	if server.Config.Command == "" {
		err := errors.New("mcp server command is empty")
		server.State = ServerStopped
		server.LastError = err.Error()
		m.mu.Unlock()
		return err
	}
	if server.State == ServerRunning {
		m.mu.Unlock()
		return nil
	}
	cfg := server.Config
	m.mu.Unlock()

	if err := startServerProcess(ctx, server, cfg); err != nil {
		m.mu.Lock()
		server.State = ServerStopped
		server.LastError = err.Error()
		m.mu.Unlock()
		return err
	}

	if err := initializeServer(ctx, server); err != nil {
		_ = stopServerProcess(server)
		m.mu.Lock()
		server.State = ServerStopped
		server.LastError = err.Error()
		m.mu.Unlock()
		return err
	}
	tools, err := listServerTools(ctx, server)
	if err != nil {
		_ = stopServerProcess(server)
		m.mu.Lock()
		server.State = ServerStopped
		server.LastError = err.Error()
		m.mu.Unlock()
		return err
	}

	normalized, err := normalizeServerTools(server, tools)
	if err != nil {
		_ = stopServerProcess(server)
		m.mu.Lock()
		server.State = ServerStopped
		server.LastError = err.Error()
		m.mu.Unlock()
		return err
	}

	m.mu.Lock()
	for _, tool := range normalized {
		if existing, exists := m.tools[tool.Name]; exists {
			m.mu.Unlock()
			err = fmt.Errorf("mcp tool %q conflicts with server %q", tool.Name, existing.Server)
			_ = stopServerProcess(server)
			m.mu.Lock()
			server.State = ServerStopped
			server.LastError = err.Error()
			m.mu.Unlock()
			return err
		}
	}
	server.State = ServerRunning
	server.StartedAt = time.Now()
	server.LastError = ""
	server.catalog = make(map[string]ToolBinding, len(normalized))
	for _, tool := range normalized {
		m.tools[tool.Name] = tool
		server.catalog[tool.Name] = ToolBinding{Name: tool.Name, Server: tool.Server, InputSchemaSHA256: tool.InputSchemaSHA256, ServerConfigSHA256: tool.ServerConfigSHA256}
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) StartAll(ctx context.Context) []error {
	servers := m.ListServers()
	errs := make([]error, 0)
	for _, server := range servers {
		if err := m.Start(ctx, server.Name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", server.Name, err))
		}
	}
	return errs
}

// CloneForWorkingDir starts an independent copy of every currently running
// server with the child workspace as its process working directory.
func (m *Manager) CloneForWorkingDir(ctx context.Context, workingDir string) (*Manager, error) {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return nil, errors.New("mcp child working directory is required")
	}
	resolved, err := filepath.Abs(workingDir)
	if err != nil {
		return nil, errors.New("resolve mcp child working directory")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return nil, errors.New("mcp child working directory is unavailable")
	}

	child := NewManager()
	child.authority = m
	if m.authority != nil {
		child.authority = m.authority
	}
	servers := m.Snapshot()
	for i := range servers {
		server := &servers[i]
		if server.State != ServerRunning {
			continue
		}
		cfg := server.Config
		cfg.WorkingDir = resolved
		child.RegisterServer(cfg)
	}
	if startErrs := child.StartAll(ctx); len(startErrs) > 0 {
		_ = child.Close()
		return nil, errors.Join(startErrs...)
	}
	return child, nil
}

func (m *Manager) Close() error {
	var errs []error
	for _, server := range m.ListServers() {
		if err := m.Stop(server.Name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	server, ok := m.servers[name]
	m.mu.Unlock()
	if !ok {
		return errors.New("mcp server not registered")
	}
	server.lifecycleMu.Lock()
	defer server.lifecycleMu.Unlock()
	server.requestMu.Lock()
	defer server.requestMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := stopServerProcess(server); err != nil {
		return err
	}
	server.State = ServerStopped
	server.LastError = ""
	server.poisoned = false
	server.catalog = nil
	for toolName, tool := range m.tools {
		if tool.Server == name {
			delete(m.tools, toolName)
		}
	}
	return nil
}

func (m *Manager) ListServers() []ServerConfig {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]ServerConfig, 0, len(m.servers))
	for _, server := range m.servers {
		out = append(out, cloneServerConfig(server.Config))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func (m *Manager) Snapshot() []Server {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Server, 0, len(m.servers))
	for _, server := range m.servers {
		out = append(out, Server{
			Config:    cloneServerConfig(server.Config),
			State:     server.State,
			LastError: server.LastError,
			StartedAt: server.StartedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Config.Name < out[j].Config.Name
	})
	return out
}

func (m *Manager) RegisterTool(tool ToolDefinition) error {
	if strings.TrimSpace(tool.ServerConfigSHA256) == "" {
		tool.ServerConfigSHA256 = serverConfigSHA256(ServerConfig{Name: strings.TrimSpace(tool.Server)})
	}
	tool, err := normalizeToolDefinition(tool)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.tools[tool.Name]; ok {
		return fmt.Errorf("mcp tool %q conflicts with server %q", tool.Name, existing.Server)
	}
	m.tools[tool.Name] = tool
	return nil
}

func (m *Manager) ResolveTool(name string) (ToolDefinition, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tool, ok := m.tools[name]
	return tool, ok
}

func (m *Manager) ResolveBinding(name string) (ToolBinding, bool) {
	tool, ok := m.ResolveTool(name)
	if !ok {
		return ToolBinding{}, false
	}
	return ToolBinding{Name: tool.Name, Server: tool.Server, InputSchemaSHA256: tool.InputSchemaSHA256, ServerConfigSHA256: tool.ServerConfigSHA256}, true
}

func (m *Manager) ListTools() []ToolDefinition {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]ToolDefinition, 0, len(m.tools))
	for _, tool := range m.tools {
		out = append(out, tool)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// Catalog is a metadata projection for the existing versioned Go/Python
// boundary; it grants no execution authority and writes no task files.
func (m *Manager) Catalog() *codeagentpb.MCPToolCatalog {
	catalog := &codeagentpb.MCPToolCatalog{SchemaVersion: 1}
	if m == nil {
		return catalog
	}
	for _, tool := range m.ListTools() {
		catalog.Tools = append(catalog.Tools, &codeagentpb.MCPToolDefinition{Name: tool.Name, Description: tool.Description, Server: tool.Server,
			InputSchemaJson: string(tool.InputSchema), InputSchemaSha256: tool.InputSchemaSHA256, ServerConfigSha256: tool.ServerConfigSHA256})
	}
	return catalog
}

func (m *Manager) WriteToolsManifest(workspaceRoot string) error {
	m.mu.Lock()
	tools := make([]ToolDefinition, 0, len(m.tools))
	for _, tool := range m.tools {
		tools = append(tools, tool)
	}
	m.mu.Unlock()
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return nil
	}
	root, err := os.OpenRoot(workspaceRoot)
	if err != nil {
		return fmt.Errorf("open mcp manifest root: %w", err)
	}
	defer root.Close()
	if err := root.MkdirAll(".agent", 0o755); err != nil {
		return fmt.Errorf("create mcp manifest dir: %w", err)
	}
	payload := struct {
		Tools []ToolDefinition `json:"tools"`
	}{Tools: tools}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode mcp manifest: %w", err)
	}
	if err := root.WriteFile(".agent/mcp-tools.json", data, 0o644); err != nil {
		return fmt.Errorf("write mcp manifest: %w", err)
	}
	return nil
}

func (m *Manager) CallTool(ctx context.Context, name string, arguments map[string]any) (ToolCallResult, error) {
	return m.callTool(ctx, name, arguments, nil)
}

// CallToolBound rejects catalog drift between delegation and dispatch.
func (m *Manager) CallToolBound(ctx context.Context, binding ToolBinding, arguments map[string]any) (ToolCallResult, error) {
	return m.callTool(ctx, binding.Name, arguments, &binding)
}

func (m *Manager) callTool(ctx context.Context, name string, arguments map[string]any, expected *ToolBinding) (ToolCallResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	m.mu.Lock()
	tool, ok := m.tools[name]
	if !ok {
		m.mu.Unlock()
		return ToolCallResult{}, &CallError{Kind: "refused", Cause: errors.New("mcp tool not registered")}
	}
	if expected != nil && (expected.Name != tool.Name || expected.Server != tool.Server || expected.InputSchemaSHA256 != tool.InputSchemaSHA256 || expected.ServerConfigSHA256 != tool.ServerConfigSHA256) {
		m.mu.Unlock()
		return ToolCallResult{}, &CallError{Kind: "refused", Cause: errors.New("mcp tool binding changed")}
	}
	server, ok := m.servers[tool.Server]
	if !ok || server.State != ServerRunning {
		m.mu.Unlock()
		return ToolCallResult{}, &CallError{Kind: "unavailable", Cause: errors.New("mcp server not running")}
	}
	m.mu.Unlock()

	if m.authority != nil {
		binding, approved := m.authority.ResolveBinding(name)
		if !approved || binding != (ToolBinding{Name: tool.Name, Server: tool.Server, InputSchemaSHA256: tool.InputSchemaSHA256, ServerConfigSHA256: tool.ServerConfigSHA256}) {
			stopped := m.Stop(tool.Server) == nil
			return ToolCallResult{}, &CallError{Kind: "refused", Stopped: stopped, Cause: errors.New("parent MCP catalog approval was revoked or changed")}
		}
	}

	if arguments == nil {
		arguments = map[string]any{}
	}
	if tool.input == nil || tool.input.Validate(arguments) != nil {
		return ToolCallResult{}, &CallError{Kind: "refused", Cause: errors.New("arguments do not match the approved input schema")}
	}
	params := map[string]any{
		"name":      name,
		"arguments": arguments,
	}
	raw, err := sendRequest(ctx, server, "tools/call", params)
	if err != nil {
		return ToolCallResult{}, err
	}
	var result ToolCallResult
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			return ToolCallResult{}, fmt.Errorf("decode tools/call result: %w", err)
		}
	}
	return result, nil
}

func normalizeToolDefinition(tool ToolDefinition) (ToolDefinition, error) {
	tool.Name = strings.TrimSpace(tool.Name)
	tool.Server = strings.TrimSpace(tool.Server)
	if tool.Name == "" || tool.Server == "" {
		return ToolDefinition{}, errors.New("mcp tool name and server are required")
	}
	if _, reserved := reservedToolNames[strings.ToLower(tool.Name)]; reserved {
		return ToolDefinition{}, fmt.Errorf("mcp tool %q conflicts with a Harness tool", tool.Name)
	}
	raw := bytes.TrimSpace(tool.InputSchema)
	if len(raw) == 0 {
		raw = []byte(`{"type":"object","properties":{}}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var schema map[string]any
	if err := decoder.Decode(&schema); err != nil || schema == nil {
		return ToolDefinition{}, fmt.Errorf("mcp tool %q has invalid input schema", tool.Name)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ToolDefinition{}, fmt.Errorf("mcp tool %q has invalid input schema", tool.Name)
	}
	canonical, err := json.Marshal(schema)
	if err != nil {
		return ToolDefinition{}, fmt.Errorf("canonicalize mcp tool %q input schema: %w", tool.Name, err)
	}
	var input jsonschema.Schema
	if json.Unmarshal(canonical, &input) != nil || input.Type != "object" {
		return ToolDefinition{}, fmt.Errorf("mcp tool %q input schema must describe an object", tool.Name)
	}
	tool.input, err = input.Resolve(nil)
	if err != nil {
		return ToolDefinition{}, fmt.Errorf("mcp tool %q input schema cannot be resolved locally", tool.Name)
	}
	digest := sha256.Sum256(canonical)
	tool.InputSchema = canonical
	tool.InputSchemaSHA256 = fmt.Sprintf("%x", digest[:])
	if !validSHA256(tool.ServerConfigSHA256) {
		return ToolDefinition{}, fmt.Errorf("mcp tool %q has invalid server config checksum", tool.Name)
	}
	return tool, nil
}

func normalizeServerTools(server *Server, tools []ToolDefinition) ([]ToolDefinition, error) {
	if trust := server.Config.Trust; trust != nil && len(tools) != len(trust.Tools) {
		return nil, errors.New("mcp approved catalog pin changed")
	}
	normalized := make([]ToolDefinition, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		tool.Server = server.Config.Name
		tool.ServerConfigSHA256 = serverConfigSHA256(server.Config)
		var err error
		tool, err = normalizeToolDefinition(tool)
		if err != nil {
			return nil, err
		}
		if trust := server.Config.Trust; trust != nil && trust.Tools[tool.Name] != tool.InputSchemaSHA256 {
			return nil, errors.New("mcp approved catalog pin changed")
		}
		if _, duplicate := seen[tool.Name]; duplicate {
			return nil, fmt.Errorf("duplicate mcp tool %q from server %q", tool.Name, server.Config.Name)
		}
		seen[tool.Name] = struct{}{}
		normalized = append(normalized, tool)
	}
	return normalized, nil
}

func serverCatalogMatches(pinned map[string]ToolBinding, tools []ToolDefinition) bool {
	if len(pinned) != len(tools) {
		return false
	}
	for _, tool := range tools {
		binding, ok := pinned[tool.Name]
		if !ok || binding != (ToolBinding{Name: tool.Name, Server: tool.Server, InputSchemaSHA256: tool.InputSchemaSHA256, ServerConfigSHA256: tool.ServerConfigSHA256}) {
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func serverConfigSHA256(cfg ServerConfig) string {
	pinned := struct {
		Name    string            `json:"name"`
		Command string            `json:"command"`
		Args    []string          `json:"args,omitempty"`
		Env     map[string]string `json:"env,omitempty"`
		Trust   *ServerTrust      `json:"trust,omitempty"`
	}{Name: cfg.Name, Command: cfg.Command, Args: cfg.Args, Env: cfg.Env, Trust: cfg.Trust}
	canonical, _ := json.Marshal(pinned)
	digest := sha256.Sum256(canonical)
	return fmt.Sprintf("%x", digest[:])
}

func cloneServerConfig(cfg ServerConfig) ServerConfig {
	copy := cfg
	copy.Args = append([]string(nil), cfg.Args...)
	if cfg.Trust != nil {
		trust := *cfg.Trust
		trust.Tools = make(map[string]string, len(cfg.Trust.Tools))
		for name, checksum := range cfg.Trust.Tools {
			trust.Tools[name] = checksum
		}
		copy.Trust = &trust
	}
	if cfg.Env != nil {
		copy.Env = make(map[string]string, len(cfg.Env))
		for key, value := range cfg.Env {
			copy.Env[key] = value
		}
	}
	return copy
}

func startServerProcess(ctx context.Context, server *Server, cfg ServerConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.Trust != nil {
		if !filepath.IsAbs(cfg.Command) || cfg.Trust.Name == "" || cfg.Trust.Version == "" || cfg.Trust.License == "" || len(cfg.Trust.Tools) == 0 || !validSHA256(cfg.Trust.ExecutableSHA256) {
			return errors.New("mcp trust pin is incomplete")
		}
		unpin, err := safety.PinReadOnlyFiles(cfg.Command)
		if err != nil {
			return errors.New("mcp executable pin is unavailable")
		}
		checksum, err := executableSHA256(cfg.Command)
		if err != nil || checksum != cfg.Trust.ExecutableSHA256 {
			unpin()
			return errors.New("mcp executable pin mismatch")
		}
		server.unpin = unpin
		defer func() {
			if server.cmd == nil && server.unpin != nil {
				server.unpin()
				server.unpin = nil
			}
		}()
	}
	// The Manager, not the startup request, owns the server process lifetime.
	// Per-call cancellation is enforced by sendRequest and poison recovery.
	cmd := exec.Command(cfg.Command, cfg.Args...)
	if cfg.WorkingDir != "" {
		cmd.Dir = cfg.WorkingDir
	}
	cmd.Env = mcpProcessEnvironment(os.Environ(), cfg.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open mcp stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open mcp stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start mcp server: %w", err)
	}
	server.cmd = cmd
	server.stdin = stdin
	server.stdout = bufio.NewReader(stdout)
	return nil
}

func mcpProcessEnvironment(ambient []string, explicit map[string]string) []string {
	env := safety.ScrubEnvironment(ambient)
	keys := make([]string, 0, len(explicit))
	for key := range explicit {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for i := len(env) - 1; i >= 0; i-- {
			existing, _, _ := strings.Cut(env[i], "=")
			if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(key)) {
				env = append(env[:i], env[i+1:]...)
			}
		}
		env = append(env, key+"="+explicit[key])
	}
	return env
}

func stopServerProcess(server *Server) error {
	if server.stdin != nil {
		_ = server.stdin.Close()
	}
	if server.cmd != nil && server.cmd.Process != nil {
		_ = server.cmd.Process.Kill()
		_ = server.cmd.Wait()
	}
	server.cmd = nil
	server.stdin = nil
	server.stdout = nil
	if server.unpin != nil {
		server.unpin()
		server.unpin = nil
	}
	return nil
}

func initializeServer(ctx context.Context, server *Server) error {
	server.requestMu.Lock()
	defer server.requestMu.Unlock()
	return initializeServerLocked(ctx, server)
}

func initializeServerLocked(ctx context.Context, server *Server) error {
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "code-agent",
			"version": "0.1.0",
		},
	}
	raw, err := sendRequestLocked(ctx, server, "initialize", params)
	if err != nil {
		return err
	}
	var initialized struct {
		ProtocolVersion string                     `json:"protocolVersion"`
		Capabilities    map[string]json.RawMessage `json:"capabilities"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if json.Unmarshal(raw, &initialized) != nil || initialized.ProtocolVersion != "2024-11-05" || initialized.ServerInfo.Name == "" || len(initialized.Capabilities["tools"]) == 0 {
		return errors.New("mcp initialization protocol or tool capability is invalid")
	}
	if trust := server.Config.Trust; trust != nil && (initialized.ServerInfo.Name != trust.Name || initialized.ServerInfo.Version != trust.Version) {
		return errors.New("mcp server identity does not match the approved pin")
	}
	return sendNotificationLocked(ctx, server, "notifications/initialized", map[string]any{})
}

func listServerTools(ctx context.Context, server *Server) ([]ToolDefinition, error) {
	raw, err := sendRequest(ctx, server, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return decodeServerTools(raw)
}

func listServerToolsLocked(ctx context.Context, server *Server) ([]ToolDefinition, error) {
	raw, err := sendRequestLocked(ctx, server, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return decodeServerTools(raw)
}

func decodeServerTools(raw json.RawMessage) ([]ToolDefinition, error) {
	var payload struct {
		Tools []ToolDefinition `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode tools/list result: %w", err)
	}
	return payload.Tools, nil
}

func sendRequest(ctx context.Context, server *Server, method string, params any) (json.RawMessage, error) {
	// Acquire the mutex up front so the poisoned-restart below is serialized
	// against concurrent callers and touches stdin/stdout/cmd safely.
	server.requestMu.Lock()
	defer server.requestMu.Unlock()
	if server.poisoned {
		// A prior call timed out and abandoned a goroutine on the old stdout.
		// Kill+restart: the EOF reaps that goroutine and gives us a clean
		// JSON-RPC stream so stale/late responses can't corrupt this read.
		_ = stopServerProcess(server)
		if err := startServerProcess(ctx, server, server.Config); err != nil {
			// Keep the pinned catalog and logical running state. poisoned stays
			// true so a later call can retry recovery without catalog drift.
			server.poisoned = true
			return nil, &CallError{Kind: "unavailable", Stopped: true, Cause: fmt.Errorf("restart poisoned mcp server: %w", err)}
		}
		if err := initializeServerLocked(ctx, server); err != nil {
			// The replacement process is not usable yet, but the next call can
			// kill it and retry the same pinned server configuration.
			server.poisoned = true
			_ = stopServerProcess(server)
			return nil, &CallError{Kind: "unavailable", Stopped: true, Cause: fmt.Errorf("re-init mcp server: %w", err)}
		}
		tools, err := listServerToolsLocked(ctx, server)
		if err != nil {
			_ = stopServerProcess(server)
			server.poisoned = true
			return nil, &CallError{Kind: "unavailable", Stopped: true, Cause: fmt.Errorf("re-list mcp server tools: %w", err)}
		}
		normalized, err := normalizeServerTools(server, tools)
		if err != nil {
			_ = stopServerProcess(server)
			server.poisoned = true
			return nil, &CallError{Kind: "unavailable", Stopped: true, Cause: fmt.Errorf("validate restarted mcp server catalog: %w", err)}
		}
		if !serverCatalogMatches(server.catalog, normalized) {
			_ = stopServerProcess(server)
			server.poisoned = true
			return nil, &CallError{Kind: "unavailable", Stopped: true, Cause: errors.New("mcp server tool catalog changed after restart")}
		}
		server.poisoned = false
	}
	return sendRequestLocked(ctx, server, method, params)
}

func sendRequestLocked(ctx context.Context, server *Server, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, &CallError{Kind: "cancelled", Cause: err}
	}
	if server.stdin == nil || server.stdout == nil {
		return nil, errors.New("mcp server stdio is not ready")
	}
	id := atomic.AddInt64(&server.nextID, 1)
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode mcp params: %w", err)
	}
	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramsJSON,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode mcp request: %w", err)
	}
	if len(data) > 8<<20 {
		return nil, &CallError{Kind: "refused", Cause: errors.New("mcp request exceeds the frame limit")}
	}

	if err := writeJSONLine(ctx, server.stdin, data); err != nil {
		return nil, failServerCall(server, err)
	}
	for {
		line, err := readJSONLine(ctx, server)
		if err != nil {
			return nil, failServerCall(server, err)
		}
		fields, err := decodeMCPEnvelope(line)
		if err != nil {
			return nil, failServerCall(server, errors.New("mcp protocol response is malformed"))
		}
		var version string
		if json.Unmarshal(fields["jsonrpc"], &version) != nil || version != "2.0" {
			return nil, failServerCall(server, errors.New("mcp protocol version is invalid"))
		}
		if len(fields["id"]) == 0 && len(fields["result"]) == 0 && len(fields["error"]) == 0 {
			var method string
			if json.Unmarshal(fields["method"], &method) != nil || !strings.HasPrefix(method, "notifications/") {
				return nil, failServerCall(server, errors.New("mcp protocol notification is invalid"))
			}
			continue
		}
		if string(bytes.TrimSpace(fields["id"])) != strconv.FormatInt(id, 10) || (len(fields["result"]) > 0) == (len(fields["error"]) > 0) || len(fields["method"]) > 0 {
			return nil, failServerCall(server, errors.New("mcp protocol response identity or result fields are invalid"))
		}
		if len(fields["error"]) > 0 {
			var rpcError *ErrorObject
			if json.Unmarshal(fields["error"], &rpcError) != nil || rpcError == nil {
				return nil, failServerCall(server, errors.New("mcp protocol error result is invalid"))
			}
			err := fmt.Errorf("mcp rpc error code %d", rpcError.Code)
			if rpcError.Code == -32600 || rpcError.Code == -32601 || rpcError.Code == -32602 {
				return nil, &CallError{Kind: "refused", Cause: err}
			}
			return nil, failServerCall(server, err)
		}
		if method == "tools/call" {
			var result struct {
				Content []struct {
					Type string  `json:"type"`
					Text *string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			}
			if json.Unmarshal(fields["result"], &result) != nil || result.Content == nil {
				return nil, failServerCall(server, errors.New("mcp protocol tool result is invalid"))
			}
			for _, item := range result.Content {
				if item.Type != "text" || item.Text == nil {
					return nil, failServerCall(server, errors.New("mcp protocol tool content is invalid"))
				}
			}
		}
		return fields["result"], nil
	}
}

func decodeMCPEnvelope(line []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, errors.New("mcp envelope must be an object")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok || fields[name] != nil {
			return nil, errors.New("mcp envelope contains a duplicate field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("mcp envelope has trailing data")
	}
	return fields, nil
}

func failServerCall(server *Server, err error) error {
	kind := "connection_lost"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		kind = "timeout"
	case errors.Is(err, context.Canceled):
		kind = "cancelled"
	case strings.Contains(err.Error(), "protocol"):
		kind = "protocol_invalid"
	case strings.Contains(err.Error(), "rpc error"):
		kind = "rpc_failure"
	}
	server.poisoned = true
	_ = stopServerProcess(server)
	return &CallError{Kind: kind, OutcomeUnknown: true, Stopped: true, Cause: err}
}

func sendNotification(ctx context.Context, server *Server, method string, params any) error {
	server.requestMu.Lock()
	defer server.requestMu.Unlock()
	return sendNotificationLocked(ctx, server, method, params)
}

func sendNotificationLocked(ctx context.Context, server *Server, method string, params any) error {
	if server.stdin == nil {
		return errors.New("mcp server stdio is not ready")
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode mcp notification params: %w", err)
	}
	notification := Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
	}
	data, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("encode mcp notification: %w", err)
	}

	return writeJSONLine(ctx, server.stdin, data)
}

func writeJSONLine(ctx context.Context, writer io.Writer, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return errors.New("mcp protocol request exceeds the frame limit")
	}
	written := make(chan error, 1)
	go func() {
		_, err := writer.Write(append(data, '\n'))
		written <- err
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-written:
		if err != nil {
			return fmt.Errorf("write mcp request: %w", err)
		}
		return nil
	}
}

func readJSONLine(ctx context.Context, server *Server) ([]byte, error) {
	type readResult struct {
		line []byte
		err  error
	}
	ch := make(chan readResult, 1)
	reader := server.stdout
	go func() {
		var line []byte
		for {
			part, err := reader.ReadSlice('\n')
			if len(line)+len(part) > 8<<20 {
				ch <- readResult{err: errors.New("mcp protocol response exceeds the frame limit")}
				return
			}
			line = append(line, part...)
			if err != bufio.ErrBufferFull {
				ch <- readResult{line: line, err: err}
				return
			}
		}
	}()
	select {
	case <-ctx.Done():
		server.poisoned = true
		return nil, ctx.Err()
	case result := <-ch:
		if result.err != nil {
			return nil, fmt.Errorf("read mcp response: %w", result.err)
		}
		return result.line, nil
	}
}

func readConfigFile(path string) ([]byte, string, error) {
	if path == "" {
		return nil, "", nil
	}
	resolved := path
	if !filepath.IsAbs(resolved) {
		abs, err := filepath.Abs(resolved)
		if err == nil {
			resolved = abs
		}
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, resolved, nil
		}
		return nil, resolved, fmt.Errorf("read mcp config %s: %w", resolved, err)
	}
	return data, resolved, nil
}
