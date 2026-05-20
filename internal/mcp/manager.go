package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

func (m *Manager) Start(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	server, ok := m.servers[name]
	if !ok {
		return errors.New("mcp server not registered")
	}
	if server.Config.Command == "" {
		err := errors.New("mcp server command is empty")
		server.State = ServerStopped
		server.LastError = err.Error()
		return err
	}
	server.State = ServerRunning
	server.StartedAt = time.Now()
	server.LastError = ""
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
	return out
}

func (m *Manager) Snapshot() []Server {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Server, 0, len(m.servers))
	for _, server := range m.servers {
		out = append(out, *server)
	}
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
