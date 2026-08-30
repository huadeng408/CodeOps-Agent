package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		"--network none",
		"--read-only",
		"--cap-drop ALL",
		"--security-opt no-new-privileges",
		"--pids-limit 128",
		"--workdir /workspace/internal/tool",
		"type=bind",
		"dst=/workspace",
		"readonly",
		"code-agent/sandbox:test sh -lc go test ./...",
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
