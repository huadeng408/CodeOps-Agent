package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/telemetry/genai"
	"code-agent/internal/tools"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type AgentWorkspaceRequest struct {
	TaskID           string
	ParentSessionID  string
	ChildSessionID   string
	ParentWorkingDir string
	Kind             string
}

type agentTaskCreated struct {
	Task                 json.RawMessage    `json:"task"`
	Signature            string             `json:"signature"`
	AllowedTools         []string           `json:"allowed_tools"`
	ExternalToolBindings []AgentToolBinding `json:"external_tool_bindings,omitempty"`
	Actor                identity.Actor     `json:"actor"`
}

// AgentToolBinding pins a dynamic MCP tool to the server and canonical schema
// admitted by the Harness when the task was created.
type AgentToolBinding struct {
	Name               string `json:"name"`
	Server             string `json:"server"`
	InputSchemaSHA256  string `json:"input_schema_sha256"`
	ServerConfigSHA256 string `json:"server_config_sha256"`
}

type agentTaskLink struct {
	TaskID         string `json:"task_id"`
	ChildSessionID string `json:"child_session_id"`
	Signature      string `json:"signature"`
}

type agentTaskMutation struct {
	RequestID string          `json:"request_id"`
	Message   json.RawMessage `json:"message,omitempty"`
	Artifact  json.RawMessage `json:"artifact,omitempty"`
}

const agentWorkspaceLifecycleAcknowledgedEventType = "agent/workspace-lifecycle-acknowledged"

type agentWorkspaceLifecycleAcknowledged struct {
	RequestID      string `json:"request_id"`
	ChildSessionID string `json:"child_session_id"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
}

var agentJSON = protojson.MarshalOptions{UseProtoNames: true}

func agentCard(kind string) (*pb.AgentCard, []string, error) {
	var capabilities, allowed []string
	switch kind {
	case "explore", "deep":
		capabilities = []string{"repository exploration", "source analysis"}
	case "review":
		capabilities = []string{"code review", "regression analysis"}
	case "security":
		capabilities = []string{"security review", "trust boundary analysis"}
	case "plan":
		capabilities = []string{"task planning", "design analysis"}
	case "general", "background":
		capabilities = []string{"coding", "testing", "repository analysis"}
	default:
		return nil, nil, errors.New("unknown agent kind")
	}
	allowed = []string{"Read", "Glob", "Grep", "Skill", "RecallMemory", "TodoWrite", "PlanWrite", "PublishArtifact", "AskUser", "AgentTask"}
	if kind == "general" || kind == "background" {
		allowed = append(allowed, "Edit", "Write", "Bash", "Git")
	}
	return &pb.AgentCard{SchemaVersion: "agent.v2", Id: kind, Name: kind, Description: "Independent model agent with Harness-scoped tools",
		Capabilities: capabilities, Address: "harness://agents/" + kind, Transports: []string{"protobuf/grpc"}, Authentication: []string{"authenticated-session-owner"}}, allowed, nil
}

func agentTool(name string) bool {
	return name == "SpawnAgent" || name == "AgentTask" || name == "PublishArtifact"
}

func staticAgentTool(name string) bool {
	switch name {
	case "Read", "Glob", "Grep", "Skill", "RecallMemory", "TodoWrite", "PlanWrite",
		"PublishArtifact", "AskUser", "AgentTask", "Edit", "Write", "Bash", "Git":
		return true
	default:
		return false
	}
}
func taskTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "canceled"
}

func agentParent(events []Event, actor identity.Actor) (uint, string, error) {
	if len(events) == 0 || actor.Validate() != nil || actor.SessionID != events[0].SessionID {
		return 0, "", ErrSessionNotFound
	}
	if events[0].Type == sessionCreatedEventType {
		view, err := reduceSessionView(events)
		if err != nil || view.Status == "deleted" {
			return 0, "", ErrSessionNotFound
		}
		for _, event := range events {
			if event.Type != continuationEventType {
				continue
			}
			var admitted continuationPayload
			if json.Unmarshal(event.Payload, &admitted) != nil || admitted.Actor.ScopeKey() != actor.ScopeKey() {
				return 0, "", ErrSessionStateConflict
			}
		}
		return view.UserID, view.WorkingDir, nil
	}
	if events[0].Type == sessionStateEventType || events[0].Type == "legacy/import" {
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Type != sessionStateEventType {
				continue
			}
			var state Session
			if json.Unmarshal(events[i].Payload, &state) != nil || state.Actor.ScopeKey() != actor.ScopeKey() {
				return 0, "", ErrSessionNotFound
			}
			copy := actor
			copy.SessionID = ""
			digest := sha256.Sum256([]byte(copy.ScopeKey()))
			owner := uint(0)
			for _, b := range digest[:8] {
				owner = (owner << 8) | uint(b)
			}
			owner |= uint(1) << 63
			return owner, state.WorkingDir, nil
		}
	}
	return 0, "", ErrSessionNotFound
}

// ExecuteAgentTool accepts only the actor already authenticated by the parent
// Harness invocation. Neither owner IDs nor child histories come from the model.
func (r *SessionRunner) ExecuteAgentTool(ctx context.Context, actor identity.Actor, parentSessionID string, call orchestrator.ToolCall) orchestrator.ToolResult {
	result := orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, ExitCode: 1}
	snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, parentSessionID)
	if err != nil {
		result.Error = "agent parent ledger unavailable"
		return result
	}
	owner, workingDir, err := agentParent(snapshot.Events, actor)
	if err != nil {
		result.Error = "agent parent authorization rejected"
		return result
	}
	var output string
	switch call.Name {
	case "SpawnAgent":
		output, err = r.spawnTask(ctx, owner, actor, workingDir, call)
	case "AgentTask":
		output, err = r.controlTask(ctx, owner, actor, call)
	case "PublishArtifact":
		output, err = r.publishArtifact(ctx, owner, actor, call)
	default:
		err = errors.New("unknown agent tool")
	}
	if err != nil {
		result.Error = "agent task request rejected"
		return result
	}
	result.Output, result.ExitCode = output, 0
	return result
}

func (r *SessionRunner) spawnTask(ctx context.Context, owner uint, actor identity.Actor, parentDir string, call orchestrator.ToolCall) (output string, returnErr error) {
	var args struct {
		Kind         string          `json:"kind"`
		Title        string          `json:"title"`
		Objective    string          `json:"objective"`
		Context      json.RawMessage `json:"context"`
		ContextJSON  string          `json:"context_json"`
		Parallel     bool            `json:"parallel"`
		Message      json.RawMessage `json:"message"`
		AllowedTools []string        `json:"allowed_tools"`
	}
	decoder := json.NewDecoder(strings.NewReader(call.ParametersJSON))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil || call.ID == "" || len(args.Title) > 240 || strings.TrimSpace(args.Title) == "" || len(args.Objective) > 16000 || strings.TrimSpace(args.Objective) == "" {
		return "", ErrInvalidSessionInput
	}
	if strings.ContainsAny(args.Title, "\r\n\t") {
		return "", ErrInvalidSessionInput
	}
	canonical, err := canonicalToolArguments(call.ParametersJSON)
	if err != nil {
		return "", err
	}
	signature := agentDigest([]byte(canonical))
	taskID := "agent-" + agentDigest([]byte(actor.SessionID + "\x00" + call.ID))[:32]
	card, allowed, err := agentCard(args.Kind)
	if err != nil {
		return "", err
	}
	if existing, _, err := r.readAgentTask(ctx, owner, taskID, actor.SessionID); err == nil {
		childEvents, _ := r.workbench.ledger.Events(ctx, taskID)
		var created agentTaskCreated
		for _, e := range childEvents {
			if e.Type == "agent/task-created" {
				_ = json.Unmarshal(e.Payload, &created)
				break
			}
		}
		if created.Signature != signature {
			return "", ErrSessionStateConflict
		}
		if err := r.admitInitialAgentTurn(ctx, owner, actor, existing); err != nil {
			return "", err
		}
		return r.taskResult(ctx, owner, taskID, actor.SessionID, !args.Parallel)
	} else if !errors.Is(err, ErrSessionNotFound) {
		return "", err
	}
	externalBindings := []AgentToolBinding{}
	if len(args.AllowedTools) > 0 {
		subset := []string{}
		seen := make(map[string]bool)
		for _, name := range args.AllowedTools {
			if seen[name] {
				continue
			}
			seen[name] = true
			found := false
			for _, permitted := range allowed {
				if name == permitted {
					found = true
					break
				}
			}
			if !found && args.Kind != "general" && args.Kind != "background" {
				return "", ErrInvalidSessionInput
			}
			if !found && r.options.AgentToolBinding != nil {
				binding, ok := r.options.AgentToolBinding(name)
				if ok && binding.Name == name && validAgentToolBinding(binding) {
					found = true
					externalBindings = append(externalBindings, binding)
				}
			}
			if !found {
				return "", ErrInvalidSessionInput
			}
			subset = append(subset, name)
		}
		// Lifecycle communication is mandatory even with a narrower tool set.
		allowed = subset
		for _, name := range []string{"PublishArtifact", "AskUser", "AgentTask"} {
			if !seen[name] {
				allowed = append(allowed, name)
			}
		}
	}
	message := &pb.AgentMessage{Id: "assignment", Role: "user", Parts: []*pb.AgentPart{{Payload: &pb.AgentPart_Text{Text: tools.RedactSensitive(args.Objective)}}}}
	if len(args.Context) > 0 && string(args.Context) != "null" {
		if !json.Valid(args.Context) || len(args.Context) > 16000 {
			return "", ErrInvalidSessionInput
		}
		message.Parts = append(message.Parts, &pb.AgentPart{Payload: &pb.AgentPart_DataJson{DataJson: tools.RedactSensitive(string(args.Context))}})
	} else if args.ContextJSON != "" {
		if !json.Valid([]byte(args.ContextJSON)) || len(args.ContextJSON) > 16000 {
			return "", ErrInvalidSessionInput
		}
		message.Parts = append(message.Parts, &pb.AgentPart{Payload: &pb.AgentPart_DataJson{DataJson: tools.RedactSensitive(args.ContextJSON)}})
	}
	if len(args.Message) > 0 {
		var extra pb.AgentMessage
		if protojson.Unmarshal(args.Message, &extra) != nil {
			return "", ErrInvalidSessionInput
		}
		message.Parts = append(message.Parts, extra.Parts...)
	}
	// Validate caller-controlled structure and source files before allocating a
	// managed checkout. A second pass below copies verified files into it.
	if err := validateAgentParts(message.Parts, parentDir, parentDir); err != nil {
		return "", err
	}
	workingDir := parentDir
	workspaceAllocated := false
	taskCreated := false
	if args.Kind == "general" || args.Kind == "background" {
		if r.options.AgentWorkspace == nil {
			return "", errors.New("isolated agent workspace unavailable")
		}
		workingDir, err = r.options.AgentWorkspace(ctx, AgentWorkspaceRequest{TaskID: taskID, ParentSessionID: actor.SessionID, ChildSessionID: taskID, ParentWorkingDir: parentDir, Kind: args.Kind})
		if err != nil {
			return "", err
		}
		workspaceAllocated = true
		defer func() {
			if returnErr == nil || !workspaceAllocated || taskCreated {
				return
			}
			var lifecycleErr error
			for attempt := 0; attempt < 3; attempt++ {
				lifecycleErr = r.callAgentLifecycle(ctx, &pb.AgentLifecycle{
					RequestId: taskID, ChildSessionId: taskID, Status: "cancelled", Reason: "agent assignment rejected",
				})
				if lifecycleErr == nil {
					break
				}
			}
			if lifecycleErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("compensate agent workspace: %w", lifecycleErr))
			}
		}()
	}
	if workingDir != parentDir {
		if err := validateAgentParts(message.Parts, parentDir, workingDir); err != nil {
			return "", err
		}
	}
	if err := pinAgentFiles(message.Parts, workingDir, ".agent/materials"); err != nil {
		return "", err
	}
	link := agentTaskLink{TaskID: taskID, ChildSessionID: taskID, Signature: signature}
	if err := r.appendAgentFact(ctx, actor.SessionID, "agent/task-linked", link, func(events []Event) bool {
		for _, event := range events {
			var old agentTaskLink
			if event.Type == "agent/task-linked" && json.Unmarshal(event.Payload, &old) == nil && old.TaskID == taskID {
				return true
			}
		}
		return false
	}); err != nil {
		return "", err
	}
	task := &pb.AgentTask{SchemaVersion: "agent.v2", Id: taskID, ParentSessionId: actor.SessionID, ChildSessionId: taskID, Status: "submitted", Card: card, Messages: []*pb.AgentMessage{message}, Revision: 1, WorkingDir: workingDir}
	taskJSON, err := agentJSON.Marshal(task)
	if err != nil {
		return "", err
	}
	childActor := actor
	childActor.SessionID = ""
	childActor, err = childActor.BindSession(taskID)
	if err != nil {
		return "", err
	}
	events, err := r.workbench.ledger.Events(ctx, taskID)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		_, err = r.workbench.ledger.Append(ctx, taskID, 0, sessionCreatedEventType, sessionCreatedPayload{OwnerID: owner, ProjectName: "agent", Title: tools.RedactSensitive(args.Title), Goal: "Independent " + args.Kind + " task", WorkingDir: workingDir, Status: "running"})
		if err != nil && !errors.Is(err, ErrSequenceConflict) {
			return "", err
		}
	}
	if err := r.appendAgentFact(ctx, taskID, "agent/task-created", agentTaskCreated{Task: taskJSON, Signature: signature, AllowedTools: allowed, ExternalToolBindings: externalBindings, Actor: childActor}, func(events []Event) bool {
		for _, event := range events {
			if event.Type == "agent/task-created" {
				return true
			}
		}
		return false
	}); err != nil {
		return "", err
	}
	taskCreated = true
	actual, metadata, err := r.readAgentTask(ctx, owner, taskID, actor.SessionID)
	if err != nil {
		return "", err
	}
	if metadata.Signature != signature {
		return "", ErrSessionStateConflict
	}
	task = actual
	if err := r.admitInitialAgentTurn(ctx, owner, actor, task); err != nil {
		return "", err
	}
	return r.taskResult(ctx, owner, taskID, actor.SessionID, !args.Parallel)
}

func (r *SessionRunner) admitInitialAgentTurn(ctx context.Context, owner uint, actor identity.Actor, task *pb.AgentTask) error {
	snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, task.ChildSessionId)
	if err != nil {
		return err
	}
	if task.Status == "canceled" {
		return nil
	}
	if _, admitted, err := runForRequest(snapshot.Events, "agent-assignment"); err != nil || admitted {
		return err
	}
	childActor := actor
	childActor.SessionID = ""
	childActor, err = childActor.BindSession(task.ChildSessionId)
	if err != nil {
		return err
	}
	content, err := agentJSON.Marshal(task.Messages[0])
	if err != nil {
		return err
	}
	_, err = r.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "agent-assignment", SessionID: task.ChildSessionId, OwnerID: owner, ExpectedSeq: int64(len(snapshot.Events)), Content: "Assigned task and materials:\n" + string(content), Actor: childActor})
	return err
}

func (r *SessionRunner) readAgentTask(ctx context.Context, owner uint, taskID, callerSessionID string) (*pb.AgentTask, agentTaskCreated, error) {
	snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, taskID)
	if err != nil {
		return nil, agentTaskCreated{}, err
	}
	view, err := reduceSessionView(snapshot.Events)
	if err != nil || view.UserID != owner || view.Status == "deleted" {
		return nil, agentTaskCreated{}, ErrSessionNotFound
	}
	task, created, err := projectAgentTask(snapshot.Events)
	if err != nil {
		return nil, created, err
	}
	if task == nil {
		return nil, created, ErrSessionNotFound
	}
	if task.WorkingDir != "" && task.WorkingDir != view.WorkingDir {
		return nil, created, ErrEventIntegrity
	}
	task.WorkingDir = view.WorkingDir
	if callerSessionID != task.ParentSessionId && callerSessionID != task.ChildSessionId {
		return nil, created, ErrSessionNotFound
	}
	parent, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, task.ParentSessionId)
	if err != nil {
		return nil, created, err
	}
	for _, event := range parent.Events {
		if event.Type == "session/deleted" {
			return nil, created, ErrSessionNotFound
		}
	}
	linked := false
	for _, event := range parent.Events {
		var link agentTaskLink
		if event.Type == "agent/task-linked" && json.Unmarshal(event.Payload, &link) == nil && link.TaskID == task.Id && link.ChildSessionID == task.ChildSessionId && link.Signature == created.Signature {
			linked = true
		}
	}
	if !linked {
		return nil, created, ErrEventIntegrity
	}
	if err := verifyAgentArtifacts(task.Artifacts, view.WorkingDir); err != nil {
		return nil, created, err
	}
	return task, created, nil
}

func projectAgentTask(events []Event) (*pb.AgentTask, agentTaskCreated, error) {
	var task *pb.AgentTask
	var created agentTaskCreated
	lifecycleAcknowledged := false
	for _, event := range events {
		switch event.Type {
		case "agent/task-created":
			if task != nil || json.Unmarshal(event.Payload, &created) != nil {
				return nil, created, ErrEventIntegrity
			}
			task = &pb.AgentTask{}
			if protojson.Unmarshal(created.Task, task) != nil || task.SchemaVersion != "agent.v2" || task.ChildSessionId != event.SessionID || task.Id != event.SessionID || len(task.Messages) != 1 || created.Actor.Validate() != nil || created.Actor.SessionID != event.SessionID {
				return nil, created, ErrEventIntegrity
			}
			if !validAgentToolBindings(created) {
				return nil, created, ErrEventIntegrity
			}
		case "agent/task-message", "agent/task-input-required", "agent/task-artifact", "agent/task-canceled":
			if task == nil || task.Status == "canceled" {
				return nil, created, ErrEventIntegrity
			}
			var mutation agentTaskMutation
			if json.Unmarshal(event.Payload, &mutation) != nil || mutation.RequestID == "" {
				return nil, created, ErrEventIntegrity
			}
			if event.Type == "agent/task-canceled" {
				task.Status = "canceled"
				continue
			}
			if len(mutation.Message) > 0 {
				message := &pb.AgentMessage{}
				if protojson.Unmarshal(mutation.Message, message) != nil {
					return nil, created, ErrEventIntegrity
				}
				task.Messages = append(task.Messages, message)
				if event.Type == "agent/task-input-required" {
					task.Status = "input_required"
				} else {
					task.Status = "submitted"
				}
			}
			if len(mutation.Artifact) > 0 {
				artifact := &pb.AgentArtifact{}
				if protojson.Unmarshal(mutation.Artifact, artifact) != nil || artifact.Id != mutation.RequestID || verifyArtifactChecksum(artifact) != nil {
					return nil, created, ErrEventIntegrity
				}
				task.Artifacts = append(task.Artifacts, artifact)
			}
		case continuationEventType, runLeasedEventType:
			if task != nil && task.Status != "canceled" {
				task.Status = "working"
				task.ErrorCode = ""
			}
		case runCompletedEventType:
			if task != nil && task.Status != "input_required" && task.Status != "canceled" {
				task.Status = "completed"
			}
		case runFailedEventType:
			if task != nil && task.Status != "canceled" {
				task.Status = "failed"
				task.ErrorCode = "agent_run_failed"
			}
		case runCanceledEventType:
			if task != nil {
				task.Status = "canceled"
			}
		case agentWorkspaceLifecycleAcknowledgedEventType:
			var acknowledged agentWorkspaceLifecycleAcknowledged
			if task == nil || lifecycleAcknowledged || json.Unmarshal(event.Payload, &acknowledged) != nil ||
				acknowledged.RequestID != task.Id || acknowledged.ChildSessionID != task.ChildSessionId ||
				acknowledged.Status != "cancelled" || task.Status != "canceled" {
				return nil, created, ErrEventIntegrity
			}
			lifecycleAcknowledged = true
		}
	}
	if task != nil {
		// A terminal event closes its run, not messages queued for later runs.
		if task.Status != "canceled" {
			for _, event := range events {
				if event.Type != "agent/task-message" {
					continue
				}
				var mutation agentTaskMutation
				if json.Unmarshal(event.Payload, &mutation) != nil {
					return nil, created, ErrEventIntegrity
				}
				if _, admitted, err := runForRequest(events, "agent-message-"+mutation.RequestID); err != nil {
					return nil, created, err
				} else if !admitted {
					task.Status = "submitted"
					task.ErrorCode = ""
				}
			}
		}
		task.Revision = uint64(len(events))
		if !taskTerminal(task.Status) {
			for _, event := range events {
				if event.Type != approvalPendingEventType {
					continue
				}
				var pending toolApprovalPayload
				if json.Unmarshal(event.Payload, &pending) != nil {
					return nil, created, ErrEventIntegrity
				}
				approval, err := projectToolApproval(events, pending.RunID, pending.ToolCallID)
				if err != nil {
					return nil, created, err
				}
				run, err := projectRun(events, pending.RunID)
				if err != nil {
					return nil, created, err
				}
				if approval.decision == nil && !run.terminal && approval.pendingEvent.EventID == event.EventID {
					task.PendingApprovals = append(task.PendingApprovals, agentApprovalView(task.ChildSessionId, event, pending))
				}
			}
		}
	}
	return task, created, nil
}

func (r *SessionRunner) taskResult(ctx context.Context, owner uint, taskID, parentID string, wait bool) (string, error) {
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		task, _, err := r.readAgentTask(ctx, owner, taskID, parentID)
		if err != nil {
			return "", err
		}
		if !wait || taskTerminal(task.Status) || task.Status == "input_required" || len(task.PendingApprovals) > 0 {
			encoded, err := agentJSON.Marshal(task)
			return string(encoded), err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			encoded, err := agentJSON.Marshal(task)
			return string(encoded), err
		case <-ticker.C:
		}
	}
}

func (r *SessionRunner) controlTask(ctx context.Context, owner uint, actor identity.Actor, call orchestrator.ToolCall) (string, error) {
	var args struct {
		Action  string          `json:"action"`
		TaskID  string          `json:"task_id"`
		Message json.RawMessage `json:"message"`
	}
	decoder := json.NewDecoder(strings.NewReader(call.ParametersJSON))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil {
		return "", ErrInvalidSessionInput
	}
	if args.Action == "cards" {
		cards := []*pb.AgentCard{}
		for _, kind := range []string{"deep", "explore", "plan", "general", "background", "review", "security"} {
			card, _, _ := agentCard(kind)
			cards = append(cards, card)
		}
		items := make([]json.RawMessage, 0, len(cards))
		for _, card := range cards {
			encoded, err := agentJSON.Marshal(card)
			if err != nil {
				return "", err
			}
			items = append(items, encoded)
		}
		encoded, err := json.Marshal(items)
		return string(encoded), err
	}
	if args.Action == "list" {
		parent, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, actor.SessionID)
		if err != nil {
			return "", err
		}
		results := []json.RawMessage{}
		for _, event := range parent.Events {
			var link agentTaskLink
			if event.Type == "agent/task-linked" && json.Unmarshal(event.Payload, &link) == nil {
				encoded, err := r.taskResult(ctx, owner, link.TaskID, actor.SessionID, false)
				if err != nil {
					return "", err
				}
				results = append(results, json.RawMessage(encoded))
			}
		}
		encoded, err := json.Marshal(results)
		return string(encoded), err
	}
	if args.TaskID == "" {
		args.TaskID = actor.SessionID
	}
	task, created, err := r.readAgentTask(ctx, owner, args.TaskID, actor.SessionID)
	if err != nil {
		return "", err
	}
	if args.Action == "get" || args.Action == "wait" {
		return r.taskResult(ctx, owner, args.TaskID, actor.SessionID, args.Action == "wait")
	}
	if args.Action == "cancel" {
		if task.Status == "canceled" {
			if err := r.reconcileAgentWorkspaceLifecycle(ctx, task); err != nil {
				return "", err
			}
			return r.taskResult(ctx, owner, args.TaskID, actor.SessionID, false)
		}
		if actor.SessionID != task.ParentSessionId || taskTerminal(task.Status) {
			return "", ErrSessionStateConflict
		}
		if err := r.cancelAgentTask(ctx, task, call.ID); err != nil {
			return "", err
		}
		return r.taskResult(ctx, owner, args.TaskID, actor.SessionID, false)
	}
	if args.Action != "message" && args.Action != "input_required" {
		return "", ErrInvalidSessionInput
	}
	if args.Action == "message" && (actor.SessionID != task.ParentSessionId || task.Status == "canceled") || args.Action == "input_required" && actor.SessionID != task.ChildSessionId {
		return "", ErrSessionStateConflict
	}
	message := &pb.AgentMessage{}
	if protojson.Unmarshal(args.Message, message) != nil || len(message.Parts) == 0 {
		return "", ErrInvalidSessionInput
	}
	message.Id = call.ID
	message.Role = "user"
	if args.Action == "input_required" {
		message.Role = "agent"
	}
	view, err := r.workbench.Get(ctx, owner, task.ChildSessionId)
	if err != nil {
		return "", err
	}
	sourceDir := view.WorkingDir
	if args.Action == "message" {
		parent, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, actor.SessionID)
		if err != nil {
			return "", err
		}
		_, sourceDir, err = agentParent(parent.Events, actor)
		if err != nil {
			return "", err
		}
	}
	if err := validateAgentParts(message.Parts, sourceDir, view.WorkingDir); err != nil {
		return "", err
	}
	if err := pinAgentFiles(message.Parts, view.WorkingDir, ".agent/materials"); err != nil {
		return "", err
	}
	encoded, err := agentJSON.Marshal(message)
	if err != nil {
		return "", err
	}
	eventType := "agent/task-message"
	if args.Action == "input_required" {
		eventType = "agent/task-input-required"
	}
	if err := r.appendAgentFact(ctx, task.ChildSessionId, eventType, agentTaskMutation{RequestID: call.ID, Message: encoded}, agentMutationExists(eventType, call.ID)); err != nil {
		return "", err
	}
	if args.Action == "message" {
		// Messages arriving during a run remain durable. A later child turn is
		// admitted after the current run closes; the parent never injects them
		// into a live provider transcript.
		if err := r.admitAgentMessages(ctx, owner, task.ChildSessionId, created.Actor); err != nil {
			return "", err
		}
	}
	return r.taskResult(ctx, owner, args.TaskID, actor.SessionID, false)
}

func (r *SessionRunner) admitAgentMessages(ctx context.Context, owner uint, childID string, actor identity.Actor) error {
	snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, childID)
	if err != nil {
		return err
	}
	if active, err := hasActiveRun(snapshot.Events); err != nil || active {
		return err
	}
	for _, event := range snapshot.Events {
		if event.Type != "agent/task-message" {
			continue
		}
		var mutation agentTaskMutation
		if json.Unmarshal(event.Payload, &mutation) != nil {
			return ErrEventIntegrity
		}
		requestID := "agent-message-" + mutation.RequestID
		if _, exists, err := runForRequest(snapshot.Events, requestID); err != nil {
			return err
		} else if exists {
			continue
		}
		_, err = r.SubmitMessage(ctx, SubmitMessageCommand{RequestID: requestID, SessionID: childID, OwnerID: owner, ExpectedSeq: int64(len(snapshot.Events)), Content: "Additional task materials:\n" + string(mutation.Message), Actor: actor})
		return err
	}
	return nil
}

func agentMutationExists(eventType, requestID string) func([]Event) bool {
	return func(events []Event) bool {
		for _, event := range events {
			var mutation agentTaskMutation
			if event.Type == eventType && json.Unmarshal(event.Payload, &mutation) == nil && mutation.RequestID == requestID {
				return true
			}
		}
		return false
	}
}

func (r *SessionRunner) appendAgentFact(ctx context.Context, sessionID, eventType string, payload any, exists func([]Event) bool) error {
	for attempt := 0; attempt < 16; attempt++ {
		snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, sessionID)
		if err != nil {
			return err
		}
		if exists != nil && exists(snapshot.Events) {
			encoded, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			canonical, err := canonicalToolArguments(string(encoded))
			if err != nil {
				return err
			}
			var desired agentTaskMutation
			_ = json.Unmarshal(encoded, &desired)
			for _, e := range snapshot.Events {
				if e.Type != eventType {
					continue
				}
				var existing agentTaskMutation
				_ = json.Unmarshal(e.Payload, &existing)
				if eventType == "agent/task-created" || desired.RequestID != "" && existing.RequestID == desired.RequestID {
					previous, _ := canonicalToolArguments(string(e.Payload))
					if previous != canonical {
						return ErrSessionStateConflict
					}
				}
				if eventType == "agent/task-linked" {
					var old, next agentTaskLink
					_ = json.Unmarshal(e.Payload, &old)
					_ = json.Unmarshal(encoded, &next)
					if old.TaskID == next.TaskID && old != next {
						return ErrSessionStateConflict
					}
				}
			}
			return nil
		}
		for _, e := range snapshot.Events {
			if e.Type == "session/deleted" {
				return ErrSessionNotFound
			}
			if e.Type == "agent/task-canceled" {
				return ErrSessionStateConflict
			}
		}
		if eventType == "agent/task-artifact" {
			task, _, err := projectAgentTask(snapshot.Events)
			if err != nil {
				return err
			}
			if task == nil || len(task.Artifacts) >= 16 {
				return ErrInvalidSessionInput
			}
		}
		_, err = r.workbench.ledger.Append(ctx, sessionID, int64(len(snapshot.Events)), eventType, payload)
		if err == nil {
			r.workbench.signal(sessionID)
			return nil
		}
		if !errors.Is(err, ErrSequenceConflict) {
			return err
		}
	}
	return ErrSequenceConflict
}

func agentApprovalView(childID string, event Event, pending toolApprovalPayload) *pb.AgentToolApproval {
	return &pb.AgentToolApproval{ChildSessionId: childID, RunId: pending.RunID, ToolCallId: pending.ToolCallID,
		ToolName: pending.ToolName, ArgumentsJson: tools.RedactSensitive(pending.ArgumentsJSON), PendingEventId: event.EventID, PendingSeq: event.Seq}
}

func verifyArtifactChecksum(artifact *pb.AgentArtifact) error {
	if artifact == nil || artifact.Id == "" || len(artifact.Parts) == 0 || len(artifact.Parts) > 16 {
		return ErrEventIntegrity
	}
	copy := proto.Clone(artifact).(*pb.AgentArtifact)
	copy.Checksum = ""
	body, err := agentJSON.Marshal(copy)
	if err != nil || agentDigest(body) != artifact.Checksum {
		return ErrEventIntegrity
	}
	return nil
}

func verifyAgentArtifacts(artifacts []*pb.AgentArtifact, dir string) error {
	for _, artifact := range artifacts {
		if err := verifyArtifactChecksum(artifact); err != nil {
			return err
		}
		for _, part := range artifact.Parts {
			file := part.GetFile()
			if file == nil {
				continue
			}
			if !filepath.IsLocal(file.Path) || !strings.HasPrefix(filepath.ToSlash(filepath.Clean(file.Path)), ".agent/artifacts/") {
				return ErrEventIntegrity
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				return ErrEventIntegrity
			}
			stream, err := root.Open(file.Path)
			if err != nil {
				_ = root.Close()
				return ErrEventIntegrity
			}
			info, statErr := stream.Stat()
			body, readErr := io.ReadAll(io.LimitReader(stream, (4<<20)+1))
			_ = stream.Close()
			_ = root.Close()
			if statErr != nil || !info.Mode().IsRegular() || readErr != nil || int64(len(body)) != file.SizeBytes || len(body) > 4<<20 || agentDigest(body) != file.Sha256 {
				return ErrEventIntegrity
			}
		}
	}
	return nil
}

func (r *SessionRunner) askAgentInput(ctx context.Context, actor identity.Actor, sessionID string, call orchestrator.ToolCall) orchestrator.ToolResult {
	message := &pb.AgentMessage{Id: call.ID, Role: "agent", Parts: []*pb.AgentPart{{Payload: &pb.AgentPart_Text{Text: "Additional input required: " + tools.RedactSensitive(call.ParametersJSON)}}}}
	encoded, err := agentJSON.Marshal(message)
	if err != nil {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "invalid agent input request", ExitCode: 1}
	}
	args, _ := json.Marshal(map[string]any{"action": "input_required", "task_id": sessionID, "message": json.RawMessage(encoded)})
	result := r.ExecuteAgentTool(ctx, actor, sessionID, orchestrator.ToolCall{ID: call.ID, Name: "AgentTask", ParametersJSON: string(args)})
	result.ToolName = call.Name
	return result
}

func (r *SessionRunner) publishArtifact(ctx context.Context, owner uint, actor identity.Actor, call orchestrator.ToolCall) (output string, err error) {
	ctx = genai.WithEvalInstance(ctx, actor.SessionID)
	var span genai.Span
	if r.options.Tracer != nil {
		ctx, span = r.options.Tracer.StartSpan(ctx, "artifact.publish", genai.OperationExecuteTool, genai.SystemGenAI)
		span.SetAttributes(genai.ToolNameKV("PublishArtifact"))
		defer func() {
			if err != nil {
				genai.MarkSpanError(span)
			}
			span.End()
		}()
	}
	task, _, err := r.readAgentTask(ctx, owner, actor.SessionID, actor.SessionID)
	if err != nil || task.Status == "canceled" {
		return "", ErrSessionNotFound
	}
	var args struct {
		Artifact json.RawMessage `json:"artifact"`
	}
	decoder := json.NewDecoder(strings.NewReader(call.ParametersJSON))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil {
		return "", ErrInvalidSessionInput
	}
	artifact := &pb.AgentArtifact{}
	if protojson.Unmarshal(args.Artifact, artifact) != nil || strings.TrimSpace(artifact.Name) == "" || len(artifact.Name) > 240 || call.ID == "" {
		return "", ErrInvalidSessionInput
	}
	view, err := r.workbench.Get(ctx, owner, task.ChildSessionId)
	if err != nil {
		return "", err
	}
	if err := validateAgentParts(artifact.Parts, view.WorkingDir, view.WorkingDir); err != nil {
		return "", err
	}
	if err := pinAgentFiles(artifact.Parts, view.WorkingDir, ".agent/artifacts"); err != nil {
		return "", err
	}
	artifact.Id = call.ID
	artifact.Checksum = ""
	body, err := agentJSON.Marshal(artifact)
	if err != nil {
		return "", err
	}
	artifact.Checksum = agentDigest(body)
	encoded, err := agentJSON.Marshal(artifact)
	if err != nil {
		return "", err
	}
	for _, existing := range task.Artifacts {
		if existing.Id == call.ID {
			if !proto.Equal(existing, artifact) {
				return "", ErrSessionStateConflict
			}
			return string(encoded), nil
		}
	}
	if len(task.Artifacts) >= 16 {
		return "", ErrInvalidSessionInput
	}
	if err := r.appendAgentFact(ctx, task.ChildSessionId, "agent/task-artifact", agentTaskMutation{RequestID: call.ID, Artifact: encoded}, agentMutationExists("agent/task-artifact", call.ID)); err != nil {
		return "", err
	}
	return string(encoded), nil
}

func validateAgentParts(parts []*pb.AgentPart, sourceDir, targetDir string) error {
	if len(parts) == 0 || len(parts) > 16 {
		return ErrInvalidSessionInput
	}
	for _, part := range parts {
		if part == nil {
			return ErrInvalidSessionInput
		}
		switch payload := part.Payload.(type) {
		case *pb.AgentPart_Text:
			if len(payload.Text) > 24000 {
				return ErrInvalidSessionInput
			}
			payload.Text = tools.RedactSensitive(payload.Text)
		case *pb.AgentPart_DataJson:
			if len(payload.DataJson) > 16000 || !json.Valid([]byte(payload.DataJson)) {
				return ErrInvalidSessionInput
			}
			payload.DataJson = tools.RedactSensitive(payload.DataJson)
			if !json.Valid([]byte(payload.DataJson)) {
				return ErrInvalidSessionInput
			}
		case *pb.AgentPart_File:
			if payload.File == nil {
				return ErrInvalidSessionInput
			}
			file := payload.File
			path := filepath.Clean(file.Path)
			base := strings.ToLower(filepath.Base(path))
			if file.Path == "" || filepath.IsAbs(path) || !filepath.IsLocal(path) || strings.ContainsAny(file.Path, "\x00\r\n") || strings.Contains(path, ":") || strings.HasPrefix(base, ".env") || base == "credentials" || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || strings.Contains(path, ".git") {
				return ErrInvalidSessionInput
			}
			root, err := os.OpenRoot(sourceDir)
			if err != nil {
				return err
			}
			stream, err := root.Open(path)
			if err != nil {
				_ = root.Close()
				return err
			}
			info, err := stream.Stat()
			if err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
				_ = stream.Close()
				_ = root.Close()
				return ErrInvalidSessionInput
			}
			body, err := io.ReadAll(io.LimitReader(stream, (4<<20)+1))
			_ = stream.Close()
			_ = root.Close()
			if err != nil || len(body) > 4<<20 {
				return ErrInvalidSessionInput
			}
			digest := agentDigest(body)
			if file.Sha256 != "" && file.Sha256 != digest || file.SizeBytes != 0 && file.SizeBytes != int64(len(body)) {
				return ErrInvalidSessionInput
			}
			file.Sha256 = digest
			file.SizeBytes = int64(len(body))
			if sourceDir != targetDir {
				target, err := os.OpenRoot(targetDir)
				if err != nil {
					return err
				}
				if err = target.MkdirAll(".agent/materials", 0700); err == nil {
					file.Path = filepath.ToSlash(filepath.Join(".agent", "materials", digest+"-"+filepath.Base(path)))
					err = target.WriteFile(file.Path, body, 0600)
				}
				_ = target.Close()
				if err != nil {
					return err
				}
			}
		default:
			return ErrInvalidSessionInput
		}
	}
	return nil
}

func agentDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func pinAgentFiles(parts []*pb.AgentPart, dir, destination string) error {
	hasFiles := false
	for _, part := range parts {
		hasFiles = hasFiles || part.GetFile() != nil
	}
	if !hasFiles {
		return nil
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(destination, 0700); err != nil {
		return err
	}
	for _, part := range parts {
		file := part.GetFile()
		if file == nil {
			continue
		}
		stream, err := root.Open(file.Path)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(io.LimitReader(stream, (4<<20)+1))
		_ = stream.Close()
		if err != nil || len(body) > 4<<20 || agentDigest(body) != file.Sha256 {
			return ErrEventIntegrity
		}
		path := filepath.ToSlash(filepath.Join(destination, file.Sha256+"-"+filepath.Base(file.Path)))
		if existing, err := root.ReadFile(path); err == nil {
			if agentDigest(existing) != file.Sha256 {
				return ErrEventIntegrity
			}
		} else {
			output, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			_, err = output.Write(body)
			closeErr := output.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		file.Path = path
	}
	return nil
}

func (r *SessionRunner) cancelAgentTask(ctx context.Context, task *pb.AgentTask, requestID string) error {
	if err := r.appendAgentFact(ctx, task.ChildSessionId, "agent/task-canceled", agentTaskMutation{RequestID: requestID}, agentMutationExists("agent/task-canceled", requestID)); err != nil {
		return err
	}
	if err := r.fenceAgentCancellation(ctx, task); err != nil {
		return err
	}
	task.Status = "canceled"
	r.releaseSessionResources(task.ChildSessionId)
	return r.reconcileAgentWorkspaceLifecycle(ctx, task)
}

func (r *SessionRunner) fenceAgentCancellation(ctx context.Context, task *pb.AgentTask) error {
	r.agentMu.Lock()
	for key, cancel := range r.activeAgentRuns {
		if key.sessionID == task.ChildSessionId {
			cancel()
		}
	}
	r.agentMu.Unlock()
	for attempt := 0; attempt < 16; attempt++ {
		snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, task.ChildSessionId)
		if err != nil {
			return err
		}
		events := snapshot.Events
		runs, err := projectRuns(events)
		if err != nil {
			return err
		}
		var active *runProjection
		for _, run := range runs {
			if !run.terminal {
				copy := run
				active = &copy
				break
			}
		}
		if active == nil {
			r.workbench.signal(task.ChildSessionId)
			return nil
		}
		_, err = r.workbench.ledger.Append(ctx, task.ChildSessionId, int64(len(events)), runCanceledEventType, runTerminalPayload{RunID: active.view.RunID, RequestID: active.view.RequestID, LeaseID: active.leaseID, Attempt: active.view.Attempt})
		if err != nil && !errors.Is(err, ErrSequenceConflict) {
			return err
		}
	}
	return ErrSequenceConflict
}

// DecideAgentToolApproval is a human transport interface, not an AgentTask
// action. The pending fact supplies immutable tool identity and arguments.
func (r *SessionRunner) DecideAgentToolApproval(ctx context.Context, actor identity.Actor, taskID string, command ToolApprovalDecisionCommand) (EventView, error) {
	parent, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, actor.SessionID)
	if err != nil {
		return EventView{}, err
	}
	owner, _, err := agentParent(parent.Events, actor)
	if err != nil {
		return EventView{}, err
	}
	task, _, err := r.readAgentTask(ctx, owner, taskID, actor.SessionID)
	if err != nil {
		return EventView{}, err
	}
	if task.ParentSessionId != actor.SessionID || task.Status == "canceled" {
		return EventView{}, ErrSessionStateConflict
	}
	return r.workbench.DecideToolApproval(ctx, owner, task.ChildSessionId, command)
}

func (r *SessionRunner) recoverAgentTask(ctx context.Context, owner uint, events []Event) error {
	task, created, err := projectAgentTask(events)
	if err != nil || task == nil {
		return err
	}
	if task.Status == "canceled" {
		if err := r.fenceAgentCancellation(ctx, task); err != nil {
			return err
		}
		return r.reconcileAgentWorkspaceLifecycle(ctx, task)
	}
	if task.Status == "failed" {
		return nil
	}
	if _, _, err := r.readAgentTask(ctx, owner, task.Id, task.ChildSessionId); err != nil {
		return err
	}
	if err := r.admitInitialAgentTurn(ctx, owner, created.Actor, task); err != nil {
		return err
	}
	return r.admitAgentMessages(ctx, owner, task.ChildSessionId, created.Actor)
}

func (r *SessionRunner) completeAgentTurn(ctx context.Context, key runKey) {
	snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, key.sessionID)
	if err != nil {
		return
	}
	task, created, err := projectAgentTask(snapshot.Events)
	if err != nil || task == nil {
		return
	}
	if taskTerminal(task.Status) {
		defer r.releaseSessionResources(key.sessionID)
	}
	if task.Status == "canceled" {
		// Keep the durable canceled task untouched when the Harness callback
		// fails. Recover() will see the missing acknowledgement and retry the
		// cleanup on the next process start instead of projecting success.
		if err := r.reconcileAgentWorkspaceLifecycle(ctx, task); err != nil {
			return
		}
	}
	if task.Status == "canceled" {
		return
	}
	if task.Status == "completed" {
		requestID := "report-" + key.runID
		for i := len(snapshot.Events) - 1; i >= 0; i-- {
			if snapshot.Events[i].Type == "assistant/message" {
				var message messagePayload
				if json.Unmarshal(snapshot.Events[i].Payload, &message) != nil {
					return
				}
				if message.RunID != key.runID {
					continue
				}
				artifact := &pb.AgentArtifact{Id: requestID, Name: "Task report", Parts: []*pb.AgentPart{{Payload: &pb.AgentPart_Text{Text: tools.RedactSensitive(message.Content)}}}}
				body, _ := agentJSON.Marshal(artifact)
				artifact.Checksum = agentDigest(body)
				body, _ = agentJSON.Marshal(artifact)
				_ = r.appendAgentFact(ctx, key.sessionID, "agent/task-artifact", agentTaskMutation{RequestID: requestID, Artifact: body}, agentMutationExists("agent/task-artifact", requestID))
				break
			}
		}
	}
	view, err := reduceSessionView(snapshot.Events)
	if err != nil {
		return
	}
	_ = r.admitAgentMessages(ctx, view.UserID, key.sessionID, created.Actor)
}

func (r *SessionRunner) callAgentLifecycle(ctx context.Context, lifecycle *pb.AgentLifecycle) error {
	if r.options.AgentLifecycle == nil {
		return nil
	}
	callbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return r.options.AgentLifecycle(callbackCtx, lifecycle)
}

func (r *SessionRunner) reconcileAgentWorkspaceLifecycle(ctx context.Context, task *pb.AgentTask) error {
	if task == nil || task.Card == nil || task.Card.Id != "general" && task.Card.Id != "background" || task.Status != "canceled" || r.options.AgentLifecycle == nil {
		return nil
	}
	const status = "cancelled"
	const reason = "independent agent task cancelled"

	r.agentLifecycleMu.Lock()
	defer r.agentLifecycleMu.Unlock()
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	acknowledged := agentWorkspaceLifecycleAcknowledged{
		RequestID: task.Id, ChildSessionID: task.ChildSessionId, Status: status, Reason: reason,
	}
	callbackDone := false
	for attempt := 0; attempt < 16; attempt++ {
		snapshot, err := ReadVerifiedSnapshot(reconcileCtx, r.workbench.ledger, task.ChildSessionId)
		if err != nil {
			return err
		}
		for _, event := range snapshot.Events {
			if event.Type != agentWorkspaceLifecycleAcknowledgedEventType {
				continue
			}
			var existing agentWorkspaceLifecycleAcknowledged
			if json.Unmarshal(event.Payload, &existing) != nil || existing != acknowledged {
				return ErrEventIntegrity
			}
			return nil
		}
		if !callbackDone {
			if err := r.options.AgentLifecycle(reconcileCtx, &pb.AgentLifecycle{
				RequestId: task.Id, ChildSessionId: task.ChildSessionId, Status: status, Reason: reason,
			}); err != nil {
				return err
			}
			callbackDone = true
		}
		if _, err := r.workbench.ledger.Append(reconcileCtx, task.ChildSessionId, int64(len(snapshot.Events)), agentWorkspaceLifecycleAcknowledgedEventType, acknowledged); err == nil {
			r.workbench.signal(task.ChildSessionId)
			return nil
		} else if !errors.Is(err, ErrSequenceConflict) {
			return err
		}
	}
	return ErrSequenceConflict
}

func (r *SessionRunner) agentRequest(events []Event, request *orchestrator.ConversationRequest) error {
	task, created, err := projectAgentTask(events)
	if err != nil {
		return err
	}
	if task == nil {
		return nil
	}
	if task.Status == "canceled" {
		return context.Canceled
	}
	if _, _, err := r.readAgentTask(r.ctx, projectionOwner(events), task.Id, task.ChildSessionId); err != nil {
		return err
	}
	request.AgentTask = proto.Clone(task).(*pb.AgentTask)
	request.AllowedTools = append([]string(nil), created.AllowedTools...)
	return nil
}

func agentToolPolicy(events []Event, name string) (bool, *AgentToolBinding, error) {
	task, created, err := projectAgentTask(events)
	if err != nil {
		return false, nil, err
	}
	if task != nil && task.Status == "canceled" {
		return false, nil, nil
	}
	if task == nil {
		return true, nil, nil
	}
	for _, allowed := range created.AllowedTools {
		if name == allowed {
			if staticAgentTool(name) {
				return true, nil, nil
			}
			if task.Card == nil || task.Card.Id != "general" && task.Card.Id != "background" {
				return false, nil, nil
			}
			for _, binding := range created.ExternalToolBindings {
				if binding.Name == name {
					copy := binding
					return true, &copy, nil
				}
			}
			// Legacy dynamic delegations without an immutable binding fail closed.
			return false, nil, nil
		}
	}
	return false, nil, nil
}

func validAgentToolBindings(created agentTaskCreated) bool {
	allowed := make(map[string]bool, len(created.AllowedTools))
	for _, name := range created.AllowedTools {
		if strings.TrimSpace(name) == "" || allowed[name] {
			return false
		}
		allowed[name] = true
	}
	seen := make(map[string]bool, len(created.ExternalToolBindings))
	for _, binding := range created.ExternalToolBindings {
		if !validAgentToolBinding(binding) || !allowed[binding.Name] || staticAgentTool(binding.Name) || seen[binding.Name] {
			return false
		}
		seen[binding.Name] = true
	}
	return true
}

func validAgentToolBinding(binding AgentToolBinding) bool {
	if strings.TrimSpace(binding.Name) != binding.Name || binding.Name == "" || strings.TrimSpace(binding.Server) != binding.Server || binding.Server == "" || len(binding.InputSchemaSHA256) != 64 || len(binding.ServerConfigSHA256) != 64 {
		return false
	}
	_, schemaErr := hex.DecodeString(binding.InputSchemaSHA256)
	_, configErr := hex.DecodeString(binding.ServerConfigSHA256)
	return schemaErr == nil && configErr == nil && strings.ToLower(binding.InputSchemaSHA256) == binding.InputSchemaSHA256 && strings.ToLower(binding.ServerConfigSHA256) == binding.ServerConfigSHA256
}
