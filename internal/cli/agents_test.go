package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
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
