package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWSLDockerCommandConvertsWindowsWorkspaceAndUsesRestrictedDocker(t *testing.T) {
	workspace := filepath.Join("C:\\", "work", "repo")
	binary, args, err := BuildWSLDockerCommand("windows", Config{
		Backend:   BackendWSL2,
		WSLDistro: "Ubuntu-24.04",
		TrustRoot: filepath.Join("C:\\", "work"),
	}, Request{Workspace: workspace, WorkingDir: filepath.Join(workspace, "pkg"), Command: "printf ok"})
	if err != nil {
		t.Fatalf("BuildWSLDockerCommand() error = %v", err)
	}
	if binary != "wsl.exe" {
		t.Fatalf("binary = %q, want wsl.exe", binary)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--distribution Ubuntu-24.04",
		"--exec docker run --rm --init",
		"--network none",
		"--read-only",
		"src=/mnt/c/work/repo,dst=/workspace,readonly",
		"--workdir /workspace/pkg",
		"sh -lc printf ok",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("WSL command missing %q: %s", want, joined)
		}
	}
}

func TestSelectRunnerAutoPrefersDockerAndRequiresTrustedRoot(t *testing.T) {
	root := t.TempDir()
	runner, err := SelectRunner(Config{Backend: BackendAuto, TrustRoot: root}, "windows", Availability{Docker: true, WSL2: true})
	if err != nil {
		t.Fatalf("SelectRunner() error = %v", err)
	}
	if got := runner.Backend(); got != BackendDocker {
		t.Fatalf("selected backend = %q, want docker", got)
	}
	if _, err := SelectRunner(Config{Backend: BackendAuto}, "windows", Availability{Docker: true}); err == nil || !strings.Contains(err.Error(), "trust root") {
		t.Fatalf("missing trust root error = %v", err)
	}
}

func TestSelectRunnerAutoFallsBackToWSL2AndExplicitNativeFailsClosed(t *testing.T) {
	root := t.TempDir()
	runner, err := SelectRunner(Config{Backend: BackendAuto, TrustRoot: root, WSLDistro: "Ubuntu-24.04"}, "windows", Availability{WSL2: true})
	if err != nil {
		t.Fatalf("WSL fallback error = %v", err)
	}
	if got := runner.Backend(); got != BackendWSL2 {
		t.Fatalf("selected backend = %q, want wsl2", got)
	}
	if _, err := SelectRunner(Config{Backend: BackendNative, TrustRoot: root}, "windows", Availability{Native: true}); err == nil || !strings.Contains(err.Error(), "unconfined") {
		t.Fatalf("native selection error = %v", err)
	}
}

func TestRoutingRunnerFailsClosedWhenNoBackendIsAvailable(t *testing.T) {
	root := t.TempDir()
	runner := NewRoutingRunner(Config{Backend: BackendAuto, TrustRoot: root}, "windows", Availability{})
	result, err := runner.Run(context.Background(), Request{Workspace: root, WorkingDir: root, Command: "printf no"})
	if err == nil || result.ExitCode == 0 || !strings.Contains(strings.ToLower(err.Error()), "sandbox") {
		t.Fatalf("unavailable runner result=%+v err=%v", result, err)
	}
}

func TestRoutingRunnerRejectsWorkspaceOutsideTrustRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	runner := NewRoutingRunner(Config{Backend: BackendDocker, TrustRoot: root}, "windows", Availability{Docker: true})
	result, err := runner.Run(context.Background(), Request{Workspace: outside, WorkingDir: outside, Command: "printf no"})
	if err == nil || result.ExitCode == 0 || !strings.Contains(err.Error(), "outside trust root") {
		t.Fatalf("trust-root escape result=%+v err=%v", result, err)
	}
}

func TestRoutingRunnerRejectsSymlinkWorkspaceEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	runner := NewRoutingRunner(Config{Backend: BackendDocker, TrustRoot: root}, "windows", Availability{Docker: true})
	result, err := runner.Run(context.Background(), Request{Workspace: link, WorkingDir: link, Command: "printf no"})
	if err == nil || result.ExitCode == 0 || !strings.Contains(err.Error(), "escapes trust root") {
		t.Fatalf("symlink escape result=%+v err=%v", result, err)
	}
}
