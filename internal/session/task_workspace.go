package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"code-agent/internal/sandbox"
	"code-agent/internal/worktree"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	taskPrepareIntentEvent = "workspace/task-prepare-intent"
	taskPreparedEvent      = "workspace/task-prepared"
	taskPrepareFailedEvent = "workspace/task-prepare-failed"
)

type TaskWorkspaceModule interface {
	Inspect(context.Context, uint, string) (TaskWorkspaceView, error)
	Prepare(context.Context, uint, string, int64, string) (TaskWorkspaceView, error)
	PrepareDependency(context.Context, uint, string, int64, string) (TaskWorkspaceView, error)
	RunBaseline(context.Context, uint, string, int64, string) (TaskWorkspaceView, error)
}

// TaskWorkspaces keeps preparation on the Workbench's canonical Ledger. Only
// the configured local owner/repository is admitted, never an HTTP-supplied root.
type TaskWorkspaces struct {
	workbench           *Workbench
	owner               func(context.Context) (uint, error)
	repository, storage string
	manager             *worktree.Manager
	baselineRuntime     *TaskBaselineRuntime
}

type TaskWorkspaceView struct {
	Available           bool                `json:"available"`
	State               string              `json:"state"`
	Reason              string              `json:"reason,omitempty"`
	Repository          string              `json:"repository"`
	WorkspaceID         string              `json:"workspaceId,omitempty"`
	Baseline            *worktree.Baseline  `json:"baseline,omitempty"`
	LeaseExpiresAt      *time.Time          `json:"leaseExpiresAt,omitempty"`
	EventCount          int                 `json:"eventCount"`
	TestAvailable       bool                `json:"testAvailable"`
	TestReason          string              `json:"testReason,omitempty"`
	Test                *TaskBaselineView   `json:"test,omitempty"`
	Dependency          *TaskDependencyView `json:"dependency,omitempty"`
	DependencyAvailable bool                `json:"dependencyAvailable"`
	Toolchain           string              `json:"toolchain"`
}

// TaskModelWorkspace is a Harness-only execution context, never an HTTP root.
type TaskModelWorkspace struct {
	WorkingDir            string
	DependencyOperationID string
}

type taskPreparePayload struct {
	RequestID     string                 `json:"request_id"`
	Task          worktree.TaskWorkspace `json:"task"`
	IntentEventID string                 `json:"intent_event_id,omitempty"`
}

func NewTaskWorkspaces(w *Workbench, owner func(context.Context) (uint, error), repository, storage string) *TaskWorkspaces {
	return &TaskWorkspaces{workbench: w, owner: owner, repository: repository, storage: storage, manager: worktree.NewManager(repository, "HEAD")}
}

func (t *TaskWorkspaces) Inspect(ctx context.Context, ownerID uint, sessionID string) (TaskWorkspaceView, error) {
	snapshot, view, err := t.read(ctx, ownerID, sessionID)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	return t.project(ctx, snapshot.Events, view)
}

func (t *TaskWorkspaces) ModelWorkspace(ctx context.Context, owner uint, id string) (TaskModelWorkspace, error) {
	return t.modelWorkspace(ctx, owner, id, "")
}

// ToolWorkspace admits only the current Go-dispatched call while its result is pending.
func (t *TaskWorkspaces) ToolWorkspace(ctx context.Context, owner uint, id, callID string) (TaskModelWorkspace, error) {
	if callID == "" {
		return TaskModelWorkspace{}, ErrInvalidSessionInput
	}
	return t.modelWorkspace(ctx, owner, id, callID)
}

func (t *TaskWorkspaces) modelWorkspace(ctx context.Context, owner uint, id, currentToolID string) (TaskModelWorkspace, error) {
	snapshot, sessionView, err := t.read(ctx, owner, id)
	if err != nil {
		return TaskModelWorkspace{}, err
	}
	view, err := t.project(ctx, snapshot.Events, sessionView)
	if err != nil {
		return TaskModelWorkspace{}, err
	}
	if !view.Available || view.State != "prepared" || !view.TestAvailable || t.baselineRuntime.Ready == nil ||
		view.Test != nil && view.Test.State == "unknown" && !view.Test.Stopped {
		return TaskModelWorkspace{}, ErrSessionStateConflict
	}
	if err := taskToolOutcomesKnown(snapshot.Events, currentToolID); err != nil {
		return TaskModelWorkspace{}, err
	}
	workspace := TaskModelWorkspace{WorkingDir: filepath.Join(t.storage, view.WorkspaceID)}
	if view.Dependency != nil {
		workspace.DependencyOperationID = view.Dependency.OperationID
	}
	return workspace, nil
}

func taskToolOutcomesKnown(events []Event, currentToolID string) error {
	pending := map[string]toolCallPayload{}
	dispatched := map[string]bool{}
	prepared := false
	for _, event := range events {
		if event.Type == taskPreparedEvent {
			prepared = true
		}
		if !prepared {
			continue
		}
		switch event.Type {
		case commitPendingEventType, toolDispatchedType:
			var call toolCallPayload
			if json.Unmarshal(event.Payload, &call) != nil || call.RunID == "" || call.ToolCallID == "" {
				return ErrEventIntegrity
			}
			key := invocationHistoryKey(call.RunID, call.ToolCallID)
			pending[key] = call
			if event.Type == toolDispatchedType {
				dispatched[key] = true
			}
		case "tool/result":
			var result toolResultPayload
			if json.Unmarshal(event.Payload, &result) != nil || result.OutcomeUnknown {
				return ErrSessionStateConflict
			}
			delete(pending, invocationHistoryKey(result.RunID, result.ToolCallID))
		case toolUnknownEventType:
			return ErrSessionStateConflict
		}
	}
	admittedCurrent := false
	for key, call := range pending {
		if currentToolID == "" || call.ToolCallID != currentToolID || !dispatched[key] {
			return ErrSessionStateConflict
		}
		if admittedCurrent {
			return ErrSessionStateConflict
		}
		run, err := projectRun(events, call.RunID)
		if err != nil || run.terminal || run.view.Status != RunRunning || run.view.LeaseUntil == nil || !run.view.LeaseUntil.After(time.Now()) {
			return ErrSessionStateConflict
		}
		admittedCurrent = true
	}
	if currentToolID != "" && !admittedCurrent {
		return ErrSessionStateConflict
	}
	return nil
}

func (t *TaskWorkspaces) Prepare(ctx context.Context, ownerID uint, sessionID string, expectedSeq int64, requestID string) (result TaskWorkspaceView, resultErr error) {
	ctx, span := otel.Tracer("code-agent/harness").Start(ctx, "workspace.prepare")
	defer func() {
		if resultErr != nil || result.State != "prepared" {
			span.SetStatus(codes.Error, "")
		}
		span.End()
	}()
	if requestID == "" || len(requestID) > 128 || strings.IndexFunc(requestID, unicode.IsControl) >= 0 || expectedSeq < 0 {
		return TaskWorkspaceView{}, ErrInvalidSessionInput
	}
	snapshot, sessionView, err := t.read(ctx, ownerID, sessionID)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	if !t.approved(ctx, ownerID, sessionView.WorkingDir) {
		return TaskWorkspaceView{}, fmt.Errorf("%w: repository is not approved for task preparation", ErrInvalidSessionInput)
	}
	span.SetAttributes(attribute.String("session.id", sessionID))
	payload, _, err := taskPreparation(snapshot.Events)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	if payload != nil {
		if payload.RequestID != requestID {
			return TaskWorkspaceView{}, fmt.Errorf("%w: retained workspace requires reconciliation", ErrSessionStateConflict)
		}
		return t.project(ctx, snapshot.Events, sessionView)
	}
	if expectedSeq != int64(len(snapshot.Events)) {
		return TaskWorkspaceView{}, ErrSequenceConflict
	}
	if sessionView.Run != nil && (sessionView.Run.Status == "queued" || sessionView.Run.Status == "running") {
		return TaskWorkspaceView{}, ErrSessionStateConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	deleted, stopWatching := t.workbench.watchDeletion(sessionID)
	defer stopWatching()
	go func() {
		select {
		case <-deleted:
			cancel()
		case <-ctx.Done():
		}
	}()
	plan, err := t.manager.PlanTask(ctx)
	if err != nil {
		return TaskWorkspaceView{}, fmt.Errorf("%w: repository baseline is unavailable or unsafe", ErrSessionStateConflict)
	}
	payload = &taskPreparePayload{RequestID: requestID, Task: plan}
	span.SetAttributes(attribute.String("workspace.lease_id", plan.LeaseID), attribute.String("workspace.baseline", plan.Baseline.Checksum))
	intent, err := t.workbench.ledger.Append(ctx, sessionID, expectedSeq, taskPrepareIntentEvent, payload)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	t.workbench.signal(sessionID)
	err = t.manager.PrepareTask(ctx, t.storage, &payload.Task)
	outcomeType := taskPreparedEvent
	if err != nil {
		outcomeType = taskPrepareFailedEvent
	}
	payload.IntentEventID = intent.EventID
	// Cancellation must still record a terminal preparation fact. A crash or
	// failed append leaves the intent unknown, never safe to execute or retry.
	recordCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	for attempt := 0; attempt < 16; attempt++ {
		latest, readErr := ReadVerifiedSnapshot(recordCtx, t.workbench.ledger, sessionID)
		if readErr != nil {
			return TaskWorkspaceView{}, readErr
		}
		_, appendErr := t.workbench.ledger.Append(recordCtx, sessionID, int64(len(latest.Events)), outcomeType, payload)
		if appendErr == nil {
			t.workbench.signal(sessionID)
			return t.Inspect(recordCtx, ownerID, sessionID)
		}
		if !errors.Is(appendErr, ErrSequenceConflict) {
			return TaskWorkspaceView{}, appendErr
		}
	}
	return TaskWorkspaceView{}, ErrSequenceConflict
}

func (t *TaskWorkspaces) read(ctx context.Context, ownerID uint, sessionID string) (LedgerSnapshot, SessionView, error) {
	if t == nil || t.workbench == nil || ownerID == 0 {
		return LedgerSnapshot{}, SessionView{}, ErrSessionNotFound
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, t.workbench.ledger, sessionID)
	if err != nil {
		return LedgerSnapshot{}, SessionView{}, err
	}
	view, err := reduceSessionView(snapshot.Events)
	if err != nil {
		return LedgerSnapshot{}, SessionView{}, err
	}
	if view.UserID != ownerID || view.Status == "deleted" {
		return LedgerSnapshot{}, SessionView{}, ErrSessionNotFound
	}
	return snapshot, view, nil
}

func (t *TaskWorkspaces) approved(ctx context.Context, ownerID uint, workingDir string) bool {
	if t.owner == nil {
		return false
	}
	approvedOwner, err := t.owner(ctx)
	if err != nil || approvedOwner == 0 || ownerID != approvedOwner || !filepath.IsAbs(t.repository) || !filepath.IsAbs(t.storage) {
		return false
	}
	left, right := filepath.Clean(workingDir), filepath.Clean(t.repository)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func (t *TaskWorkspaces) project(ctx context.Context, events []Event, sessionView SessionView) (TaskWorkspaceView, error) {
	view := TaskWorkspaceView{Available: t.approved(ctx, sessionView.UserID, sessionView.WorkingDir), State: "unprepared", Repository: sessionView.ProjectName, EventCount: len(events)}
	view.Toolchain = "go"
	if t.baselineRuntime != nil && t.baselineRuntime.Toolchain != "" {
		view.Toolchain = t.baselineRuntime.Toolchain
	}
	test, err := taskBaseline(events)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	if test != nil {
		view.Test = &test.View
	}
	dependency, err := taskDependency(events)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	if dependency != nil {
		view.Dependency = &dependency.View
		if dependency.View.State == "ready" && sandbox.ValidatePytz(ctx, t.storage, dependency.View.OperationID) != nil {
			copy := dependency.View
			copy.State, copy.Reason = "blocked", "prepared dependency differs from its retained identity"
			view.Dependency = &copy
		}
	}
	view.TestAvailable = t.baselineRuntime != nil && t.baselineRuntime.Runner != nil && t.baselineRuntime.Acquire != nil && t.baselineRuntime.Finish != nil
	if !view.TestAvailable {
		view.TestReason = "local task runtime is not attached"
	} else if t.baselineRuntime.Ready != nil && t.baselineRuntime.Ready(ctx) != nil {
		view.TestAvailable, view.TestReason = false, "approved OpenSandbox prerequisites are unavailable"
	}
	if view.Toolchain == "python" && (view.Dependency == nil || view.Dependency.State != "ready") {
		view.TestAvailable, view.TestReason = false, "approved Python test dependency is not prepared"
	}
	if !view.Available {
		view.State, view.Reason = "blocked", "repository is not approved for task preparation"
	}
	payload, state, err := taskPreparation(events)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	if payload == nil {
		return view, nil
	}
	view.WorkspaceID, view.Baseline, view.LeaseExpiresAt = payload.Task.LeaseID, &payload.Task.Baseline, &payload.Task.LeaseExpiresAt
	view.State = state
	switch {
	case !view.Available:
		view.State, view.Reason = "blocked", "repository approval changed; retained workspace requires reconciliation"
	case state == "unknown":
		view.Reason = "preparation outcome is unknown; retained lease requires reconciliation"
	case state == "blocked":
		view.Reason = "preparation failed; retained lease requires reconciliation"
	case time.Now().After(payload.Task.LeaseExpiresAt):
		view.State, view.Reason = "blocked", "preparation lease expired; retained workspace requires reconciliation"
	case t.verifyTask(ctx, payload.Task, events) != nil:
		view.State, view.Reason = "blocked", "task copy, Git registration or retained tool outcome requires reconciliation"
	}
	view.DependencyAvailable = view.Available && view.State == "prepared" && view.Toolchain == "python" && view.Dependency == nil && view.Test == nil
	return view, nil
}

func (t *TaskWorkspaces) verifyTask(ctx context.Context, task worktree.TaskWorkspace, events []Event) error {
	type dispatchProof struct {
		seq int64
		toolCallPayload
	}
	type resultProof struct {
		seq int64
		toolResultPayload
	}
	dispatched := map[string]dispatchProof{}
	results := map[string]resultProof{}
	prepared := false
	for _, event := range events {
		if event.Type == taskPreparedEvent {
			prepared = true
		}
		if !prepared {
			continue
		}
		switch event.Type {
		case toolDispatchedType, toolUnknownEventType:
			var call toolCallPayload
			if json.Unmarshal(event.Payload, &call) != nil {
				return ErrEventIntegrity
			}
			key := call.RunID + "\x00" + call.ToolCallID
			if event.Type == toolUnknownEventType {
				return ErrSessionStateConflict
			}
			if _, exists := dispatched[key]; exists {
				return ErrEventIntegrity
			}
			dispatched[key] = dispatchProof{event.Seq, call}
		case "tool/result":
			var result toolResultPayload
			if json.Unmarshal(event.Payload, &result) != nil {
				return ErrEventIntegrity
			}
			if result.OutcomeUnknown {
				return ErrSessionStateConflict
			}
			key := result.RunID + "\x00" + result.ToolCallID
			if _, exists := results[key]; exists {
				return ErrEventIntegrity
			}
			results[key] = resultProof{event.Seq, result}
		}
	}
	changes := []worktree.TaskFileChange{}
	prepared = false
	for _, event := range events {
		if event.Type == taskPreparedEvent {
			prepared = true
		}
		if !prepared || event.Type != codeModifiedEventType {
			continue
		}
		var change codeModificationPayload
		if json.Unmarshal(event.Payload, &change) != nil || validateCodeModificationPayload(change) != nil || change.BeforeSHA256 != hashCodeState(change.Before) || change.AfterSHA256 != hashCodeState(change.After) || change.DiffSHA256 != hashCodeTransition(change.Before, change.After) {
			return ErrEventIntegrity
		}
		key := change.RunID + "\x00" + change.ToolCallID
		result, exists := results[key]
		if !exists || result.seq <= event.Seq || result.Error != "" || result.ExitCode != 0 || result.OutcomeUnknown || result.ToolName != change.ToolName || dispatched[key].ToolName != change.ToolName || dispatched[key].seq >= event.Seq {
			return ErrSessionStateConflict
		}
		changes = append(changes, worktree.TaskFileChange{Path: change.Path, Before: change.Before, After: change.After, Operation: change.Operation})
	}
	return t.manager.VerifyTask(ctx, t.storage, task, changes...)
}

func taskPreparation(events []Event) (*taskPreparePayload, string, error) {
	var prepared *taskPreparePayload
	intentID, state := "", ""
	for _, event := range events {
		if event.Type != taskPrepareIntentEvent && event.Type != taskPreparedEvent && event.Type != taskPrepareFailedEvent {
			continue
		}
		var payload taskPreparePayload
		if json.Unmarshal(event.Payload, &payload) != nil || payload.RequestID == "" || payload.Task.Validate() != nil {
			return nil, "", ErrEventIntegrity
		}
		if event.Type == taskPrepareIntentEvent {
			if prepared != nil || payload.IntentEventID != "" {
				return nil, "", ErrEventIntegrity
			}
			prepared, intentID, state = &payload, event.EventID, "unknown"
			continue
		}
		if prepared == nil || state != "unknown" || payload.IntentEventID != intentID || payload.RequestID != prepared.RequestID || payload.Task.LeaseID != prepared.Task.LeaseID || payload.Task.Baseline.Checksum != prepared.Task.Baseline.Checksum || !payload.Task.LeaseExpiresAt.Equal(prepared.Task.LeaseExpiresAt) {
			return nil, "", ErrEventIntegrity
		}
		state = "prepared"
		if event.Type == taskPreparedEvent && payload.Task.IndexSHA256 == "" {
			return nil, "", ErrEventIntegrity
		}
		if event.Type == taskPrepareFailedEvent {
			state = "blocked"
		}
		prepared = &payload
	}
	return prepared, state, nil
}
