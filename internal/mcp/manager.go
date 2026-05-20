package mcp

import (
	"context"
	"errors"
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

func NewManager() *Manager {
	return &Manager{
		servers: make(map[string]*Server),
		tools:   make(map[string]ToolDefinition),
	}
}

func (m *Manager) RegisterServer(cfg ServerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.servers[cfg.Name] = &Server{Config: cfg, State: ServerStopped}
}

func (m *Manager) Start(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	server, ok := m.servers[name]
	if !ok {
		return errors.New("mcp server not registered")
	}
	server.State = ServerRunning
	server.StartedAt = time.Now()
	server.LastError = ""
	return nil
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
