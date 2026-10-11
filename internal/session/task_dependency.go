package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"code-agent/internal/sandbox"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const taskDependencyIntentEvent = "workspace/dependency-intent"
const taskDependencyOutcomeEvent = "workspace/dependency-outcome"

type TaskDependencyView struct {
	State       string `json:"state"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	License     string `json:"license"`
	Source      string `json:"source"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	OperationID string `json:"operationId"`
	TraceID     string `json:"traceId,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type taskDependencyPayload struct {
	RequestID        string             `json:"request_id"`
	BaselineChecksum string             `json:"baseline_checksum"`
	IntentEventID    string             `json:"intent_event_id,omitempty"`
	View             TaskDependencyView `json:"view"`
}

func (t *TaskWorkspaces) PrepareDependency(ctx context.Context, owner uint, id string, seq int64, requestID string) (view TaskWorkspaceView, resultErr error) {
	ctx, span := otel.Tracer("code-agent/harness").Start(ctx, "workspace.prepare-dependency")
	defer func() {
		if resultErr != nil || view.Dependency == nil || view.Dependency.State != "ready" {
			span.SetStatus(codes.Error, "")
		}
		span.End()
	}()
	if requestID == "" || len(requestID) > 128 || strings.IndexFunc(requestID, unicode.IsControl) >= 0 || seq < 0 {
		return view, ErrInvalidSessionInput
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	deleted, stopWatching := t.workbench.watchDeletion(id)
	defer stopWatching()
	go func() {
		select {
		case <-deleted:
			cancel()
		case <-ctx.Done():
		}
	}()
	snapshot, sessionView, err := t.read(ctx, owner, id)
	if err != nil {
		return view, err
	}
	if !t.approved(ctx, owner, sessionView.WorkingDir) {
		return view, ErrInvalidSessionInput
	}
	previous, err := taskDependency(snapshot.Events)
	if err != nil {
		return view, err
	}
	if previous != nil {
		if previous.RequestID != requestID {
			return view, ErrSessionStateConflict
		}
		return t.project(ctx, snapshot.Events, sessionView)
	}
	if seq != int64(len(snapshot.Events)) {
		return view, ErrSequenceConflict
	}
	if sessionView.Run != nil && (sessionView.Run.Status == "queued" || sessionView.Run.Status == "running") {
		return view, ErrSessionStateConflict
	}
	prepared, state, err := taskPreparation(snapshot.Events)
	if err != nil {
		return view, err
	}
	profile := t.baselineRuntime
	if prepared == nil || state != "prepared" || time.Now().After(prepared.Task.LeaseExpiresAt) || t.manager.VerifyTask(ctx, t.storage, prepared.Task) != nil || profile == nil || profile.Toolchain != "python" || profile.Acquire == nil || profile.Finish == nil {
		return view, ErrSessionStateConflict
	}
	python := false
	for _, file := range prepared.Task.Baseline.Files {
		if file.Exists && (file.Path == "setup.py" || file.Path == "pyproject.toml") {
			python = true
		}
	}
	if !python {
		return view, ErrSessionStateConflict
	}
	test, err := taskBaseline(snapshot.Events)
	if err != nil {
		return view, err
	}
	if test != nil {
		return view, ErrSessionStateConflict
	}
	digest := sha256.Sum256([]byte(id + "\x00" + requestID + "\x00" + prepared.Task.LeaseID + "\x00" + sandbox.PytzSHA256))
	operation := hex.EncodeToString(digest[:])
	payload := taskDependencyPayload{RequestID: requestID, BaselineChecksum: prepared.Task.Baseline.Checksum, View: TaskDependencyView{State: "unknown", Name: "pytz", Version: "2026.5", License: "MIT", Source: sandbox.PytzSource, SHA256: sandbox.PytzSHA256, Size: sandbox.PytzSize, OperationID: operation, Reason: "dependency preparation is unknown; reconcile the retained operation"}}
	if span.SpanContext().IsValid() {
		payload.View.TraceID = span.SpanContext().TraceID().String()
	}
	span.SetAttributes(attribute.String("session.id", id), attribute.String("workspace.operation_id", operation), attribute.String("dependency.sha256", sandbox.PytzSHA256))
	intent, err := t.workbench.ledger.Append(ctx, id, seq, taskDependencyIntentEvent, payload)
	if err != nil {
		return view, err
	}
	t.workbench.signal(id)
	payload.IntentEventID = intent.EventID
	if _, _, err := t.read(ctx, owner, id); err != nil {
		return view, err
	}
	acquired := profile.Acquire(ctx, owner, operation) == nil
	payload.View.State, payload.View.Reason = "blocked", "task admission is unavailable; reconcile the retained operation"
	if acquired {
		_, _, prepareErr := t.read(ctx, owner, id)
		if prepareErr == nil {
			prepareErr = ctx.Err()
		}
		if prepareErr == nil {
			prepareErr = sandbox.PreparePytz(ctx, t.storage, operation, profile.DependencyClient)
		}
		payload.View.Reason = "approved Python dependency preparation failed; retained operation requires reconciliation"
		if prepareErr == nil {
			payload.View.State, payload.View.Reason = "ready", ""
		}
	}
	recordCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer stop()
	current, captureErr := t.manager.CaptureBaseline(recordCtx)
	if captureErr != nil || current.Checksum != payload.BaselineChecksum || t.manager.VerifyTask(recordCtx, t.storage, prepared.Task) != nil {
		payload.View.State, payload.View.Reason = "blocked", "repository or task baseline changed; reconcile dependency preparation"
	}
	if acquired && profile.Finish(recordCtx, owner, operation) != nil {
		payload.View.State, payload.View.Reason = "unknown", "dependency admission release requires reconciliation"
	}
	for attempt := 0; attempt < 16; attempt++ {
		latest, err := ReadVerifiedSnapshot(recordCtx, t.workbench.ledger, id)
		if err != nil {
			return view, err
		}
		_, err = t.workbench.ledger.Append(recordCtx, id, int64(len(latest.Events)), taskDependencyOutcomeEvent, payload)
		if err == nil {
			t.workbench.signal(id)
			return t.Inspect(recordCtx, owner, id)
		}
		if !errors.Is(err, ErrSequenceConflict) {
			return view, err
		}
	}
	return view, ErrSequenceConflict
}

func taskDependency(events []Event) (*taskDependencyPayload, error) {
	var retained *taskDependencyPayload
	intentID, finished := "", false
	for _, event := range events {
		if event.Type != taskDependencyIntentEvent && event.Type != taskDependencyOutcomeEvent {
			continue
		}
		var payload taskDependencyPayload
		if json.Unmarshal(event.Payload, &payload) != nil || payload.RequestID == "" || payload.BaselineChecksum == "" || payload.View.Name != "pytz" || payload.View.Version != "2026.5" || payload.View.License != "MIT" || payload.View.Source != sandbox.PytzSource || payload.View.SHA256 != sandbox.PytzSHA256 || payload.View.Size != sandbox.PytzSize || len(payload.View.OperationID) != 64 {
			return nil, ErrEventIntegrity
		}
		if _, err := hex.DecodeString(payload.View.OperationID); err != nil {
			return nil, ErrEventIntegrity
		}
		if event.Type == taskDependencyIntentEvent {
			if retained != nil || payload.IntentEventID != "" || payload.View.State != "unknown" {
				return nil, ErrEventIntegrity
			}
			retained, intentID = &payload, event.EventID
		} else {
			if retained == nil || finished || payload.IntentEventID != intentID || payload.RequestID != retained.RequestID || payload.BaselineChecksum != retained.BaselineChecksum || payload.View.OperationID != retained.View.OperationID {
				return nil, ErrEventIntegrity
			}
			if payload.View.State != "ready" && payload.View.State != "blocked" && payload.View.State != "unknown" {
				return nil, ErrEventIntegrity
			}
			retained, finished = &payload, true
		}
	}
	return retained, nil
}
