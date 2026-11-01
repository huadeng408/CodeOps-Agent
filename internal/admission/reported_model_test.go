package admission_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/session"
)

func TestEvaluationRequiresExactTrustedReportedModel(t *testing.T) {
	for _, scenario := range []struct {
		name, pin, reported string
		accepted            bool
	}{
		{"unset-rejects-build", "", "grok-4.6-build", false},
		{"explicit-exact-build", "grok-4.6-build", "grok-4.6-build", true},
		{"explicit-rejects-other", "grok-4.6-build", "grok-4.6-other", false},
		{"explicit-rejects-missing", "grok-4.6-build", "", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Trace-Id") != "opaque-evaluation-cohort" {
					t.Error("provider routing pin was not applied")
				}
				var payload map[string]any
				if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["model"] != "grok-4.6" {
					t.Error("reported-model pin changed requested model")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"model": scenario.reported, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 3}})
			}))
			defer upstream.Close()
			t.Setenv("LLM_PROVIDER", "openai")
			t.Setenv("OPENAI_MODEL", "grok-4.6")
			t.Setenv("OPENAI_API_KEY", "fixture-only-key")
			t.Setenv("OPENAI_BASE_URL", upstream.URL)
			t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "100")
			t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "64")
			t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
			t.Setenv("CODE_AGENT_MODEL_REASONING_EFFORT", "xhigh")
			t.Setenv("CODE_AGENT_MODEL_REPORTED_MODEL", scenario.pin)
			t.Setenv("CODE_AGENT_MODEL_PROVIDER_SESSION_ID", "opaque-evaluation-cohort")
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			view, err := session.NewWorkbench(ledger, nil).Create(context.Background(), 7, "fixture", "model identity", "")
			if err != nil {
				t.Fatal(err)
			}
			policy := session.EvaluationPolicy{ContextStrategy: "metadata_on_demand", ExecutionStrategy: "single", MaxToolRounds: 4, ContextMaxBytes: 12000}
			if err := session.BindEvaluation(context.Background(), ledger, view.ID, session.EvaluationBinding{SchemaVersion: 1, RunID: "reported-model", TaskID: "reported-model", MaxTokens: 1000, WallClockSeconds: 30, Policy: policy}); err != nil {
				t.Fatal(err)
			}
			budget := admission.NewBudget(ledger)
			gateway := admission.NewGateway(budget, admission.ProviderFromEnv())
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, view.ID, view.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			response, err := gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "identity-call", PayloadJson: []byte(`{"model":"grok-4.6","messages":[]}`)})
			if err != nil || (response.GetErrorCode() == "") != scenario.accepted {
				t.Fatalf("unexpected identity decision: accepted=%v code=%s err=%v", scenario.accepted, response.GetErrorCode(), err)
			}
			batch, err := budget.Inspect(context.Background(), 7)
			if err != nil || batch.UsedTokens != 13 || batch.ReservedTokens != 0 {
				t.Fatal("model identity refusal erased actual usage")
			}
			events, err := ledger.Events(context.Background(), view.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range events {
				if event.Type != "evaluation/model-response" {
					continue
				}
				if strings.Contains(string(event.Payload), "fixture-only-key") || strings.Contains(string(event.Payload), "opaque-evaluation-cohort") {
					t.Fatal("credential escaped into model profile")
				}
				var fact map[string]any
				if json.Unmarshal(event.Payload, &fact) != nil {
					t.Fatal("invalid model audit")
				}
				profile := fact["effective_request_profile"].(map[string]any)
				expected := scenario.pin
				if expected == "" {
					expected = "grok-4.6"
				}
				if profile["expected_reported_model"] != expected {
					t.Fatal("model response pin is absent from the audit")
				}
				hash := sha256.Sum256([]byte("opaque-evaluation-cohort"))
				if profile["provider_session_sha256"] != hex.EncodeToString(hash[:]) {
					t.Fatal("provider routing profile lacks its frozen hash")
				}
			}
		})
	}
}
