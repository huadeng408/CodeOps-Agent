package session

import (
	"code-agent/internal/worktree"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
)

const proposalApplicationIntent = "workspace/application-intent"
const proposalApplicationOutcome = "workspace/application-outcome"

type ProposalApplicationView struct {
	RequestID  string `json:"requestId"`
	ProposalID string `json:"proposalId"`
	Decision   string `json:"decision"`
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
}

type proposalApplicationPayload struct {
	SchemaVersion    int                     `json:"schema_version"`
	OwnerID          uint                    `json:"owner_id"`
	BaselineChecksum string                  `json:"baseline_checksum"`
	FinalStateSHA256 string                  `json:"final_state_sha256"`
	IntentEventID    string                  `json:"intent_event_id,omitempty"`
	View             ProposalApplicationView `json:"view"`
}

// DecideProposal accepts identity and an explicit decision, never file bodies or a root.
func (t *TaskWorkspaces) DecideProposal(ctx context.Context, owner uint, id string, seq int64, requestID, proposalID, decision string) (TaskWorkspaceView, error) {
	if seq < 0 || requestID == "" || len(requestID) > 128 || strings.IndexFunc(requestID, unicode.IsControl) >= 0 || len(proposalID) != 64 || decision != "approve" && decision != "reject" {
		return TaskWorkspaceView{}, ErrInvalidSessionInput
	}
	snapshot, sessionView, err := t.read(ctx, owner, id)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	applications, err := taskApplications(snapshot.Events)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	if previous, exists := applications[requestID]; exists {
		if previous.View.ProposalID != proposalID || previous.View.Decision != decision {
			return TaskWorkspaceView{}, ErrSessionStateConflict
		}
		view, err := t.project(ctx, snapshot.Events, sessionView)
		view.Application = &previous.View
		return view, err
	}
	if seq != int64(len(snapshot.Events)) {
		return TaskWorkspaceView{}, ErrSequenceConflict
	}
	for _, previous := range applications {
		if previous.View.State == "unknown" || previous.View.ProposalID == proposalID {
			return TaskWorkspaceView{}, ErrSessionStateConflict
		}
	}
	proposal, err := taskProposal(snapshot.Events)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	if proposal == nil || proposal.View.ID != proposalID || proposal.OwnerID != owner || !proposal.View.Test.Stopped || !t.approved(ctx, owner, sessionView.WorkingDir) || !applicationIdle(snapshot.Events) {
		return TaskWorkspaceView{}, ErrSessionStateConflict
	}
	payload := proposalApplicationPayload{SchemaVersion: 1, OwnerID: owner, BaselineChecksum: proposal.View.BaselineChecksum, FinalStateSHA256: proposal.View.FinalStateSHA256, View: ProposalApplicationView{RequestID: requestID, ProposalID: proposalID, Decision: decision, State: "unknown", Reason: "application outcome requires reconciliation"}}
	var intent Event
	appendIntent := func() error {
		if !t.approved(ctx, owner, sessionView.WorkingDir) {
			return ErrSessionStateConflict
		}
		var appendErr error
		intent, appendErr = t.workbench.ledger.Append(ctx, id, seq, proposalApplicationIntent, payload)
		if appendErr == nil {
			t.workbench.signal(id)
			latest, current, readErr := t.read(ctx, owner, id)
			if readErr != nil {
				return readErr
			}
			if !t.approved(ctx, owner, current.WorkingDir) || !applicationIdle(latest.Events) {
				return ErrSessionStateConflict
			}
		}
		return appendErr
	}
	known := true
	if decision == "reject" {
		err = appendIntent()
		payload.View.State, payload.View.Reason = "rejected", ""
	} else {
		prepared, _, prepareErr := taskPreparation(snapshot.Events)
		if prepareErr != nil || prepared == nil || proposal.View.State != "ready" || len(proposal.View.Files) != 1 || !proposal.View.Files[0].BeforeExists || !proposal.View.Files[0].AfterExists || proposal.View.Files[0].BeforeMode != proposal.View.Files[0].AfterMode || t.verifyTask(ctx, prepared.Task, snapshot.Events) != nil {
			return TaskWorkspaceView{}, ErrSessionStateConflict
		}
		files, inputs, filesErr := proposalFiles(prepared.Task.Baseline, snapshot.Events)
		if filesErr != nil || !slices.Equal(files, proposal.View.Files) || !slices.Equal(inputs, proposal.InputFiles) {
			return TaskWorkspaceView{}, ErrSessionStateConflict
		}
		change := worktree.TaskFileChange{Path: files[0].Path, Operation: "Edit"}
		seen := false
		preparedSeen := false
		for _, event := range snapshot.Events {
			if event.Type == taskPreparedEvent {
				preparedSeen = true
			}
			if !preparedSeen || event.Type != codeModifiedEventType {
				continue
			}
			var modified codeModificationPayload
			if json.Unmarshal(event.Payload, &modified) != nil {
				return TaskWorkspaceView{}, ErrEventIntegrity
			}
			if modified.Path == change.Path {
				if !seen {
					change.Before = modified.Before
					seen = true
				}
				change.After = modified.After
			}
		}
		if !seen || hashCodeState(change.Before) != files[0].BeforeSHA256 || hashCodeState(change.After) != files[0].AfterSHA256 {
			return TaskWorkspaceView{}, ErrEventIntegrity
		}
		known, err = t.manager.ApplySingleFile(ctx, prepared.Task.Baseline, change, appendIntent)
		if err == nil {
			payload.View.State, payload.View.Reason = "applied", ""
		} else if known {
			payload.View.State, payload.View.Reason = "conflict", "original baseline, file access or authorization changed"
		}
	}
	if intent.EventID == "" {
		if err != nil {
			return TaskWorkspaceView{}, errors.Join(ErrSessionStateConflict, err)
		}
		return TaskWorkspaceView{}, ErrEventIntegrity
	}
	payload.IntentEventID = intent.EventID
	if !known {
		payload.View.State = "unknown"
		payload.View.Reason = "application outcome requires reconciliation"
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	for attempt := 0; attempt < 16; attempt++ {
		latest, readErr := ReadVerifiedSnapshot(recordCtx, t.workbench.ledger, id)
		if readErr != nil {
			return TaskWorkspaceView{}, readErr
		}
		if _, appendErr := t.workbench.ledger.Append(recordCtx, id, int64(len(latest.Events)), proposalApplicationOutcome, payload); appendErr == nil {
			t.workbench.signal(id)
			return t.Inspect(recordCtx, owner, id)
		} else if !errors.Is(appendErr, ErrSequenceConflict) {
			return TaskWorkspaceView{}, appendErr
		}
	}
	return TaskWorkspaceView{}, ErrSequenceConflict
}

func applicationIdle(events []Event) bool {
	runs, err := projectRuns(events)
	if err != nil || taskToolOutcomesKnown(events, "") != nil {
		return false
	}
	for _, run := range runs {
		if !run.terminal {
			return false
		}
	}
	return true
}

func taskApplications(events []Event) (map[string]proposalApplicationPayload, error) {
	result := map[string]proposalApplicationPayload{}
	decided := map[string]bool{}
	outcomes := map[string]bool{}
	for position, event := range events {
		if event.Type != proposalApplicationIntent && event.Type != proposalApplicationOutcome {
			continue
		}
		var payload proposalApplicationPayload
		if json.Unmarshal(event.Payload, &payload) != nil || payload.SchemaVersion != 1 || payload.OwnerID == 0 || payload.View.RequestID == "" || len(payload.View.RequestID) > 128 || strings.IndexFunc(payload.View.RequestID, unicode.IsControl) >= 0 || payload.View.Decision != "approve" && payload.View.Decision != "reject" {
			return nil, ErrEventIntegrity
		}
		if event.Type == proposalApplicationIntent {
			if _, exists := result[payload.View.RequestID]; exists || decided[payload.View.ProposalID] || payload.IntentEventID != "" || payload.View.State != "unknown" {
				return nil, ErrEventIntegrity
			}
			for _, previous := range result {
				if previous.View.State == "unknown" {
					return nil, ErrEventIntegrity
				}
			}
			proposal, err := taskProposal(events[:position])
			if err != nil || proposal == nil || proposal.OwnerID != payload.OwnerID || proposal.View.ID != payload.View.ProposalID || proposal.View.BaselineChecksum != payload.BaselineChecksum || proposal.View.FinalStateSHA256 != payload.FinalStateSHA256 || !proposal.View.Test.Stopped || !applicationIdle(events[:position]) {
				return nil, ErrEventIntegrity
			}
			if payload.View.Decision == "approve" && (proposal.View.State != "ready" || len(proposal.View.Files) != 1 || !proposal.View.Files[0].BeforeExists || !proposal.View.Files[0].AfterExists || proposal.View.Files[0].BeforeMode != proposal.View.Files[0].AfterMode) {
				return nil, ErrEventIntegrity
			}
			payload.IntentEventID = event.EventID
			decided[payload.View.ProposalID] = true
		} else {
			previous, exists := result[payload.View.RequestID]
			if !exists || outcomes[payload.View.RequestID] || previous.View.State != "unknown" || previous.IntentEventID != payload.IntentEventID || previous.OwnerID != payload.OwnerID || previous.BaselineChecksum != payload.BaselineChecksum || previous.FinalStateSHA256 != payload.FinalStateSHA256 || previous.View.ProposalID != payload.View.ProposalID || previous.View.Decision != payload.View.Decision {
				return nil, ErrEventIntegrity
			}
			if payload.View.State != "unknown" && (payload.View.Decision == "reject" && payload.View.State != "rejected" || payload.View.Decision == "approve" && payload.View.State != "applied" && payload.View.State != "conflict") {
				return nil, ErrEventIntegrity
			}
			outcomes[payload.View.RequestID] = true
		}
		result[payload.View.RequestID] = payload
	}
	return result, nil
}
