package e2e_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestWorkflowCheckpointSurvivesPythonProcessTermination(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_WORKFLOW_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_WORKFLOW_E2E=1 to run the Python workflow process E2E")
	}

	repositoryRoot := contextE2ERepositoryRoot(t)
	python := contextE2EPython(t)
	projectRoot := t.TempDir()
	databasePath := filepath.Join(projectRoot, "workflows.sqlite")
	callsPath := filepath.Join(projectRoot, "worker-calls.log")
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "workflow_process_server.py")
	t.Setenv("PYTHONPATH", repositoryRoot)

	first := exec.Command(python, fixture, "--db", databasePath, "--calls", callsPath, "--wait")
	first.Dir = repositoryRoot
	first.Env = os.Environ()
	var firstOutputBuffer strings.Builder
	first.Stdout = &firstOutputBuffer
	first.Stderr = &firstOutputBuffer
	if err := first.Start(); err != nil {
		t.Fatalf("start first workflow process: %v", err)
	}
	if !waitForWorkflowEvent(t, databasePath, "done", "completed", 20*time.Second) ||
		!waitForWorkflowEvent(t, databasePath, "long", "running", 20*time.Second) {
		_ = first.Process.Kill()
		_ = first.Wait()
		t.Fatalf("long worker did not reach running checkpoint; events=%q output=%q", workflowEventSnapshot(databasePath), firstOutputBuffer.String())
	}
	if err := first.Process.Kill(); err != nil {
		t.Fatalf("terminate first workflow process: %v", err)
	}
	firstExit := first.Wait()
	if firstExit == nil {
		t.Fatal("first workflow process exited cleanly; termination recovery was not exercised")
	}

	second := exec.Command(python, fixture, "--db", databasePath, "--calls", callsPath)
	second.Dir = repositoryRoot
	second.Env = os.Environ()
	secondOutput, err := second.CombinedOutput()
	if err != nil {
		t.Fatalf("resume workflow process: %v; output=%s", err, secondOutput)
	}
	if !strings.Contains(string(secondOutput), "WORKFLOW_COMPLETED") {
		t.Fatalf("resume output = %q, want completion marker", secondOutput)
	}

	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatalf("read worker calls: %v", err)
	}
	if got := countWorkflowCall(string(calls), "done"); got != 1 {
		t.Fatalf("completed worker calls = %d, want 1", got)
	}
	if got := countWorkflowCall(string(calls), "long"); got != 2 {
		t.Fatalf("recovered worker calls = %d, want 2", got)
	}

	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open workflow checkpoint: %v", err)
	}
	defer database.Close()
	var stateJSON string
	if err := database.QueryRow(
		"SELECT state_json FROM workflow_checkpoints WHERE workflow_id = ?",
		"workflow-process-e2e",
	).Scan(&stateJSON); err != nil {
		t.Fatalf("read final workflow checkpoint: %v", err)
	}
	var checkpoint struct {
		State   string `json:"state"`
		Workers map[string]struct {
			State    string `json:"state"`
			Attempts int    `json:"attempts"`
		} `json:"workers"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &checkpoint); err != nil {
		t.Fatalf("decode final workflow checkpoint: %v", err)
	}
	if checkpoint.State != "completed" || checkpoint.Workers["done"].Attempts != 1 || checkpoint.Workers["long"].Attempts != 2 {
		t.Fatalf("unexpected final checkpoint: %s", stateJSON)
	}
	var recoveryEvents int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM workflow_events WHERE workflow_id = ? AND detail = ?",
		"workflow-process-e2e",
		"worker recovered from previous process",
	).Scan(&recoveryEvents); err != nil {
		t.Fatalf("count recovery events: %v", err)
	}
	if recoveryEvents != 1 {
		t.Fatalf("recovery event count = %d, want 1", recoveryEvents)
	}

	receipt := map[string]any{
		"status":                 "VERIFIED",
		"git_sha":                contextE2EGitSHA(t, repositoryRoot),
		"workflow_id":            "workflow-process-e2e",
		"first_process_pid":      first.Process.Pid,
		"second_process_pid":     second.ProcessState.Pid(),
		"completed_worker_calls": 1,
		"recovered_worker_calls": 2,
		"recovery_events":        recoveryEvents,
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode workflow receipt: %v", err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "workflow-process-recovery.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatalf("create workflow receipt directory: %v", err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write workflow receipt: %v", err)
	}
}

func countWorkflowCall(contents, workerID string) int {
	count := 0
	for _, value := range strings.Fields(contents) {
		if value == workerID {
			count++
		}
	}
	return count
}

func workflowEventSnapshot(databasePath string) string {
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return err.Error()
	}
	defer database.Close()
	rows, err := database.Query("SELECT worker_id, state, detail FROM workflow_events ORDER BY sequence")
	if err != nil {
		return err.Error()
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var workerID, state, detail string
		if err := rows.Scan(&workerID, &state, &detail); err != nil {
			return err.Error()
		}
		values = append(values, workerID+":"+state+":"+detail)
	}
	return strings.Join(values, "|")
}

func waitForWorkflowEvent(t *testing.T, databasePath, workerID, state string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		database, err := sql.Open("sqlite", databasePath)
		if err == nil {
			var count int
			err = database.QueryRow(
				"SELECT COUNT(*) FROM workflow_events WHERE workflow_id = ? AND worker_id = ? AND state = ?",
				"workflow-process-e2e",
				workerID,
				state,
			).Scan(&count)
			_ = database.Close()
			if err == nil && count > 0 {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
