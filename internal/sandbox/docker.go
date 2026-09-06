// Package sandbox builds and executes constrained tool commands.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config controls the container boundary for one sandboxed command.
// Network access and privilege escalation are intentionally not configurable.
type Config struct {
	// Backend selects the isolation implementation. Empty keeps the historical
	// Docker behaviour for callers that construct Config directly.
	Backend Backend
	// WSLDistro names the WSL2 distribution used by the WSL2 Docker backend.
	WSLDistro string
	// TrustRoot is the canonical host root that may be exposed to a sandbox.
	// RoutingRunner refuses requests without one or outside it.
	TrustRoot string
	// ProbeTimeout bounds backend availability probes. Zero uses the default.
	ProbeTimeout time.Duration
	// ProbeAttempts caps readiness retries. Zero keeps probing until the total
	// ProbeReadinessTimeout expires, which accommodates Docker Desktop startup.
	ProbeAttempts int
	// ProbeRetryDelay is the initial delay between readiness attempts. Zero uses
	// the default exponential backoff.
	ProbeRetryDelay time.Duration
	// ProbeReadinessTimeout bounds the total readiness wait, including probe
	// attempts and backoff. Zero uses the default 60-second budget.
	ProbeReadinessTimeout time.Duration
	Image                 string
	AllowWorkspaceWrite   bool
	MemoryLimit           string
	CPULimit              string
	PidsLimit             int
	TmpfsSize             string
}

// Backend identifies an execution isolation strategy.
type Backend string

const (
	BackendAuto    Backend = "auto"
	BackendDocker  Backend = "docker"
	BackendWSL2    Backend = "wsl2"
	BackendNative  Backend = "native"
	backendMissing Backend = "unavailable"
)

// Availability is the result of bounded backend health probes. It is exposed
// so tests and embedding applications can make selection deterministic without
// starting a process.
type Availability struct {
	Docker bool
	WSL2   bool
	Native bool
}

// DefaultConfig is suitable for Docker Desktop on Windows and Docker Engine on Linux.
func DefaultConfig() Config {
	return Config{
		Backend:               BackendAuto,
		WSLDistro:             "Ubuntu-24.04",
		Image:                 "alpine:3.20",
		MemoryLimit:           "1g",
		CPULimit:              "2",
		PidsLimit:             128,
		TmpfsSize:             "64m",
		ProbeReadinessTimeout: defaultProbeReadinessTimeout,
	}
}

// Request describes the workspace-scoped command to run.
type Request struct {
	Workspace   string
	WorkingDir  string
	Interactive bool
	// Command is interpreted by the sandbox shell. It is intended for the Bash
	// tool only; structured callers must set Program and Args instead.
	Command string
	Program string
	Args    []string
}

// Result is the normalized output of a sandboxed command.
type Result struct {
	Output   string
	ExitCode int
}

// Runner is implemented by sandbox backends. The Harness only accepts a
// Runner; a configured runner never falls back to a host shell.
type Runner interface {
	Run(context.Context, Request) (Result, error)
}

type commandExecutor interface {
	CombinedOutput(context.Context, string, []string) ([]byte, error)
}

type osCommandExecutor struct{}

func (osCommandExecutor) CombinedOutput(ctx context.Context, name string, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// DockerRunner runs commands in a Docker Linux container. Docker Desktop uses
// the same Linux container contract on Windows through its WSL2 backend.
type DockerRunner struct {
	config   Config
	platform string
	executor commandExecutor
}

// Backend identifies DockerRunner for callers that inspect a concrete runner.
func (r *DockerRunner) Backend() Backend { return BackendDocker }

func NewDockerRunner(config Config) *DockerRunner {
	return newDockerRunner(config, runtime.GOOS, osCommandExecutor{})
}

// Start launches one isolated Docker process with live stdio pipes. The
// context only gates an already-cancelled request; background job lifetime is
// controlled explicitly through Process.Kill and Process.Wait.
func (r *DockerRunner) Start(ctx context.Context, request Request) (Process, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary, args, err := BuildDockerCommand(r.platform, r.config, request)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binary, args...)
	configureSandboxProcess(cmd)
	return &commandProcess{cmd: cmd}, nil
}

func newDockerRunner(config Config, platform string, executor commandExecutor) *DockerRunner {
	if executor == nil {
		executor = osCommandExecutor{}
	}
	return &DockerRunner{config: normalizeConfig(config), platform: platform, executor: executor}
}

func (r *DockerRunner) Run(ctx context.Context, request Request) (Result, error) {
	binary, args, err := BuildDockerCommand(r.platform, r.config, request)
	if err != nil {
		return Result{ExitCode: 1}, err
	}
	output, err := r.executor.CombinedOutput(ctx, binary, args)
	result := Result{Output: string(output)}
	if err != nil {
		result.ExitCode = exitCode(err)
		return result, err
	}
	return result, nil
}

// BuildDockerCommand constructs an argument-vector rather than a shell string
// so commands and host paths cannot escape through quoting.
func BuildDockerCommand(platform string, config Config, request Request) (string, []string, error) {
	config = normalizeConfig(config)
	workspace, err := absolutePath(request.Workspace)
	if err != nil {
		return "", nil, fmt.Errorf("resolve sandbox workspace: %w", err)
	}
	if config.TrustRoot != "" {
		if err := validateTrustRoot(config.TrustRoot, workspace); err != nil {
			return "", nil, err
		}
	}
	workingDir := request.WorkingDir
	if strings.TrimSpace(workingDir) == "" {
		workingDir = workspace
	}
	workingDir, err = absolutePath(workingDir)
	if err != nil {
		return "", nil, fmt.Errorf("resolve sandbox working directory: %w", err)
	}
	rel, err := filepath.Rel(workspace, workingDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("sandbox working directory is outside workspace: %s", workingDir)
	}
	command := strings.TrimSpace(request.Command)
	program := strings.TrimSpace(request.Program)
	if (command == "") == (program == "") {
		return "", nil, errors.New("sandbox request must contain exactly one of command or program")
	}

	containerDir := "/workspace"
	if rel != "." && rel != "" {
		containerDir += "/" + filepath.ToSlash(rel)
	}
	args, err := buildDockerRunArgs(config, workspace, containerDir, request)
	if err != nil {
		return "", nil, err
	}
	args = args[1:] // the local binary is supplied separately below
	binary := "docker"
	if strings.EqualFold(strings.TrimSpace(platform), "windows") {
		binary = "docker.exe"
	}
	return binary, args, nil
}

func buildDockerRunArgs(config Config, workspaceSource, containerDir string, request Request) ([]string, error) {
	command := strings.TrimSpace(request.Command)
	program := strings.TrimSpace(request.Program)
	if (command == "") == (program == "") {
		return nil, errors.New("sandbox request must contain exactly one of command or program")
	}
	mount := "type=bind,src=" + workspaceSource + ",dst=/workspace"
	if !config.AllowWorkspaceWrite {
		mount += ",readonly"
	}
	args := []string{
		"docker", "run", "--rm", "--init",
		"--network", "none",
		"--read-only",
		"--tmpfs", "/tmp:rw,noexec,nosuid,size=" + config.TmpfsSize,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", strconv.Itoa(config.PidsLimit),
		"--memory", config.MemoryLimit,
		"--cpus", config.CPULimit,
		"--user", "65532:65532",
		"--workdir", containerDir,
		"--mount", mount,
		config.Image,
	}
	if request.Interactive {
		// Keep stdin attached for JobWrite without allocating a terminal. This
		// preserves separate stdout/stderr streams for incremental reads.
		args = append(args[:4], append([]string{"-i"}, args[4:]...)...)
	}
	if program != "" {
		args = append(args, program)
		args = append(args, request.Args...)
	} else {
		args = append(args, "sh", "-lc", command)
	}
	return args, nil
}

func normalizeConfig(config Config) Config {
	defaults := DefaultConfig()
	if config.Backend == "" {
		config.Backend = BackendDocker
	}
	if config.WSLDistro == "" {
		config.WSLDistro = "Ubuntu-24.04"
	}
	if config.ProbeTimeout <= 0 {
		config.ProbeTimeout = 5 * time.Second
	}
	if config.ProbeAttempts < 0 {
		config.ProbeAttempts = 0
	}
	if config.ProbeRetryDelay <= 0 {
		config.ProbeRetryDelay = 500 * time.Millisecond
	}
	if config.ProbeReadinessTimeout <= 0 {
		config.ProbeReadinessTimeout = defaultProbeReadinessTimeout
	}
	if strings.TrimSpace(config.Image) == "" {
		config.Image = defaults.Image
	}
	if strings.TrimSpace(config.MemoryLimit) == "" {
		config.MemoryLimit = defaults.MemoryLimit
	}
	if strings.TrimSpace(config.CPULimit) == "" {
		config.CPULimit = defaults.CPULimit
	}
	if config.PidsLimit <= 0 {
		config.PidsLimit = defaults.PidsLimit
	}
	if strings.TrimSpace(config.TmpfsSize) == "" {
		config.TmpfsSize = defaults.TmpfsSize
	}
	return config
}

func validateTrustRoot(root, workspace string) error {
	trusted, err := absolutePath(root)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTrustRootRequired, err)
	}
	rel, err := filepath.Rel(trusted, workspace)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("sandbox workspace is outside trust root: %s", workspace)
	}
	return nil
}

func absolutePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	return filepath.Abs(filepath.Clean(path))
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}
