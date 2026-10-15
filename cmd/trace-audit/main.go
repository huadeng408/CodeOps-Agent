// trace-audit verifies immutable product exports with the native Ledger readers.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/session"
)

type exportedLog struct {
	session.EventLog
	events []session.Event
}

func (l exportedLog) Events(_ context.Context, _ string) ([]session.Event, error) {
	return l.events, nil
}

func readExport(path, id string) (exportedLog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return exportedLog{}, err
	}
	var document struct {
		Events []session.Event `json:"events"`
	}
	if err := json.Unmarshal(data, &document); err != nil || len(document.Events) == 0 {
		return exportedLog{}, session.ErrEventIntegrity
	}
	if id == "" {
		id = document.Events[0].SessionID
	}
	for i := range document.Events {
		// JSON exports add indentation; the Ledger hashes compact payload bytes.
		var compact bytes.Buffer
		if err := json.Compact(&compact, document.Events[i].Payload); err != nil {
			return exportedLog{}, err
		}
		document.Events[i].Payload = append(json.RawMessage(nil), compact.Bytes()...)
	}
	log := exportedLog{events: document.Events}
	_, err = session.ReadVerifiedSnapshot(context.Background(), log, id)
	return log, err
}

func verify(sessionPath, beforePath, afterPath, runID string) error {
	ctx := context.Background()
	product, err := readExport(sessionPath, "")
	if err != nil {
		return err
	}
	var created struct {
		OwnerID uint `json:"owner_id"`
	}
	if product.events[0].Type != "session/created" || json.Unmarshal(product.events[0].Payload, &created) != nil || created.OwnerID == 0 {
		return session.ErrEventIntegrity
	}
	var actor identity.Actor
	for _, event := range product.events {
		if event.Type != "session/continued" {
			continue
		}
		var fact struct {
			RunID string         `json:"run_id"`
			Actor identity.Actor `json:"actor"`
		}
		if json.Unmarshal(event.Payload, &fact) != nil {
			return session.ErrEventIntegrity
		}
		if fact.RunID == runID {
			if actor.ActorID != "" {
				return session.ErrEventIntegrity
			}
			actor = fact.Actor
		}
	}
	if actor.Validate() != nil || actor.SessionID != product.events[0].SessionID {
		return session.ErrEventIntegrity
	}
	root, err := session.ModelTaskRoot(ctx, product, actor)
	if err != nil || root != actor.SessionID {
		return session.ErrEventIntegrity
	}
	before, err := readExport(beforePath, session.VerificationAdmissionLedgerID)
	if err != nil {
		return err
	}
	after, err := readExport(afterPath, session.VerificationAdmissionLedgerID)
	if err != nil || len(after.events) < len(before.events) {
		return session.ErrEventIntegrity
	}
	for i, event := range before.events {
		if event.Checksum != after.events[i].Checksum {
			return session.ErrEventIntegrity
		}
	}
	target := product.events[0].SessionID
	owner := uint(0)
	settled, reserved := map[string]bool{}, map[string]bool{}
	for _, event := range after.events {
		var fact struct {
			OwnerID uint   `json:"owner_id"`
			TaskID  string `json:"task_id"`
			CallID  string `json:"call_id"`
		}
		if json.Unmarshal(event.Payload, &fact) != nil {
			return session.ErrEventIntegrity
		}
		if event.Type == "admission/call_reserved" && fact.TaskID == target {
			if event.Seq < int64(len(before.events)) || fact.OwnerID != admission.ActorOwner(actor) {
				return session.ErrEventIntegrity
			}
			owner = fact.OwnerID
			reserved[fact.CallID] = true
		}
		if event.Type == "admission/call_settled" {
			settled[fact.CallID] = true
		}
	}
	if owner == 0 || len(reserved) == 0 {
		return errors.New("no model admissions for this product task")
	}
	for callID := range reserved {
		if !settled[callID] {
			return errors.New("product model usage is incomplete")
		}
	}
	if _, err := admission.NewBudget(before).Inspect(ctx, owner); err != nil {
		return err
	}
	batch, err := admission.NewBudget(after).Inspect(ctx, owner)
	if err != nil || batch.ActiveCalls != 0 || batch.TaskID != "" {
		return errors.New("product task is not quiescent")
	}
	return nil
}

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: trace-audit <session-export.json> <admission-before.json> <admission-after.json> <run-id>")
		os.Exit(2)
	}
	if verify(os.Args[1], os.Args[2], os.Args[3], os.Args[4]) != nil {
		fmt.Fprintln(os.Stderr, "native product audit refused")
		os.Exit(1)
	}
	fmt.Println("native product audit verified")
}
