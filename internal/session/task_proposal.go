package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"code-agent/internal/sandbox"
	"code-agent/internal/tools"
	"code-agent/internal/worktree"
)

const taskProposalIntentEvent = "workspace/proposal-intent"
const taskProposalOutcomeEvent = "workspace/proposal-outcome"

type ProposalFileView struct {
	Path         string `json:"path"`
	Operation    string `json:"operation,omitempty"`
	Destination  string `json:"destination,omitempty"`
	BeforeExists bool   `json:"beforeExists"`
	AfterExists  bool   `json:"afterExists"`
	BeforeSHA256 string `json:"beforeSha256"`
	AfterSHA256  string `json:"afterSha256"`
	BeforeMode   uint32 `json:"beforeMode"`
	AfterMode    uint32 `json:"afterMode"`
	Diff         string `json:"diff"`
}

type ProposalTestView struct {
	TaskBaselineView
	InputSHA256   string `json:"inputSHA256"`
	ArchiveSHA256 string `json:"archiveSHA256"`
	ModeTransform string `json:"modeTransform"`
}

type CodeChangeProposalView struct {
	ID                  string             `json:"id"`
	SessionID           string             `json:"sessionId"`
	WorkspaceID         string             `json:"workspaceId"`
	BaselineChecksum    string             `json:"baselineChecksum"`
	SourceEventChecksum string             `json:"sourceEventChecksum"`
	FinalStateSHA256    string             `json:"finalStateSHA256"`
	State               string             `json:"state"`
	Files               []ProposalFileView `json:"files"`
	Test                ProposalTestView   `json:"test"`
}

type taskProposalPayload struct {
	SchemaVersion int                    `json:"schema_version"`
	OwnerID       uint                   `json:"owner_id"`
	RequestID     string                 `json:"request_id"`
	IntentEventID string                 `json:"intent_event_id,omitempty"`
	InputFiles    []sandbox.InputFile    `json:"input_files"`
	View          CodeChangeProposalView `json:"view"`
}

// GenerateProposal freezes only completed Go effects; it never applies source changes.
func (t *TaskWorkspaces) GenerateProposal(ctx context.Context, owner uint, id string, seq int64, requestID string) (TaskWorkspaceView, error) {
	if requestID == "" || len(requestID) > 128 || strings.IndexFunc(requestID, unicode.IsControl) >= 0 || seq < 0 {
		return TaskWorkspaceView{}, ErrInvalidSessionInput
	}
	snapshot, sessionView, err := t.read(ctx, owner, id)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	_, err = taskProposal(snapshot.Events)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	for _, event := range snapshot.Events {
		if event.Type == taskProposalIntentEvent {
			var previous taskProposalPayload
			_ = json.Unmarshal(event.Payload, &previous) // The projection above validated every retained intent.
			if previous.RequestID == requestID {
				return t.project(ctx, snapshot.Events, sessionView)
			}
		}
	}
	if seq != int64(len(snapshot.Events)) {
		return TaskWorkspaceView{}, ErrSequenceConflict
	}
	view, err := t.project(ctx, snapshot.Events, sessionView)
	if err != nil {
		return view, err
	}
	if !view.ProposalAvailable {
		return view, fmt.Errorf("%w: %s", ErrSessionStateConflict, view.ProposalReason)
	}
	prepared, _, err := taskPreparation(snapshot.Events)
	if err != nil || prepared == nil || taskToolOutcomesKnown(snapshot.Events, "") != nil {
		return view, ErrSessionStateConflict
	}
	files, inputs, err := proposalFiles(prepared.Task.Baseline, snapshot.Events)
	if err != nil {
		return view, err
	}
	if len(files) == 0 {
		return view, fmt.Errorf("%w: no completed file changes to propose", ErrSessionStateConflict)
	}
	data, _ := json.Marshal(inputs)
	finalSHA := hashCodeState(string(data))
	operation := hashCodeState(id + "\x00" + requestID + "\x00" + prepared.Task.LeaseID + "\x00" + finalSHA)
	payload := taskProposalPayload{SchemaVersion: 1, OwnerID: owner, RequestID: requestID, InputFiles: inputs,
		View: CodeChangeProposalView{ID: operation, SessionID: id, WorkspaceID: prepared.Task.LeaseID,
			BaselineChecksum: prepared.Task.Baseline.Checksum, SourceEventChecksum: snapshot.Events[len(snapshot.Events)-1].Checksum,
			FinalStateSHA256: finalSHA, State: "unknown", Files: files,
			Test: ProposalTestView{TaskBaselineView: TaskBaselineView{State: "unknown", Command: []string{"go", "test", "./...", "-count=1"}, OperationID: operation, Reason: "proposal test outcome requires reconciliation"}, ModeTransform: "readonly-v1"}}}
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
	intent, err := t.workbench.ledger.Append(ctx, id, seq, taskProposalIntentEvent, payload)
	if err != nil {
		return view, err
	}
	t.workbench.signal(id)
	payload.IntentEventID = intent.EventID
	closeScope, beginErr := t.baselineRuntime.BeginProposal(ctx, owner, id)
	knownStopped := true
	if beginErr == nil {
		frozen, freezeErr := sandbox.FreezeInput(ctx, filepath.Join(t.storage, prepared.Task.LeaseID), t.storage, operation, inputs)
		current, _, readErr := t.read(ctx, owner, id)
		if freezeErr == nil && readErr == nil && t.verifyTask(ctx, prepared.Task, current.Events) == nil {
			payload.View.Test.ArchiveSHA256 = frozen.ArchiveSHA256
			result, runErr := t.baselineRuntime.Runner.Run(ctx, sandbox.Request{Workspace: frozen.Path, FrozenInput: frozen, OperationID: operation, Program: "go", Args: []string{"test", "./...", "-count=1"}})
			knownOutcome := runErr == nil || errors.Is(runErr, sandbox.ErrSandboxUnavailable) && !errors.Is(runErr, sandbox.ErrSandboxOutcomeUnknown)
			knownStopped = result.Stopped || knownOutcome
			payload.View.Test.Output = tools.RedactSensitive(result.Output)
			payload.View.Test.Truncated, payload.View.Test.RuntimeID = result.Truncated, result.RuntimeID
			payload.View.Test.Stopped, payload.View.Test.InputSHA256 = knownStopped, result.InputSHA256
			if knownOutcome {
				payload.View.Test.ExitCode = &result.ExitCode
				payload.View.Test.State, payload.View.State = "failed", "failed"
				payload.View.Test.Reason = ""
				if runErr != nil || result.InputSHA256 != finalSHA || result.Truncated {
					payload.View.Test.State, payload.View.State = "blocked", "blocked"
					payload.View.Test.Reason = "frozen proposal test input or complete outcome is not confirmed"
				} else if result.ExitCode == 0 && result.Stopped {
					payload.View.Test.State, payload.View.State = "passed", "ready"
				}
			}
		} else {
			payload.View.Test.State, payload.View.State = "blocked", "blocked"
			payload.View.Test.Reason = "proposal input differs from completed task changes"
		}
	} else {
		payload.View.Test.State, payload.View.State = "blocked", "blocked"
		payload.View.Test.Reason = "proposal execution admission is unavailable"
	}
	if knownStopped {
		payload.View.Test.Stopped = true
	}
	recordCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer stop()
	if knownStopped && closeScope != nil && closeScope() != nil {
		payload.View.State = "unknown"
		payload.View.Test.Reason = "proposal execution scope requires reconciliation"
	}
	for attempt := 0; attempt < 16; attempt++ {
		latest, readErr := ReadVerifiedSnapshot(recordCtx, t.workbench.ledger, id)
		if readErr != nil {
			return view, readErr
		}
		if _, appendErr := t.workbench.ledger.Append(recordCtx, id, int64(len(latest.Events)), taskProposalOutcomeEvent, payload); appendErr == nil {
			t.workbench.signal(id)
			return t.Inspect(recordCtx, owner, id)
		} else if !errors.Is(appendErr, ErrSequenceConflict) {
			return view, appendErr
		}
	}
	return view, ErrSequenceConflict
}

func proposalFiles(baseline worktree.Baseline, events []Event) ([]ProposalFileView, []sandbox.InputFile, error) {
	inputs := map[string]sandbox.InputFile{}
	original := map[string]worktree.BaselineFile{}
	origins := map[string]string{}
	for _, file := range baseline.Files {
		inputs[file.Path] = sandbox.InputFile{Path: file.Path, Exists: file.Exists, SHA256: file.SHA256, Size: file.Size, Mode: file.Mode}
		original[file.Path] = file
		if file.Exists {
			origins[file.Path] = file.Path
		}
	}
	before, after := map[string]string{}, map[string]string{}
	prepared := false
	for _, event := range events {
		if event.Type == taskPreparedEvent {
			prepared = true
		}
		if !prepared || event.Type != codeModifiedEventType {
			continue
		}
		var change codeModificationPayload
		if json.Unmarshal(event.Payload, &change) != nil || validateCodeModificationPayload(change) != nil || change.BeforeSHA256 != hashCodeState(change.Before) || change.AfterSHA256 != hashCodeState(change.After) || change.DiffSHA256 != hashCodeTransition(change.Before, change.After) || !utf8.ValidString(change.Before) || !utf8.ValidString(change.After) || strings.ContainsRune(change.Before+change.After, 0) {
			return nil, nil, ErrEventIntegrity
		}
		item := inputs[change.Path]
		if item.Exists && (item.SHA256 != change.BeforeSHA256 || item.Size != int64(len(change.Before))) || !item.Exists && (change.Before != "" || change.Operation != "Write") || change.BeforeExists != nil && *change.BeforeExists != item.Exists {
			return nil, nil, ErrEventIntegrity
		}
		origin := origins[change.Path]
		if origin != "" {
			if _, seen := before[origin]; !seen {
				before[origin] = change.Before
			}
		}
		if change.Operation == "Delete" || change.Operation == "Rename" {
			if change.Operation == "Rename" {
				if inputs[change.Destination].Exists {
					return nil, nil, ErrEventIntegrity
				}
				moved := item
				moved.Path = change.Destination
				inputs[change.Destination] = moved
				origins[change.Destination], after[change.Destination] = origin, change.After
			}
			inputs[change.Path] = sandbox.InputFile{Path: change.Path, Exists: false}
			origins[change.Path], after[change.Path] = "", ""
			continue
		}
		item.Path, item.Exists, item.SHA256, item.Size = change.Path, true, change.AfterSHA256, int64(len(change.After))
		if change.Operation == "Write" {
			item.Mode = 0644
			if runtime.GOOS == "windows" {
				item.Mode = 0666
			}
		}
		inputs[change.Path], after[change.Path] = item, change.After
	}
	files, total := []ProposalFileView{}, 0
	consumed := map[string]bool{}
	appendFile := func(file ProposalFileView, oldContent, newContent string) error {
		file.Diff = proposalDiff(file, oldContent, newContent)
		total += len(file.Diff)
		if total > 1<<20 {
			return fmt.Errorf("%w: complete proposal diff exceeds review bounds", ErrSessionStateConflict)
		}
		files = append(files, file)
		return nil
	}
	for path, item := range inputs {
		origin := origins[path]
		if !item.Exists || origin == "" || origin == path || !original[origin].Exists || original[path].Exists || inputs[origin].Exists {
			continue
		}
		previous := original[origin]
		file := ProposalFileView{Path: origin, Destination: path, Operation: "rename", BeforeExists: true, AfterExists: true, BeforeSHA256: previous.SHA256, AfterSHA256: item.SHA256, BeforeMode: previous.Mode, AfterMode: item.Mode}
		if err := appendFile(file, before[origin], after[path]); err != nil {
			return nil, nil, err
		}
		consumed[origin], consumed[path] = true, true
	}
	for path, item := range inputs {
		previous := original[path]
		if consumed[path] || previous.Exists == item.Exists && previous.SHA256 == item.SHA256 && previous.Mode == item.Mode {
			continue
		}
		if !previous.Exists && !item.Exists {
			continue
		}
		operation := "edit"
		if !previous.Exists {
			operation = "create"
		} else if !item.Exists {
			operation = "delete"
		}
		file := ProposalFileView{Path: path, Operation: operation, BeforeExists: previous.Exists, AfterExists: item.Exists, BeforeSHA256: previous.SHA256, AfterSHA256: item.SHA256, BeforeMode: previous.Mode, AfterMode: item.Mode}
		if err := appendFile(file, before[path], after[path]); err != nil {
			return nil, nil, err
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	inventory := make([]sandbox.InputFile, 0, len(inputs))
	for _, file := range inputs {
		inventory = append(inventory, file)
	}
	sort.Slice(inventory, func(i, j int) bool { return inventory[i].Path < inventory[j].Path })
	return files, inventory, nil
}

func taskProposal(events []Event) (*taskProposalPayload, error) {
	var result *taskProposalPayload
	requests := map[string]bool{}
	for position, event := range events {
		if event.Type != taskProposalIntentEvent && event.Type != taskProposalOutcomeEvent {
			continue
		}
		var payload taskProposalPayload
		if json.Unmarshal(event.Payload, &payload) != nil || payload.SchemaVersion != 1 || payload.OwnerID == 0 || payload.RequestID == "" || len(payload.View.ID) != 64 || payload.View.SessionID != event.SessionID || payload.View.WorkspaceID == "" || len(payload.View.BaselineChecksum) != 64 || len(payload.View.SourceEventChecksum) != 64 || len(payload.View.Files) == 0 {
			return nil, ErrEventIntegrity
		}
		// Legacy proposal bytes remain immutable; only their read projection gains lifecycle labels.
		for index, file := range payload.View.Files {
			if file.Operation == "" {
				if file.Destination != "" {
					return nil, ErrEventIntegrity
				}
				file.Operation = "edit"
				if !file.BeforeExists {
					file.Operation = "create"
				} else if !file.AfterExists {
					file.Operation = "delete"
				}
				payload.View.Files[index] = file
			}
		}
		data, _ := json.Marshal(payload.InputFiles)
		if hashCodeState(string(data)) != payload.View.FinalStateSHA256 || payload.View.Test.OperationID != payload.View.ID || !slices.Equal(payload.View.Test.Command, []string{"go", "test", "./...", "-count=1"}) || payload.View.Test.ModeTransform != "readonly-v1" {
			return nil, ErrEventIntegrity
		}
		if event.Type == taskProposalIntentEvent {
			if requests[payload.RequestID] || result != nil && !result.View.Test.Stopped || payload.IntentEventID != "" || payload.View.State != "unknown" || payload.View.Test.ExitCode != nil {
				return nil, ErrEventIntegrity
			}
			requests[payload.RequestID] = true
			prepared, _, err := taskPreparation(events[:position])
			if err != nil || prepared == nil || payload.View.SourceEventChecksum != event.PrevChecksum || payload.View.WorkspaceID != prepared.Task.LeaseID || payload.View.BaselineChecksum != prepared.Task.Baseline.Checksum {
				return nil, ErrEventIntegrity
			}
			files, inputs, err := proposalFiles(prepared.Task.Baseline, events[:position])
			if err != nil || !slices.Equal(files, payload.View.Files) || !slices.Equal(inputs, payload.InputFiles) || payload.View.ID != hashCodeState(event.SessionID+"\x00"+payload.RequestID+"\x00"+payload.View.WorkspaceID+"\x00"+payload.View.FinalStateSHA256) {
				return nil, ErrEventIntegrity
			}
			payload.IntentEventID = event.EventID
		} else {
			if result == nil || result.IntentEventID != payload.IntentEventID || result.RequestID != payload.RequestID || result.View.ID != payload.View.ID || result.OwnerID != payload.OwnerID || result.View.FinalStateSHA256 != payload.View.FinalStateSHA256 ||
				result.View.SessionID != payload.View.SessionID || result.View.WorkspaceID != payload.View.WorkspaceID || result.View.BaselineChecksum != payload.View.BaselineChecksum || result.View.SourceEventChecksum != payload.View.SourceEventChecksum ||
				!slices.Equal(result.InputFiles, payload.InputFiles) || !slices.Equal(result.View.Files, payload.View.Files) || !slices.Equal(result.View.Test.Command, payload.View.Test.Command) || result.View.Test.ModeTransform != payload.View.Test.ModeTransform {
				return nil, ErrEventIntegrity
			}
			if payload.View.State == "ready" && (payload.View.Test.State != "passed" || payload.View.Test.ExitCode == nil || *payload.View.Test.ExitCode != 0 || payload.View.Test.Truncated || !payload.View.Test.Stopped || payload.View.Test.InputSHA256 != payload.View.FinalStateSHA256 || len(payload.View.Test.ArchiveSHA256) != 64 || payload.View.Test.RuntimeID == "") {
				return nil, ErrEventIntegrity
			}
		}
		result = &payload
	}
	return result, nil
}
