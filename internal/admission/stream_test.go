package admission_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/session"
)

func TestGatewayStreamDiagnosticContainsOnlyFixedMetadata(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	gateway, _, request := streamingGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":{"message":"fixture-only-key private upstream detail"}}`)
	})
	result, err := gateway.Invoke(context.Background(), request)
	if err != nil || result.GetErrorCode() != "provider_response_unknown" || len(result.GetPayloadJson()) != 0 {
		t.Fatal("fixed diagnostic changed the refusal")
	}
	text := output.String()
	if !strings.Contains(text, "http_status=200 reason=unexpected model stream content type") || strings.Contains(text, "fixture-only-key") || strings.Contains(text, "private upstream detail") {
		t.Fatal("stream diagnostic lost its fixed category or exposed upstream content")
	}
}

func TestGatewaySSEProviderErrorLabelsRemainWhitelisted(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	gateway, budget, request := streamingGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"error":{"type":"invalid_request_error","code":"unsupported_parameter","param":"max_completion_tokens","message":"fixture-only-key private upstream detail"}}`+"\n\n")
	})
	result, err := gateway.Invoke(context.Background(), request)
	if err != nil || result.GetErrorCode() != "provider_response_unknown" || len(result.GetPayloadJson()) != 0 {
		t.Fatal("provider stream error crossed the boundary")
	}
	text := output.String()
	if !strings.Contains(text, "reason=provider_stream_error") || !strings.Contains(text, "type=invalid_request_error code=unsupported_parameter param=max_completion_tokens") || strings.Contains(text, "fixture-only-key") || strings.Contains(text, "private upstream detail") {
		t.Fatal("SSE error diagnostics exposed untrusted content or lost safe labels")
	}
	state, err := budget.Inspect(context.Background(), 7)
	if err != nil || !state.UnknownUsage || state.ReservedTokens != 164 || state.ActiveCalls != 0 {
		t.Fatal("provider error lost retained unknown accounting")
	}
}

func TestGatewayAdmittedSSECompletesToolArgumentsAndAccountsFinalUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid request JSON")
		}
		options, _ := payload["stream_options"].(map[string]any)
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Error("Go did not request the admitted stream content type")
		}
		if payload["stream"] != true || options["include_usage"] != true || payload["reasoning_effort"] != "xhigh" || payload["max_completion_tokens"] != float64(64) {
			t.Error("Go did not apply the admitted streaming, reasoning and output profile")
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		fmt.Fprint(w, ": keepalive\n\n")
		fmt.Fprint(w, `data: {"id":"stream-1","model":"fixture-model","system_fingerprint":"same","choices":[{"index":0,"delta":{"role":"assistant","content":"rea","reasoning_content":"deep ","tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"Read","arguments":"{\"path\":"}}]},"finish_reason":null}]}`+"\n\n")
		w.(http.Flusher).Flush()
		fmt.Fprint(w, `data: {"id":"stream-1","model":"fixture-model","system_fingerprint":"same","choices":[{"index":0,"delta":{"content":"dy fixture-only-key","reasoning_content":"work","tool_calls":[{"index":0,"function":{"arguments":"\"file.txt\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\n")
		fmt.Fprint(w, `data: {"id":"stream-1","model":"fixture-model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":2}}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "fixture-model")
	t.Setenv("OPENAI_BASE_URL", upstream.URL)
	t.Setenv("OPENAI_API_KEY", "fixture-only-key")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "100")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "64")
	t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
	t.Setenv("CODE_AGENT_MODEL_REASONING_EFFORT", "xhigh")
	t.Setenv("CODE_AGENT_MODEL_RESPONSE_MODE", "sse")
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	provider := admission.ProviderFromEnv()
	gateway := admission.NewGateway(budget, provider)
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, "stream-task", "stream-session")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	response, err := gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "stream-call", PayloadJson: []byte(`{"model":"fixture-model","messages":[],"stream":false}`)})
	if err != nil || response.GetErrorCode() != "" {
		t.Fatalf("admitted stream did not complete: %s / %v", response.GetErrorCode(), err)
	}
	var content struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Function struct{ Name, Arguments string }
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(response.PayloadJson, &content) != nil || len(content.Choices) != 1 {
		t.Fatal("stream did not yield one complete JSON response")
	}
	choice := content.Choices[0]
	if choice.Message.Content != "ready <redacted>" || choice.Message.ReasoningContent != "deep work" || choice.FinishReason != "tool_calls" || len(choice.Message.ToolCalls) != 1 {
		t.Fatal("stream content, reasoning or tool completion was lost")
	}
	tool := choice.Message.ToolCalls[0]
	if tool.ID != "call-1" || tool.Function.Name != "Read" || tool.Function.Arguments != `{"path":"file.txt"}` || strings.Contains(string(response.PayloadJson), "fixture-only-key") {
		t.Fatal("stream tool fragments or credential suppression are incorrect")
	}
	if response.InputTokens != 10 || response.OutputTokens != 3 || response.CachedInputTokens != 2 {
		t.Fatal("final-only usage was lost")
	}
	state, err := budget.Inspect(context.Background(), 7)
	if err != nil || state.UsedTokens != 13 || state.ReservedTokens != 0 || state.ActiveCalls != 0 || state.UnknownUsage {
		t.Fatal("complete stream did not settle its canonical reservation")
	}
	profile, _ := json.Marshal(provider.EvaluationProfile())
	if !strings.Contains(string(profile), `"response_mode":"sse"`) {
		t.Fatal("Go did not expose the effective response mode")
	}
}

func TestGatewayIncompleteOrAmbiguousStreamRetainsUnknownWithoutReplay(t *testing.T) {
	start := `data: {"id":"stream-1","model":"fixture-model","choices":[{"index":0,"delta":{"content":"ready"},"finish_reason":"stop"}]}` + "\n\n"
	usage := `data: {"model":"fixture-model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3}}` + "\n\n"
	done := "data: [DONE]\n\n"
	for _, scenario := range []struct {
		name, kind, body string
		status           int
	}{
		{"missing done", "text/event-stream", start + usage, 200},
		{"missing usage", "text/event-stream", start + done, 200},
		{"only intermediate usage", "text/event-stream", `data: {"model":"fixture-model","choices":[{"index":0,"delta":{"content":"first"},"finish_reason":null}],"usage":{"prompt_tokens":10,"completion_tokens":1}}` + "\n\n" + start + done, 200},
		{"early done", "text/event-stream", done, 200},
		{"mixed model", "text/event-stream", start + `data: {"model":"other-model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3}}` + "\n\n" + done, 200},
		{"duplicate accounting", "text/event-stream", start + `data: {"model":"fixture-model","choices":[],"usage":{"prompt_tokens":80,"completion_tokens":3},"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\n" + done, 200},
		{"case alias accounting", "text/event-stream", start + `data: {"model":"fixture-model","choices":[],"usage":{"prompt_tokens":80,"completion_tokens":3},"Usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\n" + done, 200},
		{"case alias choice", "text/event-stream", `data: {"model":"fixture-model","choices":[{"index":0,"delta":{"content":"safe"},"Delta":{"content":"other"},"finish_reason":"stop"}]}` + "\n\n" + usage + done, 200},
		{"case alias tool arguments", "text/event-stream", `data: {"model":"fixture-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"Read","arguments":"{}","Arguments":"{\"path\":\"other\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" + usage + done, 200},
		{"decreasing usage", "text/event-stream", start + usage + `data: {"model":"fixture-model","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\n" + done, 200},
		{"unexpected JSON", "application/json", `{"model":"fixture-model","usage":{"prompt_tokens":10,"completion_tokens":3}}`, 200},
		{"upstream 503", "application/json", `{"error":{"type":"server_error","message":"fixture-only-key"}}`, 503},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var calls atomic.Int32
			gateway, budget, request := streamingGateway(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", scenario.kind)
				w.WriteHeader(scenario.status)
				fmt.Fprint(w, scenario.body)
			})
			result, err := gateway.Invoke(context.Background(), request)
			if err != nil || result.GetErrorCode() == "" || len(result.GetPayloadJson()) != 0 || calls.Load() != 1 {
				t.Fatalf("ambiguous stream escaped or was replayed: %s / calls=%d / %v", result.GetErrorCode(), calls.Load(), err)
			}
			state, err := budget.Inspect(context.Background(), 7)
			if err != nil || !state.UnknownUsage || state.ReservedTokens != 164 || state.ActiveCalls != 0 || state.UsedTokens != 0 {
				t.Fatal("ambiguous stream changed retained unknown accounting")
			}
		})
	}
}

func streamingGateway(t *testing.T, handler http.HandlerFunc) (*admission.Gateway, *admission.Budget, *pb.ModelCallRequest) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ledger.Close() })
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-only-key", InputLimit: 100, OutputLimit: 64, ResponseMode: "sse"})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	binding, release, err := gateway.Bind(7, "stream-task", "stream-session")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return gateway, budget, &pb.ModelCallRequest{Capability: binding.Capability, CallId: "stream-call", PayloadJson: []byte(`{"model":"fixture-model","messages":[]}`)}
}

func TestGatewaySSEAcceptsKeepalivesAndMultilineCRLF(t *testing.T) {
	gateway, _, request := streamingGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "\ufeff: ping\r\n\r\ndata:\r\n\r\n")
		fmt.Fprint(w, `data: {"model":"fixture-model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":1}}`+"\r\n\r\n")
		fmt.Fprint(w, "data: {\"model\":\"fixture-model\",\r\n")
		fmt.Fprint(w, `data: "choices":[{"index":0,"delta":{"content":"ready"},"finish_reason":"stop"}]}`+"\r\n\r\n")
		fmt.Fprint(w, `data: {"model":"fixture-model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3}}`+"\r\n\r\n")
		fmt.Fprint(w, "data: [DONE]\r\n")
	})
	response, err := gateway.Invoke(context.Background(), request)
	if err != nil || response.GetErrorCode() != "" || !strings.Contains(string(response.PayloadJson), `"content":"ready"`) {
		t.Fatalf("valid SSE framing failed: %s / %v", response.GetErrorCode(), err)
	}
}

func TestGatewaySSECancellationNeverReleasesPartialToolArguments(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	gateway, budget, request := streamingGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"model":"fixture-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"pending-tool","type":"function","function":{"name":"Write","arguments":"{\"path\":"}}]}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(stopped)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan *pb.ModelCallResponse, 1)
	go func() { response, _ := gateway.Invoke(ctx, request); finished <- response }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not start")
	}
	cancel()
	select {
	case response := <-finished:
		if response.GetErrorCode() == "" || len(response.GetPayloadJson()) != 0 {
			t.Fatal("cancelled stream released a tool call")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled stream remained active")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream stream was not cancelled")
	}
	state, err := budget.Inspect(context.Background(), 7)
	if err != nil || state.ActiveCalls != 0 || !state.UnknownUsage || state.ReservedTokens != 164 || state.UsedTokens != 0 {
		t.Fatal("cancellation lost its retained reservation")
	}
}

func TestGatewaySSEBoundsAssembledContentAcrossEvents(t *testing.T) {
	gateway, budget, request := streamingGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		part := strings.Repeat("a", (8<<20)+1)
		for i := 0; i < 2; i++ {
			data, _ := json.Marshal(map[string]any{"model": "fixture-model", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": part}}}})
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
	})
	response, err := gateway.Invoke(context.Background(), request)
	if err != nil || response.GetErrorCode() == "" || len(response.GetPayloadJson()) != 0 {
		t.Fatal("oversized completion escaped")
	}
	state, err := budget.Inspect(context.Background(), 7)
	if err != nil || !state.UnknownUsage || state.ReservedTokens != 164 || state.ActiveCalls != 0 {
		t.Fatal("oversized stream lost accounting")
	}
}

func TestGatewaySSEKeepsExactEvaluationIdentityAndConfirmedUsage(t *testing.T) {
	for _, reported := range []string{"fixture-model", "other-model"} {
		t.Run(reported, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ready\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n", reported)
			}))
			defer upstream.Close()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			provider := admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-only-key", InputLimit: 100, OutputLimit: 64, ResponseMode: "sse"}
			view, err := session.NewWorkbench(ledger, nil).Create(context.Background(), 7, "fixture", "identity", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := session.BindEvaluation(context.Background(), ledger, view.ID, session.EvaluationBinding{SchemaVersion: 2, RunID: "sse-identity", TaskID: "sse-identity", MaxTokens: 1000, WallClockSeconds: 30, Policy: session.EvaluationPolicy{ContextStrategy: "metadata_on_demand", ExecutionStrategy: "single", MaxToolRounds: 4, ContextMaxBytes: 12000}, Runtime: session.EvaluationRuntime{ModelProfile: provider.EvaluationProfile()}}); err != nil {
				t.Fatal(err)
			}
			budget := admission.NewBudget(ledger)
			gateway := admission.NewGateway(budget, provider)
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, view.ID, view.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			result, err := gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "sse-identity-call", PayloadJson: []byte(`{"model":"fixture-model","messages":[]}`)})
			if err != nil || (result.GetErrorCode() == "") != (reported == "fixture-model") {
				t.Fatal("stream changed the exact model-identity decision")
			}
			if reported != "fixture-model" && len(result.GetPayloadJson()) != 0 {
				t.Fatal("substituted model response crossed the boundary")
			}
			state, err := budget.Inspect(context.Background(), 7)
			if err != nil || state.UsedTokens != 13 || state.ReservedTokens != 0 || state.UnknownUsage {
				t.Fatal("model refusal erased confirmed stream usage")
			}
		})
	}
}

func TestGatewayRefusesInvalidResponseModeBeforeReservation(t *testing.T) {
	for _, scenario := range []struct{ protocol, mode string }{{"openai", "unknown"}, {"anthropic", "sse"}} {
		t.Run(scenario.protocol+"-"+scenario.mode, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			defer upstream.Close()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			budget := admission.NewBudget(ledger)
			gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: scenario.protocol, ResponseMode: scenario.mode, BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-only-key", InputLimit: 100, OutputLimit: 64})
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, "invalid-mode", "invalid-mode")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			result, err := gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "invalid-mode-call", PayloadJson: []byte(`{"model":"fixture-model","messages":[]}`)})
			if err != nil || result.GetErrorCode() != "model_prerequisites_missing" || calls.Load() != 0 {
				t.Fatal("unadmitted transport reached the provider")
			}
			state, err := budget.Inspect(context.Background(), 7)
			if err != nil || state.ReservedTokens != 0 || state.ActiveCalls != 0 {
				t.Fatal("invalid transport reserved tokens")
			}
		})
	}
}
