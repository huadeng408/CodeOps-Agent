package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-agent/internal/orchestrator"
	"code-agent/internal/tools"
)

// TestProductionToolSpillSurvivesPythonRoundTrip exercises the real Go
// Executor and real Python gRPC server. It is opt-in because it starts a
// subprocess and writes only an ignored runtime receipt.
func TestProductionToolSpillSurvivesPythonRoundTrip(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_TOOL_SPILL_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_TOOL_SPILL_E2E=1 to run the cross-process spill E2E")
	}
	repositoryRoot := e2ERepositoryRoot(t)
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "large.txt"), []byte(strings.Repeat("line\n", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	address := contextE2EAddress(t)
	python := contextE2EPython(t)
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "tool_spill_server.py")
	t.Setenv("PYTHONPATH", repositoryRoot)
	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address: address, AutoStart: true, Command: python, Args: []string{fixture},
		ProjectRoot: projectRoot, WorkingDir: projectRoot, MemoryDir: filepath.Join(projectRoot, ".agent", "memory"),
		StartupTimeout: 15 * time.Second, ConversationTimeout: 30 * time.Second,
	})
	t.Cleanup(manager.Stop)
	client, err := manager.Client(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(projectRoot)
	executor.MaxOutputLines = 0
	executor.MaxOutputBytes = 64
	response, err := client.Converse(context.Background(), "spill", func(ctx context.Context, call orchestrator.ToolCall) orchestrator.ToolResult {
		var args map[string]any
		if err := json.Unmarshal([]byte(call.ParametersJSON), &args); err != nil {
			return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: err.Error(), ExitCode: 1}
		}
		result, executeErr := executor.Execute(ctx, tools.ToolRequest{Name: call.Name, Arguments: args})
		converted := orchestrator.ToolResult{ToolCallID: call.ID, ToolName: result.Name, Output: result.Output, Error: result.Error, ExitCode: int32(result.ExitCode), Truncated: result.Truncated}
		if result.Spill != nil {
			converted.Spill = &orchestrator.SpillRef{Locator: result.Spill.Locator, SHA256: result.Spill.SHA256, Bytes: result.Spill.Bytes}
		}
		if executeErr != nil && converted.Error == "" {
			converted.Error, converted.ExitCode = executeErr.Error(), 1
		}
		return converted
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(response, "SPILL_ROUNDTRIP_OK:") {
		t.Fatalf("spill round trip failed: %q", response)
	}
	receipt := map[string]any{
		"kind":    "production-tool-spill-process-recovery",
		"git_sha": productionGitSHA(t, repositoryRoot),
		"command": "CODE_AGENT_RUN_TOOL_SPILL_E2E=1 go test ./tests/e2e -run TestProductionToolSpillSurvivesPythonRoundTrip -count=1",
		"result":  "SPILL_ROUNDTRIP_OK",
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "tool-spill-process-recovery.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
