package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type ProcessConfig struct {
	Address        string
	AutoStart      bool
	Command        string
	Args           []string
	ProjectRoot    string
	WorkingDir     string
	MemoryDir      string
	MaxTokens      int
	MaxCost        float64
	StartupTimeout time.Duration
}

type ProcessManager struct {
	cfg     ProcessConfig
	client  *Client
	process *exec.Cmd
	owned   bool
}

func NewProcessManager(cfg ProcessConfig) *ProcessManager {
	if strings.TrimSpace(cfg.Address) == "" {
		cfg.Address = "127.0.0.1:50051"
	}
	if strings.TrimSpace(cfg.Command) == "" {
		cfg.Command = "python"
	}
	if len(cfg.Args) == 0 {
		cfg.Args = []string{"-m", "orchestrator.server"}
	}
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = 5 * time.Second
	}
	return &ProcessManager{cfg: cfg}
}

func (m *ProcessManager) Client(ctx context.Context) (*Client, error) {
	if m.client != nil {
		return m.client, nil
	}

	client, err := NewClient(m.cfg.Address)
	if err != nil {
		return nil, err
	}
	if healthy(ctx, client) {
		m.client = client
		return client, nil
	}
	_ = client.Close()

	if !m.cfg.AutoStart {
		return nil, errors.New("orchestrator is not reachable and auto-start is disabled")
	}
	if err := m.start(ctx); err != nil {
		return nil, err
	}

	client, err = NewClient(m.cfg.Address)
	if err != nil {
		m.Stop()
		return nil, err
	}
	if err := m.waitUntilHealthy(ctx, client); err != nil {
		_ = client.Close()
		m.Stop()
		return nil, err
	}
	m.client = client
	return client, nil
}

func (m *ProcessManager) Stop() {
	if m.client != nil {
		_ = m.client.Close()
		m.client = nil
	}
	if !m.owned || m.process == nil || m.process.Process == nil {
		return
	}
	_ = m.process.Process.Kill()
	_, _ = m.process.Process.Wait()
	m.process = nil
	m.owned = false
}

func (m *ProcessManager) Owned() bool {
	return m.owned
}

func (m *ProcessManager) start(ctx context.Context) error {
	args := append([]string{}, m.cfg.Args...)
	host, port := splitAddress(m.cfg.Address)
	args = append(args,
		"--host", host,
		"--port", port,
		"--project-root", m.cfg.ProjectRoot,
		"--working-dir", m.cfg.WorkingDir,
		"--memory-dir", m.cfg.MemoryDir,
	)
	if m.cfg.MaxTokens > 0 {
		args = append(args, "--max-tokens", strconv.Itoa(m.cfg.MaxTokens))
	}
	if m.cfg.MaxCost > 0 {
		args = append(args, "--max-cost", strconv.FormatFloat(m.cfg.MaxCost, 'f', -1, 64))
	}

	cmd := exec.CommandContext(ctx, m.cfg.Command, args...)
	cmd.Dir = m.cfg.ProjectRoot
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start orchestrator: %w", err)
	}
	m.process = cmd
	m.owned = true
	return nil
}

func (m *ProcessManager) waitUntilHealthy(ctx context.Context, client *Client) error {
	deadline := time.Now().Add(m.cfg.StartupTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if healthy(ctx, client) {
			return nil
		}
		_, lastErr = client.Health(ctx)
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return fmt.Errorf("orchestrator did not become healthy: %w", lastErr)
	}
	return errors.New("orchestrator did not become healthy")
}

func healthy(ctx context.Context, client *Client) bool {
	response, err := client.Health(ctx)
	return err == nil && response != nil && strings.EqualFold(response.Status, "ok")
}

func splitAddress(address string) (string, string) {
	host, port, err := net.SplitHostPort(address)
	if err == nil {
		if host == "" {
			host = "127.0.0.1"
		}
		return host, port
	}
	if strings.Contains(address, ":") {
		parts := strings.Split(address, ":")
		return strings.Join(parts[:len(parts)-1], ":"), parts[len(parts)-1]
	}
	return "127.0.0.1", "50051"
}
