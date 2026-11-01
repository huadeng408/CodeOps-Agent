package admission_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/session"
)

func TestGatewayReasoningUsageCountsEveryToken(t *testing.T) {
	for _, mode := range []string{"", "sse"} {
		for _, scenario := range []struct {
			name, usage string
			output      int64
		}{
			{"separate reasoning", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":135,"completion_tokens_details":{"reasoning_tokens":94}}`, 103},
			{"included reasoning", `{"prompt_tokens":32,"completion_tokens":103,"total_tokens":135,"completion_tokens_details":{"reasoning_tokens":94}}`, 103},
			{"relay normalized reasoning", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":41,"completion_tokens_details":{"reasoning_tokens":4}}`, 9},
			{"visible output at cap", `{"prompt_tokens":32,"completion_tokens":64,"total_tokens":97,"completion_tokens_details":{"reasoning_tokens":1}}`, 65},
		} {
			t.Run(mode+"/"+scenario.name, func(t *testing.T) {
				gateway, budget, request := reasoningUsageGateway(t, mode, "grok-4.6", scenario.usage, false)
				response, err := gateway.Invoke(context.Background(), request)
				if err != nil || response.GetErrorCode() != "" || response.InputTokens != 32 || response.OutputTokens != scenario.output || len(response.PayloadJson) == 0 {
					t.Fatalf("reasoning usage omitted or counted twice: code=%s input=%d output=%d err=%v", response.GetErrorCode(), response.GetInputTokens(), response.GetOutputTokens(), err)
				}
				state, err := budget.Inspect(context.Background(), 7)
				if err != nil || state.UsedTokens != 32+scenario.output || state.ReservedTokens != 0 || state.UnknownUsage || state.ActiveCalls != 0 {
					t.Fatal("all attributable reasoning was not settled once through the Ledger")
				}
			})
		}
	}
}

func TestGatewayReasoningKeepsVisibleAndTotalBounds(t *testing.T) {
	for _, mode := range []string{"", "sse"} {
		for _, scenario := range []struct{ name, usage string }{
			{"separate visible bound", `{"prompt_tokens":32,"completion_tokens":65,"total_tokens":98,"completion_tokens_details":{"reasoning_tokens":1}}`},
			{"included visible bound", `{"prompt_tokens":32,"completion_tokens":66,"total_tokens":98,"completion_tokens_details":{"reasoning_tokens":1}}`},
			{"zero reasoning visible bound", `{"prompt_tokens":32,"completion_tokens":65,"total_tokens":97,"completion_tokens_details":{"reasoning_tokens":0}}`},
			{"total bound", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":165,"completion_tokens_details":{"reasoning_tokens":124}}`},
		} {
			t.Run(mode+"/"+scenario.name, func(t *testing.T) {
				gateway, budget, request := reasoningUsageGateway(t, mode, "grok-4.6", scenario.usage, false)
				response, err := gateway.Invoke(context.Background(), request)
				if err != nil || response.GetErrorCode() != "provider_bound_violated" || len(response.PayloadJson) != 0 {
					t.Fatalf("reasoning accounting bypassed the visible or total bound: %s / %v", response.GetErrorCode(), err)
				}
				state, err := budget.Inspect(context.Background(), 7)
				if err != nil || !state.UnknownUsage || state.ReservedTokens != 164 || state.ActiveCalls != 0 {
					t.Fatal("bound violation did not retain its original unknown reservation")
				}
			})
		}
	}
}

func TestGatewayAmbiguousReasoningUsageRetainsReservation(t *testing.T) {
	for _, mode := range []string{"", "sse"} {
		for _, scenario := range []struct {
			name, model, usage string
		}{
			{"inconsistent total", "grok-4.6", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":134,"completion_tokens_details":{"reasoning_tokens":94}}`},
			{"duplicate total", "grok-4.6", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":135,"total_tokens":41,"completion_tokens_details":{"reasoning_tokens":94}}`},
			{"null total", "grok-4.6", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":null}`},
			{"negative total", "grok-4.6", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":-1}`},
			{"fractional total", "grok-4.6", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":41.5}`},
			{"overflow total", "grok-4.6", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":9223372036854775808}`},
			{"missing total with reasoning", "grok-4.6", `{"prompt_tokens":32,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":4}}`},
			{"reasoning exceeds included output", "grok-4.6", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":41,"completion_tokens_details":{"reasoning_tokens":94}}`},
			{"duplicate reasoning", "grok-4.6", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":41,"completion_tokens_details":{"reasoning_tokens":94,"reasoning_tokens":0}}`},
			{"duplicate detail", "grok-4.6", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":41,"completion_tokens_details":{"reasoning_tokens":94},"completion_tokens_details":{"reasoning_tokens":0}}`},
			{"null reasoning", "grok-4.6", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":41,"completion_tokens_details":{"reasoning_tokens":null}}`},
			{"fractional reasoning", "grok-4.6", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":41,"completion_tokens_details":{"reasoning_tokens":0.5}}`},
			{"negative reasoning", "grok-4.6", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":41,"completion_tokens_details":{"reasoning_tokens":-1}}`},
			{"unexpected separate format", "fixture-model", `{"prompt_tokens":32,"completion_tokens":9,"total_tokens":135,"completion_tokens_details":{"reasoning_tokens":94}}`},
		} {
			t.Run(mode+"/"+scenario.name, func(t *testing.T) {
				gateway, budget, request := reasoningUsageGateway(t, mode, scenario.model, scenario.usage, false)
				response, err := gateway.Invoke(context.Background(), request)
				want := "provider_usage_unknown"
				if mode == "sse" {
					want = "provider_response_unknown"
				}
				if err != nil || response.GetErrorCode() != want || len(response.PayloadJson) != 0 {
					t.Fatalf("ambiguous accounting crossed the Gateway: %s / %v", response.GetErrorCode(), err)
				}
				state, err := budget.Inspect(context.Background(), 7)
				if err != nil || !state.UnknownUsage || state.ReservedTokens != 164 || state.UsedTokens != 0 || state.ActiveCalls != 0 {
					t.Fatal("ambiguous accounting released or settled its original reservation")
				}
			})
		}
	}
}

func TestGatewayReasoningToolTokensKeepTheirOwnBudget(t *testing.T) {
	for _, mode := range []string{"", "sse"} {
		for _, scenario := range []struct {
			name, usage string
			output      int64
		}{
			{"separate reasoning", `{"prompt_tokens":32,"completion_tokens":65,"total_tokens":98,"completion_tokens_details":{"reasoning_tokens":1}}`, 66},
			{"included reasoning", `{"prompt_tokens":32,"completion_tokens":66,"total_tokens":98,"completion_tokens_details":{"reasoning_tokens":1}}`, 66},
			{"no reasoning", `{"prompt_tokens":32,"completion_tokens":65,"total_tokens":97,"completion_tokens_details":{"reasoning_tokens":0}}`, 65},
			{"total exceeded", `{"prompt_tokens":32,"completion_tokens":140,"total_tokens":173,"completion_tokens_details":{"reasoning_tokens":1}}`, 141},
		} {
			t.Run(mode+"/"+scenario.name, func(t *testing.T) {
				gateway, budget, request := reasoningUsageGateway(t, mode, "grok-4.6", scenario.usage, true)
				response, err := gateway.Invoke(context.Background(), request)
				state, stateErr := budget.Inspect(context.Background(), 7)
				if scenario.name == "total exceeded" {
					if err != nil || response.GetErrorCode() != "provider_bound_violated" || len(response.PayloadJson) != 0 || stateErr != nil || !state.UnknownUsage || state.ReservedTokens != 164 || state.ActiveCalls != 0 {
						t.Fatal("tool tokens escaped the total budget or its retained reservation")
					}
					return
				}
				if err != nil || response.GetErrorCode() != "" || response.OutputTokens != scenario.output || len(response.PayloadJson) == 0 || stateErr != nil || state.UnknownUsage || state.UsedTokens != 32+scenario.output || state.ReservedTokens != 0 {
					t.Fatalf("tool tokens became visible answer tokens: code=%s output=%d err=%v", response.GetErrorCode(), response.GetOutputTokens(), err)
				}
			})
		}
	}
}

func reasoningUsageGateway(t *testing.T, mode, model, usage string, withTools bool) (*admission.Gateway, *admission.Budget, *pb.ModelCallRequest) {
	t.Helper()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) != 1 {
			t.Error("ambiguous reasoning usage was automatically replayed")
		}
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["reasoning_effort"] != "xhigh" || payload["max_completion_tokens"] != float64(64) || payload["model"] != model || payload["stream"] != (mode == "sse") {
			t.Error("accounting changed the admitted request profile")
		}
		message := `{"content":"ready"}`
		delta := message
		finish := "stop"
		if withTools {
			message = `{"content":"checking","tool_calls":[{"id":"call-1","type":"function","function":{"name":"Read","arguments":"{}"}}]}`
			delta = `{"content":"checking","tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"Read","arguments":"{}"}}]}`
			finish = "tool_calls"
		}
		if mode == "sse" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"model\":%q,\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":%q}],\"usage\":%s}\n\ndata: [DONE]\n\n", model, delta, finish, usage)
			return
		}
		fmt.Fprintf(w, `{"model":%q,"choices":[{"message":%s}],"usage":%s}`, model, message, usage)
	}))
	t.Cleanup(upstream.Close)
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ledger.Close() })
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: model, APIKey: "fixture-only", InputLimit: 100, OutputLimit: 64, TotalLimit: 164, ResponseMode: mode, ReasoningEffort: "xhigh"})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	binding, release, err := gateway.Bind(7, "reasoning-task", "reasoning-session")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return gateway, budget, &pb.ModelCallRequest{Capability: binding.Capability, CallId: "reasoning-call", PayloadJson: []byte(fmt.Sprintf(`{"model":%q,"messages":[]}`, model))}
}
