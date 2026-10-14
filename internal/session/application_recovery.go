package session

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"
)

type applicationReconcilePayload struct {
	SchemaVersion        int      `json:"schema_version"`
	OwnerID              uint     `json:"owner_id"`
	RequestID            string   `json:"request_id"`
	ApplicationRequestID string   `json:"application_request_id"`
	IntentEventID        string   `json:"intent_event_id"`
	SourceEventChecksum  string   `json:"source_event_checksum"`
	ObservedChecksum     string   `json:"observed_checksum"`
	ItemStates           []string `json:"item_states"`
	State                string   `json:"state"`
	Reason               string   `json:"reason,omitempty"`
}

func (t *TaskWorkspaces) ReconcileApplication(ctx context.Context, owner uint, id string, seq int64, requestID, applicationRequestID string) (TaskWorkspaceView, error) {
	if seq < 0 || requestID == "" || len(requestID) > 128 || strings.IndexFunc(requestID, unicode.IsControl) >= 0 || applicationRequestID == "" {
		return TaskWorkspaceView{}, ErrInvalidSessionInput
	}
	snapshot, current, err := t.read(ctx, owner, id)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	applications, err := taskApplications(snapshot.Events)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	for _, event := range snapshot.Events {
		if event.Type != proposalApplicationReconciled {
			continue
		}
		var previous applicationReconcilePayload
		if json.Unmarshal(event.Payload, &previous) != nil {
			return TaskWorkspaceView{}, ErrEventIntegrity
		}
		if previous.RequestID == requestID {
			if previous.ApplicationRequestID != applicationRequestID || previous.OwnerID != owner {
				return TaskWorkspaceView{}, ErrSessionStateConflict
			}
			return t.Inspect(ctx, owner, id)
		}
	}
	application, exists := applications[applicationRequestID]
	if seq != int64(len(snapshot.Events)) {
		return TaskWorkspaceView{}, ErrSequenceConflict
	}
	if !exists || application.View.Decision != "approve" || application.OwnerID != owner || !t.approved(ctx, owner, current.WorkingDir) || !applicationIdle(snapshot.Events) {
		return TaskWorkspaceView{}, ErrSessionStateConflict
	}
	if !application.OutcomeRecorded {
		allRecorded := true
		for _, item := range application.View.Items {
			allRecorded = allRecorded && item.State == "applied"
		}
		if !allRecorded && (application.Process == nil || !application.Process.ConfirmedStopped()) {
			return TaskWorkspaceView{}, ErrSessionStateConflict
		}
	}
	proposal, err := taskProposal(snapshot.Events)
	prepared, _, prepareErr := taskPreparation(snapshot.Events)
	if err != nil || prepareErr != nil || prepared == nil || proposal == nil || proposal.View.ID != application.View.ProposalID {
		return TaskWorkspaceView{}, ErrEventIntegrity
	}
	changes, err := proposalFileApplications(prepared.Task.Baseline, proposal, snapshot.Events)
	if err != nil {
		return TaskWorkspaceView{}, err
	}
	checksum, states, err := t.manager.ObserveApplication(ctx, prepared.Task.Baseline, changes)
	if err != nil {
		return TaskWorkspaceView{}, ErrSessionStateConflict
	}
	items := append([]ProposalApplicationItem(nil), application.View.Items...)
	for index, state := range states {
		previous := items[index].State
		if application.ItemIntentIDs[index] == "" {
			if state == "not_applied" {
				state = "not_started"
			} else {
				state = "unknown"
			}
		} else if previous == "applied" && state != "applied" {
			state = "unknown"
		}
		states[index], items[index].State = state, state
	}
	if !t.approved(ctx, owner, current.WorkingDir) {
		return TaskWorkspaceView{}, ErrSessionStateConflict
	}
	last := snapshot.Events[len(snapshot.Events)-1]
	state := applicationItemsState(items)
	reason := ""
	if state != "applied" {
		reason = "remaining or ambiguous items require a fresh reviewed baseline"
	}
	payload := applicationReconcilePayload{SchemaVersion: 1, OwnerID: owner, RequestID: requestID, ApplicationRequestID: applicationRequestID, IntentEventID: application.IntentEventID, SourceEventChecksum: last.Checksum, ObservedChecksum: checksum, ItemStates: states, State: state, Reason: reason}
	if _, err := t.workbench.ledger.Append(ctx, id, seq, proposalApplicationReconciled, payload); err != nil {
		return TaskWorkspaceView{}, err
	}
	t.workbench.signal(id)
	return t.Inspect(ctx, owner, id)
}
