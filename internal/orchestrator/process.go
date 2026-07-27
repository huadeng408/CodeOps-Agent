package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ProcessConfig struct {
	Address             string
	AutoStart           bool
	Command             string
	Args                []string
	ProjectRoot         string
	WorkingDir          string
	MemoryDir           string
	MaxTokens           int
	MaxCost             float64
	ModelFast           string
	StartupTimeout      time.Duration
	ConversationTimeout time.Duration
}

// ManagedProcess abstracts the orchestrator subprocess so the supervisor can
// restart it on crash and so the restart state machine can be unit-tested
// without spawning real OS processes. The default implementation wraps an
// *exec.Cmd; tests may inject their own via ProcessManager.SetProcessStarter.
type ManagedProcess interface {
	// Wait blocks until the process exits and returns its wait error.
	Wait() error
	// Kill asks the process to terminate immediately.
	Kill() error
}

// ProcessStarter launches one orchestrator process instance. It is invoked
// every time the ProcessManager starts or restarts the orchestrator.
type ProcessStarter func(ctx context.Context) (ManagedProcess, error)

type ProcessManager struct {
	cfg     ProcessConfig
	mu      sync.Mutex
	client  *Client
	process ManagedProcess
	exitCh  chan error // receives the exit error of the current process
	owned   bool
	stopping bool

	// starter launches a fresh orchestrator process. Defaults to the
	// exec-based launcher; overridable for tests.
	starter ProcessStarter

	// monitorOn guards against concurrent Monitor loops.
	monitorOn atomic.Bool
}

// Tunables for the supervisor loop. Kept as package vars so tests can tighten
// them if needed.
var (
	supervisorPollInterval = 100 * time.Millisecond
	initialRestartBackoff  = 250 * time.Millisecond
	maxRestartBackoff      = 2 * time.Second
	maxRestartAttempts     = 5
	restartReapTimeout     = 2 * time.Second
)

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
	if cfg.ConversationTimeout <= 0 {
		cfg.ConversationTimeout = 5 * time.Minute
	}
	m := &ProcessManager{cfg: cfg}
	m.starter = defaultProcessStarter(cfg)
	return m
}

// SetProcessStarter overrides the function used to launch the orchestrator
// process. It is intended for tests that inject a fake process.
func (m *ProcessManager) SetProcessStarter(starter ProcessStarter) {
	if starter != nil {
		m.starter = starter
	}
}

// Client returns a healthy orchestrator client, reusing the cached connection
// when it is still live. A connection left stale by a crashed orchestrator is
// detected (via a health probe) and transparently re-dialed, relaunching the
// subprocess when auto-start is enabled.
func (m *ProcessManager) Client(ctx context.Context) (*Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopping {
		return nil, errors.New("orchestrator process manager is stopping")
	}

	// Re-validate the cached connection: a crashed orchestrator leaves it stale.
	if m.client != nil {
		if healthy(ctx, m.client) {
			return m.client, nil
		}
		_ = m.client.Close()
		m.client = nil
	}

	return m.acquireLocked(ctx)
}

// acquireLocked returns a healthy client, dialing an already-running server or
// (when auto-start is enabled) launching the orchestrator process. The caller
// must hold m.mu.
func (m *ProcessManager) acquireLocked(ctx context.Context) (*Client, error) {
	client, err := NewClient(m.cfg.Address)
	if err != nil {
		return nil, err
	}
	client.SetConversationTimeout(m.cfg.ConversationTimeout)
	if healthy(ctx, client) {
		m.client = client
		return client, nil
	}
	_ = client.Close()

	if !m.cfg.AutoStart {
		return nil, errors.New("orchestrator is not reachable and auto-start is disabled")
	}
	return m.ensureStartedLocked(ctx)
}

// ensureStartedLocked launches the orchestrator process, dials it, waits until
// it is healthy, and caches the resulting client. The caller must hold m.mu.
func (m *ProcessManager) ensureStartedLocked(ctx context.Context) (*Client, error) {
	proc, exit, err := m.launchLocked(ctx)
	if err != nil {
		return nil, err
	}

	client, err := NewClient(m.cfg.Address)
	if err != nil {
		m.reapProcessLocked(proc, exit)
		return nil, err
	}
	client.SetConversationTimeout(m.cfg.ConversationTimeout)
	if err := m.waitUntilHealthy(ctx, client); err != nil {
		_ = client.Close()
		m.reapProcessLocked(proc, exit)
		return nil, err
	}

	m.process = proc
	m.exitCh = exit
	m.owned = true
	m.client = client
	return client, nil
}

// launchLocked invokes the configured starter and spawns a dedicated waiter
// goroutine so the process is always reaped exactly once. The caller must hold
// m.mu (the starter itself is allowed to block briefly).
func (m *ProcessManager) launchLocked(ctx context.Context) (ManagedProcess, chan error, error) {
	proc, err := m.starter(ctx)
	if err != nil {
		return nil, nil, err
	}
	exit := make(chan error, 1)
	go func() {
		exit <- proc.Wait()
	}()
	return proc, exit, nil
}

// reapProcessLocked kills a just-launched process whose dial/health-check
// failed so it does not leak. The exit channel is buffered, so the waiter
// goroutine always completes its send and exits even if nobody drains it.
func (m *ProcessManager) reapProcessLocked(proc ManagedProcess, exit chan error) {
	if proc != nil {
		_ = proc.Kill()
	}
}

// Restart tears down the current connection and (when the manager owns the
// orchestrator process) kills and relaunches the orchestrator, then re-dials.
// It is the recovery entry point used by the harness when a turn fails with a
// connection-level error. Returns a fresh, healthy client.
func (m *ProcessManager) Restart(ctx context.Context) (*Client, error) {
	prevExit := m.teardownForRestart()

	// Wait for the killed process to be reaped by its waiter goroutine.
	// Done outside the lock so Stop()/Monitor are not blocked while reaping.
	if prevExit != nil {
		select {
		case <-prevExit:
		case <-time.After(restartReapTimeout):
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopping {
		return nil, errors.New("orchestrator process manager is stopping")
	}
	return m.acquireLocked(ctx)
}

// teardownForRestart closes the cached client and kills the owned process,
// returning the previous process's exit channel so the caller can wait for it
// to be reaped. Non-owned (external) orchestrators are simply un-cached.
func (m *ProcessManager) teardownForRestart() chan error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.client != nil {
		_ = m.client.Close()
		m.client = nil
	}
	var prevExit chan error
	if m.owned && m.process != nil {
		if err := m.process.Kill(); err != nil {
			log.Printf("[orchestrator] kill during restart failed: %v", err)
		}
		prevExit = m.exitCh
	}
	m.process = nil
	m.exitCh = nil
	return prevExit
}

// Monitor runs the supervisor loop until ctx is cancelled. When the owned
// orchestrator process exits unexpectedly (i.e. while the manager is not
// deliberately stopping), Monitor logs the crash, invalidates the stale gRPC
// client, and relaunches the orchestrator with exponential backoff. At most
// one Monitor loop runs at a time. Monitor is a no-op when the orchestrator is
// not owned (external server).
func (m *ProcessManager) Monitor(ctx context.Context) {
	if !m.monitorOn.CompareAndSwap(false, true) {
		return
	}
	defer m.monitorOn.Store(false)
	m.supervise(ctx)
}

func (m *ProcessManager) supervise(ctx context.Context) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}

		m.mu.Lock()
		stopping := m.stopping
		owned := m.owned
		proc := m.process
		exit := m.exitCh
		m.mu.Unlock()

		if stopping {
			return
		}
		if !owned || proc == nil || exit == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(supervisorPollInterval):
				continue
			}
		}

		select {
		case <-ctx.Done():
			return
		case waitErr := <-exit:
			// process exited; decide whether to treat it as a crash.
			m.mu.Lock()
			if m.stopping {
				m.mu.Unlock()
				return
			}
			// If the process was already replaced (concurrent Restart), ignore.
			if m.process != proc {
				m.mu.Unlock()
				continue
			}
			staleClient := m.client
			m.client = nil
			m.process = nil
			m.exitCh = nil
			m.mu.Unlock()

			if staleClient != nil {
				_ = staleClient.Close()
			}
			log.Printf("[orchestrator] process exited unexpectedly (err=%v); restarting", waitErr)
			if err := m.restartLoop(ctx); err != nil {
				log.Printf("[orchestrator] gave up restarting: %v", err)
				return
			}
		}
	}
}

// restartLoop relaunches the orchestrator with exponential backoff, up to a
// bounded number of attempts. It returns nil once a healthy client is cached.
func (m *ProcessManager) restartLoop(ctx context.Context) error {
	backoff := initialRestartBackoff
	var lastErr error
	for attempt := 1; attempt <= maxRestartAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := m.tryRestart(ctx)
		if err == nil {
			log.Printf("[orchestrator] process restarted on attempt %d/%d", attempt, maxRestartAttempts)
			return nil
		}
		lastErr = err

		m.mu.Lock()
		stopping := m.stopping
		m.mu.Unlock()
		if stopping {
			return nil
		}

		log.Printf("[orchestrator] restart attempt %d/%d failed: %v", attempt, maxRestartAttempts, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < maxRestartBackoff {
			backoff *= 2
		}
	}
	return fmt.Errorf("restart orchestrator after %d attempts: %w", maxRestartAttempts, lastErr)
}

// tryRestart performs a single relaunch attempt. It acquires m.mu for the
// duration of the launch + dial + health-check.
func (m *ProcessManager) tryRestart(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopping {
		return errors.New("process manager is stopping")
	}
	_, err := m.ensureStartedLocked(ctx)
	return err
}

func (m *ProcessManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopping = true
	if m.client != nil {
		_ = m.client.Close()
		m.client = nil
	}
	if m.owned && m.process != nil {
		if err := m.process.Kill(); err != nil {
			log.Printf("[orchestrator] kill during stop failed: %v", err)
		}
		// The waiter goroutine reaps the process via the buffered exit channel.
	}
	m.process = nil
	m.exitCh = nil
	m.owned = false
}

func (m *ProcessManager) Owned() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.owned
}

// defaultProcessStarter returns the production starter that spawns the Python
// orchestrator as a subprocess configured from cfg.
func defaultProcessStarter(cfg ProcessConfig) ProcessStarter {
	// The starter matches the ProcessStarter signature (ctx is accepted but not
	// forwarded): the orchestrator process must outlive any single request, so
	// its lifetime is bound to context.Background() inside buildOrchestratorCmd.
	return func(_ context.Context) (ManagedProcess, error) {
		cmd := buildOrchestratorCmd(cfg)
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("start orchestrator: %w", err)
		}
		return &osProcess{cmd: cmd}, nil
	}
}

func buildOrchestratorCmd(cfg ProcessConfig) *exec.Cmd {
	args := append([]string{}, cfg.Args...)
	host, port := splitAddress(cfg.Address)
	args = append(args,
		"--host", host,
		"--port", port,
		"--project-root", cfg.ProjectRoot,
		"--working-dir", cfg.WorkingDir,
		"--memory-dir", cfg.MemoryDir,
	)
	if cfg.MaxTokens > 0 {
		args = append(args, "--max-tokens", strconv.Itoa(cfg.MaxTokens))
	}
	if cfg.MaxCost > 0 {
		args = append(args, "--max-cost", strconv.FormatFloat(cfg.MaxCost, 'f', -1, 64))
	}

	// Bind the process lifetime to the manager, not to an individual request,
	// so a cancelled request context cannot tear down the orchestrator.
	cmd := exec.CommandContext(context.Background(), cfg.Command, args...)
	cmd.Dir = cfg.ProjectRoot
	if strings.TrimSpace(cfg.ModelFast) != "" {
		cmd.Env = append(os.Environ(), "MODEL_FAST="+strings.TrimSpace(cfg.ModelFast))
	}
	return cmd
}

// osProcess adapts *exec.Cmd to the ManagedProcess interface.
type osProcess struct {
	cmd *exec.Cmd
}

func (p *osProcess) Wait() error {
	if p == nil || p.cmd == nil {
		return errors.New("orchestrator process is nil")
	}
	return p.cmd.Wait()
}

func (p *osProcess) Kill() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
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
