package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"code-agent/internal/orchestrator"
	"code-agent/internal/sandbox"
)

const evaluationPolicyEvent = "evaluation/trial-policy"

type FrozenMemoryReference struct {
	SessionID string `json:"session_id"`
	EventID   string `json:"event_id"`
	Checksum  string `json:"checksum"`
}

// EvaluationPolicy is admitted by the Go entry point and frozen in the Session Ledger.
type EvaluationPolicy struct {
	ContextStrategy   string                  `json:"context_strategy"`
	MemoryRead        bool                    `json:"memory_read"`
	FrozenMemory      []FrozenMemoryReference `json:"frozen_memory"`
	ExecutionStrategy string                  `json:"execution_strategy"`
	MaxToolRounds     int                     `json:"max_tool_rounds"`
	ContextMaxBytes   int                     `json:"context_max_bytes"`
	FaultWindow       string                  `json:"fault_window,omitempty"`
	FaultTool         string                  `json:"fault_tool,omitempty"`
	ForceCompaction   bool                    `json:"force_compaction,omitempty"`
}

func (p EvaluationPolicy) Validate() error {
	if p.ContextStrategy != "metadata_on_demand" && p.ContextStrategy != "retrieval_pre_read" {
		return errors.New("unsupported evaluation Context strategy")
	}
	if p.ExecutionStrategy != "single" && p.ExecutionStrategy != "verify_retry" && p.ExecutionStrategy != "subagent" {
		return errors.New("unsupported evaluation execution strategy")
	}
	if p.MaxToolRounds < 1 || p.MaxToolRounds > 64 || p.ContextMaxBytes < 1 || p.ContextMaxBytes > 32000 || len(p.FrozenMemory) > 64 {
		return errors.New("invalid evaluation policy limits")
	}
	if p.FaultWindow != "" && p.FaultWindow != "before_effect" && p.FaultWindow != "after_effect" && p.FaultWindow != "after_record" {
		return errors.New("unsupported evaluation fault window")
	}
	for _, ref := range p.FrozenMemory {
		if ref.SessionID == "" || ref.EventID == "" || len(ref.Checksum) != 64 {
			return errors.New("invalid Frozen Memory reference")
		}
	}
	return nil
}

type EvaluationBinding struct {
	MemoryNamespace   string            `json:"memory_namespace,omitempty"`
	RuntimeContract   string            `json:"runtime_contract,omitempty"`
	SourceWorkspace   string            `json:"source_workspace,omitempty"`
	SchemaVersion     int               `json:"schema_version"`
	RunID             string            `json:"run_id"`
	TaskID            string            `json:"task_id"`
	Model             string            `json:"model"`
	Workspace         string            `json:"workspace"`
	MaxTokens         int64             `json:"max_tokens"`
	WallClockSeconds  float64           `json:"wall_clock_seconds"`
	PermissionsSHA256 string            `json:"permissions_sha256"`
	Policy            EvaluationPolicy  `json:"policy"`
	Runtime           EvaluationRuntime `json:"runtime"`
	Lifecycle         string            `json:"lifecycle,omitempty"`
}

func (binding EvaluationBinding) validSchema() bool {
	if binding.SchemaVersion == 3 {
		return binding.Policy.FaultWindow != "" && (binding.Runtime.ModelProfile.ResponseMode == "" || binding.Runtime.ModelProfile.ResponseMode == "sse" && binding.Runtime.ModelProfile.Protocol == "openai")
	}
	if binding.SchemaVersion == 1 {
		return binding.Runtime.ModelProfile.ResponseMode == ""
	}
	return binding.SchemaVersion == 2 && binding.Runtime.ModelProfile.ResponseMode == "sse" && binding.Runtime.ModelProfile.Protocol == "openai"
}

// EvaluationRuntime records the effective, Go-owned process configuration.
type EvaluationRuntime struct {
	TaskWorkspaceRoot           string                       `json:"task_workspace_root,omitempty"`
	OpenSandbox                 *sandbox.OpenSandboxIdentity `json:"opensandbox,omitempty"`
	ModelProfile                EvaluationModelProfile       `json:"model_profile"`
	OrchestratorCommand         string                       `json:"orchestrator_command"`
	StartupSeconds              int                          `json:"orchestrator_startup_seconds"`
	ConversationSeconds         int                          `json:"orchestrator_conversation_seconds,omitempty"`
	ContextWindow               int                          `json:"context_window"`
	VisibleVerificationCommands []string                     `json:"visible_verification_commands"`
	SandboxEnabled              bool                         `json:"sandbox_enabled"`
	SandboxBackend              string                       `json:"sandbox_backend"`
	SandboxImage                string                       `json:"sandbox_image"`
	SandboxToolchain            string                       `json:"sandbox_toolchain"`
	SandboxAllowWorkspaceWrite  bool                         `json:"sandbox_allow_workspace_write"`
	SandboxConfigSHA256         string                       `json:"sandbox_config_sha256"`
	AgentBaseRef                string                       `json:"agent_base_ref"`
	AgentBaseCommit             string                       `json:"agent_base_commit"`
}

type EvaluationModelProfile struct {
	ResponseMode          string `json:"response_mode,omitempty"`
	ProviderSessionSHA256 string `json:"provider_session_sha256,omitempty"`
	ExpectedReportedModel string `json:"expected_reported_model"`
	Protocol              string `json:"protocol"`
	Model                 string `json:"model"`
	ReasoningEffort       string `json:"reasoning_effort,omitempty"`
	InputLimit            int64  `json:"input_limit"`
	OutputLimit           int64  `json:"output_limit"`
	TotalLimit            int64  `json:"total_limit,omitempty"`
	RequestTimeoutSeconds int64  `json:"request_timeout_seconds"`
	EndpointSHA256        string `json:"endpoint_sha256"`
}

func BindEvaluation(ctx context.Context, ledger EventLog, id string, binding EvaluationBinding) error {
	if binding.Policy.ExecutionStrategy == "verify_retry" && len(binding.Runtime.VisibleVerificationCommands) == 0 {
		return ErrInvalidSessionInput
	}
	if !binding.validSchema() || binding.RunID == "" || binding.TaskID == "" || binding.Policy.Validate() != nil || binding.Lifecycle != "" && binding.Lifecycle != "hold" || binding.RuntimeContract != "" && binding.RuntimeContract != "production_opensandbox" || binding.MemoryNamespace != "" && binding.MemoryNamespace != id {
		return ErrInvalidSessionInput
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, ledger, id)
	if err != nil {
		return err
	}
	previous, err := ReadEvaluation(snapshot.Events)
	if err != nil {
		return err
	}
	if previous != nil {
		a, _ := json.Marshal(previous)
		b, _ := json.Marshal(binding)
		if string(a) != string(b) {
			return ErrSessionStateConflict
		}
		return nil
	}
	preparedPrefix := false
	if binding.RuntimeContract == "production_opensandbox" && binding.SourceWorkspace != "" && len(snapshot.Events) == 3 && snapshot.Events[0].Type == sessionCreatedEventType && snapshot.Events[1].Type == taskPrepareIntentEvent && snapshot.Events[2].Type == taskPreparedEvent {
		prepared, state, err := taskPreparation(snapshot.Events)
		var source struct {
			WorkingDir string `json:"working_dir"`
		}
		preparedPrefix = err == nil && state == "prepared" && prepared != nil && json.Unmarshal(snapshot.Events[0].Payload, &source) == nil && source.WorkingDir == binding.SourceWorkspace && binding.Runtime.OpenSandbox != nil && filepath.IsAbs(binding.Runtime.TaskWorkspaceRoot) && filepath.Clean(binding.Workspace) == filepath.Join(binding.Runtime.TaskWorkspaceRoot, prepared.Task.LeaseID)
	}
	if binding.RuntimeContract == "production_opensandbox" && !preparedPrefix || !preparedPrefix && (len(snapshot.Events) != 1 || snapshot.Events[0].Type != sessionCreatedEventType) {
		return ErrSessionStateConflict
	}
	_, err = ledger.Append(ctx, id, int64(len(snapshot.Events)), evaluationPolicyEvent, binding)
	return err
}

// EvaluationTaskRoot resolves authenticated Agent links before choosing a
// trial-local Memory namespace. An empty result is an ordinary task.
func EvaluationTaskRoot(ctx context.Context, ledger EventLog, id string) (string, error) {
	snapshot, err := ReadVerifiedSnapshot(ctx, ledger, id)
	if err != nil {
		return "", err
	}
	binding, err := ReadEvaluation(snapshot.Events)
	if err != nil {
		return "", err
	}
	if binding != nil {
		return binding.MemoryNamespace, nil
	}
	task, created, err := projectAgentTask(snapshot.Events)
	if err != nil || task == nil {
		return "", err
	}
	root, err := ModelTaskRoot(ctx, ledger, created.Actor)
	if err != nil {
		return "", err
	}
	parent, err := ReadVerifiedSnapshot(ctx, ledger, root)
	if err != nil {
		return "", err
	}
	binding, err = ReadEvaluation(parent.Events)
	if err != nil || binding == nil {
		return "", err
	}
	return binding.MemoryNamespace, nil
}

type EvaluationFinalization struct {
	Mode string `json:"mode"`
}

func ReadEvaluationFinalization(events []Event) (*EvaluationFinalization, error) {
	var found *EvaluationFinalization
	for _, event := range events {
		if event.Type != "evaluation/finalized" {
			continue
		}
		var item EvaluationFinalization
		if found != nil || json.Unmarshal(event.Payload, &item) != nil || item.Mode != "finish" && item.Mode != "abandon" {
			return nil, ErrEventIntegrity
		}
		found = &item
	}
	return found, nil
}

func FinalizeEvaluation(ctx context.Context, ledger EventLog, id, mode string) error {
	if mode == "" {
		mode = "finish"
	}
	if mode != "finish" && mode != "abandon" {
		return ErrInvalidSessionInput
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, ledger, id)
	if err != nil {
		return err
	}
	binding, err := ReadEvaluation(snapshot.Events)
	if err != nil || binding == nil {
		return ErrSessionStateConflict
	}
	previous, err := ReadEvaluationFinalization(snapshot.Events)
	if err != nil {
		return err
	}
	if previous != nil {
		if previous.Mode != mode {
			return ErrSessionStateConflict
		}
		return nil
	}
	_, err = ledger.Append(ctx, id, int64(len(snapshot.Events)), "evaluation/finalized", EvaluationFinalization{Mode: mode})
	return err
}

func ReadEvaluation(events []Event) (*EvaluationBinding, error) {
	var found *EvaluationBinding
	for _, event := range events {
		if event.Type != evaluationPolicyEvent {
			continue
		}
		if found != nil {
			return nil, ErrEventIntegrity
		}
		var binding EvaluationBinding
		if json.Unmarshal(event.Payload, &binding) != nil || !binding.validSchema() || binding.Policy.Validate() != nil || binding.Lifecycle != "" && binding.Lifecycle != "hold" || binding.RuntimeContract != "" && binding.RuntimeContract != "production_opensandbox" || binding.MemoryNamespace != "" && binding.MemoryNamespace != event.SessionID {
			return nil, ErrEventIntegrity
		}
		found = &binding
	}
	return found, nil
}

func (r *SessionRunner) evaluationBinding(ctx context.Context, events []Event) (*EvaluationBinding, error) {
	for depth := 0; depth < 8; depth++ {
		binding, err := ReadEvaluation(events)
		if err != nil || binding != nil {
			return binding, err
		}
		task, _, err := projectAgentTask(events)
		if err != nil || task == nil {
			return nil, err
		}
		snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, task.ParentSessionId)
		if err != nil {
			return nil, err
		}
		events = snapshot.Events
	}
	return nil, ErrEventIntegrity
}

func (r *SessionRunner) applyEvaluationContext(ctx context.Context, key runKey, lease runLeasePayload, request *orchestrator.ConversationRequest, binding *EvaluationBinding) error {
	if binding == nil {
		return nil
	}
	policy := binding.Policy
	if binding.Runtime.VisibleVerificationCommands == nil {
		binding.Runtime.VisibleVerificationCommands = []string{}
	}
	if request.AgentTask != nil {
		policy.ExecutionStrategy = "single"
	}
	data := map[string]any{"schema_version": 1, "context_strategy": policy.ContextStrategy, "execution_strategy": policy.ExecutionStrategy, "max_tool_rounds": policy.MaxToolRounds, "context_text": "", "visible_verification_commands": binding.Runtime.VisibleVerificationCommands}
	if policy.ContextStrategy == "retrieval_pre_read" && request.ContextEnvelope != nil {
		query := strings.ToLower(request.Input)
		if query == "" {
			for _, message := range request.History {
				if message.Role == "user" {
					query = strings.ToLower(message.Content)
				}
			}
		}
		terms := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		type candidate struct {
			path  string
			score int
		}
		var candidates []candidate
		for _, file := range request.ContextEnvelope.P0 {
			score := 0
			for _, term := range terms {
				if len(term) > 2 && strings.Contains(strings.ToLower(file.Path), term) {
					score++
				}
			}
			if score > 0 {
				candidates = append(candidates, candidate{file.Path, score})
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].score != candidates[j].score {
				return candidates[i].score > candidates[j].score
			}
			return candidates[i].path < candidates[j].path
		})
		var text strings.Builder
		for i, file := range candidates {
			if i == 3 || text.Len() >= policy.ContextMaxBytes {
				break
			}
			arguments, _ := json.Marshal(map[string]any{"path": file.path, "limit": 120})
			result, unsafe := r.executeTool(ctx, key, lease, request.Actor, request.WorkingDir, orchestrator.ToolCall{ID: fmt.Sprintf("eval-preread-%d", i), Name: "Read", ParametersJSON: string(arguments)})
			if unsafe {
				return errors.New("evaluation pre-reading failed")
			}
			if result.Error != "" {
				continue
			}
			fragment := "FILE " + file.path + "\n" + result.Output + "\n"
			remaining := policy.ContextMaxBytes - text.Len()
			if len(fragment) > remaining {
				for remaining > 0 && !utf8.RuneStart(fragment[remaining]) {
					remaining--
				}
				fragment = fragment[:remaining]
			}
			text.WriteString(fragment)
		}
		data["context_text"] = text.String()
		// Preserve the verified event/head fields; only repository hints vary by arm.
		request.ContextEnvelope.P0 = nil
		request.ContextEnvelope.P1 = nil
		request.ContextEnvelope.P2Candidates = nil
	}
	encoded, err := json.Marshal(data)
	request.EvaluationContextJSON = string(encoded)
	return err
}

func evaluationAllowsTool(binding *EvaluationBinding, events []Event, call orchestrator.ToolCall) bool {
	if binding == nil {
		return true
	}
	policy := binding.Policy
	if call.Name == "RecallMemory" && !policy.MemoryRead {
		return false
	}
	if call.Name == "ManageMemory" && !policy.MemoryRead {
		var command MemoryCommand
		if json.Unmarshal([]byte(call.ParametersJSON), &command) != nil || command.Action == "read" || command.Action == "list" {
			return false
		}
	}
	if call.Name == "SpawnAgent" {
		if task, _, err := projectAgentTask(events); err != nil || task != nil {
			return false
		}
		if policy.ExecutionStrategy != "subagent" {
			return false
		}
		for _, event := range events {
			if event.Type == "agent/task-linked" {
				return false
			}
		}
	}
	return true
}
