package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"code-agent/internal/admission"
	"code-agent/internal/session"
)

func TestNativeAuditRejectsTamperingAndIncompleteCalls(t *testing.T) {
	fixture := filepath.Join("..", "..", "tests", "fixtures", "product_trace")
	for _, scenario := range []string{"valid", "sequence", "payload", "reservation-only", "missing-owner", "foreign-admission-owner"} {
		t.Run(scenario, func(t *testing.T) {
			paths := map[string]string{}
			for _, name := range []string{"session", "before", "after"} {
				data, err := os.ReadFile(filepath.Join(fixture, name+".json"))
				if err != nil {
					t.Fatal(err)
				}
				var document struct {
					Events []session.Event `json:"events"`
				}
				if err := json.Unmarshal(data, &document); err != nil {
					t.Fatal(err)
				}
				events := document.Events
				if name == "session" && scenario == "sequence" {
					events[1].Seq = 99
				}
				if name == "session" && scenario == "payload" {
					events[1].Payload = json.RawMessage(`{"run_id":"run:1","tool_call_id":"tool-1","tool_name":"Read"}`)
				}
				if name == "session" && scenario == "missing-owner" {
					events[0].Payload = json.RawMessage(`{}`)
				}
				if name == "after" && scenario == "reservation-only" {
					document.Events = events[:2]
				}
				data, err = json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				paths[name] = filepath.Join(t.TempDir(), name+".json")
				if err := os.WriteFile(paths[name], data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "foreign-admission-owner" {
				ctx := context.Background()
				ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "foreign.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				defer ledger.Close()
				budget := admission.NewBudget(ledger)
				if _, err := budget.Open(ctx, 8); err != nil {
					t.Fatal(err)
				}
				write := func(name string) {
					events, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
					if err != nil {
						t.Fatal(err)
					}
					data, err := json.Marshal(map[string]any{"events": events})
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(paths[name], data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				write("before")
				if _, err := budget.Reserve(ctx, 8, "session-1", "call-1", 100); err != nil {
					t.Fatal(err)
				}
				if _, err := budget.Settle(ctx, 8, "call-1", 20); err != nil {
					t.Fatal(err)
				}
				if _, err := budget.FinishTask(ctx, 8, "session-1"); err != nil {
					t.Fatal(err)
				}
				write("after")
			}
			data, err := os.ReadFile(paths["session"])
			if err != nil {
				t.Fatal(err)
			}
			var document struct {
				Events []session.Event `json:"events"`
			}
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			var run struct {
				RunID string `json:"run_id"`
			}
			for _, event := range document.Events {
				if event.Type == "session/continued" {
					_ = json.Unmarshal(event.Payload, &run)
					break
				}
			}
			err = verify(paths["session"], paths["before"], paths["after"], run.RunID)
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("native audit scenario %s: %v", scenario, err)
			}
		})
	}
}
