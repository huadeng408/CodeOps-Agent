package tools_test

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"code-agent/internal/sandbox"
	"code-agent/internal/tools"
)

type jobsTestSandbox struct{}

func (jobsTestSandbox) Run(context.Context, sandbox.Request) (sandbox.Result, error) {
	return sandbox.Result{}, nil
}

type jobToolView struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func jobCommand(body string) string {
	if runtime.GOOS == "windows" {
		return body
	}
	return body
}

func TestExecutorBackgroundJobToolsRunAndWaitForRealProcess(t *testing.T) {
	root := t.TempDir()
	executor := tools.NewExecutor(root)
	body := "Write-Output first; Start-Sleep -Milliseconds 250; Write-Output second"
	if runtime.GOOS != "windows" {
		body = "printf 'first\\n'; sleep 0.25; printf 'second\\n'"
	}
	started, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobStart",
		OwnerSessionID: "session-a",
		Arguments:      map[string]any{"command": jobCommand(body), "kind": "shell", "label": "test"},
	})
	if err != nil {
		t.Fatalf("JobStart: %v", err)
	}
	var startView jobToolView
	if err := json.Unmarshal([]byte(started.Output), &startView); err != nil {
		t.Fatalf("decode JobStart output %q: %v", started.Output, err)
	}
	if startView.ID == "" || startView.Status != "running" {
		t.Fatalf("unexpected start view: %+v", startView)
	}

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobOutput",
		OwnerSessionID: "session-a",
		Arguments:      map[string]any{"job_id": startView.ID, "wait": true, "timeout_ms": 5000},
	})
	if err != nil {
		t.Fatalf("JobOutput wait: %v", err)
	}
	if !strings.Contains(result.Output, "first") || !strings.Contains(result.Output, "second") {
		t.Fatalf("unexpected waited output: %q", result.Output)
	}
	if !strings.Contains(result.Output, `"status":"completed"`) {
		t.Fatalf("waited output did not expose completed status: %q", result.Output)
	}
}

func TestExecutorBackgroundJobsEnforceOwnerAndCancellation(t *testing.T) {
	executor := tools.NewExecutor(t.TempDir())
	body := "Start-Sleep -Seconds 10"
	if runtime.GOOS != "windows" {
		body = "sleep 10"
	}
	started, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "job_start",
		OwnerSessionID: "alice",
		Arguments:      map[string]any{"command": body},
	})
	if err != nil {
		t.Fatalf("job_start: %v", err)
	}
	var view jobToolView
	if err := json.Unmarshal([]byte(started.Output), &view); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobKill",
		OwnerSessionID: "bob",
		Arguments:      map[string]any{"job_id": view.ID, "reason": "wrong owner"},
	}); err == nil {
		t.Fatal("other owner cancelled a job")
	}
	killed, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobKill",
		OwnerSessionID: "alice",
		Arguments:      map[string]any{"job_id": view.ID, "reason": "test cleanup"},
	})
	if err != nil {
		t.Fatalf("JobKill: %v", err)
	}
	if !strings.Contains(killed.Output, view.ID) {
		t.Fatalf("kill output missing id: %q", killed.Output)
	}
	waited, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobWait",
		OwnerSessionID: "alice",
		Arguments:      map[string]any{"job_id": view.ID, "timeout_ms": 3000},
	})
	if err != nil {
		t.Fatalf("JobWait: %v", err)
	}
	if !strings.Contains(waited.Output, `"status":"killed"`) {
		t.Fatalf("wait output did not expose killed status: %q", waited.Output)
	}
}

func TestExecutorInteractiveBackgroundJobAcceptsInput(t *testing.T) {
	executor := tools.NewExecutor(t.TempDir())
	body := "$line = [Console]::In.ReadLine(); Write-Output ('got:' + $line)"
	if runtime.GOOS != "windows" {
		body = "IFS= read -r line; printf 'got:%s\\n' \"$line\""
	}
	started, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobStart",
		OwnerSessionID: "session-a",
		Arguments:      map[string]any{"command": body, "interactive": true},
	})
	if err != nil {
		t.Fatalf("start interactive: %v", err)
	}
	var view jobToolView
	if err := json.Unmarshal([]byte(started.Output), &view); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobWrite",
		OwnerSessionID: "session-a",
		Arguments:      map[string]any{"job_id": view.ID, "input": "hello\n"},
	}); err != nil {
		t.Fatalf("write interactive input: %v", err)
	}
	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:           "JobOutput",
		OwnerSessionID: "session-a",
		Arguments:      map[string]any{"job_id": view.ID, "wait": true, "timeout_ms": 3000},
	})
	if err != nil {
		t.Fatalf("read interactive output: %v", err)
	}
	if !strings.Contains(result.Output, "got:hello") {
		t.Fatalf("interactive output = %q", result.Output)
	}
}

func TestExecutorJobWaitHonorsCallerCancellation(t *testing.T) {
	executor := tools.NewExecutor(t.TempDir())
	body := "Start-Sleep -Seconds 2"
	if runtime.GOOS != "windows" {
		body = "sleep 2"
	}
	started, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "JobStart", Arguments: map[string]any{"command": body},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var view jobToolView
	if err := json.Unmarshal([]byte(started.Output), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result, err := executor.Execute(ctx, tools.ToolRequest{
		Name: "JobOutput", Arguments: map[string]any{"job_id": view.ID, "wait": true},
	})
	if err != nil {
		t.Fatalf("deadline wait should return state: %v", err)
	}
	if !strings.Contains(result.Output, `"status":"running"`) {
		t.Fatalf("deadline wait output = %q", result.Output)
	}
	_, _ = executor.Execute(context.Background(), tools.ToolRequest{Name: "JobKill", Arguments: map[string]any{"job_id": view.ID}})
}

func TestExecutorBackgroundJobsDoNotBypassConfiguredSandbox(t *testing.T) {
	executor := tools.NewExecutor(t.TempDir())
	executor.SetSandbox(jobsTestSandbox{})
	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "JobStart",
		Arguments: map[string]any{"command": "echo should-not-run"},
	})
	if err == nil {
		t.Fatal("background job bypassed the configured sandbox")
	}
	if !strings.Contains(result.Error, "streaming sandbox") {
		t.Fatalf("unexpected sandbox refusal: %q", result.Error)
	}
}

func TestExecutorBackgroundJobDoesNotForwardCredentialEnvironment(t *testing.T) {
	t.Setenv("CODE_AGENT_TEST_SECRET_TOKEN", "must-not-reach-child")
	executor := tools.NewExecutor(t.TempDir())
	body := "if ($env:CODE_AGENT_TEST_SECRET_TOKEN) { exit 7 } else { Write-Output CLEAN_JOB_ENV }"
	if runtime.GOOS != "windows" {
		body = "if [ -n \"$CODE_AGENT_TEST_SECRET_TOKEN\" ]; then exit 7; else printf CLEAN_JOB_ENV; fi"
	}
	started, err := executor.Execute(context.Background(), tools.ToolRequest{Name: "JobStart", Arguments: map[string]any{"command": body}})
	if err != nil {
		t.Fatalf("JobStart: %v", err)
	}
	var view jobToolView
	if err := json.Unmarshal([]byte(started.Output), &view); err != nil {
		t.Fatalf("decode JobStart output %q: %v", started.Output, err)
	}
	result, err := executor.Execute(context.Background(), tools.ToolRequest{Name: "JobOutput", Arguments: map[string]any{"job_id": view.ID, "wait": true, "timeout_ms": 3000}})
	if err != nil || !strings.Contains(result.Output, "CLEAN_JOB_ENV") || !strings.Contains(result.Output, `"status":"completed"`) {
		t.Fatalf("credential environment reached background job: result=%+v err=%v", result, err)
	}
}
