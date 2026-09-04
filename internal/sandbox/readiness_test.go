package sandbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type readinessTestRunner struct {
	runs atomic.Int32
}

func (r *readinessTestRunner) Run(_ context.Context, _ Request) (Result, error) {
	r.runs.Add(1)
	return Result{Output: "sandbox-ready", ExitCode: 0}, nil
}

func TestRoutingRunnerRetriesReadinessDuringRun(t *testing.T) {
	root := t.TempDir()
	var probes atomic.Int32
	probe := func(_ context.Context, _ time.Duration, binary string, _ ...string) bool {
		if binary != "docker" {
			return false
		}
		return probes.Add(1) >= 2
	}
	testRunner := &readinessTestRunner{}
	config := Config{
		Backend:               BackendDocker,
		TrustRoot:             root,
		ProbeAttempts:         1,
		ProbeTimeout:          10 * time.Millisecond,
		ProbeRetryDelay:       time.Millisecond,
		ProbeReadinessTimeout: time.Second,
	}
	initial := detectAvailabilityWithRetry(context.Background(), config, "linux", probe)
	if initial.Docker {
		t.Fatal("initial probe unexpectedly reported Docker ready")
	}
	runner := newRoutingRunner(config, "linux", initial, probe, func(Config, string, Backend) (Runner, error) {
		return testRunner, nil
	})

	result, err := runner.Run(context.Background(), Request{Workspace: root, WorkingDir: root, Command: "printf ok"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "sandbox-ready" || result.ExitCode != 0 {
		t.Fatalf("Run() result = %+v, want sandbox result", result)
	}
	if got := probes.Load(); got != 2 {
		t.Fatalf("readiness probes = %d, want initial failure plus Run retry", got)
	}
	if got := testRunner.runs.Load(); got != 1 {
		t.Fatalf("isolated runner calls = %d, want 1", got)
	}
	if got := runner.Backend(); got != BackendDocker {
		t.Fatalf("backend after readiness recovery = %q, want docker", got)
	}
}

func TestRoutingRunnerCancellationFailsClosedBeforeReadinessProbe(t *testing.T) {
	root := t.TempDir()
	var probes atomic.Int32
	probe := func(_ context.Context, _ time.Duration, _ string, _ ...string) bool {
		probes.Add(1)
		return true
	}
	runner := newRoutingRunner(Config{
		Backend:               BackendDocker,
		TrustRoot:             root,
		ProbeAttempts:         1,
		ProbeReadinessTimeout: time.Second,
	}, "linux", Availability{}, probe, func(Config, string, Backend) (Runner, error) {
		return &readinessTestRunner{}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := runner.Run(ctx, Request{Workspace: root, WorkingDir: root, Command: "printf no"})
	if err == nil || !errors.Is(err, ErrSandboxUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Run() error = %v, want sandbox unavailable wrapping context canceled", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("canceled Run() result = %+v, want fail-closed exit code", result)
	}
	if got := probes.Load(); got != 0 {
		t.Fatalf("readiness probes after canceled context = %d, want 0", got)
	}
}

func TestRoutingRunnerConcurrentRunsShareOneReadinessSelection(t *testing.T) {
	root := t.TempDir()
	var probes atomic.Int32
	var selections atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	probe := func(_ context.Context, _ time.Duration, binary string, _ ...string) bool {
		if binary != "docker" {
			return false
		}
		probes.Add(1)
		startOnce.Do(func() { close(started) })
		<-release
		return true
	}
	testRunner := &readinessTestRunner{}
	runner := newRoutingRunner(Config{
		Backend:               BackendDocker,
		TrustRoot:             root,
		ProbeAttempts:         1,
		ProbeReadinessTimeout: time.Second,
	}, "linux", Availability{}, probe, func(Config, string, Backend) (Runner, error) {
		selections.Add(1)
		return testRunner, nil
	})

	const concurrentRuns = 8
	errs := make(chan error, concurrentRuns)
	var wg sync.WaitGroup
	for i := 0; i < concurrentRuns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := runner.Run(context.Background(), Request{Workspace: root, WorkingDir: root, Command: "printf ok"})
			errs <- err
		}()
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("concurrent Run calls did not start readiness selection")
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Run() error = %v", err)
		}
	}
	if got := probes.Load(); got != 1 {
		t.Fatalf("readiness probes = %d, want one shared probe", got)
	}
	if got := selections.Load(); got != 1 {
		t.Fatalf("runner selections = %d, want one shared selection", got)
	}
	if got := testRunner.runs.Load(); got != concurrentRuns {
		t.Fatalf("isolated runner calls = %d, want %d", got, concurrentRuns)
	}
}

func TestDetectAvailabilityRetriesTransientDockerReadiness(t *testing.T) {
	var calls atomic.Int32
	probe := func(_ context.Context, _ time.Duration, binary string, _ ...string) bool {
		if binary != "docker" {
			return false
		}
		return calls.Add(1) >= 3
	}

	availability := detectAvailabilityWithRetry(context.Background(), Config{
		Backend:         BackendDocker,
		ProbeTimeout:    10 * time.Millisecond,
		ProbeAttempts:   3,
		ProbeRetryDelay: 1 * time.Millisecond,
	}, "linux", probe)
	if !availability.Docker {
		t.Fatalf("Docker should become available after transient probe failures: %+v", availability)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("probe calls = %d, want 3", got)
	}
}

func TestDetectAvailabilityPublicContractRetriesTransientReadiness(t *testing.T) {
	var calls atomic.Int32
	probe := func(_ context.Context, _ time.Duration, binary string, _ ...string) bool {
		if binary != "docker" {
			return false
		}
		return calls.Add(1) >= 2
	}

	availability := detectAvailability(context.Background(), Config{
		Backend:         BackendDocker,
		ProbeTimeout:    10 * time.Millisecond,
		ProbeAttempts:   2,
		ProbeRetryDelay: 1 * time.Millisecond,
	}, "linux", probe)
	if !availability.Docker {
		t.Fatalf("public availability contract should recover transient Docker readiness: %+v", availability)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("public availability probe calls = %d, want 2", got)
	}
}

func TestDetectAvailabilityStopsRetryingWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	probe := func(_ context.Context, _ time.Duration, _ string, _ ...string) bool {
		calls.Add(1)
		return false
	}

	availability := detectAvailabilityWithRetry(ctx, Config{
		Backend:         BackendDocker,
		ProbeTimeout:    10 * time.Millisecond,
		ProbeAttempts:   5,
		ProbeRetryDelay: 1 * time.Millisecond,
	}, "linux", probe)
	if availability.Docker {
		t.Fatal("canceled probe unexpectedly reported Docker available")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe calls after canceled context = %d, want 1", got)
	}
}

func TestDetectAvailabilityExplicitDockerDoesNotAcceptWSLReadiness(t *testing.T) {
	var dockerCalls atomic.Int32
	var wslCalls atomic.Int32
	probe := func(_ context.Context, _ time.Duration, binary string, _ ...string) bool {
		switch binary {
		case "docker.exe":
			dockerCalls.Add(1)
			return false
		case "wsl.exe":
			wslCalls.Add(1)
			return true
		default:
			return false
		}
	}

	availability := detectAvailabilityWithRetry(context.Background(), Config{
		Backend:         BackendDocker,
		ProbeTimeout:    10 * time.Millisecond,
		ProbeAttempts:   3,
		ProbeRetryDelay: 1 * time.Millisecond,
	}, "windows", probe)
	if availability.Docker {
		t.Fatal("Docker should remain unavailable")
	}
	if !availability.WSL2 {
		t.Fatal("test probe should report WSL2 readiness")
	}
	if got := dockerCalls.Load(); got != 3 {
		t.Fatalf("Docker probe calls = %d, want 3", got)
	}
	if got := wslCalls.Load(); got != 3 {
		t.Fatalf("WSL2 probe calls = %d, want 3", got)
	}
}

func TestDetectAvailabilityHonorsTotalReadinessBudget(t *testing.T) {
	var calls atomic.Int32
	started := time.Now()
	availability := detectAvailabilityWithRetry(context.Background(), Config{
		Backend:               BackendDocker,
		ProbeTimeout:          time.Second,
		ProbeAttempts:         100,
		ProbeRetryDelay:       20 * time.Millisecond,
		ProbeReadinessTimeout: 25 * time.Millisecond,
	}, "linux", func(_ context.Context, _ time.Duration, binary string, _ ...string) bool {
		if binary == "docker" {
			calls.Add(1)
		}
		return false
	})
	if availability.Docker {
		t.Fatal("budget-limited readiness unexpectedly reported Docker")
	}
	if got := calls.Load(); got < 1 || got > 2 {
		t.Fatalf("readiness probes within budget = %d, want one or two", got)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("readiness retry exceeded bounded test budget: %s", elapsed)
	}
}
