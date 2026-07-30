package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	ProjectRoot                     string           `json:"-"`
	WorkingDir                      string           `json:"-"`
	Model                           string           `json:"model"`
	ModelFast                       string           `json:"model_fast"`
	ContextWindow                   int              `json:"context_window"`
	MaxTokensPerSession             int              `json:"max_tokens_per_session"`
	MaxCostPerSession               float64          `json:"max_cost_per_session"`
	OrchestratorAddr                string           `json:"orchestrator_addr"`
	OrchestratorAutoStart           bool             `json:"orchestrator_auto_start"`
	OrchestratorCommand             string           `json:"orchestrator_command"`
	OrchestratorArgs                []string         `json:"orchestrator_args"`
	OrchestratorStartupTimeout      int              `json:"orchestrator_startup_timeout_seconds"`
	OrchestratorConversationTimeout int              `json:"orchestrator_conversation_timeout_seconds"`
	SessionDBPath                   string           `json:"session_db_path"`
	Permissions                     PermissionConfig `json:"permissions"`
	Hooks                           []HookConfig     `json:"hooks"`
	MCPConfig                       string           `json:"mcp_config"`
	MemoryDir                       string           `json:"memory_dir"`
	WorktreeBaseRef                 string           `json:"worktree_base_ref"`
	RAGEnabled                      bool             `json:"rag_enabled"`
	RAGServerURL                    string           `json:"rag_server_url"`
	RAGInternalSecret               string           `json:"rag_internal_secret"`
	RAGUserID                       uint             `json:"rag_user_id"`
	RAGOrgTag                       string           `json:"rag_org_tag"`
	RAGIngestPublic                 bool             `json:"rag_ingest_public"`
	ThinkingEnabled                 bool             `json:"thinking_enabled"`
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
		ProjectRoot:                     projectRoot,
		WorkingDir:                      workingDir,
		Model:                           "gpt-4o",
		ModelFast:                       "gpt-4o-mini",
		ContextWindow:                   256000,
		MaxTokensPerSession:             1_000_000,
		MaxCostPerSession:               5.0,
		OrchestratorAddr:                "127.0.0.1:50051",
		OrchestratorAutoStart:           true,
		OrchestratorCommand:             "python",
		OrchestratorArgs:                []string{"-m", "orchestrator.server"},
		OrchestratorStartupTimeout:      5,
		OrchestratorConversationTimeout: 300,
		SessionDBPath:                   filepath.Join(projectRoot, ".agent", "sessions", "sessions.sqlite"),
		MCPConfig:                       ".mcp.json",
		MemoryDir:                       filepath.Join(projectRoot, ".agent", "memory"),
		WorktreeBaseRef:                 "fresh",
		RAGServerURL:                    "http://127.0.0.1:8081",
		ThinkingEnabled:                 true,
	}
}

func Load(projectRoot string) (Config, error) {
	cfg := Default(projectRoot)

	if err := applyDotenvModel(filepath.Join(cfg.ProjectRoot, ".env.local"), &cfg); err != nil {
		return Config{}, err
	}

	applyThinkingEnv(&cfg)

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

func applyDotenvModel(path string, cfg *Config) error {
	values, err := readDotenv(path)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}

	provider := strings.ToLower(strings.TrimSpace(values["LLM_PROVIDER"]))
	model := ""
	switch provider {
	case "anthropic":
		model = values["ANTHROPIC_MODEL"]
	case "local":
		model = values["LOCAL_LLM_MODEL"]
	case "openai":
		model = values["OPENAI_MODEL"]
	default:
		for _, key := range []string{"OPENAI_MODEL", "ANTHROPIC_MODEL", "LOCAL_LLM_MODEL"} {
			if strings.TrimSpace(values[key]) != "" {
				model = values[key]
				break
			}
		}
	}

	if strings.TrimSpace(model) != "" {
		cfg.Model = strings.TrimSpace(model)
	}
	return nil
}

func applyThinkingEnv(cfg *Config) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("THINKING_ENABLED")))
	if raw == "" {
		return
	}
	cfg.ThinkingEnabled = raw == "1" || raw == "true" || raw == "yes"
}

func readDotenv(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read env file %s: %w", path, err)
	}

	values := map[string]string{}
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(rawLine, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key != "" {
			values[key] = value
		}
	}
	return values, nil
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
	if patch.OrchestratorConversationTimeout != 0 {
		dst.OrchestratorConversationTimeout = patch.OrchestratorConversationTimeout
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
	if _, ok := raw["rag_enabled"]; ok {
		dst.RAGEnabled = patch.RAGEnabled
	}
	if patch.RAGServerURL != "" {
		dst.RAGServerURL = patch.RAGServerURL
	}
	if patch.RAGInternalSecret != "" {
		dst.RAGInternalSecret = patch.RAGInternalSecret
	}
	if patch.RAGUserID != 0 {
		dst.RAGUserID = patch.RAGUserID
	}
	if patch.RAGOrgTag != "" {
		dst.RAGOrgTag = patch.RAGOrgTag
	}
	if _, ok := raw["rag_ingest_public"]; ok {
		dst.RAGIngestPublic = patch.RAGIngestPublic
	}
	if _, ok := raw["thinking_enabled"]; ok {
		dst.ThinkingEnabled = patch.ThinkingEnabled
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
