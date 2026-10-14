package session

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const proposalApplicationIntent = "workspace/application-intent"
const proposalApplicationOutcome = "workspace/application-outcome"
const proposalItemIntent = "workspace/application-item-intent"
const proposalItemOutcome = "workspace/application-item-outcome"

type ProposalApplicationView struct {
	RequestID  string                    `json:"requestId"`
	ProposalID string                    `json:"proposalId"`
	Decision   string                    `json:"decision"`
	State      string                    `json:"state"`
	Reason     string                    `json:"reason,omitempty"`
	Items      []ProposalApplicationItem `json:"items,omitempty"`
}

type ProposalApplicationItem struct {
	Path               string   `json:"path"`
	Destination        string   `json:"destination,omitempty"`
	Operation          string   `json:"operation"`
	State              string   `json:"state"`
	Reason             string   `json:"reason,omitempty"`
	CreatedDirectories []string `json:"createdDirectories,omitempty"`
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
		if previous.View.State == "unknown" || previous.View.State == "partial" || previous.View.ProposalID == proposalID {
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
	if decision == "approve" {
		return t.applyProposal(ctx, owner, id, seq, requestID, proposal, snapshot.Events)
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
	err = appendIntent()
	payload.View.State, payload.View.Reason = "rejected", ""
	if intent.EventID == "" {
		if err != nil {
			return TaskWorkspaceView{}, errors.Join(ErrSessionStateConflict, err)
		}
		return TaskWorkspaceView{}, ErrEventIntegrity
	}
	payload.IntentEventID = intent.EventID
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
	itemIntents := map[string]string{}
	for position, event := range events {
		if event.Type == proposalItemIntent || event.Type == proposalItemOutcome {
			var item proposalItemPayload
			if json.Unmarshal(event.Payload, &item) != nil || item.SchemaVersion != 2 {
				return nil, ErrEventIntegrity
			}
			parent, exists := result[item.RequestID]
			if !exists || parent.SchemaVersion != 2 || parent.OwnerID != item.OwnerID || parent.IntentEventID != item.ApplicationIntentID || outcomes[item.RequestID] || item.Index < 0 || item.Index >= len(parent.View.Items) {
				return nil, ErrEventIntegrity
			}
			key := item.RequestID + "\x00" + strconv.Itoa(item.Index)
			view := parent.View.Items[item.Index]
			if event.Type == proposalItemIntent {
				if view.State != "not_started" || item.IntentEventID != "" || item.Result.State != "" || item.Result.Reason != "" || len(item.Result.CreatedDirectories) != 0 {
					return nil, ErrEventIntegrity
				}
				for _, previous := range parent.View.Items[:item.Index] {
					if previous.State != "applied" {
						return nil, ErrEventIntegrity
					}
				}
				itemIntents[key] = event.EventID
				view.State = "pending"
			} else {
				if view.State != "pending" || itemIntents[key] == "" || item.IntentEventID != itemIntents[key] || item.Result.State != "applied" && item.Result.State != "conflict" && item.Result.State != "unknown" {
					return nil, ErrEventIntegrity
				}
				target := view.Path
				if view.Destination != "" {
					target = view.Destination
				}
				for _, directory := range item.Result.CreatedDirectories {
					if !fs.ValidPath(directory) || strings.ContainsAny(directory, "\\:\x00\r\n") || !strings.HasPrefix(target, directory+"/") {
						return nil, ErrEventIntegrity
					}
				}
				view.State, view.Reason, view.CreatedDirectories = item.Result.State, item.Result.Reason, item.Result.CreatedDirectories
			}
			parent.View.Items[item.Index] = view
			result[item.RequestID] = parent
			continue
		}
		if event.Type != proposalApplicationIntent && event.Type != proposalApplicationOutcome {
			continue
		}
		var payload proposalApplicationPayload
		if json.Unmarshal(event.Payload, &payload) != nil || payload.SchemaVersion != 1 && payload.SchemaVersion != 2 || len(payload.View.Items) != 0 || payload.OwnerID == 0 || payload.View.RequestID == "" || len(payload.View.RequestID) > 128 || strings.IndexFunc(payload.View.RequestID, unicode.IsControl) >= 0 || payload.View.Decision != "approve" && payload.View.Decision != "reject" {
			return nil, ErrEventIntegrity
		}
		if event.Type == proposalApplicationIntent {
			if _, exists := result[payload.View.RequestID]; exists || decided[payload.View.ProposalID] || payload.IntentEventID != "" || payload.View.State != "unknown" {
				return nil, ErrEventIntegrity
			}
			for _, previous := range result {
				if previous.View.State == "unknown" || previous.View.State == "partial" {
					return nil, ErrEventIntegrity
				}
			}
			proposal, err := taskProposal(events[:position])
			if err != nil || proposal == nil || proposal.OwnerID != payload.OwnerID || proposal.View.ID != payload.View.ProposalID || proposal.View.BaselineChecksum != payload.BaselineChecksum || proposal.View.FinalStateSHA256 != payload.FinalStateSHA256 || !proposal.View.Test.Stopped || !applicationIdle(events[:position]) {
				return nil, ErrEventIntegrity
			}
			if payload.View.Decision == "approve" && (proposal.View.State != "ready" || payload.SchemaVersion == 1 && (len(proposal.View.Files) != 1 || !proposal.View.Files[0].BeforeExists || !proposal.View.Files[0].AfterExists || proposal.View.Files[0].BeforeMode != proposal.View.Files[0].AfterMode)) {
				return nil, ErrEventIntegrity
			}
			if payload.SchemaVersion == 2 && payload.View.Decision != "approve" {
				return nil, ErrEventIntegrity
			}
			for _, file := range proposal.View.Files {
				state := "not_started"
				if payload.SchemaVersion == 1 && payload.View.Decision == "approve" {
					state = "unknown"
				}
				payload.View.Items = append(payload.View.Items, ProposalApplicationItem{Path: file.Path, Destination: file.Destination, Operation: file.Operation, State: state})
			}
			payload.IntentEventID = event.EventID
			decided[payload.View.ProposalID] = true
		} else {
			previous, exists := result[payload.View.RequestID]
			if !exists || outcomes[payload.View.RequestID] || previous.SchemaVersion != payload.SchemaVersion || previous.View.State != "unknown" || previous.IntentEventID != payload.IntentEventID || previous.OwnerID != payload.OwnerID || previous.BaselineChecksum != payload.BaselineChecksum || previous.FinalStateSHA256 != payload.FinalStateSHA256 || previous.View.ProposalID != payload.View.ProposalID || previous.View.Decision != payload.View.Decision {
				return nil, ErrEventIntegrity
			}
			if payload.SchemaVersion == 2 && payload.View.State != applicationItemsState(previous.View.Items) || payload.SchemaVersion == 1 && payload.View.State != "unknown" && (payload.View.Decision == "reject" && payload.View.State != "rejected" || payload.View.Decision == "approve" && payload.View.State != "applied" && payload.View.State != "conflict") {
				return nil, ErrEventIntegrity
			}
			payload.View.Items = previous.View.Items
			if payload.SchemaVersion == 1 && payload.View.Decision == "approve" {
				for index := range payload.View.Items {
					payload.View.Items[index].State = payload.View.State
				}
			}
			outcomes[payload.View.RequestID] = true
		}
		result[payload.View.RequestID] = payload
	}
	return result, nil
}
