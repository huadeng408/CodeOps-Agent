package e2e_test

import (
	"encoding/json"
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
}
