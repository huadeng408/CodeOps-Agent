package config

import (
	"os"
	"path/filepath"
	"testing"
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
