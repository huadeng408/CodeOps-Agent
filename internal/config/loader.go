package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"code-agent/internal/sandbox"
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
	ActorID                         string           `json:"actor_id"`
	ActorSubject                    string           `json:"actor_subject"`
	ActorTenantID                   string           `json:"actor_tenant_id"`
	ActorRoles                      []string         `json:"actor_roles"`
	SessionDBPath                   string           `json:"session_db_path"`
	Sandbox                         SandboxConfig    `json:"sandbox"`
	SkillDirectories                []string         `json:"skill_directories"`
	Permissions                     PermissionConfig `json:"permissions"`
	Hooks                           []HookConfig     `json:"hooks"`
	MCPConfig                       string           `json:"mcp_config"`
	MemoryDir                       string           `json:"memory_dir"`
	WorktreeBaseRef                 string           `json:"worktree_base_ref"`
	RAGEnabled                      bool             `json:"rag_enabled"`
	RAGServerURL                    string           `json:"rag_server_url"`
	RAGInternalSecret               string           `json:"-"`
	RAGUserID                       uint             `json:"rag_user_id"`
	RAGOrgTag                       string           `json:"rag_org_tag"`
	RAGIngestPublic                 bool             `json:"rag_ingest_public"`
	RAGSourceID                     string           `json:"rag_source_id"`
	RAGSourcePathPrefix             string           `json:"rag_source_path_prefix"`
	RAGSourceURL                    string           `json:"rag_source_url"`
	RAGSourceCommit                 string           `json:"rag_source_commit"`
	RAGTargetIndex                  string           `json:"rag_target_index"`
	RAGCorpusGeneration             string           `json:"rag_corpus_generation"`
	RAGIngestRunID                  string           `json:"rag_ingest_run_id"`
	ThinkingEnabled                 bool             `json:"thinking_enabled"`
}

// SandboxConfig is the persisted policy for Docker-backed Bash execution.
// It intentionally has no host-fallback or network-enable option.
type SandboxConfig struct {
	Backend             sandbox.Backend `json:"backend"`
	WSLDistro           string          `json:"wsl_distro"`
	TrustRoot           string          `json:"trust_root"`
	Enabled             bool            `json:"enabled"`
	Image               string          `json:"image"`
	AllowWorkspaceWrite bool            `json:"allow_workspace_write"`
	MemoryLimit         string          `json:"memory_limit"`
	CPULimit            string          `json:"cpu_limit"`
	PidsLimit           int             `json:"pids_limit"`
	TmpfsSize           string          `json:"tmpfs_size"`
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

	sandboxDefaults := sandbox.DefaultConfig()
	return Config{
		ProjectRoot:                     projectRoot,
		WorkingDir:                      workingDir,
		Model:                           "gpt-4o",
		ModelFast:                       "gpt-5.5-openai-compact",
		ContextWindow:                   256000,
		MaxTokensPerSession:             1_000_000,
		MaxCostPerSession:               5.0,
		OrchestratorAddr:                "127.0.0.1:50051",
		OrchestratorAutoStart:           true,
		OrchestratorCommand:             "python",
		OrchestratorArgs:                []string{"-m", "orchestrator.server"},
		OrchestratorStartupTimeout:      5,
		OrchestratorConversationTimeout: 300,
		ActorID:                         "actor:local",
		ActorSubject:                    "local",
		ActorTenantID:                   "tenant:local",
		ActorRoles:                      []string{"LOCAL"},
		SessionDBPath:                   filepath.Join(projectRoot, ".agent", "sessions", "sessions.sqlite"),
		Sandbox: SandboxConfig{
			Backend:             sandbox.BackendAuto,
			WSLDistro:           sandbox.DefaultConfig().WSLDistro,
			TrustRoot:           projectRoot,
			Enabled:             true,
			Image:               sandboxDefaults.Image,
			AllowWorkspaceWrite: sandboxDefaults.AllowWorkspaceWrite,
			MemoryLimit:         sandboxDefaults.MemoryLimit,
			CPULimit:            sandboxDefaults.CPULimit,
			PidsLimit:           sandboxDefaults.PidsLimit,
			TmpfsSize:           sandboxDefaults.TmpfsSize,
		},
		MCPConfig:       ".mcp.json",
		MemoryDir:       filepath.Join(projectRoot, ".agent", "memory"),
		WorktreeBaseRef: "fresh",
		RAGServerURL:    "http://127.0.0.1:8081",
		ThinkingEnabled: true,
	}
}

func Load(projectRoot string) (Config, error) {
	cfg := Default(projectRoot)

	if err := applyDotenvModel(filepath.Join(cfg.ProjectRoot, ".env.local"), &cfg); err != nil {
		return Config{}, err
	}
	applyThinkingEnv(&cfg)
	applyRAGSecretEnv(&cfg)

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
	applyModelEnv(&cfg)

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

func applyModelEnv(cfg *Config) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER")))
	keys := []string{}
	switch provider {
	case "anthropic":
		keys = []string{"ANTHROPIC_MODEL"}
	case "local":
		keys = []string{"LOCAL_LLM_MODEL"}
	case "openai":
		keys = []string{"OPENAI_MODEL"}
	default:
		keys = []string{"OPENAI_MODEL", "ANTHROPIC_MODEL", "LOCAL_LLM_MODEL"}
	}
	for _, key := range keys {
		if model := strings.TrimSpace(os.Getenv(key)); model != "" {
			cfg.Model = model
			break
		}
	}
	if modelFast, ok := os.LookupEnv("MODEL_FAST"); ok {
		cfg.ModelFast = strings.TrimSpace(modelFast)
	}
}

func applyThinkingEnv(cfg *Config) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("THINKING_ENABLED")))
	if raw == "" {
		return
	}
	cfg.ThinkingEnabled = raw == "1" || raw == "true" || raw == "yes"
}

func applyRAGSecretEnv(cfg *Config) {
	cfg.RAGInternalSecret = strings.TrimSpace(os.Getenv("CODE_AGENT_RAG_INTERNAL_SECRET"))
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
	if patch.ActorID != "" {
		dst.ActorID = patch.ActorID
	}
	if patch.ActorSubject != "" {
		dst.ActorSubject = patch.ActorSubject
	}
	if patch.ActorTenantID != "" {
		dst.ActorTenantID = patch.ActorTenantID
	}
	if len(patch.ActorRoles) > 0 {
		dst.ActorRoles = append([]string(nil), patch.ActorRoles...)
	}
	if patch.SessionDBPath != "" {
		dst.SessionDBPath = patch.SessionDBPath
	}
	if rawSandbox, ok := raw["sandbox"]; ok {
		mergeSandboxConfig(&dst.Sandbox, patch.Sandbox, rawSandbox)
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
	if patch.RAGUserID != 0 {
		dst.RAGUserID = patch.RAGUserID
	}
	if patch.RAGOrgTag != "" {
		dst.RAGOrgTag = patch.RAGOrgTag
	}
	if _, ok := raw["rag_ingest_public"]; ok {
		dst.RAGIngestPublic = patch.RAGIngestPublic
	}
	if patch.RAGSourceID != "" {
		dst.RAGSourceID = patch.RAGSourceID
	}
	if patch.RAGSourcePathPrefix != "" {
		dst.RAGSourcePathPrefix = patch.RAGSourcePathPrefix
	}
	if patch.RAGSourceURL != "" {
		dst.RAGSourceURL = patch.RAGSourceURL
	}
	if patch.RAGSourceCommit != "" {
		dst.RAGSourceCommit = patch.RAGSourceCommit
	}
	if patch.RAGTargetIndex != "" {
		dst.RAGTargetIndex = patch.RAGTargetIndex
	}
	if patch.RAGCorpusGeneration != "" {
		dst.RAGCorpusGeneration = patch.RAGCorpusGeneration
	}
	if patch.RAGIngestRunID != "" {
		dst.RAGIngestRunID = patch.RAGIngestRunID
	}
	if _, ok := raw["thinking_enabled"]; ok {
		dst.ThinkingEnabled = patch.ThinkingEnabled
	}
	if _, ok := raw["skill_directories"]; ok {
		dst.SkillDirectories = append([]string(nil), patch.SkillDirectories...)
	}
}

func mergeSandboxConfig(dst *SandboxConfig, patch SandboxConfig, raw json.RawMessage) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return
	}
	if _, ok := fields["enabled"]; ok {
		dst.Enabled = patch.Enabled
	}
	if _, ok := fields["backend"]; ok {
		dst.Backend = patch.Backend
	}
	if _, ok := fields["wsl_distro"]; ok {
		dst.WSLDistro = patch.WSLDistro
	}
	if _, ok := fields["trust_root"]; ok {
		dst.TrustRoot = patch.TrustRoot
	}
	if _, ok := fields["image"]; ok {
		dst.Image = patch.Image
	}
	if _, ok := fields["allow_workspace_write"]; ok {
		dst.AllowWorkspaceWrite = patch.AllowWorkspaceWrite
	}
	if _, ok := fields["memory_limit"]; ok {
		dst.MemoryLimit = patch.MemoryLimit
	}
	if _, ok := fields["cpu_limit"]; ok {
		dst.CPULimit = patch.CPULimit
	}
	if _, ok := fields["pids_limit"]; ok {
		dst.PidsLimit = patch.PidsLimit
	}
	if _, ok := fields["tmpfs_size"]; ok {
		dst.TmpfsSize = patch.TmpfsSize
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
