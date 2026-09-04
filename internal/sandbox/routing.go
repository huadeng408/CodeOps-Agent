package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const defaultProbeTimeout = 5 * time.Second
const defaultProbeAttempts = 5
const defaultProbeRetryDelay = 500 * time.Millisecond

var (
	// ErrSandboxUnavailable is returned when no configured isolated backend can run.
	ErrSandboxUnavailable = errors.New("sandbox unavailable")
	// ErrTrustRootRequired is returned when a request has no trusted workspace root.
	ErrTrustRootRequired = errors.New("sandbox trust root is required")
)

// RoutingRunner selects exactly one isolated backend for the lifetime of an
// executor. It never falls back to an unconfined host process.
type RoutingRunner struct {
	selected     Runner
	backend      Backend
	trustRoot    string
	selectionErr error
}

// Backend reports the selected isolation backend. "unavailable" means every
// candidate was rejected and Run will fail closed.
func (r *RoutingRunner) Backend() Backend {
	if r == nil || r.backend == "" {
		return backendMissing
	}
	return r.backend
}

func (r *RoutingRunner) Run(ctx context.Context, request Request) (Result, error) {
	if r == nil {
		return Result{ExitCode: 126}, fmt.Errorf("%w: nil runner", ErrSandboxUnavailable)
	}
	if r.selectionErr != nil {
		return Result{ExitCode: 126}, fmt.Errorf("%w: %v", ErrSandboxUnavailable, r.selectionErr)
	}
	if err := validateRequestTrustRoot(request, r.trustRoot, r.selected); err != nil {
		return Result{ExitCode: 126}, err
	}
	return r.selected.Run(ctx, request)
}

// SelectRunner chooses a backend from a previously completed availability
// probe. It is deterministic and does not start a process.
func SelectRunner(config Config, platform string, availability Availability) (*RoutingRunner, error) {
	config = normalizeConfig(config)
	if strings.TrimSpace(config.TrustRoot) == "" {
		return nil, ErrTrustRootRequired
	}
	backend, err := chooseBackend(config.Backend, platform, availability)
	if err != nil {
		return nil, err
	}
	var selected Runner
	switch backend {
	case BackendDocker:
		selected = newDockerRunner(config, platform, osCommandExecutor{})
	case BackendWSL2:
		selected = newWSLRunner(config)
	default:
		return nil, fmt.Errorf("sandbox backend %q has no isolated implementation", backend)
	}
	return &RoutingRunner{selected: selected, backend: backend, trustRoot: config.TrustRoot}, nil
}

// NewRoutingRunner is the deterministic constructor used by the App and tests.
// Selection failures are represented by a rejecting runner so a caller cannot
// accidentally continue on the host.
func NewRoutingRunner(config Config, platform string, availability Availability) *RoutingRunner {
	runner, err := SelectRunner(config, platform, availability)
	if err != nil {
		return &RoutingRunner{backend: backendMissing, selectionErr: err, trustRoot: config.TrustRoot}
	}
	return runner
}

// NewSandboxRunner probes the configured platform with bounded readiness
// retries and returns a runner. A failed probe is a normal fail-closed state,
// not permission to use host execution.
func NewSandboxRunner(config Config) *RoutingRunner {
	config = normalizeConfig(config)
	availability := detectAvailabilityWithRetry(context.Background(), config, runtime.GOOS, probeCommand)
	return NewRoutingRunner(config, runtime.GOOS, availability)
}

// DetectAvailability performs bounded, output-discarding health probes for
// Docker and WSL2. Native execution is deliberately never considered isolated.
func DetectAvailability(ctx context.Context, config Config, platform string) Availability {
	config = normalizeConfig(config)
	return detectAvailabilityOnce(ctx, config, platform, probeCommand)
}

type availabilityProbe func(context.Context, time.Duration, string, ...string) bool

func detectAvailabilityWithRetry(ctx context.Context, config Config, platform string, probe availabilityProbe) Availability {
	config = normalizeConfig(config)
	attempts := config.ProbeAttempts
	if attempts <= 0 {
		attempts = defaultProbeAttempts
	}
	delay := config.ProbeRetryDelay
	if delay <= 0 {
		delay = defaultProbeRetryDelay
	}
	var availability Availability
	for attempt := 0; attempt < attempts; attempt++ {
		availability = detectAvailabilityOnce(ctx, config, platform, probe)
		if backendReady(config.Backend, platform, availability) || attempt == attempts-1 {
			return availability
		}
		wait := delay
		for i := 0; i < attempt; i++ {
			if wait >= 30*time.Second {
				wait = 30 * time.Second
				break
			}
			wait *= 2
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return availability
		case <-timer.C:
		}
	}
	return availability
}

func backendReady(requested Backend, platform string, availability Availability) bool {
	switch requested {
	case BackendDocker:
		return availability.Docker
	case BackendWSL2:
		return strings.EqualFold(strings.TrimSpace(platform), "windows") && availability.WSL2
	case BackendAuto, "":
		return availability.Docker || (strings.EqualFold(strings.TrimSpace(platform), "windows") && availability.WSL2)
	default:
		return false
	}
}

func detectAvailabilityOnce(ctx context.Context, config Config, platform string, probe availabilityProbe) Availability {
	timeout := config.ProbeTimeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	result := Availability{}
	dockerBinary := "docker"
	wslBinary := "wsl"
	if strings.EqualFold(strings.TrimSpace(platform), "windows") {
		dockerBinary = "docker.exe"
		wslBinary = "wsl.exe"
	}
	result.Docker = probe(ctx, timeout, dockerBinary, "version", "--format", "{{.Server.Version}}")
	if strings.EqualFold(strings.TrimSpace(platform), "windows") {
		result.WSL2 = probe(ctx, timeout, wslBinary, "--distribution", config.WSLDistro, "--exec", "docker", "version", "--format", "{{.Server.Version}}")
	}
	return result
}

func probeCommand(parent context.Context, timeout time.Duration, binary string, args ...string) bool {
	if _, err := exec.LookPath(binary); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

func chooseBackend(requested Backend, platform string, availability Availability) (Backend, error) {
	if requested == "" {
		requested = BackendAuto
	}
	requested = Backend(strings.ToLower(strings.TrimSpace(string(requested))))
	switch requested {
	case BackendAuto:
		if availability.Docker {
			return BackendDocker, nil
		}
		if strings.EqualFold(strings.TrimSpace(platform), "windows") && availability.WSL2 {
			return BackendWSL2, nil
		}
		return "", errors.New("no isolated sandbox backend is available")
	case BackendDocker:
		if !availability.Docker {
			return "", errors.New("docker sandbox is unavailable")
		}
		return BackendDocker, nil
	case BackendWSL2:
		if !strings.EqualFold(strings.TrimSpace(platform), "windows") {
			return "", errors.New("wsl2 sandbox requires Windows")
		}
		if !availability.WSL2 {
			return "", errors.New("wsl2 sandbox is unavailable")
		}
		return BackendWSL2, nil
	case BackendNative:
		return "", errors.New("native sandbox would run unconfined; refusing")
	default:
		return "", fmt.Errorf("unknown sandbox backend %q", requested)
	}
}

func validateRequestTrustRoot(request Request, trustRoot string, selected Runner) error {
	if request.Workspace == "" {
		return fmt.Errorf("%w for every request", ErrTrustRootRequired)
	}
	// RoutingRunner has already validated its configured root at construction;
	// the concrete builders repeat path confinement for working directories.
	if selected == nil {
		return errors.New("sandbox unavailable: no selected backend")
	}
	workspace, err := absolutePath(request.Workspace)
	if err != nil {
		return fmt.Errorf("resolve sandbox workspace: %w", err)
	}
	if err := validateTrustRoot(trustRoot, workspace); err != nil {
		return err
	}
	trustedReal, err := filepath.EvalSymlinks(trustRoot)
	if err != nil {
		return fmt.Errorf("sandbox trust root is not usable: %w", err)
	}
	workspaceReal, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return fmt.Errorf("sandbox workspace is not usable: %w", err)
	}
	if err := validateTrustRoot(trustedReal, workspaceReal); err != nil {
		return fmt.Errorf("sandbox workspace escapes trust root through symlink: %w", err)
	}
	if _, err := os.Stat(workspace); err != nil {
		return fmt.Errorf("sandbox workspace is not usable: %w", err)
	}
	return nil
}
