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
}

// TaskWorkspaces keeps preparation on the Workbench's canonical Ledger. Only
// the configured local owner/repository is admitted, never an HTTP-supplied root.
type TaskWorkspaces struct {
	workbench           *Workbench
	owner               func(context.Context) (uint, error)
	repository, storage string
	manager             *worktree.Manager
}

type TaskWorkspaceView struct {
	Available      bool               `json:"available"`
	State          string             `json:"state"`
	Reason         string             `json:"reason,omitempty"`
	Repository     string             `json:"repository"`
	WorkspaceID    string             `json:"workspaceId,omitempty"`
	Baseline       *worktree.Baseline `json:"baseline,omitempty"`
	LeaseExpiresAt *time.Time         `json:"leaseExpiresAt,omitempty"`
	EventCount     int                `json:"eventCount"`
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
	case t.manager.VerifyTask(ctx, t.storage, payload.Task) != nil:
		view.State, view.Reason = "blocked", "task copy or Git registration changed; retained workspace requires reconciliation"
	}
	return view, nil
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
