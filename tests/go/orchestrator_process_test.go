package codeagent_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestProcessManagerUsesExistingHealthyServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(server, &testOrchestratorServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address:             listener.Addr().String(),
		AutoStart:           true,
		Command:             "definitely-not-used",
		StartupTimeout:      200 * time.Millisecond,
		ConversationTimeout: 2 * time.Minute,
	})
	defer manager.Stop()

	client, err := manager.Client(context.Background())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if client == nil {
		t.Fatal("expected client")
	}
	if manager.Owned() {
		t.Fatal("manager should not own an externally healthy server")
	}
	if client.ConversationTimeout() != 2*time.Minute {
		t.Fatalf("unexpected conversation timeout: %s", client.ConversationTimeout())
	}
}

func TestProcessManagerReportsDisabledAutoStart(t *testing.T) {
	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address:        "127.0.0.1:1",
		AutoStart:      false,
		StartupTimeout: 100 * time.Millisecond,
	})
	defer manager.Stop()

	_, err := manager.Client(context.Background())
	if err == nil {
		t.Fatal("expected error when auto-start is disabled")
	}
}

// fakeServerProcess is a fake orchestrator process coupled to an in-process
// gRPC server, mirroring production where the process IS the server. It lets
// the supervisor's restart state machine be exercised without spawning real
// subprocesses. Each instance binds the given address (retrying briefly while a
// previous instance's port is being released), and reports its exit through the
// ManagedProcess interface.
type fakeServerProcess struct {
	server *grpc.Server
	exit   chan error
	killed atomic.Bool
}

func newFakeServerProcess(addr string) (*fakeServerProcess, error) {
	listener, err := listenWithRetry(addr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	srv := grpc.NewServer()
	codeagentpb.RegisterOrchestratorServer(srv, &testOrchestratorServer{})
	go func() { _ = srv.Serve(listener) }()
	return &fakeServerProcess{server: srv, exit: make(chan error, 1)}, nil
}

func (p *fakeServerProcess) Wait() error {
	return <-p.exit
}

func (p *fakeServerProcess) Kill() error {
	p.killed.Store(true)
	p.terminate(errors.New("killed"))
	return nil
}

// crash simulates an unexpected orchestrator exit: it stops the server (so the
// manager's health probe fails) and signals the waiter goroutine.
func (p *fakeServerProcess) crash() {
	p.terminate(errors.New("segfault"))
}

func (p *fakeServerProcess) terminate(err error) {
	if p == nil || p.server == nil {
		return
	}
	p.server.Stop()
	if err == nil {
		err = errors.New("exited")
	}
	select {
	case p.exit <- err:
	default:
	}
}

func listenWithRetry(addr string, timeout time.Duration) (net.Listener, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			return listener, nil
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("timed out binding address")
	}
	return nil, lastErr
}

func freePortAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return addr
}

func waitFor(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return condition()
}

func assertClientHealthy(t *testing.T, client *orchestrator.Client, msg string) {
	t.Helper()
	resp, err := client.Health(context.Background())
	if err != nil || resp == nil || !strings.EqualFold(resp.GetStatus(), "ok") {
		t.Fatalf("%s: client not healthy (resp=%v, err=%v)", msg, resp, err)
	}
}

// TestProcessManagerMonitorRestartsOnCrash verifies the supervisor (Monitor)
// detects an unexpected orchestrator process exit, relaunches the process, and
// leaves Client() able to return a fresh healthy connection.
func TestProcessManagerMonitorRestartsOnCrash(t *testing.T) {
	addr := freePortAddr(t)
	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address:             addr,
		AutoStart:           true,
		Command:             "unused-by-fake-starter",
		StartupTimeout:      2 * time.Second,
		ConversationTimeout: time.Minute,
	})
	defer manager.Stop()

	var starts atomic.Int64
	var current atomic.Value // *fakeServerProcess
	manager.SetProcessStarter(func(ctx context.Context) (orchestrator.ManagedProcess, error) {
		proc, err := newFakeServerProcess(addr)
		if err != nil {
			return nil, err
		}
		current.Store(proc)
		starts.Add(1)
		return proc, nil
	})

	monitorCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Monitor(monitorCtx)

	// No server is running yet, so the first Client() must launch the process.
	client1, err := manager.Client(context.Background())
	if err != nil {
		t.Fatalf("initial client: %v", err)
	}
	if !waitFor(time.Second, func() bool { return starts.Load() >= 1 }) {
		t.Fatalf("starter not invoked, starts=%d", starts.Load())
	}
	if !manager.Owned() {
		t.Fatal("expected manager to own the launched process")
	}
	assertClientHealthy(t, client1, "initial client")

	// Snapshot and crash the running process.
	proc1, _ := current.Load().(*fakeServerProcess)
	if proc1 == nil {
		t.Fatal("no running process captured")
	}
	proc1.crash()

	// Monitor should relaunch the orchestrator.
	if !waitFor(5*time.Second, func() bool { return starts.Load() >= 2 }) {
		t.Fatalf("monitor did not restart the process, starts=%d", starts.Load())
	}

	// Client() must hand out the fresh, healthy connection.
	client2, err := manager.Client(context.Background())
	if err != nil {
		t.Fatalf("post-crash client: %v", err)
	}
	assertClientHealthy(t, client2, "post-crash client")
	if client2 == client1 {
		t.Fatal("expected a fresh client after restart")
	}
}

// TestProcessManagerRestartRelaunchesProcess verifies the explicit Restart()
// recovery path used by the harness: it kills the owned process (stopping its
// server) and relaunches, returning a fresh healthy client.
func TestProcessManagerRestartRelaunchesProcess(t *testing.T) {
	addr := freePortAddr(t)
	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address:             addr,
		AutoStart:           true,
		Command:             "unused-by-fake-starter",
		StartupTimeout:      2 * time.Second,
		ConversationTimeout: time.Minute,
	})
	defer manager.Stop()

	var starts atomic.Int64
	var current atomic.Value // *fakeServerProcess
	manager.SetProcessStarter(func(ctx context.Context) (orchestrator.ManagedProcess, error) {
		proc, err := newFakeServerProcess(addr)
		if err != nil {
			return nil, err
		}
		current.Store(proc)
		starts.Add(1)
		return proc, nil
	})

	client1, err := manager.Client(context.Background())
	if err != nil {
		t.Fatalf("initial client: %v", err)
	}
	if !waitFor(time.Second, func() bool { return starts.Load() == 1 }) {
		t.Fatalf("expected exactly 1 start, got %d", starts.Load())
	}
	proc1, _ := current.Load().(*fakeServerProcess)
	if proc1 == nil {
		t.Fatal("no running process captured")
	}

	client2, err := manager.Restart(context.Background())
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if !waitFor(2*time.Second, func() bool { return proc1.killed.Load() }) {
		t.Fatal("old process was not killed during restart")
	}
	if !waitFor(2*time.Second, func() bool { return starts.Load() >= 2 }) {
		t.Fatalf("process was not relaunched, starts=%d", starts.Load())
	}
	assertClientHealthy(t, client2, "restarted client")
	if client2 == client1 {
		t.Fatal("expected a fresh client after restart")
	}
}

func TestNewClientUsesLongCodeTaskDefaultTimeout(t *testing.T) {
	client, err := orchestrator.NewClient("127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()
	if got, want := client.ConversationTimeout(), 30*time.Minute; got != want {
		t.Fatalf("ConversationTimeout() = %s, want %s", got, want)
	}
}

// TestIsConnectionErrorClassification locks in the heuristic the harness uses
// to decide whether to retry a turn after restarting the orchestrator.
func TestIsConnectionErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain", errors.New("something else"), false},
		{"connection refused", errors.New("dial tcp: connection refused"), true},
		{"rpc error", errors.New("rpc error: code = Unavailable desc = transport"), true},
		{"provider authentication failure", status.Error(codes.Unknown, "Exception iterating responses: OpenAI HTTP 401: invalid api key"), false},
		{"provider permission failure", status.Error(codes.PermissionDenied, "provider rejected request"), false},
		{"eof", errors.New("unexpected EOF"), true},
		{"send user input", errors.New("send user input: read: connection reset"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := orchestrator.IsConnectionError(tc.err); got != tc.want {
				t.Fatalf("IsConnectionError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestConversationTransportErrorClassification(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		deadline bool
		canceled bool
	}{
		{name: "context deadline", err: context.DeadlineExceeded, deadline: true},
		{name: "grpc deadline", err: status.Error(codes.DeadlineExceeded, "context deadline exceeded"), deadline: true},
		{name: "wrapped grpc deadline", err: fmt.Errorf("receive orchestrator message: %w", status.Error(codes.DeadlineExceeded, "context deadline exceeded")), deadline: true},
		{name: "context canceled", err: context.Canceled, canceled: true},
		{name: "grpc canceled", err: status.Error(codes.Canceled, "context canceled"), canceled: true},
		{name: "runtime", err: errors.New("provider failed"), deadline: false, canceled: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := orchestrator.IsDeadlineError(tc.err); got != tc.deadline {
				t.Fatalf("IsDeadlineError(%v) = %v, want %v", tc.err, got, tc.deadline)
			}
			if got := orchestrator.IsCanceledError(tc.err); got != tc.canceled {
				t.Fatalf("IsCanceledError(%v) = %v, want %v", tc.err, got, tc.canceled)
			}
		})
	}
}
