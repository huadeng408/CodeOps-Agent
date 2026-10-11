package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/sandbox"
	"code-agent/internal/tools"
)

func TestCLIAgentToolsReleaseSession(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("child"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := &cliAgentTools{cfg: config.Config{WorkingDir: root}, items: make(map[string]*tools.Executor)}
	t.Cleanup(func() { _ = manager.Close() })
	result := manager.ExecuteInWorkingDir(context.Background(), identity.Actor{}, "agent-child", root, orchestrator.ToolCall{
		ID: "read", Name: "Read", ParametersJSON: `{"path":"README.md"}`,
	})
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if err := manager.ReleaseSession("agent-child"); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.items) != 0 || len(manager.mcps) != 0 {
		t.Fatalf("released session remained registered: executors=%d mcp=%d", len(manager.items), len(manager.mcps))
	}
}

func TestCLIAgentExplicitOpenSandboxUsesSelectedRuntimeRefusal(t *testing.T) {
	root := t.TempDir()
	manager := &cliAgentTools{cfg: config.Config{WorkingDir: root, Sandbox: config.SandboxConfig{Backend: sandbox.Backend(" OpenSandbox "), Enabled: false}}, items: make(map[string]*tools.Executor)}
	t.Cleanup(func() { _ = manager.Close() })
	result := manager.ExecuteInWorkingDir(context.Background(), identity.Actor{}, "agent-child", root, orchestrator.ToolCall{
		ID: "unapproved-shell", Name: "Bash", ParametersJSON: `{"command":"echo bypassed > escaped.txt"}`,
	})
	if result.ExitCode == 0 || result.Error == "" || strings.Contains(result.Error, "independent agent shell requires a configured sandbox") {
		t.Fatalf("explicit backend did not reach its refusal boundary: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("unapproved child command reached the host: %v", err)
	}
}
