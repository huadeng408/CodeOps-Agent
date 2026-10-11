package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"code-agent/internal/sandbox"
	"code-agent/internal/tools"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const taskBaselineIntentEvent = "workspace/test-intent"
const taskBaselineOutcomeEvent = "workspace/test-outcome"

// Callbacks adapt the existing admission module without a Session import cycle.
type TaskBaselineRuntime struct {
	Toolchain        string
	DependencyClient *http.Client
	Runner           sandbox.Runner
	Ready            func(context.Context) error
	Acquire, Finish  func(context.Context, uint, string) error
}

type TaskBaselineView struct {
	State       string   `json:"state"`
	Command     []string `json:"command"`
	OperationID string   `json:"operationId"`
	RuntimeID   string   `json:"runtimeId,omitempty"`
	TraceID     string   `json:"traceId,omitempty"`
	ExitCode    *int     `json:"exitCode,omitempty"`
	Output      string   `json:"output"`
	Truncated   bool     `json:"truncated"`
	Reason      string   `json:"reason,omitempty"`
}

type taskBaselinePayload struct {
	RequestID             string           `json:"request_id"`
	BaselineChecksum      string           `json:"baseline_checksum"`
	IntentEventID         string           `json:"intent_event_id,omitempty"`
	DependencyOperationID string           `json:"dependency_operation_id,omitempty"`
	View                  TaskBaselineView `json:"view"`
}

// WithBaselineRuntime is configured once by the local Harness, never by HTTP.
func (t *TaskWorkspaces) WithBaselineRuntime(profile TaskBaselineRuntime) *TaskWorkspaces {
	t.baselineRuntime = &profile
	return t
}

func (t *TaskWorkspaces) SetPublicTestFixtures(head string, files map[string]string) error {
	return t.manager.SetPublicTestFixtures(head, files)
}

func (t *TaskWorkspaces) RunBaseline(ctx context.Context, owner uint, id string, seq int64, requestID string) (view TaskWorkspaceView, resultErr error) {
	ctx, span := otel.Tracer("code-agent/harness").Start(ctx, "workspace.test-baseline")
	defer func() {
		if resultErr != nil || view.Test == nil || view.Test.State != "passed" {
			span.SetStatus(codes.Error, "")
		}
		span.End()
	}()
	if requestID == "" || len(requestID) > 128 || strings.IndexFunc(requestID, unicode.IsControl) >= 0 || seq < 0 {
		return view, ErrInvalidSessionInput
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
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
	previous, err := taskBaseline(snapshot.Events)
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
	if prepared == nil || state != "prepared" || time.Now().After(prepared.Task.LeaseExpiresAt) || t.manager.VerifyTask(ctx, t.storage, prepared.Task) != nil {
		return view, ErrSessionStateConflict
	}
	goModule, pythonModule := false, false
	for _, file := range prepared.Task.Baseline.Files {
		if file.Path == "go.mod" && file.Exists {
			goModule = true
		}
		if file.Exists && (file.Path == "setup.py" || file.Path == "pyproject.toml") {
			pythonModule = true
		}
	}
	profile := t.baselineRuntime
	if profile == nil || profile.Runner == nil || profile.Acquire == nil || profile.Finish == nil {
		return view, ErrSessionStateConflict
	}
	command := []string{"go", "test", "./...", "-count=1"}
	dependencyOperation := ""
	if profile.Toolchain == "python" {
		dependency, err := taskDependency(snapshot.Events)
		if err != nil {
			return view, err
		}
		if !pythonModule || dependency == nil || dependency.View.State != "ready" || dependency.BaselineChecksum != prepared.Task.Baseline.Checksum || sandbox.ValidatePytz(ctx, t.storage, dependency.View.OperationID) != nil {
			return view, ErrSessionStateConflict
		}
		dependencyOperation = dependency.View.OperationID
		command = []string{"python", "-m", "unittest", "discover", "-v"}
	} else if (profile.Toolchain != "" && profile.Toolchain != "go") || !goModule {
		return view, ErrSessionStateConflict
	}
	digest := sha256.Sum256([]byte(id + "\x00" + requestID + "\x00" + prepared.Task.LeaseID + "\x00" + prepared.Task.Baseline.Checksum))
	operation := hex.EncodeToString(digest[:])
	payload := taskBaselinePayload{RequestID: requestID, BaselineChecksum: prepared.Task.Baseline.Checksum, DependencyOperationID: dependencyOperation, View: TaskBaselineView{State: "unknown", Command: command, OperationID: operation, Reason: "test outcome is unknown; reconcile the retained operation"}}
	if span.SpanContext().IsValid() {
		payload.View.TraceID = span.SpanContext().TraceID().String()
	}
	span.SetAttributes(attribute.String("session.id", id), attribute.String("workspace.operation_id", operation), attribute.String("workspace.baseline", payload.BaselineChecksum))
	intent, err := t.workbench.ledger.Append(ctx, id, seq, taskBaselineIntentEvent, payload)
	if err != nil {
		return view, err
	}
	t.workbench.signal(id)
	payload.IntentEventID = intent.EventID
	// A different process may commit a tombstone without a local deletion hint.
	if _, _, err := t.read(ctx, owner, id); err != nil {
		return view, err
	}
	acquired := profile.Acquire(ctx, owner, operation) == nil
	knownStopped := false
	if !acquired {
		payload.View.State, payload.View.Reason = "blocked", "task admission is unavailable; reconcile the retained operation"
	} else {
		if _, _, err := t.read(ctx, owner, id); err != nil || ctx.Err() != nil {
			releaseCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer stop()
			if releaseErr := profile.Finish(releaseCtx, owner, operation); releaseErr != nil {
				return view, releaseErr
			}
			if err != nil {
				return view, err
			}
			return view, ctx.Err()
		}
		workspace := filepath.Join(t.storage, prepared.Task.LeaseID)
		request := sandbox.Request{Workspace: workspace, WorkingDir: workspace, OperationID: operation, Program: command[0], Args: command[1:], DependencyOperationID: dependencyOperation}
		if dependencyOperation != "" {
			// Installation stays in sandbox scratch; neither host nor source inventory changes.
			script := "import os,subprocess,sys;subprocess.run([sys.executable,'-m','pip','install','--no-index','--no-deps','--disable-pip-version-check','--target','/run/codeops/python-site','/deps/" + sandbox.PytzName + "'],check=True);os.execv(sys.executable,[sys.executable,'-m','unittest','discover','-v'])"
			request.Args = []string{"-c", script}
		}
		result, runErr := profile.Runner.Run(ctx, request)
		payload.View.Output, payload.View.Truncated, payload.View.RuntimeID = tools.RedactSensitive(result.Output), result.Truncated, result.RuntimeID
		for _, key := range []string{"CODE_AGENT_OPENSANDBOX_API_KEY", "CODE_AGENT_OPENSANDBOX_EXECD_TOKEN", "JWT_SECRET"} {
			if value := os.Getenv(key); value != "" {
				payload.View.Output = strings.ReplaceAll(payload.View.Output, value, "[REDACTED]")
			}
		}
		if len(payload.View.Output) > 1<<20 {
			payload.View.Output, payload.View.Truncated = payload.View.Output[:1<<20], true
		}
		knownStopped = runErr == nil || errors.Is(runErr, sandbox.ErrSandboxUnavailable) && !errors.Is(runErr, sandbox.ErrSandboxOutcomeUnknown)
		if knownStopped {
			payload.View.ExitCode = &result.ExitCode
			payload.View.State, payload.View.Reason = "passed", ""
			if runErr != nil {
				payload.View.State, payload.View.Reason = "blocked", "approved OpenSandbox prerequisites are unavailable"
			} else if result.ExitCode != 0 {
				payload.View.State = "failed"
			}
			checkCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			current, captureErr := t.manager.CaptureBaseline(checkCtx)
			if captureErr != nil || current.Checksum != payload.BaselineChecksum || t.manager.VerifyTask(checkCtx, t.storage, prepared.Task) != nil || dependencyOperation != "" && sandbox.ValidatePytz(checkCtx, t.storage, dependencyOperation) != nil {
				payload.View.State, payload.View.Reason = "blocked", "repository or task baseline changed; reconcile the retained result"
			}
			stop()
		}
	}
	recordCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer stop()
	if knownStopped && acquired && profile.Finish(recordCtx, owner, operation) != nil {
		payload.View.State, payload.View.Reason = "unknown", "task termination or admission release requires reconciliation"
	}
	for attempt := 0; attempt < 16; attempt++ {
		latest, readErr := ReadVerifiedSnapshot(recordCtx, t.workbench.ledger, id)
		if readErr != nil {
			return view, readErr
		}
		_, appendErr := t.workbench.ledger.Append(recordCtx, id, int64(len(latest.Events)), taskBaselineOutcomeEvent, payload)
		if appendErr == nil {
			t.workbench.signal(id)
			return t.Inspect(recordCtx, owner, id)
		}
		if !errors.Is(appendErr, ErrSequenceConflict) {
			return view, appendErr
		}
	}
	return view, ErrSequenceConflict
}

func taskBaseline(events []Event) (*taskBaselinePayload, error) {
	var result *taskBaselinePayload
	intentID := ""
	finished := false
	for _, event := range events {
		if event.Type != taskBaselineIntentEvent && event.Type != taskBaselineOutcomeEvent {
			continue
		}
		var payload taskBaselinePayload
		if json.Unmarshal(event.Payload, &payload) != nil || payload.RequestID == "" || len(payload.View.OperationID) != 64 || payload.BaselineChecksum == "" {
			return nil, ErrEventIntegrity
		}
		command := strings.Join(payload.View.Command, "\x00")
		if command == "go\x00test\x00./...\x00-count=1" {
			if payload.DependencyOperationID != "" {
				return nil, ErrEventIntegrity
			}
		} else if command == "python\x00-m\x00unittest\x00discover\x00-v" {
			if len(payload.DependencyOperationID) != 64 {
				return nil, ErrEventIntegrity
			}
			if _, err := hex.DecodeString(payload.DependencyOperationID); err != nil {
				return nil, ErrEventIntegrity
			}
		} else {
			return nil, ErrEventIntegrity
		}
		if _, err := hex.DecodeString(payload.View.OperationID); err != nil {
			return nil, ErrEventIntegrity
		}
		if event.Type == taskBaselineIntentEvent {
			if result != nil || payload.IntentEventID != "" || payload.View.State != "unknown" || payload.View.ExitCode != nil {
				return nil, ErrEventIntegrity
			}
			result, intentID = &payload, event.EventID
		} else {
			if result == nil || finished || payload.IntentEventID != intentID || payload.RequestID != result.RequestID || payload.BaselineChecksum != result.BaselineChecksum || payload.View.OperationID != result.View.OperationID || payload.DependencyOperationID != result.DependencyOperationID || strings.Join(payload.View.Command, "\x00") != strings.Join(result.View.Command, "\x00") {
				return nil, ErrEventIntegrity
			}
			switch payload.View.State {
			case "passed", "failed", "blocked", "unknown":
			default:
				return nil, ErrEventIntegrity
			}
			if (payload.View.State == "passed" || payload.View.State == "failed") && (payload.View.ExitCode == nil || (*payload.View.ExitCode == 0) != (payload.View.State == "passed")) {
				return nil, ErrEventIntegrity
			}
			result, finished = &payload, true
		}
	}
	return result, nil
}
