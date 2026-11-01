package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/safety"
)

const evaluationWritePreimageEvent = "evaluation/write-preimage"
const evaluationWriteReconciledEvent = "evaluation/write-reconciled"

type evaluationFileState struct {
	Exists bool   `json:"exists"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type evaluationWritePreimage struct {
	SchemaVersion int                    `json:"schema_version"`
	RunID         string                 `json:"run_id"`
	ToolCallID    string                 `json:"tool_call_id"`
	LeaseID       string                 `json:"lease_id"`
	OperationID   string                 `json:"operation_id"`
	ArgumentsHash string                 `json:"arguments_sha256"`
	ContentHash   string                 `json:"content_sha256"`
	ContentSize   int64                  `json:"content_size"`
	Path          string                 `json:"path"`
	Workspace     string                 `json:"workspace"`
	BindingHash   string                 `json:"binding_checksum"`
	Actor         identity.Actor         `json:"actor"`
	Process       safety.ProcessIdentity `json:"process"`
	Before        evaluationFileState    `json:"before"`
}

type EvaluationWriteReconcileCommand struct {
	Actor                       identity.Actor
	SessionID                   string
	RunID                       string
	ToolCallID                  string
	ExpectedSessionHeadChecksum string
}

// State describes an observed file transition, never its physical execution count.
type EvaluationWriteReconciliation struct {
	State                  string `json:"state"`
	EffectKnown            bool   `json:"effect_known"`
	ReplayAllowed          bool   `json:"replay_allowed"`
	AlreadyRecorded        bool   `json:"already_recorded"`
	ReconciliationEventID  string `json:"reconciliation_event_id"`
	ReconciliationChecksum string `json:"reconciliation_checksum"`
	ToolResultRecorded     bool   `json:"tool_result_recorded"`
	PhysicalExecutionCount *int   `json:"physical_execution_count"`
}

type evaluationWriteProof struct {
	SchemaVersion          int                    `json:"schema_version"`
	RunID                  string                 `json:"run_id"`
	ToolCallID             string                 `json:"tool_call_id"`
	State                  string                 `json:"state"`
	Reason                 string                 `json:"reason,omitempty"`
	SourceHead             string                 `json:"source_head_checksum"`
	PreimageEventID        string                 `json:"preimage_event_id"`
	PreimageChecksum       string                 `json:"preimage_checksum"`
	PendingEventID         string                 `json:"pending_event_id"`
	PendingChecksum        string                 `json:"pending_checksum"`
	ArgumentsHash          string                 `json:"arguments_sha256"`
	Actor                  identity.Actor         `json:"actor"`
	StoppedProcess         safety.ProcessIdentity `json:"stopped_process"`
	Observed               *evaluationFileState   `json:"observed"`
	PhysicalExecutionCount *int                   `json:"physical_execution_count"`
}

func evaluationPlainWrite(workspace, arguments string) (string, string, error) {
	var args map[string]any
	if json.Unmarshal([]byte(arguments), &args) != nil || len(args) != 2 {
		return "", "", ErrInvalidSessionInput
	}
	path, pathOK := args["path"].(string)
	content, contentOK := args["content"].(string)
	if !pathOK || !contentOK || !utf8.ValidString(path) || strings.IndexFunc(path, unicode.IsControl) >= 0 || len(content) > 8<<20 {
		return "", "", ErrInvalidSessionInput
	}
	if filepath.IsAbs(path) {
		var err error
		path, err = filepath.Rel(workspace, path)
		if err != nil {
			return "", "", ErrInvalidSessionInput
		}
	}
	path = filepath.ToSlash(path)
	if !fs.ValidPath(path) || path == "." || strings.ContainsAny(path, "\\:") {
		return "", "", ErrInvalidSessionInput
	}
	for _, part := range strings.Split(path, "/") {
		if strings.EqualFold(part, ".git") || strings.TrimRight(part, " .") != part {
			return "", "", ErrInvalidSessionInput
		}
	}
	if strings.EqualFold(filepath.Ext(path), ".pdf") || strings.EqualFold(filepath.Ext(path), ".ipynb") {
		return "", "", ErrInvalidSessionInput
	}
	return path, content, nil
}

// The caller must first authorize Read. Existing parent directories are required;
// this bounded observer neither creates directories nor renders file contents.
func observeEvaluationFile(ctx context.Context, workspace, path string) (evaluationFileState, error) {
	if err := ctx.Err(); err != nil {
		return evaluationFileState{}, err
	}
	abs, err := filepath.Abs(workspace)
	if err != nil || !strings.EqualFold(filepath.Clean(abs), filepath.Clean(workspace)) {
		return evaluationFileState{}, ErrInvalidSessionInput
	}
	release, err := safety.PinDirectories(abs, filepath.Join(abs, filepath.FromSlash(filepath.Dir(path))))
	if err != nil {
		return evaluationFileState{}, err
	}
	defer release()
	root, err := os.OpenRoot(abs)
	if err != nil {
		return evaluationFileState{}, err
	}
	defer root.Close()
	info, err := root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return evaluationFileState{}, nil
	}
	if err != nil || !info.Mode().IsRegular() || safety.RejectReparsePoint(filepath.Join(abs, filepath.FromSlash(path))) != nil {
		return evaluationFileState{}, ErrSessionStateConflict
	}
	unpin, err := safety.PinReadOnlyFiles(filepath.Join(abs, filepath.FromSlash(path)))
	if err != nil {
		return evaluationFileState{}, err
	}
	defer unpin()
	file, err := root.Open(path)
	if err != nil {
		return evaluationFileState{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 8<<20+1))
	after, statErr := file.Stat()
	current, currentErr := root.Lstat(path)
	if err != nil || statErr != nil || currentErr != nil || len(data) > 8<<20 ||
		!os.SameFile(info, after) || !os.SameFile(after, current) || info.Size() != int64(len(data)) ||
		after.Size() != int64(len(data)) || !info.ModTime().Equal(after.ModTime()) || ctx.Err() != nil {
		return evaluationFileState{}, ErrSessionStateConflict
	}
	return evaluationFileState{Exists: true, SHA256: hashCodeState(string(data)), Size: int64(len(data))}, nil
}

// Reconciliation needs the retained identity before the full workspace gate can
// accept the repaired effect. This never grants model/tool admission by itself.
func evaluationWriteWorkspace(binding *EvaluationBinding, view SessionView, events []Event) (string, error) {
	if binding.RuntimeContract == "" {
		if strings.EqualFold(filepath.Clean(binding.Workspace), filepath.Clean(view.WorkingDir)) {
			return binding.Workspace, nil
		}
		return "", ErrSessionStateConflict
	}
	prepared, state, err := taskPreparation(events)
	if err != nil || binding.RuntimeContract != "production_opensandbox" || prepared == nil || state != "prepared" || binding.Runtime.OpenSandbox == nil ||
		!filepath.IsAbs(binding.Runtime.TaskWorkspaceRoot) || !strings.EqualFold(filepath.Clean(binding.SourceWorkspace), filepath.Clean(view.WorkingDir)) {
		return "", ErrSessionStateConflict
	}
	actual := filepath.Join(binding.Runtime.TaskWorkspaceRoot, prepared.Task.LeaseID)
	if !strings.EqualFold(filepath.Clean(binding.Workspace), filepath.Clean(actual)) {
		return "", ErrSessionStateConflict
	}
	return actual, nil
}

func (r *SessionRunner) captureEvaluationWritePreimage(ctx context.Context, key runKey, lease runLeasePayload, actor identity.Actor, workingDir string, call orchestrator.ToolCall) error {
	if call.Name != "Write" {
		return nil
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, key.sessionID)
	if err != nil {
		return err
	}
	binding, err := r.evaluationBinding(ctx, snapshot.Events)
	if err != nil || binding == nil {
		return err
	}
	if binding.Policy.FaultWindow != "before_effect" && binding.Policy.FaultWindow != "after_effect" || binding.Policy.FaultTool != "" && binding.Policy.FaultTool != "Write" {
		return nil
	}
	for _, event := range snapshot.Events {
		if event.Type == "evaluation/fault-ready" {
			return nil
		}
	}
	root, err := ModelTaskRoot(ctx, r.workbench.ledger, actor)
	if err != nil || root != key.sessionID || !strings.EqualFold(filepath.Clean(binding.Workspace), filepath.Clean(workingDir)) {
		return ErrSessionStateConflict
	}
	path, content, err := evaluationPlainWrite(binding.Workspace, call.ParametersJSON)
	if err != nil {
		return err
	}
	if r.options.Permissions == nil || r.options.Permissions.CheckFor(actor, "Read", map[string]any{"path": path}) != permission.Approve {
		return ErrSessionStateConflict
	}
	before, err := observeEvaluationFile(ctx, binding.Workspace, path)
	if err != nil {
		return err
	}
	process, err := safety.CurrentProcessIdentity()
	if err != nil {
		return err
	}
	preimage := evaluationWritePreimage{SchemaVersion: 1, RunID: key.runID, ToolCallID: call.ID, LeaseID: lease.LeaseID,
		OperationID: call.OperationID, ArgumentsHash: hashCodeState(call.ParametersJSON), ContentHash: hashCodeState(content),
		ContentSize: int64(len(content)), Path: path, Workspace: binding.Workspace, Actor: actor, Process: *process, Before: before}
	for _, event := range snapshot.Events {
		if event.Type == evaluationPolicyEvent {
			preimage.BindingHash = event.Checksum
		}
		if event.Type == evaluationWritePreimageEvent {
			var previous evaluationWritePreimage
			if json.Unmarshal(event.Payload, &previous) != nil || previous.RunID == key.runID && previous.ToolCallID == call.ID {
				return ErrSessionStateConflict
			}
		}
	}
	return r.appendLeasedFact(ctx, key, lease.LeaseID, evaluationWritePreimageEvent, preimage)
}

// ReconcileEvaluationWrite only observes an already stopped, evaluation-owned
// plain Write. It never executes Write or settles model usage.
func (r *SessionRunner) ReconcileEvaluationWrite(ctx context.Context, command EvaluationWriteReconcileCommand) (EvaluationWriteReconciliation, error) {
	result := EvaluationWriteReconciliation{State: "unknown"}
	if r == nil || r.workbench == nil || command.Actor.Validate() != nil || command.Actor.SessionID != command.SessionID || command.RunID == "" || command.ToolCallID == "" || len(command.ExpectedSessionHeadChecksum) != 64 {
		return result, ErrInvalidSessionInput
	}
	r.appendMu.Lock()
	defer r.appendMu.Unlock()
	snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, command.SessionID)
	if err != nil {
		return result, err
	}
	events := snapshot.Events
	if len(events) == 0 || events[len(events)-1].Checksum != command.ExpectedSessionHeadChecksum {
		return result, ErrSequenceConflict
	}
	root, err := ModelTaskRoot(ctx, r.workbench.ledger, command.Actor)
	if err != nil || root != command.SessionID {
		return result, ErrSessionStateConflict
	}
	binding, err := ReadEvaluation(events)
	view, viewErr := reduceSessionView(events)
	run, runErr := projectRun(events, command.RunID)
	if err != nil || viewErr != nil || runErr != nil || binding == nil || view.Run == nil || view.Run.RunID != command.RunID || run.actor.ScopeKey() != command.Actor.ScopeKey() ||
		(binding.Policy.FaultWindow != "before_effect" && binding.Policy.FaultWindow != "after_effect") || binding.Policy.FaultTool != "" && binding.Policy.FaultTool != "Write" {
		return result, ErrSessionStateConflict
	}
	workingDir, workspaceErr := evaluationWriteWorkspace(binding, view, events)
	if workspaceErr != nil {
		return result, workspaceErr
	}
	var preimage evaluationWritePreimage
	var preimageEvent, pendingEvent, preparedEvent, dispatchedEvent, proofEvent Event
	var proof evaluationWriteProof
	var pending commitPayload
	var prepared toolCallPayload
	counts := map[string]int{}
	faultSeq := int64(-1)
	for _, event := range events {
		switch event.Type {
		case evaluationWritePreimageEvent:
			var value evaluationWritePreimage
			if json.Unmarshal(event.Payload, &value) != nil {
				return result, ErrEventIntegrity
			}
			if value.RunID == command.RunID && value.ToolCallID == command.ToolCallID {
				preimage, preimageEvent = value, event
				counts[event.Type]++
			}
		case "tool/call", toolDispatchedType:
			var value toolCallPayload
			if json.Unmarshal(event.Payload, &value) != nil {
				return result, ErrEventIntegrity
			}
			if value.RunID == command.RunID && value.ToolCallID == command.ToolCallID {
				counts[event.Type]++
				if event.Type == "tool/call" {
					prepared, preparedEvent = value, event
				} else {
					dispatchedEvent = event
					if value.ToolName != "Write" || value.ArgumentsJSON != prepared.ArgumentsJSON || value.OperationID != prepared.OperationID {
						return result, ErrEventIntegrity
					}
				}
			}
		case commitPendingEventType:
			var value commitPayload
			if json.Unmarshal(event.Payload, &value) != nil {
				return result, ErrEventIntegrity
			}
			if value.RunID == command.RunID && value.ToolCallID == command.ToolCallID {
				pending, pendingEvent = value, event
				counts[event.Type]++
			}
		case "evaluation/fault-ready":
			var value struct {
				Window string `json:"window"`
				CallID string `json:"tool_call_id"`
			}
			if json.Unmarshal(event.Payload, &value) != nil || value.Window != binding.Policy.FaultWindow || value.CallID != command.ToolCallID || faultSeq >= 0 {
				return result, ErrSessionStateConflict
			}
			faultSeq = event.Seq
		case evaluationWriteReconciledEvent:
			var value evaluationWriteProof
			if json.Unmarshal(event.Payload, &value) != nil {
				return result, ErrEventIntegrity
			}
			if value.RunID == command.RunID && value.ToolCallID == command.ToolCallID {
				proof, proofEvent = value, event
				counts[event.Type]++
			}
		}
	}
	for _, kind := range []string{evaluationWritePreimageEvent, "tool/call", toolDispatchedType, commitPendingEventType} {
		if counts[kind] != 1 {
			return result, ErrSessionStateConflict
		}
	}
	if counts[evaluationWriteReconciledEvent] > 1 || prepared.ToolName != "Write" || pending.ToolName != "Write" ||
		pending.CommitID != commitIDFor(command.RunID, command.ToolCallID) || pending.ArgumentsJSON != prepared.ArgumentsJSON ||
		preimage.SchemaVersion != 1 || preimage.LeaseID == "" || preimage.LeaseID != run.leaseID || preimage.Actor.ScopeKey() != command.Actor.ScopeKey() || preimage.Workspace != binding.Workspace ||
		preimage.ArgumentsHash != hashCodeState(prepared.ArgumentsJSON) || preimage.OperationID != prepared.OperationID ||
		preparedEvent.Seq >= preimageEvent.Seq || preimageEvent.Seq >= pendingEvent.Seq || pendingEvent.Seq >= dispatchedEvent.Seq || faultSeq <= dispatchedEvent.Seq ||
		!preimage.Process.ConfirmedStopped() {
		return result, ErrSessionStateConflict
	}
	for _, event := range events {
		if event.Type == evaluationPolicyEvent && event.Checksum != preimage.BindingHash {
			return result, ErrSessionStateConflict
		}
		if event.Seq > preimageEvent.Seq && event.Type == toolDispatchedType && event.EventID != dispatchedEvent.EventID {
			var other toolCallPayload
			if json.Unmarshal(event.Payload, &other) != nil || !isReadOnlyTool(other.ToolName) {
				return result, ErrSessionStateConflict
			}
		}
	}
	path, content, err := evaluationPlainWrite(workingDir, prepared.ArgumentsJSON)
	if err != nil || path != preimage.Path || hashCodeState(content) != preimage.ContentHash || int64(len(content)) != preimage.ContentSize ||
		r.options.Permissions == nil || r.options.Permissions.CheckFor(command.Actor, "Read", map[string]any{"path": path}) != permission.Approve {
		return result, ErrSessionStateConflict
	}
	invocation := invocationProjection(events, command.RunID, command.ToolCallID)
	if proofEvent.EventID == "" {
		if invocation.result != nil {
			return result, ErrSessionStateConflict
		}
		observed, observeErr := observeEvaluationFile(ctx, workingDir, path)
		proof = evaluationWriteProof{SchemaVersion: 1, RunID: command.RunID, ToolCallID: command.ToolCallID, State: "unknown",
			SourceHead: command.ExpectedSessionHeadChecksum, PreimageEventID: preimageEvent.EventID, PreimageChecksum: preimageEvent.Checksum,
			PendingEventID: pendingEvent.EventID, PendingChecksum: pendingEvent.Checksum, ArgumentsHash: preimage.ArgumentsHash,
			Actor: command.Actor, StoppedProcess: preimage.Process, Reason: "file_state_unavailable"}
		if observeErr == nil {
			proof.Observed = &observed
			proof.Reason = "file_state_ambiguous"
			postimage := evaluationFileState{Exists: true, SHA256: preimage.ContentHash, Size: preimage.ContentSize}
			if preimage.Before != postimage {
				if observed == postimage {
					proof.State, proof.Reason = "applied", ""
				} else if observed == preimage.Before {
					proof.State, proof.Reason = "not_applied", ""
				}
			}
		}
		proofEvent, err = r.workbench.ledger.Append(ctx, command.SessionID, int64(len(events)), evaluationWriteReconciledEvent, proof)
		if err != nil {
			return result, err
		}
		events = append(events, proofEvent)
	} else {
		result.AlreadyRecorded = true
		if proof.SchemaVersion != 1 || proof.PreimageEventID != preimageEvent.EventID || proof.PreimageChecksum != preimageEvent.Checksum ||
			proof.PendingEventID != pendingEvent.EventID || proof.PendingChecksum != pendingEvent.Checksum || proof.ArgumentsHash != preimage.ArgumentsHash ||
			proof.Actor.ScopeKey() != command.Actor.ScopeKey() || proof.StoppedProcess != preimage.Process || proof.PhysicalExecutionCount != nil ||
			(proof.State != "applied" && proof.State != "not_applied" && proof.State != "unknown") {
			return result, ErrEventIntegrity
		}
	}
	result.State = proof.State
	result.ReconciliationEventID, result.ReconciliationChecksum = proofEvent.EventID, proofEvent.Checksum
	result.EffectKnown, result.ReplayAllowed = proof.State != "unknown", proof.State == "not_applied"
	if !result.EffectKnown {
		return result, nil
	}
	postimage := evaluationFileState{Exists: true, SHA256: preimage.ContentHash, Size: preimage.ContentSize}
	if proof.SourceHead != proofEvent.PrevChecksum || proof.Observed == nil ||
		proof.State == "applied" && (*proof.Observed != postimage || preimage.Before == postimage) ||
		proof.State == "not_applied" && *proof.Observed != preimage.Before {
		return result, ErrEventIntegrity
	}
	if proof.State == "applied" {
		if preimage.Before.Exists {
			// Old contents were never retained by the preimage contract. A hash
			// alone cannot reconstruct the production code transition.
			if binding.RuntimeContract == "production_opensandbox" {
				return result, ErrSessionStateConflict
			}
		} else {
			if preimage.Before != (evaluationFileState{}) || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
				return result, ErrSessionStateConflict
			}
			beforeExists, afterExists := false, true
			modifications := codeModificationPayloads(orchestrator.ToolResult{ToolCallID: command.ToolCallID, ToolName: "Write", Changes: []orchestrator.CodeChange{{Path: path, Before: "", After: content, BeforeExists: &beforeExists, AfterExists: &afterExists}}}, command.RunID)
			if len(modifications) != 1 {
				return result, ErrEventIntegrity
			}
			expected, _ := json.Marshal(modifications[0])
			var codeEvent Event
			resultSeq := int64(-1)
			for _, event := range events {
				if event.Type == "tool/result" {
					var value toolResultPayload
					if json.Unmarshal(event.Payload, &value) != nil {
						return result, ErrEventIntegrity
					}
					if value.RunID == command.RunID && value.ToolCallID == command.ToolCallID {
						if resultSeq >= 0 {
							return result, ErrEventIntegrity
						}
						resultSeq = event.Seq
					}
				}
				if event.Type != codeModifiedEventType {
					continue
				}
				var value codeModificationPayload
				if json.Unmarshal(event.Payload, &value) != nil {
					return result, ErrEventIntegrity
				}
				if value.RunID != command.RunID || value.ToolCallID != command.ToolCallID {
					continue
				}
				encoded, _ := json.Marshal(value)
				if codeEvent.EventID != "" || string(encoded) != string(expected) || event.Seq <= dispatchedEvent.Seq {
					return result, ErrEventIntegrity
				}
				codeEvent = event
			}
			if resultSeq >= 0 && (codeEvent.EventID == "" || codeEvent.Seq >= resultSeq) {
				// Historical result-only repairs remain historical; appending a
				// later code event would violate the production ordering contract.
				return result, ErrSessionStateConflict
			}
			if codeEvent.EventID == "" {
				payload := struct {
					codeModificationPayload
					ProofID   string `json:"reconciliation_event_id"`
					ProofHash string `json:"reconciliation_checksum"`
				}{modifications[0], proofEvent.EventID, proofEvent.Checksum}
				event, err := r.workbench.ledger.Append(ctx, command.SessionID, int64(len(events)), codeModifiedEventType, payload)
				if err != nil {
					return result, err
				}
				events = append(events, event)
			}
		}
	}
	if invocation.result == nil {
		projection := toolResultPayload{RunID: command.RunID, ToolCallID: command.ToolCallID, ToolName: "Write", Output: "written (file-state reconciliation)", Stopped: true}
		if proof.State == "not_applied" {
			projection.Output, projection.Error, projection.ExitCode = "", "Write was not applied (file-state reconciliation)", 1
		}
		payload := struct {
			toolResultPayload
			ProofID   string `json:"reconciliation_event_id"`
			ProofHash string `json:"reconciliation_checksum"`
		}{projection, proofEvent.EventID, proofEvent.Checksum}
		event, err := r.workbench.ledger.AppendSurface(ctx, command.SessionID, int64(len(events)), "tool/result", payload, SurfaceOperation{Op: "append"})
		if err != nil {
			return result, err
		}
		events = append(events, event)
	}
	result.ToolResultRecorded = true
	for _, item := range pendingCommits(events, command.RunID) {
		if item.ToolCallID == command.ToolCallID {
			if _, err := r.workbench.ledger.Append(ctx, command.SessionID, int64(len(events)), commitDoneEventType, item); err != nil {
				return result, err
			}
			break
		}
	}
	r.workbench.signal(command.SessionID)
	return result, nil
}
