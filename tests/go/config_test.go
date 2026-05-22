package codeagent_test

import (
	"os"
	"path/filepath"
	"testing"

	"code-agent/internal/config"
)

func TestConfigLoadMergesTokenBudget(t *testing.T) {
	projectRoot := t.TempDir()
	agentDir := filepath.Join(projectRoot, ".agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("create agent dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(agentDir, "settings.local.json"),
		[]byte(`{"max_tokens_per_session":12345,"max_cost_per_session":1.25}`),
		0o644,
	); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.MaxTokensPerSession != 12345 {
		t.Fatalf("unexpected max token budget: %d", cfg.MaxTokensPerSession)
	}
	if cfg.MaxCostPerSession != 1.25 {
		t.Fatalf("unexpected max cost budget: %.2f", cfg.MaxCostPerSession)
	}
}

func TestConfigLoadMergesOrchestratorLifecycle(t *testing.T) {
	projectRoot := t.TempDir()
	agentDir := filepath.Join(projectRoot, ".agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("create agent dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(agentDir, "settings.local.json"),
		[]byte(`{"orchestrator_auto_start":false,"orchestrator_command":"python3","orchestrator_args":["-m","orchestrator.server"],"orchestrator_startup_timeout_seconds":9}`),
		0o644,
	); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.OrchestratorAutoStart {
		t.Fatal("orchestrator_auto_start=false should be preserved")
	}
	if cfg.OrchestratorCommand != "python3" {
		t.Fatalf("unexpected orchestrator command: %q", cfg.OrchestratorCommand)
	}
	if len(cfg.OrchestratorArgs) != 2 || cfg.OrchestratorArgs[0] != "-m" || cfg.OrchestratorArgs[1] != "orchestrator.server" {
		t.Fatalf("unexpected orchestrator args: %#v", cfg.OrchestratorArgs)
	}
	if cfg.OrchestratorStartupTimeout != 9 {
		t.Fatalf("unexpected startup timeout: %d", cfg.OrchestratorStartupTimeout)
	}
}
