package session_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"code-agent/internal/session"
)

func TestEvaluationTransportBindingRequiresVersionedSSEProfile(t *testing.T) {
	for _, scenario := range []struct {
		name, mode, protocol string
		version              int
		accepted             bool
	}{
		{"legacy JSON", "", "", 1, true},
		{"explicit SSE", "sse", "openai", 2, true},
		{"SSE cannot downgrade", "sse", "openai", 1, false},
		{"version 2 needs SSE", "", "openai", 2, false},
		{"SSE needs OpenAI", "sse", "anthropic", 2, false},
		{"unknown mode", "unknown", "openai", 2, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			view, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "fixture", "transport binding", "")
			if err != nil {
				t.Fatal(err)
			}
			binding := session.EvaluationBinding{SchemaVersion: scenario.version, RunID: "transport-binding", TaskID: "transport-binding", MaxTokens: 1000, WallClockSeconds: 30,
				Policy:  session.EvaluationPolicy{ContextStrategy: "metadata_on_demand", ExecutionStrategy: "single", MaxToolRounds: 4, ContextMaxBytes: 12000},
				Runtime: session.EvaluationRuntime{ModelProfile: session.EvaluationModelProfile{ResponseMode: scenario.mode, Protocol: scenario.protocol}}}
			err = session.BindEvaluation(ctx, ledger, view.ID, binding)
			if (err == nil) != scenario.accepted {
				t.Fatalf("unexpected binding admission: %v", err)
			}
			payload, _ := json.Marshal(binding)
			decoded, err := session.ReadEvaluation([]session.Event{{SessionID: view.ID, Type: "evaluation/trial-policy", Payload: payload}})
			if (err == nil) != scenario.accepted {
				t.Fatalf("unexpected binding read decision: %v", err)
			}
			if scenario.accepted && (decoded.SchemaVersion != scenario.version || decoded.Runtime.ModelProfile.ResponseMode != scenario.mode) {
				t.Fatal("transport pin was lost on readback")
			}
		})
	}
}
