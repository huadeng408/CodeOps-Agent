package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"code-agent/internal/orchestrator"
)

// SessionMemory is a Harness-owned extension. Its durable writes must target
// the Session Ledger, and recall must enforce the supplied authenticated owner.
type SessionMemory interface {
	Commit(context.Context, string) error
	Recall(context.Context, uint, string, int) (string, error)
}

func (r *SessionRunner) commitSessionMemory(ctx context.Context, key runKey) {
	if r.options.Memory == nil {
		return
	}
	commitCtx, commitCancel := context.WithTimeout(ctx, 5*time.Second)
	err := r.options.Memory.Commit(commitCtx, key.sessionID)
	commitCancel()
	if err == nil {
		r.workbench.signal(key.sessionID)
		return
	}
	// Task completion and memory completion are separate outcomes. Record a
	// fixed, retryable category without changing or leaking the terminal error.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		events, err := r.workbench.ledger.Events(ctx, key.sessionID)
		if err != nil {
			return
		}
		_, err = r.workbench.ledger.Append(ctx, key.sessionID, int64(len(events)), "memory/commit-blocked", map[string]any{
			"schema_version": 1, "run_id": key.runID, "error_code": "memory_commit_unavailable", "retryable": true,
		})
		if err == nil {
			r.workbench.signal(key.sessionID)
			return
		}
		if !errors.Is(err, ErrSequenceConflict) {
			return
		}
	}
}

func (r *SessionRunner) recallSessionMemory(ctx context.Context, events []Event, call orchestrator.ToolCall) orchestrator.ToolResult {
	result := orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, ExitCode: 1}
	if r.options.Memory == nil {
		result.Error = "session memory is unavailable"
		return result
	}
	var args struct {
		Query     string `json:"query"`
		MaxTokens int    `json:"max_tokens"`
	}
	decoder := json.NewDecoder(strings.NewReader(call.ParametersJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		result.Error = "invalid memory recall parameters"
		return result
	}
	view, err := reduceSessionView(events)
	if err != nil || view.Status == "deleted" {
		result.Error = "session memory owner is unavailable"
		return result
	}
	output, err := r.options.Memory.Recall(ctx, view.UserID, args.Query, args.MaxTokens)
	if err != nil {
		result.Error = "session memory recall is unavailable"
		return result
	}
	result.Output, result.ExitCode = output, 0
	return result
}
