package admission_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/session"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGatewayTrustedEffortOverridesEveryPurposeAndChildScope(t *testing.T) {
	var observed atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid upstream JSON")
		}
		if payload["reasoning_effort"] != "xhigh" || payload["max_completion_tokens"] != float64(64) {
			t.Errorf("Go profile was not applied: effort=%v cap=%v", payload["reasoning_effort"], payload["max_completion_tokens"])
		}
		if r.Header.Get("X-Trace-Id") != "opaque-fixture-cohort-42" {
			t.Error("Go did not bind the provider routing session across every purpose")
		}
		observed.Add(1)
		fmt.Fprint(w, `{"id":"fixture","model":"grok-4.6","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`)
	}))
	defer upstream.Close()
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "grok-4.6")
	t.Setenv("OPENAI_BASE_URL", upstream.URL)
	t.Setenv("OPENAI_API_KEY", "fixture-only")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "100")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "64")
	t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
	t.Setenv("CODE_AGENT_MODEL_REASONING_EFFORT", "xhigh")
	t.Setenv("CODE_AGENT_MODEL_PROVIDER_SESSION_ID", "opaque-fixture-cohort-42")
	profile, _ := json.Marshal(admission.ProviderFromEnv().EvaluationProfile())
	hash := sha256.Sum256([]byte("opaque-fixture-cohort-42"))
	if strings.Contains(string(profile), "opaque-fixture-cohort-42") || strings.Contains(string(profile), "fixture-only") || !strings.Contains(string(profile), hex.EncodeToString(hash[:])) {
		t.Error("safe profile omitted its routing hash or exposed raw provider session material")
	}
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	gateway := admission.NewGateway(admission.NewBudget(ledger), admission.ProviderFromEnv())
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	for i, purpose := range []string{"foreground", "foreground", "compaction", "memory_reflection", "workflow_worker"} {
		scope := "root-session"
		if i == 1 {
			scope = "child-session"
		}
		binding, release, err := gateway.Bind(7, "shared-task", scope)
		if err != nil {
			t.Fatal(err)
		}
		response, err := gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: fmt.Sprintf("profile-%d", i), Purpose: purpose, PayloadJson: []byte(`{"model":"grok-4.6","messages":[{"role":"user","content":"fixture"}],"reasoning_effort":"medium","max_tokens":999}`)})
		release()
		if err != nil || response.GetErrorCode() != "" {
			t.Fatalf("profile invocation failed: %v %s", err, response.GetErrorCode())
		}
	}
	if observed.Load() != 5 {
		t.Fatal("not all purposes reached the fixture")
	}
}

func TestGatewayDeepSeekMaxUsesDocumentedOutputCap(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		if payload["reasoning_effort"] != "max" || payload["max_tokens"] != float64(393216) || payload["max_completion_tokens"] != nil {
			t.Errorf("DeepSeek maximum profile was not applied: %v", payload)
		}
		fmt.Fprint(w, `{"model":"deepseek-v4-pro","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`)
	}))
	defer upstream.Close()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	gateway := admission.NewGateway(admission.NewBudget(ledger), admission.ProviderConfig{
		Protocol: "openai", BaseURL: upstream.URL, Model: "deepseek-v4-pro", APIKey: "fixture-only",
		InputLimit: 655360, OutputLimit: 393216, ReasoningEffort: "max",
	})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, "deepseek-task", "deepseek-session")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	response, err := gateway.Invoke(context.Background(), &pb.ModelCallRequest{
		Capability: binding.Capability, CallId: "deepseek-max", Purpose: "compaction",
		PayloadJson: []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"fixture"}],"reasoning_effort":"medium","max_tokens":999,"max_completion_tokens":999}`),
	})
	if err != nil || response.GetErrorCode() != "" {
		t.Fatalf("max effort invocation failed: %v %s", err, response.GetErrorCode())
	}
}

func TestGatewayRejectsInconsistentThinkingBeforeReservation(t *testing.T) {
	for _, thinking := range []string{`{"type":"enabled"}`, `{"type":"enabled","budget_tokens":10000}`, `{"type":"enabled","budget_tokens":1}`, `{"type":"enabled","budget_tokens":1024.5}`} {
		t.Run(thinking, func(t *testing.T) {
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				fmt.Fprint(w, `{"model":"grok-4.6","usage":{"input_tokens":10,"output_tokens":3}}`)
			}))
			defer upstream.Close()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			budget := admission.NewBudget(ledger)
			gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "anthropic", BaseURL: upstream.URL, Model: "grok-4.6", APIKey: "fixture-only", InputLimit: 32768, OutputLimit: 8192})
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, "thinking-task", "thinking-session")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			_, err = gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "invalid-thinking", PayloadJson: []byte(`{"model":"grok-4.6","messages":[{"role":"user","content":"fixture"}],"thinking":` + thinking + `}`)})
			if status.Code(err) != codes.InvalidArgument || calls.Load() != 0 {
				t.Fatalf("invalid thinking reached upstream: status=%s calls=%d", status.Code(err), calls.Load())
			}
			batch, err := budget.Inspect(context.Background(), 7)
			if err != nil || batch.ReservedTokens != 0 || batch.UsedTokens != 0 || batch.ActiveCalls != 0 {
				t.Fatal("invalid thinking created accounting")
			}
		})
	}
}

func TestGatewayPreservesValidHighThinkingAndUnsetEffort(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if json.NewDecoder(r.Body).Decode(&payload) != nil {
					t.Error("invalid upstream JSON")
				}
				if protocol == "openai" {
					if payload["reasoning_effort"] != "medium" || payload["max_completion_tokens"] != float64(131072) {
						t.Error("unset Go effort changed prior policy or lost the admitted cap")
					}
					fmt.Fprint(w, `{"model":"fixture-model","usage":{"prompt_tokens":10,"completion_tokens":3}}`)
				} else {
					if payload["max_tokens"] != float64(131072) || payload["thinking"].(map[string]any)["budget_tokens"] != float64(100000) {
						t.Error("Go silently changed the valid high thinking profile")
					}
					fmt.Fprint(w, `{"model":"fixture-model","usage":{"input_tokens":10,"output_tokens":3}}`)
				}
				calls.Add(1)
			}))
			defer upstream.Close()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			gateway := admission.NewGateway(admission.NewBudget(ledger), admission.ProviderConfig{Protocol: protocol, BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-only", InputLimit: 368928, OutputLimit: 131072})
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, "valid-profile", "valid-profile-session")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			payload := `{"model":"fixture-model","messages":[{"role":"user","content":"fixture"}],"reasoning_effort":"medium"}`
			if protocol == "anthropic" {
				payload = `{"model":"fixture-model","messages":[{"role":"user","content":"fixture"}],"thinking":{"type":"enabled","budget_tokens":100000}}`
			}
			response, err := gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "valid-call", PayloadJson: []byte(payload)})
			if err != nil || response.GetErrorCode() != "" || calls.Load() != 1 {
				t.Fatalf("valid high profile refused: %v %s", err, response.GetErrorCode())
			}
		})
	}
}

func TestGatewayRejectsUnknownTrustedEffortWithoutDispatch(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 64, ReasoningEffort: "unrecognized-profile"})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, "invalid-profile", "invalid-profile-session")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	response, err := gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "invalid-call", PayloadJson: []byte(`{"model":"fixture-model","messages":[]}`)})
	if err != nil || response.GetErrorCode() != "model_prerequisites_missing" || calls.Load() != 0 {
		t.Fatal("unknown trusted effort dispatched")
	}
	batch, err := budget.Inspect(context.Background(), 7)
	if err != nil || batch.ActiveCalls != 0 || batch.ReservedTokens != 0 {
		t.Fatal("invalid profile changed accounting")
	}
}
