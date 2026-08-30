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
)

// Config controls the container boundary for one sandboxed command.
// Network access and privilege escalation are intentionally not configurable.
type Config struct {
	Image               string
	AllowWorkspaceWrite bool
	MemoryLimit         string
	CPULimit            string
	PidsLimit           int
	TmpfsSize           string
}

// DefaultConfig is suitable for Docker Desktop on Windows and Docker Engine on Linux.
func DefaultConfig() Config {
	return Config{
		Image:       "alpine:3.20",
		MemoryLimit: "1g",
		CPULimit:    "2",
		PidsLimit:   128,
		TmpfsSize:   "64m",
	}
}

// Request describes the workspace-scoped command to run.
type Request struct {
	Workspace  string
	WorkingDir string
	// Command is interpreted by the sandbox shell. It is intended for the Bash
	// tool only; structured callers must set Program and Args instead.
	Command    string
	Program    string
	Args       []string
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

func NewDockerRunner(config Config) *DockerRunner {
	return newDockerRunner(config, runtime.GOOS, osCommandExecutor{})
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
	mount := "type=bind,src=" + workspace + ",dst=/workspace"
	if !config.AllowWorkspaceWrite {
		mount += ",readonly"
	}
	args := []string{
		"run", "--rm", "--init",
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
	if program != "" {
		args = append(args, program)
		args = append(args, request.Args...)
	} else {
		args = append(args, "sh", "-lc", command)
	}
	binary := "docker"
	if strings.EqualFold(strings.TrimSpace(platform), "windows") {
		binary = "docker.exe"
	}
	return binary, args, nil
}

func normalizeConfig(config Config) Config {
	defaults := DefaultConfig()
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
