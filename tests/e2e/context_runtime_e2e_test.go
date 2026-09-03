package e2e_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"code-agent/internal/orchestrator"
	"code-agent/internal/tools"

	_ "modernc.org/sqlite"
)

const contextE2ESessionID = "context-runtime-e2e"

func TestContextRuntimeSurvivesOrchestratorRestart(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_CONTEXT_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_CONTEXT_E2E=1 to run the cross-language runtime E2E")
	}

	repositoryRoot := contextE2ERepositoryRoot(t)
	python := contextE2EPython(t)
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "context_runtime_server.py")
	projectRoot := t.TempDir()
	address := contextE2EAddress(t)

	pythonPath := repositoryRoot
	if inherited := os.Getenv("PYTHONPATH"); inherited != "" {
		pythonPath += string(os.PathListSeparator) + inherited
	}
	t.Setenv("PYTHONPATH", pythonPath)

	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address:             address,
		AutoStart:           true,
		Command:             python,
		Args:                []string{fixture},
		ProjectRoot:         projectRoot,
		WorkingDir:          projectRoot,
		MemoryDir:           filepath.Join(projectRoot, ".agent", "memory"),
		StartupTimeout:      15 * time.Second,
		ConversationTimeout: 30 * time.Second,
	})
	t.Cleanup(manager.Stop)

	client, err := manager.Client(context.Background())
	if err != nil {
		t.Fatalf("start real Python orchestrator: %v", err)
	}

	executor := tools.NewExecutor(projectRoot)
	var toolCalls atomic.Int32
	handler := contextE2EToolHandler(executor, &toolCalls)
	response, err := client.ConverseWithHistory(
		context.Background(),
		"CREATE_CONTEXT_RECOVERY_MARKER",
		contextE2ESessionID,
		nil,
		handler,
	)
	if err != nil {
		t.Fatalf("initial conversation: %v", err)
	}
	firstProcessID := contextE2EProcessID(t, response, "WRITE_COMPLETE:")

	markerPath := filepath.Join(projectRoot, "runtime", "context-recovery.txt")
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read file written by Go tool executor: %v", err)
	}
	if string(marker) != "context recovery marker\n" {
		t.Fatalf("marker content = %q", marker)
	}
	if toolCalls.Load() != 1 {
		t.Fatalf("tool calls before restart = %d, want 1", toolCalls.Load())
	}

	restartedClient, err := manager.Restart(context.Background())
	if err != nil {
		t.Fatalf("restart real Python orchestrator: %v", err)
	}
	recovery, err := restartedClient.ConverseWithHistory(
		context.Background(),
		"RESUME_AFTER_RESTART",
		contextE2ESessionID,
		nil,
		handler,
	)
	if err != nil {
		t.Fatalf("recovery conversation: %v", err)
	}
	restartedProcessID := contextE2EProcessID(t, recovery, "RECOVERY_CONFIRMED:")
	if restartedProcessID == firstProcessID {
		t.Fatalf("orchestrator process did not change across restart: pid=%d", firstProcessID)
	}
	if toolCalls.Load() != 1 {
		t.Fatalf("recovery unexpectedly executed another tool; calls = %d", toolCalls.Load())
	}
	markerAfterRestart, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read tool-created file after orchestrator restart: %v", err)
	}
	if string(markerAfterRestart) != "context recovery marker\n" {
		t.Fatalf("marker content after restart = %q", markerAfterRestart)
	}

	contextE2EAssertSQLite(t, filepath.Join(projectRoot, ".agent", "context.sqlite"))
}

func TestContextCompactionRuntimeAcrossProcesses(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_CONTEXT_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_CONTEXT_E2E=1 to run the cross-language runtime E2E")
	}

	repositoryRoot := contextE2ERepositoryRoot(t)
	python := contextE2EPython(t)
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "context_runtime_server.py")
	projectRoot := t.TempDir()
	address := contextE2EAddress(t)
	pythonPath := repositoryRoot
	if inherited := os.Getenv("PYTHONPATH"); inherited != "" {
		pythonPath += string(os.PathListSeparator) + inherited
	}
	t.Setenv("PYTHONPATH", pythonPath)

	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address:             address,
		AutoStart:           true,
		Command:             python,
		Args:                []string{fixture, "--context-window", "512"},
		ProjectRoot:         projectRoot,
		WorkingDir:          projectRoot,
		MemoryDir:           filepath.Join(projectRoot, ".agent", "memory"),
		StartupTimeout:      15 * time.Second,
		ConversationTimeout: 30 * time.Second,
	})
	t.Cleanup(manager.Stop)

	client, err := manager.Client(context.Background())
	if err != nil {
		t.Fatalf("start real Python orchestrator: %v", err)
	}
	history := make([]orchestrator.ConversationMessage, 0, 24)
	for index := 0; index < 24; index++ {
		history = append(history, orchestrator.ConversationMessage{
			Role:    "user",
			Content: fmt.Sprintf("historical context %02d %s", index, strings.Repeat("h", 160)),
		})
	}
	response, err := client.ConverseWithHistory(
		context.Background(),
		"COMPACTION_CHECK",
		"context-compaction-e2e",
		history,
	)
	if err != nil {
		t.Fatalf("compaction conversation: %v", err)
	}
	if !strings.HasPrefix(response, "COMPACTION_OK:") {
		t.Fatalf("response = %q, want compaction confirmation", response)
	}

	databasePath := filepath.Join(projectRoot, ".agent", "context.sqlite")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open compaction context SQLite: %v", err)
	}
	defer database.Close()
	for _, kind := range []string{"compaction/start", "compaction/summary", "compaction/end"} {
		var count int
		if err := database.QueryRow(
			"SELECT COUNT(*) FROM context_events WHERE session_id = ? AND kind = ?",
			"context-compaction-e2e",
			kind,
		).Scan(&count); err != nil {
			t.Fatalf("count %s events: %v", kind, err)
		}
		if count == 0 {
			t.Fatalf("no persisted %s event", kind)
		}
	}
	gitSHA := contextE2EGitSHA(t, repositoryRoot)
	receipt := map[string]any{
		"status":         "VERIFIED",
		"git_sha":        gitSHA,
		"session_id":     "context-compaction-e2e",
		"response_pid":   contextE2EProcessID(t, response, "COMPACTION_OK:"),
		"context_window": 512,
		"event_kinds":    []string{"compaction/start", "compaction/summary", "compaction/end"},
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode compaction E2E receipt: %v", err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "context-compaction-process-recovery.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatalf("create compaction receipt directory: %v", err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write compaction E2E receipt: %v", err)
	}
}

func contextE2EProcessID(t *testing.T, response, prefix string) int {
	t.Helper()
	raw, found := strings.CutPrefix(response, prefix)
	if !found {
		t.Fatalf("response = %q, want prefix %q", response, prefix)
	}
	processID, err := strconv.Atoi(raw)
	if err != nil || processID <= 0 {
		t.Fatalf("response process ID = %q, want positive integer", raw)
	}
	return processID
}

func contextE2EToolHandler(executor *tools.Executor, calls *atomic.Int32) orchestrator.ToolHandler {
	return func(ctx context.Context, call orchestrator.ToolCall) orchestrator.ToolResult {
		calls.Add(1)
		arguments := map[string]any{}
		if err := json.Unmarshal([]byte(call.ParametersJSON), &arguments); err != nil {
			return orchestrator.ToolResult{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Error:      fmt.Sprintf("decode tool parameters: %v", err),
				ExitCode:   1,
			}
		}
		result, executeErr := executor.Execute(ctx, tools.ToolRequest{
			Name:      call.Name,
			Arguments: arguments,
		})
		converted := orchestrator.ToolResult{
			ToolCallID: call.ID,
			ToolName:   result.Name,
			Output:     result.Output,
			Error:      result.Error,
			ExitCode:   int32(result.ExitCode),
			Truncated:  result.Truncated,
		}
		if result.Spill != nil {
			converted.Spill = &orchestrator.SpillRef{
				Locator: result.Spill.Locator,
				SHA256:  result.Spill.SHA256,
				Bytes:   result.Spill.Bytes,
			}
		}
		if executeErr != nil && converted.Error == "" {
			converted.Error = executeErr.Error()
			converted.ExitCode = 1
		}
		return converted
	}
}

func contextE2EAssertSQLite(t *testing.T, databasePath string) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open context SQLite: %v", err)
	}
	defer database.Close()

	var integrity string
	if err := database.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatalf("check context SQLite integrity: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("context SQLite integrity = %q", integrity)
	}

	for _, kind := range []string{"tool_call", "file_diff", "reflection"} {
		var count int
		err := database.QueryRow(
			"SELECT COUNT(*) FROM context_events WHERE session_id = ? AND kind = ?",
			contextE2ESessionID,
			kind,
		).Scan(&count)
		if err != nil {
			t.Fatalf("count %s events: %v", kind, err)
		}
		if count == 0 {
			t.Fatalf("no persisted %s event survived restart", kind)
		}
	}
}

func contextE2ERepositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func contextE2EPython(t *testing.T) string {
	t.Helper()
	candidates := []string{os.Getenv("PYTHON_EXECUTABLE"), "python", "python3"}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved
		}
	}
	t.Fatal("Python executable not found")
	return ""
}

func contextE2EGitSHA(t *testing.T, repositoryRoot string) string {
	t.Helper()
	command := exec.Command("git", "-C", repositoryRoot, "rev-parse", "HEAD")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("resolve current git SHA: %v", err)
	}
	sha := strings.TrimSpace(string(output))
	if len(sha) < 12 {
		t.Fatalf("git SHA is unexpectedly short: %q", sha)
	}
	return sha
}

func contextE2EAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate orchestrator port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release orchestrator port: %v", err)
	}
	return address
}
