package sandbox

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

type wslRunner struct {
	config Config
}

func (r *wslRunner) Backend() Backend { return BackendWSL2 }

func newWSLRunner(config Config) *wslRunner {
	return &wslRunner{config: normalizeConfig(config)}
}

func (r *wslRunner) Run(ctx context.Context, request Request) (Result, error) {
	binary, args, err := BuildWSLDockerCommand("windows", r.config, request)
	if err != nil {
		return Result{ExitCode: 1}, err
	}
	output, err := (&osCommandExecutor{}).CombinedOutput(ctx, binary, args)
	result := Result{Output: string(output)}
	if err != nil {
		result.ExitCode = exitCode(err)
		return result, err
	}
	return result, nil
}

// BuildWSLDockerCommand constructs a WSL2 invocation that runs Docker inside
// the selected distribution. The same network, read-only root, capability,
// resource and non-root restrictions as DockerRunner are retained.
func BuildWSLDockerCommand(platform string, config Config, request Request) (string, []string, error) {
	if !strings.EqualFold(strings.TrimSpace(platform), "windows") {
		return "", nil, fmt.Errorf("wsl2 sandbox requires Windows")
	}
	config = normalizeConfig(config)
	if strings.TrimSpace(config.TrustRoot) == "" {
		return "", nil, ErrTrustRootRequired
	}
	if strings.TrimSpace(config.WSLDistro) == "" || strings.ContainsAny(config.WSLDistro, "\r\n") {
		return "", nil, fmt.Errorf("wsl2 distribution is invalid")
	}
	workspace, err := absolutePath(request.Workspace)
	if err != nil {
		return "", nil, fmt.Errorf("resolve sandbox workspace: %w", err)
	}
	if err := validateTrustRoot(config.TrustRoot, workspace); err != nil {
		return "", nil, err
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
	wslWorkspace, err := windowsPathToWSL(workspace)
	if err != nil {
		return "", nil, err
	}
	containerDir := "/workspace"
	if rel != "." && rel != "" {
		containerDir += "/" + filepath.ToSlash(rel)
	}
	dockerArgs, err := buildDockerRunArgs(config, wslWorkspace, containerDir, request)
	if err != nil {
		return "", nil, err
	}
	args := []string{"--distribution", config.WSLDistro, "--exec"}
	args = append(args, dockerArgs...)
	return "wsl.exe", args, nil
}

func windowsPathToWSL(path string) (string, error) {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' {
		return "", fmt.Errorf("sandbox workspace must use a local Windows drive: %s", path)
	}
	drive := strings.ToLower(string(volume[0]))
	rest := strings.TrimPrefix(path, volume)
	rest = strings.ReplaceAll(rest, "\\", "/")
	if rest == "" {
		rest = "/"
	}
	if !strings.HasPrefix(rest, "/") {
		rest = "/" + rest
	}
	return "/mnt/" + drive + filepath.ToSlash(rest), nil
}
