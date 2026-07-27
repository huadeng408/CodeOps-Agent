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

func TestConfigDefaultContextWindowIs256K(t *testing.T) {
	cfg := config.Default(t.TempDir())

	if cfg.ContextWindow != 256000 {
		t.Fatalf("unexpected context window: %d", cfg.ContextWindow)
	}
	if cfg.OrchestratorConversationTimeout != 300 {
		t.Fatalf("unexpected conversation timeout: %d", cfg.OrchestratorConversationTimeout)
	}
}

func TestConfigDefaultModelFastIsGpt4oMini(t *testing.T) {
	cfg := config.Default(t.TempDir())

	if cfg.ModelFast != "gpt-4o-mini" {
		t.Fatalf("unexpected default model_fast: %q", cfg.ModelFast)
	}
}

func TestConfigLoadPreservesDefaultModelFast(t *testing.T) {
	// With no settings override, the gpt-4o-mini default round-trips through
	// a full config load.
	projectRoot := t.TempDir()
	cfg, err := config.Load(projectRoot)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ModelFast != "gpt-4o-mini" {
		t.Fatalf("unexpected model_fast after load: %q", cfg.ModelFast)
	}
}

func TestConfigLoadRoundTripsModelFastOverride(t *testing.T) {
	// A settings JSON model_fast value round-trips (JSON -> struct -> merge).
	projectRoot := t.TempDir()
	agentDir := filepath.Join(projectRoot, ".agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("create agent dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(agentDir, "settings.local.json"),
		[]byte(`{"model_fast":"gpt-5-nano"}`),
		0o644,
	); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ModelFast != "gpt-5-nano" {
		t.Fatalf("unexpected model_fast: %q", cfg.ModelFast)
	}
}

func TestConfigLoadReadsDotenvModel(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(projectRoot, ".env.local"),
		[]byte("OPENAI_MODEL=gpt-5.5\n"),
		0o644,
	); err != nil {
		t.Fatalf("write dotenv: %v", err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Model != "gpt-5.5" {
		t.Fatalf("unexpected model: %q", cfg.Model)
	}
}

func TestConfigLoadSettingsModelOverridesDotenvModel(t *testing.T) {
	projectRoot := t.TempDir()
	agentDir := filepath.Join(projectRoot, ".agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("create agent dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(projectRoot, ".env.local"),
		[]byte("OPENAI_MODEL=gpt-5.5\n"),
		0o644,
	); err != nil {
		t.Fatalf("write dotenv: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(agentDir, "settings.local.json"),
		[]byte(`{"model":"custom-model"}`),
		0o644,
	); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	cfg, err := config.Load(projectRoot)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Model != "custom-model" {
		t.Fatalf("unexpected model: %q", cfg.Model)
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
		[]byte(`{"orchestrator_auto_start":false,"orchestrator_command":"python3","orchestrator_args":["-m","orchestrator.server"],"orchestrator_startup_timeout_seconds":9,"orchestrator_conversation_timeout_seconds":600}`),
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
	if cfg.OrchestratorConversationTimeout != 600 {
		t.Fatalf("unexpected conversation timeout: %d", cfg.OrchestratorConversationTimeout)
	}
}
