package e2e_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionSkillManifestIsMetadataOnly(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_SKILLS_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_SKILLS_E2E=1 to run the production Skill manifest E2E")
	}
	repositoryRoot := e2ERepositoryRoot(t)
	projectRoot := filepath.Join(t.TempDir(), "project")
	skillDir := filepath.Join(projectRoot, ".agent", "skills", "release")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("create Skill directory: %v", err)
	}
	skillBody := "---\nname: release\ndescription: Project release workflow.\ntools: [Git, Bash]\n---\nPROJECT_SKILL_BODY_MUST_NOT_BE_IN_MANIFEST\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillBody), 0o644); err != nil {
		t.Fatalf("write project Skill: %v", err)
	}
	settings, err := json.Marshal(map[string]any{
		"session_db_path":         filepath.Join(projectRoot, ".agent", "sessions", "sessions.sqlite"),
		"memory_dir":              filepath.Join(projectRoot, ".agent", "memory"),
		"orchestrator_addr":       "127.0.0.1:1",
		"orchestrator_auto_start": false,
		"mcp_config":              "missing-mcp.json",
		"sandbox":                 map[string]any{"enabled": false},
	})
	if err != nil {
		t.Fatalf("encode settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".agent", "settings.local.json"), settings, 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	agentBinary := buildProductionAgent(t, repositoryRoot)
	agent := exec.Command(agentBinary)
	agent.Dir = projectRoot
	agent.Stdin = strings.NewReader("/skills\n")
	output, err := agent.CombinedOutput()
	if err != nil {
		t.Fatalf("production Go agent failed: %v; output=%q", err, string(output))
	}

	manifestPath := filepath.Join(projectRoot, ".agent", "skills.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read generated Skill manifest: %v", err)
	}
	var payload struct {
		Skills []struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Tools       []string `json:"tools"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(manifest, &payload); err != nil {
		t.Fatalf("decode Skill manifest: %v", err)
	}
	var release struct {
		Name        string
		Description string
		Tools       []string
	}
	for _, skill := range payload.Skills {
		if skill.Name == "release" {
			release.Name = skill.Name
			release.Description = skill.Description
			release.Tools = skill.Tools
		}
	}
	if release.Name != "release" || release.Description != "Project release workflow." || strings.Join(release.Tools, ",") != "Git,Bash" {
		t.Fatalf("project Skill metadata was not exported: %+v", release)
	}
	if strings.Contains(string(manifest), "PROJECT_SKILL_BODY_MUST_NOT_BE_IN_MANIFEST") || strings.Contains(string(manifest), `"prompt"`) {
		t.Fatalf("Skill manifest leaked instruction body: %q", string(manifest))
	}
	digest := sha256.Sum256(manifest)
	receipt := map[string]any{
		"status":                  "VERIFIED",
		"exit_code":               0,
		"kind":                    "production-skill-manifest",
		"git_sha":                 productionGitSHA(t, repositoryRoot),
		"command":                 "CODE_AGENT_RUN_SKILLS_E2E=1 go test ./tests/e2e -run TestProductionSkillManifestIsMetadataOnly -count=1",
		"go_agent_pid":            agent.ProcessState.Pid(),
		"skill_count":             len(payload.Skills),
		"project_skill":           release.Name,
		"manifest_sha256":         fmt.Sprintf("%x", digest),
		"metadata_only":           true,
		"instruction_body_leaked": false,
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode Skill manifest receipt: %v", err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "skills-manifest-process.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatalf("create Skill receipt directory: %v", err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write Skill receipt: %v", err)
	}
}
