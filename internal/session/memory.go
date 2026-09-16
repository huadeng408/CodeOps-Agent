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

type MemoryQuery struct {
	Query           string `json:"query"`
	MaxTokens       int    `json:"max_tokens,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Detail          string `json:"detail,omitempty"`
	SourceSessionID string `json:"-"`
}

type MemoryCommand struct {
	Action           string   `json:"action"`
	ID               string   `json:"id,omitempty"`
	Kind             string   `json:"kind,omitempty"`
	Key              string   `json:"key,omitempty"`
	Content          string   `json:"content,omitempty"`
	ExpectedRevision uint64   `json:"expected_revision,omitempty"`
	TTLSeconds       int64    `json:"ttl_seconds,omitempty"`
	Tags             []string `json:"tags,omitempty"`
}

type AdvancedSessionMemory interface {
	RecallWithOptions(context.Context, uint, MemoryQuery) (string, error)
	Manage(context.Context, string, MemoryCommand) (string, error)
}

func (r *SessionRunner) commitSessionMemory(ctx context.Context, key runKey) {
	if r.options.Memory == nil {
		return
	}
	if events, err := r.workbench.ledger.Events(ctx, key.sessionID); err == nil {
		if task, _, err := projectAgentTask(events); err != nil || task != nil && (task.Status == "input_required" || task.Status == "canceled") {
			return
		}
	}
	commitCtx, commitCancel := context.WithTimeout(ctx, 30*time.Second)
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

func projectionOwner(events []Event) uint {
	view, err := reduceSessionView(events)
	if err != nil {
		return 0
	}
	return view.UserID
}

func (r *SessionRunner) recallSessionMemory(ctx context.Context, events []Event, call orchestrator.ToolCall) orchestrator.ToolResult {
	result := orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, ExitCode: 1}
	if r.options.Memory == nil {
		result.Error = "session memory is unavailable"
		return result
	}
	var args MemoryQuery
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
	if task, _, taskErr := projectAgentTask(events); taskErr != nil {
		result.Error = "session memory scope is unavailable"
		return result
	} else if task != nil {
		args.SourceSessionID = view.ID
	}
	var output string
	if advanced, ok := r.options.Memory.(AdvancedSessionMemory); ok {
		output, err = advanced.RecallWithOptions(ctx, view.UserID, args)
	} else if args.Kind == "" && args.Detail == "" {
		output, err = r.options.Memory.Recall(ctx, view.UserID, args.Query, args.MaxTokens)
	} else {
		err = errors.New("memory options unavailable")
	}
	if err != nil {
		result.Error = "session memory recall is unavailable"
		return result
	}
	result.Output, result.ExitCode = output, 0
	return result
}

func (r *SessionRunner) manageSessionMemory(ctx context.Context, sessionID string, call orchestrator.ToolCall) orchestrator.ToolResult {
	result := orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, ExitCode: 1}
	advanced, ok := r.options.Memory.(AdvancedSessionMemory)
	if !ok {
		result.Error = "session memory management unavailable"
		return result
	}
	var command MemoryCommand
	decoder := json.NewDecoder(strings.NewReader(call.ParametersJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		result.Error = "invalid memory command"
		return result
	}
	output, err := advanced.Manage(ctx, sessionID, command)
	if err != nil {
		result.Error = "memory command rejected"
		return result
	}
	result.Output, result.ExitCode = output, 0
	return result
}
