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
	"sync"
	"time"
)

const defaultProbeTimeout = 5 * time.Second
const defaultProbeRetryDelay = 500 * time.Millisecond
const defaultProbeReadinessTimeout = 60 * time.Second

var (
	// ErrSandboxUnavailable is returned when no configured isolated backend can run.
	ErrSandboxUnavailable = errors.New("sandbox unavailable")
	// ErrTrustRootRequired is returned when a request has no trusted workspace root.
	ErrTrustRootRequired  = errors.New("sandbox trust root is required")
	errBackendUnavailable = errors.New("sandbox backend unavailable")
)

// RoutingRunner selects exactly one isolated backend for the lifetime of an
// executor. It never falls back to an unconfined host process.
type RoutingRunner struct {
	mu            sync.Mutex
	selected      Runner
	backend       Backend
	trustRoot     string
	selectionErr  error
	config        Config
	platform      string
	probe         availabilityProbe
	runnerFactory runnerFactory
	selecting     bool
	selectionDone chan struct{}
}

// Backend reports the selected isolation backend. "unavailable" means every
// candidate was rejected and Run will fail closed.
func (r *RoutingRunner) Backend() Backend {
	if r == nil {
		return backendMissing
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.backend == "" {
		return backendMissing
	}
	return r.backend
}

func (r *RoutingRunner) Run(ctx context.Context, request Request) (Result, error) {
	if r == nil {
		return Result{ExitCode: 126}, fmt.Errorf("%w: nil runner", ErrSandboxUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.ensureSelected(ctx); err != nil {
		return Result{ExitCode: 126}, err
	}
	r.mu.Lock()
	selected := r.selected
	r.mu.Unlock()
	if err := validateRequestTrustRoot(request, r.trustRoot, selected); err != nil {
		return Result{ExitCode: 126}, err
	}
	return selected.Run(ctx, request)
}

// Start forwards a background process request to the selected isolated
// backend. A synchronous-only backend is rejected instead of falling back to
// an unconfined host process.
func (r *RoutingRunner) Start(ctx context.Context, request Request) (Process, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil runner", ErrSandboxUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.ensureSelected(ctx); err != nil {
		return nil, err
	}
	r.mu.Lock()
	selected := r.selected
	trustRoot := r.trustRoot
	r.mu.Unlock()
	if err := validateRequestTrustRoot(request, trustRoot, selected); err != nil {
		return nil, err
	}
	streamer, ok := selected.(StreamingRunner)
	if !ok {
		return nil, errors.New("sandbox backend does not support streaming jobs")
	}
	return streamer.Start(ctx, request)
}

type availabilityProbe func(context.Context, time.Duration, string, ...string) bool

type runnerFactory func(Config, string, Backend) (Runner, error)

func defaultRunnerFactory(config Config, platform string, backend Backend) (Runner, error) {
	switch backend {
	case BackendDocker:
		return newDockerRunner(config, platform, osCommandExecutor{}), nil
	case BackendWSL2:
		return newWSLRunner(config), nil
	default:
		return nil, fmt.Errorf("sandbox backend %q has no isolated implementation", backend)
	}
}

func newRoutingRunner(config Config, platform string, availability Availability, probe availabilityProbe, factory runnerFactory) *RoutingRunner {
	config = normalizeConfig(config)
	if factory == nil {
		factory = defaultRunnerFactory
	}
	runner := &RoutingRunner{
		backend:       backendMissing,
		trustRoot:     config.TrustRoot,
		config:        config,
		platform:      platform,
		probe:         probe,
		runnerFactory: factory,
		selectionDone: make(chan struct{}),
	}
	if strings.TrimSpace(config.TrustRoot) == "" {
		runner.selectionErr = ErrTrustRootRequired
		return runner
	}
	backend, err := chooseBackend(config.Backend, platform, availability)
	if err != nil {
		runner.selectionErr = err
		return runner
	}
	selected, err := factory(config, platform, backend)
	if err != nil {
		runner.selectionErr = err
		return runner
	}
	runner.selected = selected
	runner.backend = backend
	return runner
}

func (r *RoutingRunner) ensureSelected(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		r.mu.Lock()
		if r.selected != nil && r.selectionErr == nil {
			r.mu.Unlock()
			return nil
		}
		if r.probe == nil || !retryableSelectionError(r.selectionErr) {
			err := r.selectionErr
			if err == nil {
				err = errors.New("no selected backend")
			}
			r.mu.Unlock()
			return sandboxSelectionError(ctx, err)
		}
		if r.selecting {
			done := r.selectionDone
			r.mu.Unlock()
			select {
			case <-done:
				r.mu.Lock()
				selected := r.selected
				err := r.selectionErr
				r.mu.Unlock()
				if selected != nil && err == nil {
					return nil
				}
				if err != nil {
					return sandboxSelectionError(ctx, err)
				}
				continue
			case <-ctx.Done():
				return sandboxSelectionError(ctx, ctx.Err())
			}
		}
		if err := ctx.Err(); err != nil {
			r.mu.Unlock()
			return sandboxSelectionError(ctx, err)
		}
		r.selecting = true
		if r.selectionDone == nil {
			r.selectionDone = make(chan struct{})
		}
		done := r.selectionDone
		config := r.config
		platform := r.platform
		probe := r.probe
		factory := r.runnerFactory
		if factory == nil {
			factory = defaultRunnerFactory
		}
		r.mu.Unlock()

		availability := detectAvailabilityWithRetry(ctx, config, platform, probe)
		var (
			backend  Backend
			selected Runner
			err      error
		)
		if ctx.Err() != nil {
			err = ctx.Err()
		} else {
			backend, err = chooseBackend(config.Backend, platform, availability)
			if err == nil {
				selected, err = factory(config, platform, backend)
			}
		}

		r.mu.Lock()
		if err == nil {
			r.selected = selected
			r.backend = backend
			r.selectionErr = nil
		} else {
			r.selectionErr = err
		}
		r.selecting = false
		close(done)
		r.selectionDone = make(chan struct{})
		r.mu.Unlock()
		if err != nil {
			return sandboxSelectionError(ctx, err)
		}
		return nil
	}
}

func sandboxSelectionError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return fmt.Errorf("%w: readiness probe: %w", ErrSandboxUnavailable, ctx.Err())
	}
	if err == nil {
		return ErrSandboxUnavailable
	}
	return fmt.Errorf("%w: %w", ErrSandboxUnavailable, err)
}

func retryableSelectionError(err error) bool {
	return errors.Is(err, errBackendUnavailable) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

// SelectRunner chooses a backend from a previously completed availability
// probe. It is deterministic and does not start a process.
func SelectRunner(config Config, platform string, availability Availability) (*RoutingRunner, error) {
	runner := newRoutingRunner(config, platform, availability, nil, defaultRunnerFactory)
	if runner.selectionErr != nil {
		return nil, runner.selectionErr
	}
	return runner, nil
}

// NewRoutingRunner is the deterministic constructor used by the App and tests.
// Selection failures are represented by a rejecting runner so a caller cannot
// accidentally continue on the host.
func NewRoutingRunner(config Config, platform string, availability Availability) *RoutingRunner {
	return newRoutingRunner(config, platform, availability, nil, defaultRunnerFactory)
}

// NewSandboxRunner probes the configured platform with bounded readiness
// retries and returns a runner. A failed probe is a normal fail-closed state,
// not permission to use host execution.
func NewSandboxRunner(config Config) *RoutingRunner {
	config = normalizeConfig(config)
	availability := detectAvailabilityWithRetry(context.Background(), config, runtime.GOOS, probeCommand)
	return newRoutingRunner(config, runtime.GOOS, availability, probeCommand, defaultRunnerFactory)
}

// DetectAvailability performs bounded, output-discarding health probes for
// Docker and WSL2. Native execution is deliberately never considered isolated.
func DetectAvailability(ctx context.Context, config Config, platform string) Availability {
	return detectAvailability(ctx, config, platform, probeCommand)
}

func detectAvailability(ctx context.Context, config Config, platform string, probe availabilityProbe) Availability {
	config = normalizeConfig(config)
	return detectAvailabilityWithRetry(ctx, config, platform, probe)
}

func detectAvailabilityWithRetry(ctx context.Context, config Config, platform string, probe availabilityProbe) Availability {
	if ctx == nil {
		ctx = context.Background()
	}
	if probe == nil {
		probe = probeCommand
	}
	config = normalizeConfig(config)
	readinessTimeout := config.ProbeReadinessTimeout
	if readinessTimeout <= 0 {
		readinessTimeout = defaultProbeReadinessTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()
	attempts := config.ProbeAttempts
	delay := config.ProbeRetryDelay
	if delay <= 0 {
		delay = defaultProbeRetryDelay
	}
	var availability Availability
	for attempt := 0; attempts == 0 || attempt < attempts; attempt++ {
		availability = detectAvailabilityOnce(probeCtx, config, platform, probe)
		if backendReady(config.Backend, platform, availability) || (attempts > 0 && attempt == attempts-1) {
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
		case <-probeCtx.Done():
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
	if ctx == nil {
		ctx = context.Background()
	}
	if probe == nil {
		probe = probeCommand
	}
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
		return "", fmt.Errorf("%w: no isolated sandbox backend is available", errBackendUnavailable)
	case BackendDocker:
		if !availability.Docker {
			return "", fmt.Errorf("%w: docker sandbox is unavailable", errBackendUnavailable)
		}
		return BackendDocker, nil
	case BackendWSL2:
		if !strings.EqualFold(strings.TrimSpace(platform), "windows") {
			return "", errors.New("wsl2 sandbox requires Windows")
		}
		if !availability.WSL2 {
			return "", fmt.Errorf("%w: wsl2 sandbox is unavailable", errBackendUnavailable)
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
