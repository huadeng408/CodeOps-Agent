package session

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"google.golang.org/protobuf/encoding/protojson"
)

func readOnlyAgentTool(name string) bool {
	switch name {
	case "Read", "Glob", "Grep", "RecallMemory", "PublishArtifact":
		return true
	}
	return false
}

func readOnlyAgentParts(parts []*pb.AgentPart) bool {
	for _, part := range parts {
		if part == nil {
			return false
		}
		switch part.Payload.(type) {
		case *pb.AgentPart_Text, *pb.AgentPart_DataJson:
		default:
			return false
		}
	}
	return true
}

func readOnlySpawnArguments(raw string) (agentSpawnArguments, error) {
	var args agentSpawnArguments
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil || args.Kind != "explore" || args.Parallel {
		return args, ErrInvalidSessionInput
	}
	for _, name := range args.AllowedTools {
		if !readOnlyAgentTool(name) {
			return args, ErrInvalidSessionInput
		}
	}
	if len(args.Message) > 0 {
		var message pb.AgentMessage
		if protojson.Unmarshal(args.Message, &message) != nil || !readOnlyAgentParts(message.Parts) {
			return args, ErrInvalidSessionInput
		}
	}
	return args, nil
}

// A borrowed child is admitted only through the same signed task link used by
// model billing. Its directory is the prepared leaf, never the source or root.
func (t *TaskWorkspaces) readOnlyAgent(ctx context.Context, owner uint, rootID, childID string) (*pb.AgentTask, agentTaskCreated, []Event, error) {
	root, _, err := t.read(ctx, owner, rootID)
	if err != nil {
		return nil, agentTaskCreated{}, nil, err
	}
	binding, err := ReadEvaluation(root.Events)
	prepared, state, prepareErr := taskPreparation(root.Events)
	if err != nil || binding == nil || binding.RuntimeContract != "production_opensandbox" || binding.Policy.ExecutionStrategy != "subagent" ||
		prepareErr != nil || prepared == nil || state != "prepared" {
		return nil, agentTaskCreated{}, nil, ErrSessionStateConflict
	}
	task, created, err := readAgentTask(ctx, t.workbench.ledger, owner, childID, rootID)
	if err != nil || task.ParentSessionId != rootID || task.Card == nil || task.Card.Id != "explore" || len(created.ExternalToolBindings) != 0 {
		return nil, created, nil, ErrSessionStateConflict
	}
	actual := filepath.Join(t.storage, prepared.Task.LeaseID)
	if !strings.EqualFold(filepath.Clean(task.WorkingDir), filepath.Clean(actual)) || !strings.EqualFold(filepath.Clean(binding.Workspace), filepath.Clean(actual)) {
		return nil, created, nil, ErrSessionStateConflict
	}
	for _, name := range created.AllowedTools {
		if !readOnlyAgentTool(name) {
			return nil, created, nil, ErrSessionStateConflict
		}
	}
	for _, message := range task.Messages {
		if !readOnlyAgentParts(message.Parts) {
			return nil, created, nil, ErrSessionStateConflict
		}
	}
	for _, artifact := range task.Artifacts {
		if !readOnlyAgentParts(artifact.Parts) {
			return nil, created, nil, ErrSessionStateConflict
		}
	}
	if root, err := ModelTaskRoot(ctx, t.workbench.ledger, created.Actor); err != nil || root != rootID {
		return nil, created, nil, ErrSessionNotFound
	}
	return task, created, root.Events, nil
}

func readOnlyParentControl(events []Event, call toolCallPayload, task *pb.AgentTask, created agentTaskCreated) bool {
	run, runErr := projectRun(events, call.RunID)
	parentActor := created.Actor
	parentActor.SessionID = task.ParentSessionId
	if runErr != nil || run.terminal || run.cancelRequest != nil || run.view.Status != RunRunning || run.view.LeaseUntil == nil || !run.view.LeaseUntil.After(time.Now()) || run.actor.ScopeKey() != parentActor.ScopeKey() {
		return false
	}
	if call.ToolName == "AgentTask" {
		args, err := readOnlyControlArguments(call.ArgumentsJSON)
		return err == nil && (args.Action == "get" || args.Action == "wait") && args.TaskID == task.Id
	}
	if call.ToolName != "SpawnAgent" {
		return false
	}
	if _, err := readOnlySpawnArguments(call.ArgumentsJSON); err != nil {
		return false
	}
	canonical, err := canonicalToolArguments(call.ArgumentsJSON)
	if err != nil || created.Signature != agentDigest([]byte(canonical)) {
		return false
	}
	invocation := agentInvocationID(call.RunID, run.view.RetryOfRunIDs, call.ToolCallID)
	return task.Id == "agent-"+agentDigest([]byte(task.ParentSessionId + "\x00" + invocation))[:32]
}

func readOnlyControlArguments(raw string) (agentControlArguments, error) {
	var args agentControlArguments
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil || len(args.Message) > 0 && string(args.Message) != "null" && string(args.Message) != "{}" {
		return args, ErrInvalidSessionInput
	}
	switch args.Action {
	case "cards", "list":
		if args.TaskID == "" {
			return args, nil
		}
	case "get", "wait":
		if args.TaskID != "" {
			return args, nil
		}
	}
	return args, ErrInvalidSessionInput
}

func pendingAgentCall(events []Event, call orchestrator.ToolCall) (toolCallPayload, error) {
	var current toolCallPayload
	found := false
	for _, event := range events {
		if event.Type == toolUnknownEventType {
			return current, ErrSessionStateConflict
		}
		if event.Type == "tool/result" {
			var result toolResultPayload
			if json.Unmarshal(event.Payload, &result) != nil || result.OutcomeUnknown {
				return current, ErrSessionStateConflict
			}
		}
		if event.Type != toolDispatchedType {
			continue
		}
		var dispatched toolCallPayload
		if json.Unmarshal(event.Payload, &dispatched) != nil {
			return current, ErrEventIntegrity
		}
		if dispatched.OperationID == "" || dispatched.OperationID != call.OperationID || dispatched.ToolName != call.Name || dispatched.ArgumentsJSON != call.ParametersJSON {
			continue
		}
		state := invocationProjection(events, dispatched.RunID, dispatched.ToolCallID)
		run, err := projectRun(events, dispatched.RunID)
		if found || state.prepared == nil || state.result != nil || err != nil || run.terminal || run.cancelRequest != nil || run.actor.ScopeKey() == "" ||
			run.view.Status != RunRunning || run.view.LeaseUntil == nil || !run.view.LeaseUntil.After(time.Now()) {
			return current, ErrSessionStateConflict
		}
		current, found = dispatched, true
	}
	if !found {
		return current, ErrSessionStateConflict
	}
	return current, nil
}

// ReadOnlyAgentModelWorkspace borrows only while the parent is synchronously
// waiting in its exact Go-dispatched SpawnAgent or AgentTask(get/wait) call.
func (t *TaskWorkspaces) ReadOnlyAgentModelWorkspace(ctx context.Context, owner uint, rootID, childID string) (TaskModelWorkspace, error) {
	task, created, root, err := t.readOnlyAgent(ctx, owner, rootID, childID)
	if err != nil {
		return TaskModelWorkspace{}, ErrSessionStateConflict
	}
	for _, event := range root {
		if event.Type != toolDispatchedType {
			continue
		}
		var call toolCallPayload
		if json.Unmarshal(event.Payload, &call) != nil {
			return TaskModelWorkspace{}, ErrEventIntegrity
		}
		state := invocationProjection(root, call.RunID, call.ToolCallID)
		if state.result == nil && readOnlyParentControl(root, call, task, created) {
			return t.ToolWorkspace(ctx, owner, rootID, call.ToolCallID)
		}
	}
	return TaskModelWorkspace{}, ErrSessionStateConflict
}

// ReadOnlyAgentToolWorkspace also requires the current invocation's canonical
// dispatch; knowing a child ID or an approved root never grants file access.
func (t *TaskWorkspaces) ReadOnlyAgentToolWorkspace(ctx context.Context, owner uint, rootID string, actor identity.Actor, call orchestrator.ToolCall) (TaskModelWorkspace, error) {
	if actor.Validate() != nil {
		return TaskModelWorkspace{}, ErrSessionNotFound
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, t.workbench.ledger, actor.SessionID)
	if err != nil {
		return TaskModelWorkspace{}, err
	}
	current, err := pendingAgentCall(snapshot.Events, call)
	if err != nil {
		return TaskModelWorkspace{}, err
	}
	run, err := projectRun(snapshot.Events, current.RunID)
	if err != nil || run.actor.ScopeKey() != actor.ScopeKey() || call.ID != current.ToolCallID && call.ID != agentInvocationID(current.RunID, run.view.RetryOfRunIDs, current.ToolCallID) {
		return TaskModelWorkspace{}, ErrSessionNotFound
	}
	if actor.SessionID == rootID {
		binding, err := ReadEvaluation(snapshot.Events)
		if err != nil || binding == nil || binding.RuntimeContract != "production_opensandbox" || binding.Policy.ExecutionStrategy != "subagent" {
			return TaskModelWorkspace{}, ErrSessionStateConflict
		}
		switch call.Name {
		case "SpawnAgent":
			if _, err := readOnlySpawnArguments(call.ParametersJSON); err != nil {
				return TaskModelWorkspace{}, err
			}
		case "AgentTask":
			args, err := readOnlyControlArguments(call.ParametersJSON)
			if err != nil {
				return TaskModelWorkspace{}, err
			}
			if args.Action == "cards" || args.Action == "list" {
				return t.ToolWorkspace(ctx, owner, rootID, current.ToolCallID)
			}
			task, created, events, err := t.readOnlyAgent(ctx, owner, rootID, args.TaskID)
			if err != nil || !readOnlyParentControl(events, current, task, created) {
				return TaskModelWorkspace{}, ErrSessionStateConflict
			}
		default:
			return TaskModelWorkspace{}, ErrInvalidSessionInput
		}
		return t.ToolWorkspace(ctx, owner, rootID, current.ToolCallID)
	}
	task, created, _, err := t.readOnlyAgent(ctx, owner, rootID, actor.SessionID)
	if err != nil || created.Actor.ScopeKey() != actor.ScopeKey() || !readOnlyAgentTool(call.Name) || taskTerminal(task.Status) {
		return TaskModelWorkspace{}, ErrSessionStateConflict
	}
	allowed, _, err := agentToolPolicy(snapshot.Events, call.Name)
	if err != nil || !allowed {
		return TaskModelWorkspace{}, ErrSessionStateConflict
	}
	if call.Name == "PublishArtifact" {
		var args struct {
			Artifact json.RawMessage `json:"artifact"`
		}
		var artifact pb.AgentArtifact
		if json.Unmarshal([]byte(call.ParametersJSON), &args) != nil || protojson.Unmarshal(args.Artifact, &artifact) != nil || !readOnlyAgentParts(artifact.Parts) {
			return TaskModelWorkspace{}, ErrInvalidSessionInput
		}
	}
	return t.ReadOnlyAgentModelWorkspace(ctx, owner, rootID, actor.SessionID)
}

func (t *TaskWorkspaces) borrowedAgentsAdmissible(ctx context.Context, owner uint, rootID string, events []Event, currentToolID string) error {
	binding, err := ReadEvaluation(events)
	if err != nil || binding == nil || binding.RuntimeContract != "production_opensandbox" {
		return err
	}
	var current toolCallPayload
	for _, event := range events {
		if event.Type == toolDispatchedType {
			var call toolCallPayload
			if json.Unmarshal(event.Payload, &call) != nil {
				return ErrEventIntegrity
			}
			if call.ToolCallID == currentToolID {
				current = call
			}
		}
	}
	for _, event := range events {
		if event.Type != "agent/task-linked" {
			continue
		}
		var link agentTaskLink
		if json.Unmarshal(event.Payload, &link) != nil {
			return ErrEventIntegrity
		}
		task, created, _, err := t.readOnlyAgent(ctx, owner, rootID, link.ChildSessionID)
		if err != nil {
			return err
		}
		if task.Id != link.TaskID || task.ChildSessionId != link.ChildSessionID || created.Signature != link.Signature {
			return ErrEventIntegrity
		}
		if !taskTerminal(task.Status) && (!readOnlyParentControl(events, current, task, created) && !isReadOnlyTool(current.ToolName)) {
			return ErrSessionStateConflict
		}
	}
	return nil
}
