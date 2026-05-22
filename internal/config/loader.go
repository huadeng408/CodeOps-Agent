package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	ProjectRoot                string           `json:"-"`
	WorkingDir                 string           `json:"-"`
	Model                      string           `json:"model"`
	ModelFast                  string           `json:"model_fast"`
	ContextWindow              int              `json:"context_window"`
	MaxTokensPerSession        int              `json:"max_tokens_per_session"`
	MaxCostPerSession          float64          `json:"max_cost_per_session"`
	OrchestratorAddr           string           `json:"orchestrator_addr"`
	OrchestratorAutoStart      bool             `json:"orchestrator_auto_start"`
	OrchestratorCommand        string           `json:"orchestrator_command"`
	OrchestratorArgs           []string         `json:"orchestrator_args"`
	OrchestratorStartupTimeout int              `json:"orchestrator_startup_timeout_seconds"`
	SessionDBPath              string           `json:"session_db_path"`
	Permissions                PermissionConfig `json:"permissions"`
	Hooks                      []HookConfig     `json:"hooks"`
	MCPConfig                  string           `json:"mcp_config"`
	MemoryDir                  string           `json:"memory_dir"`
	WorktreeBaseRef            string           `json:"worktree_base_ref"`
}

type PermissionConfig struct {
	Allow []PermissionRule `json:"allow"`
	Deny  []PermissionRule `json:"deny"`
}

type PermissionRule struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern"`
}

type HookConfig struct {
	Type    string `json:"type"`
	Matcher string `json:"matcher"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

func Default(projectRoot string) Config {
	workingDir, _ := os.Getwd()
	projectRoot = absPathOr(projectRoot, workingDir)
	if projectRoot == "" {
		projectRoot = workingDir
	}

	return Config{
		ProjectRoot:                projectRoot,
		WorkingDir:                 workingDir,
		Model:                      "gpt-4o",
		ModelFast:                  "gpt-4o-mini",
		ContextWindow:              128000,
		MaxTokensPerSession:        1_000_000,
		MaxCostPerSession:          5.0,
		OrchestratorAddr:           "127.0.0.1:50051",
		OrchestratorAutoStart:      true,
		OrchestratorCommand:        "python",
		OrchestratorArgs:           []string{"-m", "orchestrator.server"},
		OrchestratorStartupTimeout: 5,
		SessionDBPath:              filepath.Join(projectRoot, ".agent", "sessions", "sessions.sqlite"),
		MCPConfig:                  ".mcp.json",
		MemoryDir:                  filepath.Join(projectRoot, ".agent", "memory"),
		WorktreeBaseRef:            "fresh",
	}
}

func Load(projectRoot string) (Config, error) {
	cfg := Default(projectRoot)

	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if err := applyJSONPatch(filepath.Join(home, ".agent", "settings.json"), &cfg); err != nil {
			return Config{}, err
		}
	}

	for _, path := range []string{
		filepath.Join(cfg.ProjectRoot, ".agent", "settings.json"),
		filepath.Join(cfg.ProjectRoot, ".agent", "settings.local.json"),
	} {
		if err := applyJSONPatch(path, &cfg); err != nil {
			return Config{}, err
		}
	}

	for _, dir := range []string{cfg.MemoryDir, filepath.Dir(cfg.SessionDBPath)} {
		if err := os.MkdirAll(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return Config{}, fmt.Errorf("create config dir %s: %w", dir, err)
		}
	}

	return cfg, nil
}

func applyJSONPatch(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}

	var patch Config
	if err := json.Unmarshal(data, &patch); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}

	mergeConfig(cfg, patch, raw)
	return nil
}

func mergeConfig(dst *Config, patch Config, raw map[string]json.RawMessage) {
	if patch.Model != "" {
		dst.Model = patch.Model
	}
	if patch.ModelFast != "" {
		dst.ModelFast = patch.ModelFast
	}
	if patch.ContextWindow != 0 {
		dst.ContextWindow = patch.ContextWindow
	}
	if patch.MaxTokensPerSession != 0 {
		dst.MaxTokensPerSession = patch.MaxTokensPerSession
	}
	if patch.MaxCostPerSession != 0 {
		dst.MaxCostPerSession = patch.MaxCostPerSession
	}
	if patch.OrchestratorAddr != "" {
		dst.OrchestratorAddr = patch.OrchestratorAddr
	}
	if _, ok := raw["orchestrator_auto_start"]; ok {
		dst.OrchestratorAutoStart = patch.OrchestratorAutoStart
	}
	if patch.OrchestratorCommand != "" {
		dst.OrchestratorCommand = patch.OrchestratorCommand
	}
	if len(patch.OrchestratorArgs) > 0 {
		dst.OrchestratorArgs = patch.OrchestratorArgs
	}
	if patch.OrchestratorStartupTimeout != 0 {
		dst.OrchestratorStartupTimeout = patch.OrchestratorStartupTimeout
	}
	if patch.SessionDBPath != "" {
		dst.SessionDBPath = patch.SessionDBPath
	}
	if len(patch.Permissions.Allow) > 0 || len(patch.Permissions.Deny) > 0 {
		dst.Permissions = patch.Permissions
	}
	if len(patch.Hooks) > 0 {
		dst.Hooks = patch.Hooks
	}
	if patch.MCPConfig != "" {
		dst.MCPConfig = patch.MCPConfig
	}
	if patch.MemoryDir != "" {
		dst.MemoryDir = patch.MemoryDir
	}
	if patch.WorktreeBaseRef != "" {
		dst.WorktreeBaseRef = patch.WorktreeBaseRef
	}
}

func absPathOr(path string, fallback string) string {
	if path == "" {
		return fallback
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}
