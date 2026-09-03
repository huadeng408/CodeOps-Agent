package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"code-agent/internal/tools"
)

type jobE2EView struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func TestProductionBackgroundJobLifecycleE2E(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_JOBS_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_JOBS_E2E=1 to run the real background-job lifecycle E2E")
	}
	repositoryRoot := e2ERepositoryRoot(t)
	projectRoot := t.TempDir()
	runStartedAt := time.Now()
	executor := tools.NewExecutor(projectRoot)
	t.Cleanup(func() { _ = executor.Close() })

	body := "Write-Output JOB_E2E_START; Start-Sleep -Milliseconds 80; Write-Output JOB_E2E_DONE"
	if runtime.GOOS != "windows" {
		body = "printf 'JOB_E2E_START\\n'; sleep 0.08; printf 'JOB_E2E_DONE\\n'"
	}
	started, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobStart",
		OwnerSessionID: "job-e2e-session",
		Arguments:      map[string]any{"command": body, "kind": "e2e", "label": "lifecycle"},
	})
	if err != nil {
		t.Fatalf("start real background process: %v", err)
	}
	var start jobE2EView
	if err := json.Unmarshal([]byte(started.Output), &start); err != nil {
		t.Fatalf("decode start snapshot: %v", err)
	}
	if start.ID == "" || start.Status != "running" {
		t.Fatalf("unexpected start snapshot: %+v", start)
	}

	output, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobOutput",
		OwnerSessionID: "job-e2e-session",
		Arguments:      map[string]any{"job_id": start.ID, "wait": true, "timeout_ms": 5000},
	})
	if err != nil {
		t.Fatalf("wait/read real background process: %v", err)
	}
	if !strings.Contains(output.Output, "JOB_E2E_START") || !strings.Contains(output.Output, "JOB_E2E_DONE") || !strings.Contains(output.Output, `"status":"completed"`) {
		t.Fatalf("unexpected completed job output: %q", output.Output)
	}

	longBody := "Start-Sleep -Seconds 30"
	if runtime.GOOS != "windows" {
		longBody = "sleep 30"
	}
	long, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobStart",
		OwnerSessionID: "job-e2e-session",
		Arguments:      map[string]any{"command": longBody, "kind": "e2e", "label": "cancel"},
	})
	if err != nil {
		t.Fatalf("start cancellable process: %v", err)
	}
	var longView jobE2EView
	if err := json.Unmarshal([]byte(long.Output), &longView); err != nil {
		t.Fatalf("decode cancellable job: %v", err)
	}
	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobKill",
		OwnerSessionID: "other-session",
		Arguments:      map[string]any{"job_id": longView.ID},
	}); err == nil {
		t.Fatal("other session cancelled the job")
	}
	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobKill",
		OwnerSessionID: "job-e2e-session",
		Arguments:      map[string]any{"job_id": longView.ID, "reason": "e2e cleanup"},
	}); err != nil {
		t.Fatalf("cancel real process: %v", err)
	}
	waited, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobWait",
		OwnerSessionID: "job-e2e-session",
		Arguments:      map[string]any{"job_id": longView.ID, "timeout_ms": 5000},
	})
	if err != nil || !strings.Contains(waited.Output, `"status":"killed"`) {
		t.Fatalf("cancellable process did not settle as killed: err=%v output=%q", err, waited.Output)
	}

	receipt := map[string]any{
		"status":           "VERIFIED",
		"git_sha":          productionGitSHA(t, repositoryRoot),
		"session_id":       "job-e2e-session",
		"completed_job":    start.ID,
		"completed_status": "completed",
		"killed_job":       longView.ID,
		"killed_status":    "killed",
		"owner_isolation":  true,
		"runner":           "Go Executor -> internal/jobs -> real child process",
		"duration_ms":      time.Since(runStartedAt).Milliseconds(),
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode job E2E receipt: %v", err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "job-process-lifecycle.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o700); err != nil {
		t.Fatalf("create job E2E receipt directory: %v", err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write job E2E receipt: %v", err)
	}
}
