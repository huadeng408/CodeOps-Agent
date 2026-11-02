package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/session"
)

// These Ledger protocol fixtures establish schema compatibility, not provider or
// OpenSandbox acceptance. The legacy references come from the fixture's Go proposal.
type graderSchemaFixture struct {
	session.EventLog
	mode string
}

func (l graderSchemaFixture) Append(ctx context.Context, id string, seq int64, kind string, payload any) (session.Event, error) {
	if kind != "evaluation/grade-intent" && kind != "evaluation/grade-result" {
		return l.EventLog.Append(ctx, id, seq, kind, payload)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return session.Event{}, err
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return session.Event{}, err
	}
	if l.mode == "legacy-v1" {
		events, err := l.EventLog.Events(ctx, id)
		if err != nil {
			return session.Event{}, err
		}
		var proposal session.Event
		for _, event := range events {
			if event.Type == "workspace/proposal-outcome" {
				proposal = event
			}
		}
		if proposal.EventID == "" {
			return session.Event{}, errors.New("legacy fixture requires a retained Go proposal")
		}
		value["schema_version"] = 1
		value["proposal_event_id"], value["proposal_event_checksum"] = proposal.EventID, proposal.Checksum
		delete(value, "prepared_event_id")
		delete(value, "prepared_event_checksum")
	} else if kind == "evaluation/grade-intent" {
		switch l.mode {
		case "missing-prepared-event":
			value["prepared_event_id"] = "missing-prepared-fixture"
		case "wrong-prepared-checksum":
			value["prepared_event_checksum"] = strings.Repeat("f", 64)
		case "wrong-inventory-hash":
			value["view"].(map[string]any)["candidate_sha256"] = strings.Repeat("f", 64)
		}
	} else if l.mode == "mixed-result-schema" {
		value["schema_version"] = 1
	}
	return l.EventLog.Append(ctx, id, seq, kind, value)
}

func TestEvaluationGradeLegacyV1FixtureProjectsAndReplays(t *testing.T) {
	f := prepareGraderFixture(t, func(log session.EventLog) session.EventLog {
		return graderSchemaFixture{EventLog: log, mode: "legacy-v1"}
	})
	ctx := context.Background()
	checks := []byte(`{"schema_version":1,"files":[{"path":"answer.json","contains":"visible-output"}]}`)
	path := filepath.Join(t.TempDir(), "checks.json")
	if err := os.WriteFile(path, checks, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := session.ReadVerifiedSnapshot(ctx, f.ledger, f.id)
	if err != nil {
		t.Fatal(err)
	}
	command := session.EvaluationGradeCommand{OwnerID: 7, Actor: f.actor, SessionID: f.id, RequestID: "legacy-grade-fixture", ExpectedSessionHeadChecksum: before.Events[len(before.Events)-1].Checksum, ChecksPath: path, ChecksSHA256: graderHash(checks)}
	grade, err := f.tasks.GradeEvaluation(ctx, command)
	if err != nil || grade.State != "completed" || grade.Passed == nil || !*grade.Passed || grade.CandidateSHA256 != f.proposal.Proposal.FinalStateSHA256 || f.runtime.gradeCalls != 1 {
		t.Fatalf("legacy fixture did not produce a known result: %+v %v", grade, err)
	}
	stored, err := session.ReadVerifiedSnapshot(ctx, f.ledger, f.id)
	if err != nil {
		t.Fatal(err)
	}
	var proposal session.Event
	for _, event := range before.Events {
		if event.Type == "workspace/proposal-outcome" {
			proposal = event
		}
	}
	for _, event := range stored.Events[len(before.Events):] {
		var value struct {
			SchemaVersion         int    `json:"schema_version"`
			ProposalEventID       string `json:"proposal_event_id"`
			ProposalEventChecksum string `json:"proposal_event_checksum"`
			PreparedEventID       string `json:"prepared_event_id"`
		}
		if err := json.Unmarshal(event.Payload, &value); err != nil || value.SchemaVersion != 1 || value.ProposalEventID != proposal.EventID || value.ProposalEventChecksum != proposal.Checksum || value.PreparedEventID != "" {
			t.Fatalf("legacy fixture lost its actual proposal binding: %+v %v", value, err)
		}
	}
	projected, err := session.ReadEvaluationGrade(stored.Events)
	if err != nil || projected == nil || projected.State != "completed" || projected.ResultEventID != grade.ResultEventID || projected.ResultEventChecksum != grade.ResultEventChecksum {
		t.Fatalf("legacy projection changed the canonical result: %+v %v", projected, err)
	}
	again, err := f.tasks.GradeEvaluation(ctx, command)
	if err != nil || !again.AlreadyRecorded || again.ResultEventID != grade.ResultEventID || again.ResultEventChecksum != grade.ResultEventChecksum || f.runtime.gradeCalls != 1 {
		t.Fatalf("legacy result was not replayed idempotently: %+v %v", again, err)
	}
	after, err := session.ReadVerifiedSnapshot(ctx, f.ledger, f.id)
	if err != nil || len(after.Events) != len(stored.Events) || after.Events[len(after.Events)-1].Checksum != stored.Events[len(stored.Events)-1].Checksum {
		t.Fatalf("legacy replay changed immutable Ledger facts: %v", err)
	}
}

func TestEvaluationGradeV2FixtureRejectsTamperedBindings(t *testing.T) {
	for _, mode := range []string{"missing-prepared-event", "wrong-prepared-checksum", "wrong-inventory-hash", "mixed-result-schema"} {
		t.Run(mode, func(t *testing.T) {
			f := prepareGraderFixture(t, func(log session.EventLog) session.EventLog {
				return graderSchemaFixture{EventLog: log, mode: mode}
			})
			// No checker execution is needed to test persisted protocol validation.
			f.runtime.outcome = "unavailable"
			ctx := context.Background()
			checks := []byte(`{"schema_version":1,"files":[{"path":"answer.json","contains":"visible-output"}]}`)
			path := filepath.Join(t.TempDir(), "checks.json")
			if err := os.WriteFile(path, checks, 0600); err != nil {
				t.Fatal(err)
			}
			before, err := session.ReadVerifiedSnapshot(ctx, f.ledger, f.id)
			if err != nil {
				t.Fatal(err)
			}
			command := session.EvaluationGradeCommand{OwnerID: 7, Actor: f.actor, SessionID: f.id, RequestID: "tampered-grade-fixture", ExpectedSessionHeadChecksum: before.Events[len(before.Events)-1].Checksum, ChecksPath: path, ChecksSHA256: graderHash(checks)}
			grade, err := f.tasks.GradeEvaluation(ctx, command)
			if !errors.Is(err, session.ErrEventIntegrity) || grade.State != "unknown" || grade.Passed != nil || f.runtime.gradeCalls != 1 {
				t.Fatalf("tampered protocol became a known result: %+v %v", grade, err)
			}
			stored, err := session.ReadVerifiedSnapshot(ctx, f.ledger, f.id)
			if err != nil {
				t.Fatalf("fixture broke Ledger checksums instead of grade semantics: %v", err)
			}
			if len(stored.Events) != len(before.Events)+2 {
				t.Fatal("fixture did not retain both grading facts")
			}
			intent, err := session.ReadEvaluationGrade(stored.Events[:len(before.Events)+1])
			if mode == "mixed-result-schema" {
				if err != nil || intent == nil || intent.State != "unknown" || intent.ResultEventID != "" {
					t.Fatalf("valid v2 intent was rejected before the mixed result: %+v %v", intent, err)
				}
			} else if !errors.Is(err, session.ErrEventIntegrity) || intent != nil {
				t.Fatalf("malformed v2 provenance was accepted: %+v %v", intent, err)
			}
			if projected, err := session.ReadEvaluationGrade(stored.Events); !errors.Is(err, session.ErrEventIntegrity) || projected != nil {
				t.Fatalf("tampered grade readback did not fail closed: %+v %v", projected, err)
			}
			if _, err := f.tasks.GradeEvaluation(ctx, command); !errors.Is(err, session.ErrEventIntegrity) || f.runtime.gradeCalls != 1 {
				t.Fatalf("invalid retained grade was dispatched again: %v", err)
			}
		})
	}
}
