package sandbox

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

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
