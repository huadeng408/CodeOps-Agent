package session

import (
	"code-agent/internal/safety"
	"code-agent/internal/worktree"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

type proposalItemPayload struct {
	SchemaVersion       int                            `json:"schema_version"`
	OwnerID             uint                           `json:"owner_id"`
	RequestID           string                         `json:"request_id"`
	ApplicationIntentID string                         `json:"application_intent_id"`
	Index               int                            `json:"index"`
	IntentEventID       string                         `json:"intent_event_id,omitempty"`
	Result              worktree.FileApplicationResult `json:"result"`
}

func (t *TaskWorkspaces) applyProposal(ctx context.Context, owner uint, id string, seq int64, requestID string, proposal *taskProposalPayload, events []Event) (TaskWorkspaceView, error) {
	prepared, _, err := taskPreparation(events)
	if err != nil || prepared == nil || proposal.View.State != "ready" || t.verifyTask(ctx, prepared.Task, events) != nil {
		return TaskWorkspaceView{}, ErrSessionStateConflict
	}
	changes, err := proposalFileApplications(prepared.Task.Baseline, proposal, events)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	return t.recordProposalApplication(ctx, owner, id, seq, requestID, proposal, prepared.Task.Baseline, changes)
}

func proposalFileApplications(baseline worktree.Baseline, proposal *taskProposalPayload, events []Event) ([]worktree.FileApplication, error) {
	files, inputs, err := proposalFiles(baseline, events)
	if err != nil || !slices.Equal(files, proposal.View.Files) || !slices.Equal(inputs, proposal.InputFiles) {
		return nil, ErrSessionStateConflict
	}
	before, after := map[string]string{}, map[string]string{}
	started := false
	for _, event := range events {
		if event.Type == taskPreparedEvent {
			started = true
		}
		if !started || event.Type != codeModifiedEventType {
			continue
		}
		var change codeModificationPayload
		if json.Unmarshal(event.Payload, &change) != nil {
			return nil, ErrEventIntegrity
		}
		if _, seen := before[change.Path]; !seen {
			before[change.Path] = change.Before
		}
		after[change.Path] = change.After
		if change.Operation == "Rename" {
			after[change.Path] = ""
			after[change.Destination] = change.After
		}
	}
	changes := make([]worktree.FileApplication, 0, len(files))
	for _, file := range files {
		target := file.Path
		if file.Destination != "" {
			target = file.Destination
		}
		change := worktree.FileApplication{Path: file.Path, Destination: file.Destination, Operation: file.Operation, BeforeExists: file.BeforeExists, AfterExists: file.AfterExists, BeforeMode: file.BeforeMode, AfterMode: file.AfterMode}
		if file.BeforeExists {
			change.Before = before[file.Path]
			if hashCodeState(change.Before) != file.BeforeSHA256 {
				return nil, ErrEventIntegrity
			}
		}
		if file.AfterExists {
			change.After = after[target]
			if hashCodeState(change.After) != file.AfterSHA256 {
				return nil, ErrEventIntegrity
			}
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func (t *TaskWorkspaces) recordProposalApplication(ctx context.Context, owner uint, id string, seq int64, requestID string, proposal *taskProposalPayload, baseline worktree.Baseline, changes []worktree.FileApplication) (TaskWorkspaceView, error) {
	process, err := safety.CurrentProcessIdentity()
	if err != nil {
		return TaskWorkspaceView{}, ErrSessionStateConflict
	}
	payload := proposalApplicationPayload{SchemaVersion: 3, Process: process, OwnerID: owner, BaselineChecksum: proposal.View.BaselineChecksum, FinalStateSHA256: proposal.View.FinalStateSHA256, View: ProposalApplicationView{RequestID: requestID, ProposalID: proposal.View.ID, Decision: "approve", State: "unknown", Reason: "application outcome requires reconciliation"}}
	var parent Event
	itemIntents := map[int]string{}
	record := func(index int, result *worktree.FileApplicationResult) error {
		recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if parent.EventID == "" {
			var appendErr error
			parent, appendErr = t.workbench.ledger.Append(ctx, id, seq, proposalApplicationIntent, payload)
			if appendErr != nil {
				return appendErr
			}
			t.workbench.signal(id)
		}
		item := proposalItemPayload{SchemaVersion: 2, OwnerID: owner, RequestID: requestID, ApplicationIntentID: parent.EventID, Index: index}
		kind := proposalItemIntent
		if result != nil {
			kind = proposalItemOutcome
			item.IntentEventID = itemIntents[index]
			item.Result = *result
		}
		for attempt := 0; attempt < 16; attempt++ {
			latest, current, readErr := t.read(recordCtx, owner, id)
			if readErr != nil {
				return readErr
			}
			if result == nil && (!t.approved(ctx, owner, current.WorkingDir) || !applicationIdle(latest.Events)) {
				return ErrSessionStateConflict
			}
			event, appendErr := t.workbench.ledger.Append(recordCtx, id, int64(len(latest.Events)), kind, item)
			if appendErr == nil {
				if result == nil {
					itemIntents[index] = event.EventID
				}
				t.workbench.signal(id)
				if result == nil {
					latest, current, readErr = t.read(ctx, owner, id)
					if readErr != nil {
						return readErr
					}
					if !t.approved(ctx, owner, current.WorkingDir) || !applicationIdle(latest.Events) {
						return ErrSessionStateConflict
					}
				}
				return nil
			}
			if !errors.Is(appendErr, ErrSequenceConflict) {
				return appendErr
			}
		}
		return ErrSequenceConflict
	}
	_, applyErr := t.manager.ApplyFiles(ctx, baseline, changes, record)
	if parent.EventID == "" {
		return TaskWorkspaceView{}, errors.Join(ErrSessionStateConflict, applyErr)
	}
	payload.IntentEventID = parent.EventID
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	for attempt := 0; attempt < 16; attempt++ {
		latest, readErr := ReadVerifiedSnapshot(recordCtx, t.workbench.ledger, id)
		if readErr != nil {
			return TaskWorkspaceView{}, readErr
		}
		applications, readErr := taskApplications(latest.Events)
		if readErr != nil {
			return TaskWorkspaceView{}, readErr
		}
		if applications[requestID].View.Reconciled {
			return t.Inspect(recordCtx, owner, id)
		}
		payload.View.State = applicationItemsState(applications[requestID].View.Items)
		if payload.View.State == "applied" {
			payload.View.Reason = ""
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

func applicationItemsState(items []ProposalApplicationItem) string {
	applied := 0
	for _, item := range items {
		if item.State == "pending" || item.State == "unknown" {
			return "unknown"
		}
		if item.State == "applied" {
			applied++
		}
	}
	if applied == len(items) && applied > 0 {
		return "applied"
	}
	if applied > 0 {
		return "partial"
	}
	return "conflict"
}
