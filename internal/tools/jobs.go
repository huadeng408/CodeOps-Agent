package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"code-agent/internal/jobs"
	"code-agent/internal/safety"
)

type jobOutputView struct {
	Text string        `json:"text"`
	Job  jobs.Snapshot `json:"job"`
}

func (e *Executor) jobsRegistry() (*jobs.Registry, error) {
	e.mu.Lock()
	registry := e.jobs
	e.mu.Unlock()
	if registry == nil {
		return nil, errors.New("background jobs are not configured")
	}
	return registry, nil
}

func (e *Executor) executeJobStart(ctx context.Context, req ToolRequest) (ToolResult, error) {
	command, ok := stringArg(req.Arguments, "command", "cmd")
	if !ok || strings.TrimSpace(command) == "" {
		return jobFailure("JobStart", "command is required")
	}
	if e.HasSandbox() {
		return jobFailure("JobStart", "background jobs require a streaming sandbox backend; synchronous sandbox refuses detached host processes")
	}
	if analysis := safety.NewAnalyzer().AnalyzeCommand(command); !analysis.Allowed {
		return jobFailure("JobStart", fmt.Sprintf("blocked command: %s", analysis.Reason))
	}
	workingDir, err := e.jobWorkingDir(req.Arguments)
	if err != nil {
		return jobFailure("JobStart", err.Error())
	}
	kind, _ := stringArg(req.Arguments, "kind")
	label, _ := stringArg(req.Arguments, "label")
	outputLimit, hasLimit, err := intArg(req.Arguments, "output_limit_bytes", "max_output_bytes")
	if err != nil {
		return jobFailure("JobStart", err.Error())
	}
	if hasLimit && outputLimit <= 0 {
		return jobFailure("JobStart", "output_limit_bytes must be positive")
	}
	spec := jobs.Spec{
		Kind:             strings.TrimSpace(kind),
		Label:            strings.TrimSpace(label),
		Owner:            req.OwnerSessionID,
		Command:          command,
		WorkingDir:       workingDir,
		OutputLimitBytes: outputLimit,
		Timeout:          durationArg(req.Arguments, 0, "timeout_seconds", "timeout"),
		Interactive:      boolArg(req.Arguments, "interactive", "pty"),
	}
	registry, err := e.jobsRegistry()
	if err != nil {
		return jobFailure("JobStart", err.Error())
	}
	snapshot, err := registry.Start(ctx, spec)
	if err != nil {
		return jobFailure("JobStart", err.Error())
	}
	return jobJSON("JobStart", snapshot)
}

func (e *Executor) executeJobOutput(ctx context.Context, req ToolRequest) (ToolResult, error) {
	registry, err := e.jobsRegistry()
	if err != nil {
		return jobFailure("JobOutput", err.Error())
	}
	id, ok := stringArg(req.Arguments, "job_id", "id")
	if !ok || strings.TrimSpace(id) == "" {
		return jobFailure("JobOutput", "job_id is required")
	}
	if boolArg(req.Arguments, "wait") {
		waitCtx, cancel, waitErr := jobWaitContext(ctx, req.Arguments)
		if waitErr != nil {
			return jobFailure("JobOutput", waitErr.Error())
		}
		_, err = registry.Wait(waitCtx, id, req.OwnerSessionID)
		cancel()
		if err != nil {
			return jobFailure("JobOutput", err.Error())
		}
	}
	read, err := registry.Read(id, req.OwnerSessionID)
	if err != nil {
		return jobFailure("JobOutput", err.Error())
	}
	return jobJSON("JobOutput", jobOutputView{Text: read.Text, Job: read.Snapshot})
}

func (e *Executor) executeJobWait(ctx context.Context, req ToolRequest) (ToolResult, error) {
	registry, err := e.jobsRegistry()
	if err != nil {
		return jobFailure("JobWait", err.Error())
	}
	id, ok := stringArg(req.Arguments, "job_id", "id")
	if !ok || strings.TrimSpace(id) == "" {
		return jobFailure("JobWait", "job_id is required")
	}
	waitCtx, cancel, waitErr := jobWaitContext(ctx, req.Arguments)
	if waitErr != nil {
		return jobFailure("JobWait", waitErr.Error())
	}
	snapshot, err := registry.Wait(waitCtx, id, req.OwnerSessionID)
	cancel()
	if err != nil {
		return jobFailure("JobWait", err.Error())
	}
	return jobJSON("JobWait", snapshot)
}

func (e *Executor) executeJobList(_ context.Context, req ToolRequest) (ToolResult, error) {
	registry, err := e.jobsRegistry()
	if err != nil {
		return jobFailure("JobList", err.Error())
	}
	return jobJSON("JobList", registry.List(req.OwnerSessionID))
}

func (e *Executor) executeJobKill(_ context.Context, req ToolRequest) (ToolResult, error) {
	registry, err := e.jobsRegistry()
	if err != nil {
		return jobFailure("JobKill", err.Error())
	}
	id, ok := stringArg(req.Arguments, "job_id", "id")
	if !ok || strings.TrimSpace(id) == "" {
		return jobFailure("JobKill", "job_id is required")
	}
	reason, _ := stringArg(req.Arguments, "reason")
	snapshot, err := registry.Kill(id, req.OwnerSessionID, reason)
	if err != nil {
		return jobFailure("JobKill", err.Error())
	}
	return jobJSON("JobKill", snapshot)
}

func (e *Executor) executeJobWrite(_ context.Context, req ToolRequest) (ToolResult, error) {
	registry, err := e.jobsRegistry()
	if err != nil {
		return jobFailure("JobWrite", err.Error())
	}
	id, ok := stringArg(req.Arguments, "job_id", "id")
	if !ok || strings.TrimSpace(id) == "" {
		return jobFailure("JobWrite", "job_id is required")
	}
	input, ok := stringArg(req.Arguments, "input", "data", "text")
	if !ok {
		return jobFailure("JobWrite", "input is required")
	}
	if err := registry.Write(id, req.OwnerSessionID, input); err != nil {
		return jobFailure("JobWrite", err.Error())
	}
	snapshot, err := registry.Get(id, req.OwnerSessionID)
	if err != nil {
		return jobFailure("JobWrite", err.Error())
	}
	return jobJSON("JobWrite", snapshot)
}

func (e *Executor) jobWorkingDir(args map[string]any) (string, error) {
	workingDir, _ := stringArg(args, "cwd", "working_dir")
	if strings.TrimSpace(workingDir) == "" {
		return e.currentWorkingDir()
	}
	return workspacePath(e.Root, workingDir)
}

func jobWaitContext(parent context.Context, args map[string]any) (context.Context, context.CancelFunc, error) {
	if parent == nil {
		parent = context.Background()
	}
	timeout := 30 * time.Second
	if raw, exists := args["timeout_ms"]; exists {
		value, ok := numberArg(raw)
		if !ok || value <= 0 {
			return nil, nil, errors.New("timeout_ms must be positive")
		}
		timeout = time.Duration(value * float64(time.Millisecond))
	} else if value := durationArg(args, 0, "timeout", "timeout_seconds"); value > 0 {
		timeout = value
	}
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(parent, timeout)
	return waitCtx, cancel, nil
}

func numberArg(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	default:
		return 0, false
	}
}

func jobJSON(name string, value any) (ToolResult, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return jobFailure(name, "encode job response: "+err.Error())
	}
	return ToolResult{Name: name, Output: string(data)}, nil
}

func jobFailure(name, message string) (ToolResult, error) {
	err := errors.New(message)
	return ToolResult{Name: name, Error: message, ExitCode: 1}, err
}
