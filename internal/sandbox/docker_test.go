package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildDockerCommandEnforcesIsolationForLinux(t *testing.T) {
	workspace := t.TempDir()
	workingDir := filepath.Join(workspace, "internal", "tool")

	binary, args, err := BuildDockerCommand("linux", Config{Image: "code-agent/sandbox:test"}, Request{
		Workspace:  workspace,
		WorkingDir: workingDir,
		Command:    "go test ./...",
	})
	if err != nil {
		t.Fatalf("BuildDockerCommand() error = %v", err)
	}
	if binary != "docker" {
		t.Fatalf("docker binary = %q, want docker", binary)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"run --rm --init",
		"--pull never",
		"--label com.docker.compose.project=localcode",
		"--label com.docker.compose.service=sandbox",
		"--label codeops.owner=CodeOps-Agent",
		"--label codeops.purpose=task",
		"--network none",
		"--read-only",
		"--cap-drop ALL",
		"--security-opt no-new-privileges",
		"--pids-limit 128",
		"--workdir /workspace/internal/tool",
		"type=bind",
		"dst=/workspace",
		"readonly",
		"code-agent/sandbox:test sh -c go test ./...",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("isolated command missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "--privileged") {
		t.Fatalf("isolated command must not grant privileged access: %s", joined)
	}
}

func TestBuildDockerCommandUsesDockerExeForWindowsAndCanOptIntoWorkspaceWrite(t *testing.T) {
	workspace := t.TempDir()

	binary, args, err := BuildDockerCommand("windows", Config{
		Image:               "code-agent/sandbox:test",
		AllowWorkspaceWrite: true,
	}, Request{Workspace: workspace, WorkingDir: workspace, Command: "Write-Output ok"})
	if err != nil {
		t.Fatalf("BuildDockerCommand() error = %v", err)
	}
	if binary != "docker.exe" {
		t.Fatalf("docker binary = %q, want docker.exe", binary)
	}
	if got := strings.Join(args, " "); strings.Contains(got, "readonly") {
		t.Fatalf("writable workspace unexpectedly mounted readonly: %s", got)
	}
}

func TestBuildDockerCommandRejectsWorkingDirectoryOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	_, _, err := BuildDockerCommand("linux", Config{}, Request{
		Workspace:  workspace,
		WorkingDir: filepath.Dir(workspace),
		Command:    "echo unexpected",
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("BuildDockerCommand() error = %v, want workspace containment failure", err)
	}
}

func TestDockerAndWSLRefuseOpenSandboxDependencyInsteadOfIgnoringIt(t *testing.T) {
	workspace := t.TempDir()
	request := Request{Workspace: workspace, WorkingDir: workspace, Program: "python", DependencyOperationID: strings.Repeat("1", 64)}
	if _, _, err := BuildDockerCommand("linux", Config{}, request); err == nil {
		t.Fatal("Docker silently ignored the approved dependency")
	}
	request.Workspace, request.WorkingDir = `D:\approved`, `D:\approved`
	if _, _, err := BuildWSLDockerCommand("windows", Config{TrustRoot: `D:\approved`, WSLDistro: "Ubuntu"}, request); err == nil {
		t.Fatal("WSL silently ignored the approved dependency")
	}
}

func TestDockerRunnerIntegrationEnforcesReadOnlyWorkspaceAndNoNetwork(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_DOCKER_SANDBOX_TESTS") != "1" {
		t.Skip("set CODE_AGENT_RUN_DOCKER_SANDBOX_TESTS=1 to run Docker sandbox integration")
	}
	workspace := t.TempDir()
	runner := NewDockerRunner(DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	writeResult, writeErr := runner.Run(ctx, Request{
		Workspace:  workspace,
		WorkingDir: workspace,
		Command:    "touch /workspace/.sandbox-write-probe",
	})
	if writeErr == nil || writeResult.ExitCode == 0 {
		t.Fatalf("read-only workspace write unexpectedly succeeded: result=%+v err=%v", writeResult, writeErr)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".sandbox-write-probe")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sandbox write probe escaped to host: stat err=%v", err)
	}

	networkResult, networkErr := runner.Run(ctx, Request{
		Workspace:  workspace,
		WorkingDir: workspace,
		Command:    "wget -q -T 2 -O /tmp/network-probe http://1.1.1.1",
	})
	if networkErr == nil || networkResult.ExitCode == 0 {
		t.Fatalf("network-disabled sandbox reached external address: result=%+v err=%v", networkResult, networkErr)
	}
}

func TestTaskToolchainRequiresPinnedImageAndRetainsIsolation(t *testing.T) {
	root := t.TempDir()
	image := "toolchain@sha256:" + strings.Repeat("a", 64)
	for _, language := range []string{"go", "python"} {
		config := Config{TrustRoot: root, Image: image, Toolchain: language}
		_, args, err := BuildDockerCommand(runtime.GOOS, config, Request{Workspace: root, Program: "printf", Args: []string{"literal; $(host-command)"}})
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(args, " ")
		for _, want := range []string{"--network none", "--pull never", "--read-only", "/tmp:rw,noexec,nosuid", "/run/codeops:rw,exec,nosuid,nodev,size=512m,mode=1777", "dst=/workspace,readonly"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("%s missing %q", language, want)
			}
		}
		if args[len(args)-1] != "literal; $(host-command)" {
			t.Fatal("structured argument changed")
		}
		for _, invalid := range []Config{{TrustRoot: root, Image: "toolchain:latest", Toolchain: language}, {Image: image, Toolchain: language}, {TrustRoot: root, Image: image, Toolchain: language, AllowWorkspaceWrite: true}, {TrustRoot: root, Image: image, Toolchain: "node"}} {
			if _, _, err := BuildDockerCommand(runtime.GOOS, invalid, Request{Workspace: root, Program: "go"}); err == nil {
				t.Fatal("unapproved toolchain accepted")
			}
		}
	}
}

func TestDockerRunnerIntegrationRefusesMissingImageWithoutPull(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_DOCKER_SANDBOX_TESTS") != "1" {
		t.Skip("set CODE_AGENT_RUN_DOCKER_SANDBOX_TESTS=1 to run Docker sandbox integration")
	}
	workspace := t.TempDir()
	image := fmt.Sprintf("codeops-missing-%x:missing", time.Now().UnixNano())
	runner := NewDockerRunner(Config{Image: image, TrustRoot: workspace})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := runner.Run(ctx, Request{Workspace: workspace, WorkingDir: workspace, Program: "printf", Args: []string{"must-not-run"}})
	if err == nil || result.ExitCode != 125 || !strings.Contains(result.Output, "No such image: "+image) || strings.Contains(result.Output, "Pulling from") {
		t.Fatalf("missing image was not refused locally: result=%+v err=%v", result, err)
	}
}

func TestDockerRunnerIntegrationBoundsOutput(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_DOCKER_SANDBOX_TESTS") != "1" {
		t.Skip("set CODE_AGENT_RUN_DOCKER_SANDBOX_TESTS=1 to run Docker sandbox integration")
	}
	workspace := t.TempDir()
	runner := NewDockerRunner(Config{TrustRoot: workspace})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := runner.Run(ctx, Request{Workspace: workspace, Program: "sh", Args: []string{"-c", "printf BEGIN; head -c 2097152 /dev/zero; printf END"}})
	if err != nil || result.ExitCode != 0 || len(result.Output) > 1<<20 || !strings.HasPrefix(result.Output, "BEGIN") || !strings.HasSuffix(result.Output, "END") || !strings.Contains(result.Output, "[output truncated]") {
		t.Fatalf("bounded output: bytes=%d exit=%d err=%v", len(result.Output), result.ExitCode, err)
	}
}

func TestDockerRunnerIntegrationCancellationStopsContainer(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_DOCKER_SANDBOX_TESTS") != "1" {
		t.Skip("set CODE_AGENT_RUN_DOCKER_SANDBOX_TESTS=1 to run Docker sandbox integration")
	}
	workspace := t.TempDir()
	runner := NewDockerRunner(Config{TrustRoot: workspace})
	marker := fmt.Sprintf("codeops-timeout-%x", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := runner.Run(ctx, Request{Workspace: workspace, Program: "sh", Args: []string{"-c", "echo READY; sleep 120", marker}})
	if !errors.Is(err, context.DeadlineExceeded) || result.ExitCode != 124 || !strings.Contains(result.Output, "READY") {
		t.Errorf("cancellation: exit=%d output=%q err=%v", result.ExitCode, result.Output, err)
	}
	if !result.Stopped || len(result.RuntimeID) != 64 {
		t.Errorf("confirmed Docker cleanup lost its exact identity: %+v", result)
	}
	output, inspectErr := exec.Command("docker", "ps", "--no-trunc", "--format", "{{.ID}} {{.Command}} {{.Names}}").CombinedOutput()
	if inspectErr != nil || strings.Contains(string(output), marker) {
		t.Fatalf("cancelled task still running or outcome unknown: %s err=%v", output, inspectErr)
	}
}

func TestDockerRunnerIntegrationStreamingKillStopsExactContainer(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_DOCKER_SANDBOX_TESTS") != "1" {
		t.Skip("set CODE_AGENT_RUN_DOCKER_SANDBOX_TESTS=1 to run Docker sandbox integration")
	}
	workspace := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proc, err := NewDockerRunner(Config{TrustRoot: workspace}).Start(ctx, Request{Workspace: workspace, Interactive: true, Program: "sh", Args: []string{"-c", "IFS= read -r input; printf '%s\\n' \"$input\"; sleep 120"}})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := proc.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := proc.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := proc.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	defer proc.Kill()
	cancel() // Streaming lifetime belongs to the job, not the initiating HTTP turn.
	literal := "literal; $(host-command) & `host-command`"
	if _, err := io.WriteString(stdin, literal+"\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != literal+"\n" {
		t.Fatalf("stdin was interpreted or detached: %q err=%v", line, err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, stdout)
	_, _ = io.Copy(io.Discard, stderr)
	if err := proc.Wait(); errors.Is(err, ErrSandboxOutcomeUnknown) {
		t.Fatal(err)
	}
	id := proc.(*commandProcess).containerID
	output, err := exec.Command("docker", "container", "inspect", id).CombinedOutput()
	if err == nil || (!strings.Contains(string(output), "No such object: "+id) && !strings.Contains(string(output), "No such container: "+id)) {
		t.Fatalf("owned container not confirmed absent: %s err=%v", output, err)
	}
}

func TestDockerCommandRejectsMountSyntaxInWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo,src=unapproved")
	if _, _, err := BuildDockerCommand(runtime.GOOS, Config{}, Request{Workspace: root, Program: "printf"}); err == nil {
		t.Fatal("Docker mount separators accepted as a host path")
	}
}

func TestSandboxRoutingRealProcessE2E(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_SANDBOX_ROUTING_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_SANDBOX_ROUTING_E2E=1 to run sandbox routing E2E")
	}
	workspace := t.TempDir()
	config := Config{Backend: BackendAuto, TrustRoot: workspace, Image: "alpine:3.20", ProbeTimeout: 3 * time.Second}
	availability := DetectAvailability(context.Background(), config, runtime.GOOS)
	if !availability.Docker && !availability.WSL2 {
		t.Fatalf("no isolated backend available: %+v", availability)
	}
	runner := NewRoutingRunner(config, runtime.GOOS, availability)
	result, err := runner.Run(context.Background(), Request{Workspace: workspace, WorkingDir: workspace, Command: "printf SANDBOX_E2E"})
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "SANDBOX_E2E") {
		t.Fatalf("sandbox process result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "SANDBOX_E2E")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sandbox process unexpectedly wrote host workspace: %v", err)
	}
}
