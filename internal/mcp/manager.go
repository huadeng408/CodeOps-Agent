package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ServerState string

const (
	ServerStopped ServerState = "stopped"
	ServerRunning ServerState = "running"
)

type Server struct {
	Config    ServerConfig
	State     ServerState
	LastError string
	StartedAt time.Time
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Reader
	nextID    int64
	requestMu sync.Mutex
}

type Manager struct {
	mu      sync.Mutex
	servers map[string]*Server
	tools   map[string]ToolDefinition
}

type ConfigFile struct {
	Servers map[string]ServerConfig `json:"servers"`
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
	m.servers[cfg.Name] = &Server{Config: cfg, State: ServerStopped}
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
	if !ok {
		m.mu.Unlock()
		return errors.New("mcp server not registered")
	}
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

	m.mu.Lock()
	server.State = ServerRunning
	server.StartedAt = time.Now()
	server.LastError = ""
	for _, tool := range tools {
		tool.Server = server.Config.Name
		m.tools[tool.Name] = tool
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

func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	server, ok := m.servers[name]
	if !ok {
		return errors.New("mcp server not registered")
	}
	if err := stopServerProcess(server); err != nil {
		return err
	}
	server.State = ServerStopped
	return nil
}

func (m *Manager) ListServers() []ServerConfig {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]ServerConfig, 0, len(m.servers))
	for _, server := range m.servers {
		out = append(out, server.Config)
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
			Config:    server.Config,
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

func (m *Manager) RegisterTool(tool ToolDefinition) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.tools[tool.Name] = tool
}

func (m *Manager) ResolveTool(name string) (ToolDefinition, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tool, ok := m.tools[name]
	return tool, ok
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

func (m *Manager) WriteToolsManifest(path string) error {
	m.mu.Lock()
	tools := make([]ToolDefinition, 0, len(m.tools))
	for _, tool := range m.tools {
		tools = append(tools, tool)
	}
	m.mu.Unlock()
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create mcp manifest dir: %w", err)
	}
	payload := struct {
		Tools []ToolDefinition `json:"tools"`
	}{Tools: tools}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode mcp manifest: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write mcp manifest: %w", err)
	}
	return nil
}

func (m *Manager) CallTool(ctx context.Context, name string, arguments map[string]any) (ToolCallResult, error) {
	m.mu.Lock()
	tool, ok := m.tools[name]
	if !ok {
		m.mu.Unlock()
		return ToolCallResult{}, errors.New("mcp tool not registered")
	}
	server, ok := m.servers[tool.Server]
	if !ok || server.State != ServerRunning {
		m.mu.Unlock()
		return ToolCallResult{}, errors.New("mcp server not running")
	}
	m.mu.Unlock()

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

func startServerProcess(ctx context.Context, server *Server, cfg ServerConfig) error {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	if cfg.WorkingDir != "" {
		cmd.Dir = cfg.WorkingDir
	}
	if len(cfg.Env) > 0 {
		env := os.Environ()
		for key, value := range cfg.Env {
			env = append(env, key+"="+value)
		}
		cmd.Env = env
	}
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

func stopServerProcess(server *Server) error {
	if server.stdin != nil {
		_ = server.stdin.Close()
	}
	if server.cmd != nil && server.cmd.Process != nil {
		_ = server.cmd.Process.Kill()
		_, _ = server.cmd.Process.Wait()
	}
	server.cmd = nil
	server.stdin = nil
	server.stdout = nil
	return nil
}

func initializeServer(ctx context.Context, server *Server) error {
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "code-agent",
			"version": "0.1.0",
		},
	}
	if _, err := sendRequest(ctx, server, "initialize", params); err != nil {
		return err
	}
	return sendNotification(ctx, server, "notifications/initialized", map[string]any{})
}

func listServerTools(ctx context.Context, server *Server) ([]ToolDefinition, error) {
	raw, err := sendRequest(ctx, server, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tools []ToolDefinition `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode tools/list result: %w", err)
	}
	return payload.Tools, nil
}

func sendRequest(ctx context.Context, server *Server, method string, params any) (json.RawMessage, error) {
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

	server.requestMu.Lock()
	defer server.requestMu.Unlock()
	if err := writeJSONLine(ctx, server.stdin, data); err != nil {
		return nil, err
	}
	for {
		line, err := readJSONLine(ctx, server.stdout)
		if err != nil {
			return nil, err
		}
		var response Response
		if err := json.Unmarshal(line, &response); err != nil {
			continue
		}
		if fmt.Sprint(response.ID) != fmt.Sprint(id) {
			continue
		}
		if response.Error != nil {
			return nil, errors.New(response.Error.Message)
		}
		return response.Result, nil
	}
}

func sendNotification(ctx context.Context, server *Server, method string, params any) error {
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

	server.requestMu.Lock()
	defer server.requestMu.Unlock()
	return writeJSONLine(ctx, server.stdin, data)
}

func writeJSONLine(ctx context.Context, writer io.Writer, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := writer.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write mcp request: %w", err)
	}
	return nil
}

func readJSONLine(ctx context.Context, reader *bufio.Reader) ([]byte, error) {
	type readResult struct {
		line []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := reader.ReadBytes('\n')
		ch <- readResult{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
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
