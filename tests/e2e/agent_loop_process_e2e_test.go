package e2e_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestProductionAgentLoopPluginE2E proves the plugin seam through the
// production Go agent, its gRPC client, and the Python orchestrator process.
func TestProductionAgentLoopPluginE2E(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_AGENT_LOOP_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_AGENT_LOOP_E2E=1 to run the agent loop plugin E2E")
	}
	repositoryRoot := e2ERepositoryRoot(t)
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agent"), 0o755); err != nil {
		t.Fatalf("create project root: %v", err)
	}
	address := contextE2EAddress(t)
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split orchestrator address: %v", err)
	}
	python := contextE2EPython(t)
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "context_runtime_server.py")
	receiptPath := filepath.Join(t.TempDir(), "agent-loop-phases.jsonl")
	hookReceiptPath := filepath.Join(t.TempDir(), "hook-phases.jsonl")
	settings, err := json.Marshal(map[string]any{
		"session_db_path":                           filepath.Join(projectRoot, ".agent", "sessions", "sessions.sqlite"),
		"memory_dir":                                filepath.Join(projectRoot, ".agent", "memory"),
		"orchestrator_addr":                         address,
		"orchestrator_auto_start":                   false,
		"orchestrator_conversation_timeout_seconds": 30,
		"mcp_config":                                "missing-mcp.json",
		"sandbox":                                   map[string]any{"enabled": false},
		"permissions": map[string]any{
			"allow": []map[string]string{{"tool": "Write"}},
		},
	})
	if err != nil {
		t.Fatalf("encode settings: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(projectRoot, ".agent", "settings.local.json")), 0o755); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".agent", "settings.local.json"), settings, 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	server := exec.Command(python, fixture, "--host", host, "--port", port, "--project-root", projectRoot, "--working-dir", projectRoot, "--memory-dir", filepath.Join(projectRoot, ".agent", "memory"))
	server.Dir = repositoryRoot
	server.Env = append(os.Environ(),
		"PYTHONPATH="+repositoryRoot,
		"CODE_AGENT_AGENT_LOOP_E2E_RECEIPT="+receiptPath,
		"CODE_AGENT_HOOK_E2E_RECEIPT="+hookReceiptPath,
	)
	var serverOutput strings.Builder
	server.Stdout = &serverOutput
	server.Stderr = &serverOutput
	if err := server.Start(); err != nil {
		t.Fatalf("start Python orchestrator: %v", err)
	}
	t.Cleanup(func() {
		if server.Process != nil {
			_ = server.Process.Kill()
		}
		_ = server.Wait()
	})
	waitForTCP(t, address, 20*time.Second, &serverOutput)

	agentBinary := buildProductionAgent(t, repositoryRoot)
	agent := exec.Command(agentBinary)
	agent.Dir = projectRoot
	agent.Stdin = strings.NewReader("CREATE_CONTEXT_RECOVERY_MARKER\n")
	output, err := agent.CombinedOutput()
	if err != nil {
		t.Fatalf("production Go agent failed: %v; output=%q", err, string(output))
	}
	if !strings.Contains(string(output), "WRITE_COMPLETE:") {
		t.Fatalf("production agent did not complete tool round: %q", string(output))
	}
	phases, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("read plugin phase receipt: %v", err)
	}
	want := "loop_start\nmodel_before\nmodel_after\ntool_before\ntool_after\nmodel_before\nmodel_after\nloop_end\n"
	if string(phases) != want {
		t.Fatalf("plugin phases = %q, want %q", string(phases), want)
	}
	hookPhases, err := os.ReadFile(hookReceiptPath)
	if err != nil {
		t.Fatalf("read hook phase receipt: %v", err)
	}
	hookWant := "session_start:\npre_step:\npost_model:\npre_tool:Write\npost_tool:Write\npre_step:\npost_model:\nturn_stopping:\nsession_end:\n"
	if string(hookPhases) != hookWant {
		t.Fatalf("hook phases = %q, want %q", string(hookPhases), hookWant)
	}
	hookDigest := sha256.Sum256(hookPhases)
	receipt := map[string]any{
		"status":                      "VERIFIED",
		"git_sha":                     contextE2EGitSHA(t, repositoryRoot),
		"run_id":                      "agent-loop-hook-process-e2e",
		"command":                     "CODE_AGENT_RUN_AGENT_LOOP_E2E=1 go test ./tests/e2e -run TestProductionAgentLoopPluginE2E -count=1",
		"exit_code":                   0,
		"hook_schema_version":         "1",
		"hook_phase_count":            9,
		"hook_phase_sha256":           fmt.Sprintf("%x", hookDigest),
		"go_agent_pid":                agent.Process.Pid,
		"python_orchestrator_pid":     server.Process.Pid,
		"go_harness_grpc_boundary":    true,
		"python_orchestrator_process": true,
		"tool_name":                   "Write",
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode hook E2E receipt: %v", err)
	}
	persistedReceipt := filepath.Join(repositoryRoot, ".runtime", "e2e", "hook-process.json")
	if err := os.MkdirAll(filepath.Dir(persistedReceipt), 0o755); err != nil {
		t.Fatalf("create hook E2E receipt directory: %v", err)
	}
	if err := os.WriteFile(persistedReceipt, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write hook E2E receipt: %v", err)
	}
}

// TestProductionProviderRouteE2E proves that a request entering through the
// production Go agent is bound to one auditable Python provider/model route.
func TestProductionProviderRouteE2E(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_PROVIDER_ROUTE_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_PROVIDER_ROUTE_E2E=1 to run the provider route E2E")
	}
	repositoryRoot := e2ERepositoryRoot(t)
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agent"), 0o755); err != nil {
		t.Fatalf("create project root: %v", err)
	}
	address := contextE2EAddress(t)
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split orchestrator address: %v", err)
	}
	python := contextE2EPython(t)
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "context_runtime_server.py")
	receiptPath := filepath.Join(t.TempDir(), "provider-route.json")
	settings, err := json.Marshal(map[string]any{
		"session_db_path":                           filepath.Join(projectRoot, ".agent", "sessions", "sessions.sqlite"),
		"memory_dir":                                filepath.Join(projectRoot, ".agent", "memory"),
		"orchestrator_addr":                         address,
		"orchestrator_auto_start":                   false,
		"orchestrator_conversation_timeout_seconds": 30,
		"mcp_config":                                "missing-mcp.json",
		"sandbox":                                   map[string]any{"enabled": false},
		"permissions": map[string]any{
			"allow": []map[string]string{{"tool": "Write"}},
		},
	})
	if err != nil {
		t.Fatalf("encode settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".agent", "settings.local.json"), settings, 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	server := exec.Command(python, fixture, "--host", host, "--port", port, "--project-root", projectRoot, "--working-dir", projectRoot, "--memory-dir", filepath.Join(projectRoot, ".agent", "memory"))
	server.Dir = repositoryRoot
	server.Env = append(os.Environ(),
		"PYTHONPATH="+repositoryRoot,
		"CODE_AGENT_PROVIDER_ROUTE_E2E_RECEIPT="+receiptPath,
	)
	var serverOutput strings.Builder
	server.Stdout = &serverOutput
	server.Stderr = &serverOutput
	if err := server.Start(); err != nil {
		t.Fatalf("start Python orchestrator: %v", err)
	}
	t.Cleanup(func() {
		if server.Process != nil {
			_ = server.Process.Kill()
		}
		_ = server.Wait()
	})
	waitForTCP(t, address, 20*time.Second, &serverOutput)

	agentBinary := buildProductionAgent(t, repositoryRoot)
	agent := exec.Command(agentBinary)
	agent.Dir = projectRoot
	agent.Stdin = strings.NewReader("CREATE_CONTEXT_RECOVERY_MARKER\n")
	output, err := agent.CombinedOutput()
	if err != nil {
		t.Fatalf("production Go agent failed: %v; output=%q", err, string(output))
	}
	if !strings.Contains(string(output), "WRITE_COMPLETE:") {
		t.Fatalf("production agent did not complete routed tool round: %q", string(output))
	}
	rawReceipt, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("read provider route receipt: %v", err)
	}
	var receipt struct {
		Phase      string `json:"phase"`
		Provider   string `json:"provider"`
		Model      string `json:"model"`
		Generation int    `json:"generation"`
	}
	if err := json.Unmarshal(rawReceipt, &receipt); err != nil {
		t.Fatalf("decode provider route receipt: %v", err)
	}
	if receipt.Phase != "model_before" || receipt.Provider != "route-e2e" || receipt.Model != "deterministic-context-e2e" || receipt.Generation != 1 {
		t.Fatalf("unexpected provider route receipt: %+v", receipt)
	}
}

// TestProductionRealProviderRouteE2E is an explicit opt-in development smoke.
// It proves the production Go entrypoint reaches a real Python provider
// adapter, while keeping credentials and provider output out of the receipt.
func TestProductionRealProviderRouteE2E(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_REAL_PROVIDER_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_REAL_PROVIDER_E2E=1 to run the real provider E2E")
	}
	if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) == "" && strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")) == "" {
		t.Fatal("real provider E2E requires ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN")
	}
	repositoryRoot := e2ERepositoryRoot(t)
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agent"), 0o755); err != nil {
		t.Fatalf("create project root: %v", err)
	}
	address := contextE2EAddress(t)
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split orchestrator address: %v", err)
	}
	python := contextE2EPython(t)
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "context_runtime_server.py")
	receiptPath := filepath.Join(t.TempDir(), "provider-real-route.jsonl")
	settings, err := json.Marshal(map[string]any{
		"session_db_path":                           filepath.Join(projectRoot, ".agent", "sessions", "sessions.sqlite"),
		"memory_dir":                                filepath.Join(projectRoot, ".agent", "memory"),
		"orchestrator_addr":                         address,
		"orchestrator_auto_start":                   false,
		"orchestrator_conversation_timeout_seconds": 90,
		"mcp_config":                                "missing-mcp.json",
		"sandbox":                                   map[string]any{"enabled": false},
		"permissions":                               map[string]any{"allow": []map[string]string{}},
	})
	if err != nil {
		t.Fatalf("encode settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".agent", "settings.local.json"), settings, 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	server := exec.Command(python, fixture, "--host", host, "--port", port, "--project-root", projectRoot, "--working-dir", projectRoot, "--memory-dir", filepath.Join(projectRoot, ".agent", "memory"))
	server.Dir = repositoryRoot
	server.Env = append(os.Environ(),
		"PYTHONPATH="+repositoryRoot,
		"CODE_AGENT_E2E_PROVIDER=anthropic",
		"CODE_AGENT_PROVIDER_ROUTE_E2E_RECEIPT="+receiptPath,
	)
	var serverOutput strings.Builder
	server.Stdout = &serverOutput
	server.Stderr = &serverOutput
	if err := server.Start(); err != nil {
		t.Fatalf("start Python orchestrator: %v", err)
	}
	t.Cleanup(func() {
		if server.Process != nil {
			_ = server.Process.Kill()
		}
		_ = server.Wait()
	})
	waitForTCP(t, address, 20*time.Second, &serverOutput)

	agentBinary := buildProductionAgent(t, repositoryRoot)
	agent := exec.Command(agentBinary)
	agent.Dir = projectRoot
	agent.Stdin = strings.NewReader("Reply with exactly REAL_PROVIDER_ROUTE_OK and nothing else.\n")
	output, err := agent.CombinedOutput()
	if err != nil {
		t.Fatalf("production Go agent failed: %v; output=%q", err, string(output))
	}
	if !strings.Contains(string(output), "REAL_PROVIDER_ROUTE_OK") {
		t.Fatalf("real provider response missing marker: %q", string(output))
	}

	lines := strings.Split(strings.TrimSpace(string(mustReadFile(t, receiptPath))), "\n")
	var before struct {
		Phase      string `json:"phase"`
		Provider   string `json:"provider"`
		Model      string `json:"model"`
		Generation int    `json:"generation"`
	}
	var after struct {
		Phase         string         `json:"phase"`
		Provider      string         `json:"provider"`
		Model         string         `json:"model"`
		Generation    int            `json:"generation"`
		ModelIdentity map[string]any `json:"model_identity"`
	}
	for _, line := range lines {
		var envelope struct {
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatalf("decode provider route event: %v", err)
		}
		switch envelope.Phase {
		case "model_before":
			if err := json.Unmarshal([]byte(line), &before); err != nil {
				t.Fatalf("decode provider before event: %v", err)
			}
		case "model_after":
			if err := json.Unmarshal([]byte(line), &after); err != nil {
				t.Fatalf("decode provider after event: %v", err)
			}
		}
	}
	if before.Phase != "model_before" || before.Provider != "route-e2e" || before.Generation != 1 || before.Model == "" {
		t.Fatalf("unexpected real provider before receipt: %+v", before)
	}
	if after.Phase != "model_after" || after.Provider != "route-e2e" || after.Model == "" || after.Generation != 1 {
		t.Fatalf("unexpected real provider after receipt: %+v", after)
	}
	reportedModel, _ := after.ModelIdentity["reported_model"].(string)
	responseID, _ := after.ModelIdentity["response_id"].(string)
	if reportedModel == "" || responseID == "" {
		t.Fatalf("real provider did not report model identity: %+v", after.ModelIdentity)
	}

	receipt := map[string]any{
		"status":                      "SMOKE_PASS",
		"non_release_dev_smoke":       true,
		"git_sha":                     contextE2EGitSHA(t, repositoryRoot),
		"run_id":                      "provider-real-route-process-e2e",
		"command":                     "CODE_AGENT_RUN_REAL_PROVIDER_E2E=1 go test ./tests/e2e -run TestProductionRealProviderRouteE2E -count=1",
		"exit_code":                   0,
		"provider":                    "anthropic-compatible",
		"requested_model":             after.Model,
		"reported_model":              reportedModel,
		"response_id":                 responseID,
		"identity_verified":           after.ModelIdentity["identity_verified"],
		"go_harness_grpc_boundary":    true,
		"python_orchestrator_process": true,
		"go_agent_pid":                agent.Process.Pid,
		"python_orchestrator_pid":     server.Process.Pid,
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode real provider receipt: %v", err)
	}
	persistedReceipt := filepath.Join(repositoryRoot, ".runtime", "e2e", "provider-real-route.json")
	if err := os.MkdirAll(filepath.Dir(persistedReceipt), 0o755); err != nil {
		t.Fatalf("create real provider receipt directory: %v", err)
	}
	if err := os.WriteFile(persistedReceipt, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write real provider receipt: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read provider route events: %v", err)
	}
	return data
}
