package config

import (
	"os"
	"path/filepath"
	"testing"

	"code-agent/internal/sandbox"
)

func TestLoadAppliesSandboxPolicyFromProjectSettings(t *testing.T) {
	root := t.TempDir()
	settingsDir := filepath.Join(root, ".agent")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.local.json"), []byte(`{
  "sandbox": {
    "enabled": false,
    "image": "registry.example/code-agent:sandbox",
    "allow_workspace_write": true,
    "memory_limit": "2g",
    "cpu_limit": "3",
    "pids_limit": 64,
    "tmpfs_size": "96m"
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Sandbox.Enabled {
		t.Fatal("sandbox must honor explicit enabled=false")
	}
	if cfg.Sandbox.Image != "registry.example/code-agent:sandbox" || !cfg.Sandbox.AllowWorkspaceWrite {
		t.Fatalf("sandbox policy = %+v", cfg.Sandbox)
	}
	if cfg.Sandbox.MemoryLimit != "2g" || cfg.Sandbox.CPULimit != "3" || cfg.Sandbox.PidsLimit != 64 || cfg.Sandbox.TmpfsSize != "96m" {
		t.Fatalf("sandbox limits = %+v", cfg.Sandbox)
	}
}

func TestDefaultEnablesReadOnlySandbox(t *testing.T) {
	cfg := Default(t.TempDir())
	if !cfg.Sandbox.Enabled {
		t.Fatal("default configuration must enforce sandboxing")
	}
	if cfg.Sandbox.AllowWorkspaceWrite {
		t.Fatal("default sandbox must mount the workspace read-only")
	}
	if cfg.Sandbox.Image == "" || cfg.Sandbox.PidsLimit <= 0 {
		t.Fatalf("default sandbox is incomplete: %+v", cfg.Sandbox)
	}
	if cfg.Sandbox.Backend != sandbox.BackendAuto {
		t.Fatalf("default sandbox backend = %q, want auto", cfg.Sandbox.Backend)
	}
	if cfg.Sandbox.TrustRoot == "" || cfg.Sandbox.WSLDistro == "" {
		t.Fatalf("default sandbox trust boundary is incomplete: %+v", cfg.Sandbox)
	}
}

func TestLoadMergesSandboxBackendAndTrustRoot(t *testing.T) {
	projectRoot := t.TempDir()
	agentDir := filepath.Join(projectRoot, ".agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("create agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.local.json"), []byte(`{"sandbox":{"backend":"wsl2","wsl_distro":"Ubuntu-24.04","trust_root":"D:/workspace"}}`), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	cfg, err := Load(projectRoot)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Sandbox.Backend != sandbox.BackendWSL2 || cfg.Sandbox.WSLDistro != "Ubuntu-24.04" || cfg.Sandbox.TrustRoot != "D:/workspace" {
		t.Fatalf("sandbox override = %+v", cfg.Sandbox)
	}
}

func TestLoadAppliesConfiguredSkillDirectories(t *testing.T) {
	root := t.TempDir()
	settingsDir := filepath.Join(root, ".agent")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.local.json"), []byte(`{
  "skill_directories": ["C:/shared-skills", "/opt/team-skills"]
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.SkillDirectories) != 2 || cfg.SkillDirectories[0] != "C:/shared-skills" || cfg.SkillDirectories[1] != "/opt/team-skills" {
		t.Fatalf("skill directories = %#v", cfg.SkillDirectories)
	}
}

func TestLoadUsesExplicitProviderModelEnvironment(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env.local"), []byte("LLM_PROVIDER=openai\nOPENAI_MODEL=old-model\n"), 0o600); err != nil {
		t.Fatalf("write dotenv: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".agent"), 0o755); err != nil {
		t.Fatalf("create agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".agent", "settings.local.json"), []byte(`{"model":"settings-model","model_fast":"settings-fast"}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	t.Setenv("LLM_PROVIDER", "anthropic")
	t.Setenv("ANTHROPIC_MODEL", "gpt-5.6-sol")
	t.Setenv("MODEL_FAST", "")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Model != "gpt-5.6-sol" {
		t.Fatalf("model = %q, want explicit Anthropic model", cfg.Model)
	}
	if cfg.ModelFast != "" {
		t.Fatalf("empty MODEL_FAST should disable fast model, got %q", cfg.ModelFast)
	}
}
