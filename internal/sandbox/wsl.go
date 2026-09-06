package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	pathpkg "path"
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

func (r *wslRunner) Start(ctx context.Context, request Request) (Process, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary, args, err := BuildWSLDockerCommand("windows", r.config, request)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binary, args...)
	configureSandboxProcess(cmd)
	return &commandProcess{cmd: cmd}, nil
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
	workspace, err := absoluteWindowsPath(request.Workspace)
	if err != nil {
		return "", nil, fmt.Errorf("resolve sandbox workspace: %w", err)
	}
	if err := validateWindowsTrustRoot(config.TrustRoot, workspace); err != nil {
		return "", nil, err
	}
	workingDir := request.WorkingDir
	if strings.TrimSpace(workingDir) == "" {
		workingDir = workspace
	}
	workingDir, err = absoluteWindowsPath(workingDir)
	if err != nil {
		return "", nil, fmt.Errorf("resolve sandbox working directory: %w", err)
	}
	rel, err := windowsRelativePath(workspace, workingDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
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
	normalized, err := absoluteWindowsPath(path)
	if err != nil {
		return "", fmt.Errorf("sandbox workspace must use a local Windows drive: %s", path)
	}
	drive := strings.ToLower(string(normalized[0]))
	return "/mnt/" + drive + normalized[2:], nil
}

// absoluteWindowsPath normalizes a Windows drive path without relying on the
// host OS. CI exercises this Windows command builder on Linux.
func absoluteWindowsPath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("path is required")
	}
	value = strings.ReplaceAll(value, "\\", "/")
	if len(value) < 2 || value[1] != ':' {
		return "", fmt.Errorf("path must use a local Windows drive: %s", raw)
	}
	rest := value[2:]
	if rest == "" {
		rest = "/"
	}
	if !strings.HasPrefix(rest, "/") {
		return "", fmt.Errorf("path must be absolute: %s", raw)
	}
	rest = pathpkg.Clean(rest)
	if rest == "." {
		rest = "/"
	}
	return strings.ToUpper(value[:1]) + ":" + rest, nil
}

func windowsRelativePath(base, target string) (string, error) {
	base, err := absoluteWindowsPath(base)
	if err != nil {
		return "", err
	}
	target, err = absoluteWindowsPath(target)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(base[:2], target[:2]) {
		return "", fmt.Errorf("paths use different Windows drives")
	}
	baseParts := windowsPathParts(base)
	targetParts := windowsPathParts(target)
	common := 0
	for common < len(baseParts) && common < len(targetParts) &&
		strings.EqualFold(baseParts[common], targetParts[common]) {
		common++
	}
	parts := make([]string, 0, len(baseParts)-common+len(targetParts)-common)
	for i := common; i < len(baseParts); i++ {
		parts = append(parts, "..")
	}
	for i := common; i < len(targetParts); i++ {
		parts = append(parts, strings.ToLower(targetParts[i]))
	}
	if len(parts) == 0 {
		return ".", nil
	}
	return strings.Join(parts, "/"), nil
}

func windowsPathParts(value string) []string {
	value = strings.TrimPrefix(value[2:], "/")
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}

func validateWindowsTrustRoot(root, workspace string) error {
	trusted, err := absoluteWindowsPath(root)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTrustRootRequired, err)
	}
	rel, err := windowsRelativePath(trusted, workspace)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("sandbox workspace is outside trust root: %s", workspace)
	}
	return nil
}
