package admission_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestGatewayRefusesUnadmittedTaskBeforeReservationOrHTTP(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":8,"completion_tokens":3}}`))
	}))
	defer upstream.Close()
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
		Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
	runtime := sandbox.NewOpenSandboxRunner(sandbox.Config{TrustRoot: t.TempDir()})
	gateway.SetTaskAdmission(func(ctx context.Context, _ string) error { return runtime.Ready(ctx) })
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, "task-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	response, err := gateway.Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability, CallId: "call-a",
		PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}]}`)})
	if err != nil || response.GetErrorCode() != "task_prerequisites_missing" || calls.Load() != 0 {
		t.Fatalf("unadmitted task reached paid transport: %s / calls=%d / %v", response.GetErrorCode(), calls.Load(), err)
	}
	batch, err := budget.Inspect(ctx, 7)
	if err != nil || batch.ID != "" || batch.UsedTokens != 0 || batch.ReservedTokens != 0 {
		t.Fatalf("runtime refusal created billing facts: %+v %v", batch, err)
	}
	legacy := session.NewSQLiteEventStore(ledger.Path)
	defer legacy.Close()
	if listed, err := legacy.List(ctx); err != nil || len(listed) != 0 {
		t.Fatalf("a pre-batch model scope became a legacy conversation: %v", err)
	}
	runner := session.NewSessionRunner(session.NewWorkbench(ledger, nil), nil, nil, session.SessionRunnerOptions{})
	defer runner.Close()
	if err := runner.Recover(ctx); err != nil {
		t.Fatalf("a pre-batch model scope poisoned recovery: %v", err)
	}
}

func TestGatewayReservesBeforeUpstreamAndCountsUsageOnce(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batch, err := budget.Open(r.Context(), 7)
		if err != nil || batch.ReservedTokens != 103 || batch.UsedTokens != 0 {
			t.Error("upstream ran before a durable reservation")
		}
		if r.Header.Get("Authorization") != "Bearer fixture-provider-authority" {
			t.Error("Go did not own the provider credential")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["max_completion_tokens"] != float64(3) || payload["max_tokens"] != nil || payload["stream"] == true {
			t.Error("model request bypassed the Go output bound")
		}
		w.Header().Set("Content-Type", "application/json")
		// Repeated metadata and counters from another protocol do not affect OpenAI usage.
		_, _ = w.Write([]byte(`{"id":"first","id":"last","model":"fixture-model","choices":[{"message":{"content":"\u0066ixture-provider-authority"}}],"usage":{"prompt_tokens":8,"completion_tokens":3,"input_tokens":9,"input_tokens":0,"prompt_tokens_details":{"cached_tokens":4,"fixture":"first","fixture":"last"},"completion_tokens_details":{"reasoning_tokens":2}}}`))
	}))
	defer upstream.Close()
	gateway := admission.NewGateway(budget, admission.ProviderConfig{
		Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-provider-authority",
		InputLimit: 100, OutputLimit: 3,
	})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, "task-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	conn, err := grpc.NewClient(binding.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := pb.NewModelGatewayClient(conn)
	response, err := client.Invoke(ctx, &pb.ModelCallRequest{
		Capability: binding.Capability, CallId: "call-a", Purpose: "foreground",
		PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}],"max_tokens":999,"stream":true}`),
	})
	if err != nil || response.GetErrorCode() != "" {
		t.Fatalf("admitted model call failed: %v / %s", err, response.GetErrorCode())
	}
	if response.InputTokens != 8 || response.OutputTokens != 3 || response.CachedInputTokens != 4 || response.CostStatus != "unknown" {
		t.Fatal("cache/reasoning counted twice or unknown price reported as zero")
	}
	var content struct {
		Choices []struct{ Message struct{ Content string } }
	}
	if json.Unmarshal(response.PayloadJson, &content) != nil || content.Choices[0].Message.Content != "<redacted>" {
		t.Fatal("an escaped provider credential crossed into Python")
	}
	batch, err := budget.Open(ctx, 7)
	if err != nil || batch.UsedTokens != 11 || batch.ReservedTokens != 0 || batch.CostStatus != "unknown" {
		t.Fatal("usage was not settled through the canonical Ledger")
	}
}

func TestGatewayRejectsAmbiguousProviderUsage(t *testing.T) {
	for _, scenario := range []struct {
		name, protocol, response, code string
		status                         int
	}{
		{"duplicate usage", "openai", `{"usage":{"prompt_tokens":8,"completion_tokens":3},"usage":{"prompt_tokens":0,"completion_tokens":0}}`, "provider_usage_unknown", http.StatusOK},
		{"duplicate input", "openai", `{"usage":{"prompt_tokens":8,"prompt_tokens":0,"completion_tokens":3}}`, "provider_usage_unknown", http.StatusOK},
		{"escaped duplicate output", "openai", `{"usage":{"prompt_tokens":8,"completion_tokens":3,"completion\u005ftokens":0}}`, "provider_usage_unknown", http.StatusOK},
		{"duplicate cache detail", "openai", `{"usage":{"prompt_tokens":8,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":4,"cached_tokens":0}}}`, "provider_usage_unknown", http.StatusOK},
		{"rate limited duplicate usage", "openai", `{"usage":{"prompt_tokens":8,"completion_tokens":0},"usage":{"prompt_tokens":0,"completion_tokens":0}}`, "provider_rate_limited_unknown", http.StatusTooManyRequests},
		{"transient duplicate output", "openai", `{"usage":{"prompt_tokens":8,"completion_tokens":3,"completion_tokens":0}}`, "provider_transient_unknown", http.StatusServiceUnavailable},
		{"anthropic duplicate input", "anthropic", `{"usage":{"input_tokens":8,"input_tokens":0,"output_tokens":3}}`, "provider_usage_unknown", http.StatusOK},
		{"anthropic duplicate cache read", "anthropic", `{"usage":{"input_tokens":2,"output_tokens":3,"cache_read_input_tokens":5,"cache_read_input_tokens":0}}`, "provider_usage_unknown", http.StatusOK},
		{"anthropic duplicate cache creation", "anthropic", `{"usage":{"input_tokens":2,"output_tokens":3,"cache_creation_input_tokens":5,"cache_creation_input_tokens":0}}`, "provider_usage_unknown", http.StatusOK},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			budget := admission.NewBudget(ledger)
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) > 1 {
					if scenario.protocol == "anthropic" {
						fmt.Fprint(w, `{"usage":{"input_tokens":8,"output_tokens":3}}`)
					} else {
						fmt.Fprint(w, `{"usage":{"prompt_tokens":8,"completion_tokens":3}}`)
					}
					return
				}
				w.WriteHeader(scenario.status)
				fmt.Fprint(w, scenario.response)
			}))
			defer upstream.Close()
			gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: scenario.protocol, BaseURL: upstream.URL,
				Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, "ambiguous-task", "ambiguous-session")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			conn, err := grpc.NewClient(binding.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			client := pb.NewModelGatewayClient(conn)
			request := &pb.ModelCallRequest{Capability: binding.Capability, CallId: "ambiguous-call",
				PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}]}`)}
			response, err := client.Invoke(ctx, request)
			if err != nil || response.GetErrorCode() != scenario.code || response.GetHttpStatus() != int32(scenario.status) {
				t.Fatalf("ambiguous provider usage was accepted: code=%s err=%v", response.GetErrorCode(), err)
			}
			batch, err := budget.Inspect(ctx, 7)
			if err != nil || batch.UsedTokens != 0 || batch.ReservedTokens != 103 || !batch.UnknownUsage {
				t.Fatalf("ambiguous usage released or settled its reservation: %+v err=%v", batch, err)
			}
			request.CallId = "following-call"
			response, err = client.Invoke(ctx, request)
			if err != nil || response.GetErrorCode() != "" || calls.Load() != 2 {
				t.Fatalf("terminal unknown usage blocked a distinct provider request: code=%s calls=%d err=%v", response.GetErrorCode(), calls.Load(), err)
			}
			batch, err = budget.Inspect(ctx, 7)
			if err != nil || batch.UsedTokens != 11 || batch.ReservedTokens != 103 || !batch.UnknownUsage || batch.ActiveCalls != 0 {
				t.Fatalf("independent success erased or fabricated the unknown usage: %+v err=%v", batch, err)
			}
		})
	}
}

func TestGatewayRetriesOnlyAfterErrorUsageSettles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		batch, err := budget.Inspect(r.Context(), 7)
		if err != nil || batch.ReservedTokens != 103 || batch.CostStatus != "unknown" {
			t.Error("retry reached upstream without its own retained reservation")
		}
		if attempt == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":"temporary","usage":{"prompt_tokens":8,"completion_tokens":0}}`)
			return
		}
		if batch.UsedTokens != 8 {
			t.Error("retry dispatched before the original attributable usage settled")
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"recovered"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	}))
	defer upstream.Close()
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
		Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, "retry-task", "retry-session")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	conn, err := grpc.NewClient(binding.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	response, err := pb.NewModelGatewayClient(conn).Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability,
		CallId: "original-attempt", PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}]}`)})
	if err != nil || response.GetErrorCode() != "" || attempts.Load() != 2 {
		t.Fatalf("known error usage could not recover through bounded retry: calls=%d code=%s err=%v", attempts.Load(), response.GetErrorCode(), err)
	}
	if response.InputTokens != 11 || response.OutputTokens != 2 || response.CostStatus != "unknown" {
		t.Fatal("the logical invocation omitted its settled failed attempt")
	}
	batch, err := budget.Inspect(ctx, 7)
	if err != nil || batch.UsedTokens != 13 || batch.ReservedTokens != 0 || batch.UnknownUsage || batch.CostStatus != "unknown" {
		t.Fatal("retry reset, skipped or double-counted attributable usage")
	}
}

func TestGatewayRetainsRetryBoundaries(t *testing.T) {
	for _, scenario := range []struct {
		name             string
		retryAfter       []string
		code             string
		status, attempts int
		used, reserved   int64
		unknown          bool
	}{
		{"exhausted 429", nil, "provider_rate_limited", 429, 3, 24, 0, false},
		{"exhausted 503", nil, "provider_transient", 503, 3, 24, 0, false},
		{"authentication", nil, "provider_authentication", 401, 1, 8, 0, false},
		{"unsupported HTTP status", nil, "provider_http_unknown", 600, 1, 0, 103, true},
		{"excessive delay", []string{"11"}, "provider_rate_limited", 429, 1, 8, 0, false},
		{"malformed delay", []string{"invalid"}, "provider_rate_limited", 429, 1, 8, 0, false},
		{"empty delay", []string{""}, "provider_rate_limited", 429, 1, 8, 0, false},
		{"signed delay", []string{"+1"}, "provider_rate_limited", 429, 1, 8, 0, false},
		{"conflicting repeated delay", []string{"0", "11"}, "provider_rate_limited", 429, 1, 8, 0, false},
		{"repeated date delay", []string{"Thu, 01 Jan 1970 00:00:00 GMT", "11"}, "provider_transient", 503, 1, 8, 0, false},
		{"split date delay", []string{"Thu", "01 Jan 1970 00:00:00 GMT"}, "provider_transient", 503, 1, 8, 0, false},
		{"repeated identical delay", []string{"0", "0"}, "provider_rate_limited", 429, 1, 8, 0, false},
		{"single zero delay", []string{"0"}, "provider_rate_limited", 429, 3, 24, 0, false},
		{"single date delay", []string{"Thu, 01 Jan 1970 00:00:00 GMT"}, "provider_transient", 503, 3, 24, 0, false},
		{"unknown retry usage", nil, "provider_rate_limited_unknown", 429, 2, 8, 103, true},
		{"retry output violation", nil, "provider_bound_violated", 429, 2, 8, 103, true},
		{"revoked binding", nil, "model_gateway_unavailable", 429, 1, 8, 0, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "ledger.sqlite")
			ledger, err := session.OpenSQLiteEventLog(path)
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			budget := admission.NewBudget(ledger)
			var attempts atomic.Int32
			releaseHolder := make(chan func(), 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempt := attempts.Add(1)
				if scenario.name == "revoked binding" {
					(<-releaseHolder)()
				}
				for _, value := range scenario.retryAfter {
					w.Header().Add("Retry-After", value)
				}
				w.WriteHeader(scenario.status)
				if scenario.name == "unknown retry usage" && attempt == 2 {
					fmt.Fprint(w, `{"error":"fixture-only"}`)
					return
				}
				output := 0
				if scenario.name == "retry output violation" && attempt == 2 {
					output = 4
				}
				fmt.Fprintf(w, `{"usage":{"prompt_tokens":8,"completion_tokens":%d}}`, output)
			}))
			defer upstream.Close()
			gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
				Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, "retry-task", "retry-session")
			if err != nil {
				t.Fatal(err)
			}
			releaseHolder <- release
			request := &pb.ModelCallRequest{Capability: binding.Capability, CallId: "original-attempt",
				PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}]}`)}
			response, err := gateway.Invoke(ctx, request)
			if err != nil || response.GetErrorCode() != scenario.code || int(attempts.Load()) != scenario.attempts {
				t.Fatalf("retry boundary changed: code=%s attempts=%d err=%v", response.GetErrorCode(), attempts.Load(), err)
			}
			before, err := budget.Inspect(ctx, 7)
			if err != nil || before.UsedTokens != scenario.used || before.ReservedTokens != scenario.reserved || before.UnknownUsage != scenario.unknown {
				t.Fatal("retry lost complete usage or reservation accounting")
			}
			gateway.Close()
			if err := ledger.Close(); err != nil {
				t.Fatal(err)
			}
			ledger, err = session.OpenSQLiteEventLog(path)
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			after, err := admission.NewBudget(ledger).Inspect(ctx, 7)
			if err != nil || before != after {
				t.Fatal("retry facts did not survive real Ledger reopen")
			}
		})
	}
}

func TestGatewayCancelBackoffRetainsOnlySettledAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"usage":{"prompt_tokens":8,"completion_tokens":0}}`)
	}))
	defer upstream.Close()
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
		Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, "retry-task", "retry-session")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	result := make(chan *pb.ModelCallResponse, 1)
	go func() {
		response, _ := gateway.Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability, CallId: "original-attempt",
			PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}]}`)})
		result <- response
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("the initial attributed attempt did not settle")
		case <-ticker.C:
			batch, err := budget.Inspect(ctx, 7)
			if err != nil || batch.UsedTokens != 8 || batch.ReservedTokens != 0 {
				continue
			}
			cancel()
			select {
			case response := <-result:
				if response.GetErrorCode() != "provider_retry_canceled" || attempts.Load() != 1 {
					t.Fatal("canceling backoff dispatched another attempt")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("backoff did not stop after cancellation")
			}
			retained, err := budget.Inspect(context.Background(), 7)
			if err != nil || retained != batch || retained.UnknownUsage {
				t.Fatal("canceling an undispatched retry invented unknown usage or cleared accounting")
			}
			return
		}
	}
}

func TestGatewayHonorsBoundedRetryAfter(t *testing.T) {
	for _, format := range []string{"seconds", "HTTP date"} {
		t.Run(format, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			budget := admission.NewBudget(ledger)
			var attempts atomic.Int32
			allowed := make(chan time.Time, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if attempts.Add(1) == 1 {
					next := time.Now().Add(2 * time.Second)
					header := "2"
					if format == "HTTP date" {
						next = time.Now().Add(3 * time.Second).UTC().Truncate(time.Second)
						header = next.Format(http.TimeFormat)
					}
					allowed <- next
					w.Header().Set("Retry-After", header)
					w.WriteHeader(http.StatusServiceUnavailable)
					fmt.Fprint(w, `{"usage":{"prompt_tokens":8,"completion_tokens":0}}`)
					return
				}
				if time.Now().Before(<-allowed) {
					t.Error("upstream retry preceded its declared Retry-After")
				}
				fmt.Fprint(w, `{"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
			}))
			defer upstream.Close()
			gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
				Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, "retry-task", "retry-session")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			response, err := gateway.Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability, CallId: "original-attempt",
				PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}]}`)})
			if err != nil || response.GetErrorCode() != "" || attempts.Load() != 2 {
				t.Fatalf("bounded Retry-After failed: %s %v", response.GetErrorCode(), err)
			}
		})
	}
}

func TestGatewayRejectsContradictoryRetryLedgerFacts(t *testing.T) {
	for _, scenario := range []string{"missing parent", "foreign owner", "foreign task", "changed bound", "changed purpose", "unsettled parent", "nontransient parent", "branched retry", "fourth attempt"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			budget := admission.NewBudget(ledger)
			var attempts atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.Header().Set("Retry-After", "11")
				status := http.StatusTooManyRequests
				if scenario == "nontransient parent" {
					status = http.StatusUnauthorized
				}
				w.WriteHeader(status)
				if scenario == "unsettled parent" {
					fmt.Fprint(w, `{"error":"fixture-only"}`)
					return
				}
				fmt.Fprint(w, `{"usage":{"prompt_tokens":8,"completion_tokens":0}}`)
			}))
			defer upstream.Close()
			gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
				Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, _, err := gateway.Bind(7, "retry-task", "retry-session")
			if err != nil {
				t.Fatal(err)
			}
			request := &pb.ModelCallRequest{Capability: binding.Capability, CallId: "original-attempt",
				PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}]}`)}
			if _, err := gateway.Invoke(ctx, request); err != nil || attempts.Load() != 1 {
				t.Fatal("fixture did not retain its first attempt")
			}
			appendFact := func(kind string, fact map[string]any) {
				t.Helper()
				snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, session.VerificationAdmissionLedgerID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ledger.Append(ctx, session.VerificationAdmissionLedgerID, int64(len(snapshot.Events)), kind, fact); err != nil {
					t.Fatal(err)
				}
			}
			reserved := map[string]any{"owner_id": 7, "task_id": "retry-task", "call_id": "contradictory-retry",
				"tokens": 103, "purpose": "foreground", "retry_of": "original-attempt"}
			switch scenario {
			case "missing parent":
				reserved["retry_of"] = "missing-call"
			case "foreign owner":
				reserved["owner_id"] = 8
			case "foreign task":
				reserved["task_id"] = "different-task"
			case "changed bound":
				reserved["tokens"] = 102
			case "changed purpose":
				reserved["purpose"] = "compaction"
			case "branched retry", "fourth attempt":
				parent := "original-attempt"
				last := 2
				if scenario == "fourth attempt" {
					last = 3
				}
				for index := 2; index <= last; index++ {
					id := fmt.Sprintf("fixture-retry-%d", index)
					appendFact("admission/call_reserved", map[string]any{"owner_id": 7, "task_id": "retry-task", "call_id": id,
						"tokens": 103, "purpose": "foreground", "retry_of": parent})
					appendFact("admission/call_settled", map[string]any{"owner_id": 7, "call_id": id, "tokens": 8,
						"input_tokens": 8, "output_tokens": 0, "cost_status": "unknown", "http_status": 429})
					parent = id
				}
				if scenario == "fourth attempt" {
					reserved["retry_of"] = parent
				}
			}
			appendFact("admission/call_reserved", reserved)
			if _, err := budget.Inspect(ctx, 7); !errors.Is(err, session.ErrEventIntegrity) {
				t.Fatalf("contradictory retry was accepted: %v", err)
			}
			before, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
			if err != nil {
				t.Fatal(err)
			}
			request.CallId = "next-attempt"
			result, err := gateway.Invoke(ctx, request)
			if err != nil || result.GetErrorCode() != "token_admission_unavailable" || attempts.Load() != 1 {
				t.Fatal("contradictory history dispatched another model attempt")
			}
			after, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
			if err != nil || len(after) != len(before) || after[len(after)-1].Checksum != before[len(before)-1].Checksum {
				t.Fatal("contradictory retry history was rewritten")
			}
		})
	}
}

func TestGatewayUnsettledReservationFencesNewProcess(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admission.NewBudget(ledger).Reserve(ctx, 7, "task-a", "lost-call", 103); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	ledger, err = session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":8,"completion_tokens":3}}`))
	}))
	defer upstream.Close()
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
		Model: "fixture-model", APIKey: "fixture-provider-authority", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, "task-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	response, err := gateway.Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability, CallId: "new-call",
		PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}]}`)})
	if err != nil || response.GetErrorCode() != "model_concurrency_exhausted" || calls.Load() != 0 {
		t.Fatal("a restarted process dispatched past an unreconciled reservation")
	}
	batch, err := budget.Open(ctx, 7)
	if err != nil || batch.ReservedTokens != 103 || batch.UsedTokens != 0 {
		t.Fatal("restart changed the persisted reservation")
	}
}

func TestGatewayRefusesMissingAuthorityAndRetainsMissingUsage(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			fmt.Fprint(w, `{"usage":{"prompt_tokens":8,"completion_tokens":3}}`)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"missing usage"}}]}`))
	}))
	defer upstream.Close()
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
		Model: "fixture-model", APIKey: "fixture-provider-authority", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, "task-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	conn, err := grpc.NewClient(binding.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := pb.NewModelGatewayClient(conn)
	request := &pb.ModelCallRequest{CallId: "call-a", PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}]}`)}
	if _, err := client.Invoke(ctx, request); status.Code(err) != codes.Unauthenticated {
		t.Fatal("missing authority admitted")
	}
	if calls.Load() != 0 {
		t.Fatal("unauthenticated request reached upstream")
	}
	request.Capability = binding.Capability
	result, err := client.Invoke(ctx, request)
	if err != nil || result.GetErrorCode() != "provider_usage_unknown" {
		t.Fatal("missing usage did not block")
	}
	batch, err := budget.Open(ctx, 7)
	if err != nil || !batch.UnknownUsage || batch.ReservedTokens != 103 || batch.UsedTokens != 0 {
		t.Fatal("missing usage released its reservation")
	}
	request.CallId = "call-b"
	result, err = client.Invoke(ctx, request)
	if err != nil || result.GetErrorCode() != "" || calls.Load() != 2 {
		t.Fatal("terminal unknown usage blocked another upstream call")
	}
	batch, err = budget.Inspect(ctx, 7)
	if err != nil || batch.UsedTokens != 11 || !batch.UnknownUsage || batch.ReservedTokens != 103 {
		t.Fatal("independent call erased the unknown attempt or fabricated its usage")
	}
	release()
	if _, err := client.Invoke(ctx, request); status.Code(err) != codes.Unauthenticated {
		t.Fatal("released authority still usable")
	}
}

func TestGatewayCountsAnthropicCacheCreationAndReadOnce(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "fixture-provider-authority" {
			t.Error("missing Go-owned credential")
		}
		// OpenAI counters are not part of the Anthropic accounting contract.
		_, _ = w.Write([]byte(`{"model":"fixture-model","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":8,"output_tokens":3,"prompt_tokens":9,"prompt_tokens":0,"cache_read_input_tokens":4,"cache_creation_input_tokens":5}}`))
	}))
	defer upstream.Close()
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "anthropic", BaseURL: upstream.URL,
		Model: "fixture-model", APIKey: "fixture-provider-authority", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, "task-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	conn, err := grpc.NewClient(binding.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	response, err := pb.NewModelGatewayClient(conn).Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability,
		CallId: "call-a", Purpose: "memory_reflection", PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}]}`)})
	if err != nil || response.GetErrorCode() != "" {
		t.Fatalf("Anthropic accounting failed: %v / %s", err, response.GetErrorCode())
	}
	if response.InputTokens != 17 || response.OutputTokens != 3 || response.CachedInputTokens != 4 {
		t.Fatal("Anthropic cache omitted or counted twice")
	}
	batch, err := budget.Open(ctx, 7)
	if err != nil || batch.UsedTokens != 20 || batch.ReservedTokens != 0 {
		t.Fatal("Anthropic usage not persisted once")
	}
}

func TestGatewayRefusesProviderHostedToolsBeforeReservation(t *testing.T) {
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-provider-authority", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, "task-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	conn, err := grpc.NewClient(binding.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = pb.NewModelGatewayClient(conn).Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "call-a",
		PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"code_interpreter"}]}`)})
	if status.Code(err) != codes.InvalidArgument || calls.Load() != 0 {
		t.Fatal("provider-hosted side effects escaped Harness tools")
	}
	batch, err := budget.Inspect(context.Background(), 7)
	if err != nil || batch.ID != "" || batch.UsedTokens != 0 || batch.ReservedTokens != 0 {
		t.Fatal("invalid payload reserved funds")
	}
}

func TestGatewayBlocksUnconfirmedAttempts(t *testing.T) {
	for _, scenario := range []string{"missing_bound", "rate_limit", "canceled", "bound_violation", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			budget := admission.NewBudget(ledger)
			started := make(chan struct{})
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if scenario == "canceled" {
					_, _ = io.Copy(io.Discard, r.Body)
					close(started)
					<-r.Context().Done()
					return
				}
				if scenario == "rate_limit" {
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(`{"error":"fixture-provider-authority"}`))
					return
				}
				output := 3
				if scenario == "bound_violation" {
					output = 4 // Total is below the reservation, but output exceeds its bound.
				}
				_, _ = fmt.Fprintf(w, `{"usage":{"prompt_tokens":8,"completion_tokens":%d}}`, output)
			}))
			defer upstream.Close()
			config := admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model",
				APIKey: "fixture-provider-authority", InputLimit: 100, OutputLimit: 3}
			if scenario == "missing_bound" {
				config.InputLimit = 0
			}
			gateway := admission.NewGateway(budget, config)
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, "task-a", "session-a")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			request := &pb.ModelCallRequest{Capability: binding.Capability, CallId: "call-a",
				PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}]}`)}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if scenario == "canceled" {
				go func() {
					select {
					case <-started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			response, err := gateway.Invoke(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"missing_bound": "model_prerequisites_missing", "rate_limit": "provider_rate_limited_unknown",
				"canceled": "provider_transport_unknown", "bound_violation": "provider_bound_violated", "duplicate": ""}[scenario]
			if response.ErrorCode != want {
				t.Fatalf("classification %s, want %s", response.ErrorCode, want)
			}
			if scenario == "missing_bound" {
				if calls.Load() != 0 {
					t.Fatal("missing bound reached the provider")
				}
				return
			}
			batch, err := budget.Open(context.Background(), 7)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "duplicate" {
				response, err = gateway.Invoke(context.Background(), request)
				if err != nil || response.ErrorCode != "model_call_already_reserved" || calls.Load() != 1 {
					t.Fatal("duplicate call dispatched twice")
				}
			} else if !batch.UnknownUsage || batch.ReservedTokens != 103 || batch.UsedTokens != 0 || calls.Load() != 1 {
				t.Fatal("unknown attempt was released or silently retried")
			}
			if scenario == "bound_violation" {
				if _, err := budget.Settle(context.Background(), 7, "call-a", 12); !errors.Is(err, admission.ErrUsageUnknown) {
					t.Fatal("a smaller settlement erased an observed per-direction violation")
				}
			}
		})
	}
}

func TestGatewayOperatorFinishesIdleTaskWithoutResettingAllowance(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	workbench := session.NewWorkbench(ledger, nil)
	first, err := workbench.CreateWithWorkingDir(ctx, 7, "fixture", "first", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	budget := admission.NewBudget(ledger)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":8,"completion_tokens":3}}`))
	}))
	defer upstream.Close()
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
		Model: "fixture-model", APIKey: "fixture-provider-authority", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.BindActor(actor, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	call := func(binding *pb.ModelGatewayBinding, id string) *pb.ModelCallResponse {
		t.Helper()
		result, err := gateway.Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability, CallId: id,
			PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}]}`)})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := call(binding, "first-call"); result.ErrorCode != "" {
		t.Fatal(result.ErrorCode)
	}
	if err := gateway.FinishTask(ctx, actor); !errors.Is(err, admission.ErrTaskBusy) {
		t.Fatal("a live model scope was finished")
	}
	release()
	if err := gateway.FinishTask(ctx, actor); err != nil {
		t.Fatal(err)
	}
	_, delayedRelease, err := gateway.BindActor(actor, first.ID)
	if delayedRelease != nil {
		delayedRelease()
	}
	if !errors.Is(err, admission.ErrTaskClosed) {
		t.Fatal("finished root issued a new model or tool capability")
	}
	before, err := budget.Inspect(ctx, 7)
	if err != nil || before.UsedTokens != 11 || before.TaskID != "" {
		t.Fatal("completion changed consumption")
	}
	second, err := workbench.CreateWithWorkingDir(ctx, 7, "fixture", "second", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	actor.SessionID = ""
	actor, err = actor.BindSession(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding, release, err = gateway.BindActor(actor, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if result := call(binding, "second-call"); result.ErrorCode != "" {
		t.Fatal(result.ErrorCode)
	}
	after, err := budget.Inspect(ctx, 7)
	if err != nil || after.ID != before.ID || after.UsedTokens != 22 {
		t.Fatal("the second task reset or double-counted the batch")
	}
}

func TestGatewayTaskFinishRequiresKnownToolStop(t *testing.T) {
	for _, scenario := range []string{"missing result", "unknown stop", "confirmed stop"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			created, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "fixture", "finish", "")
			if err != nil {
				t.Fatal(err)
			}
			actor, err := identity.Default().BindSession(created.ID)
			if err != nil {
				t.Fatal(err)
			}
			gateway := admission.NewGateway(admission.NewBudget(ledger), admission.ProviderConfig{})
			if _, err := ledger.Append(ctx, created.ID, 1, "tool/dispatched", map[string]any{"run_id": "interrupted", "tool_call_id": "shell", "tool_name": "Bash"}); err != nil {
				t.Fatal(err)
			}
			if scenario != "missing result" {
				if _, err := ledger.Append(ctx, created.ID, 2, "tool/result", map[string]any{"run_id": "interrupted", "tool_call_id": "shell", "tool_name": "Bash", "exit_code": 126, "runtime_id": "retained-runtime", "outcome_unknown": true, "stopped": scenario == "confirmed stop"}); err != nil {
					t.Fatal(err)
				}
			}
			err = gateway.FinishTask(ctx, actor)
			if scenario == "confirmed stop" {
				if err != nil {
					t.Fatal("explicit task end refused a confirmed stopped execution")
				}
			} else if !errors.Is(err, session.ErrSessionStateConflict) {
				t.Fatal("missing outcome or unconfirmed stop allowed task completion")
			}
			events, err := ledger.Events(ctx, created.ID)
			if err != nil || len(events) != map[bool]int{true: 2, false: 3}[scenario == "missing result"] {
				t.Fatal("task end rewrote the retained tool outcome")
			}
		})
	}
}

func TestGatewayExplicitFinishClosesUnusedRootWithoutSpendingTokens(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	created, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "fixture", "unused", "")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	if err := gateway.FinishTask(ctx, actor); err != nil {
		t.Fatal(err)
	}
	_, release, err := gateway.BindActor(actor, created.ID)
	if release != nil {
		release()
	}
	if !errors.Is(err, admission.ErrTaskClosed) {
		t.Fatal("a successful explicit finish left an unused root open")
	}
	batch, err := budget.Inspect(ctx, 7)
	if err != nil || batch.UsedTokens != 0 || batch.ReservedTokens != 0 || batch.TaskID != "" || batch.CostStatus != "unknown" {
		t.Fatal("ending an unused task changed token consumption")
	}
}

func TestGatewayFinishChecksPersistedCLIInvocations(t *testing.T) {
	for _, scenario := range []string{"prepared", "dispatched", "unknown", "unconfirmed hook", "unconfirmed committed hook", "confirmed unknown", "committed"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			store := session.NewSQLiteEventStore(filepath.Join(t.TempDir(), "ledger.sqlite"))
			manager := session.NewManager(store)
			defer manager.Close()
			current := manager.NewSession(t.TempDir())
			actor, err := identity.Default().BindSession(current.ID)
			if err != nil {
				t.Fatal(err)
			}
			manager.SetActor(actor)
			if _, err := manager.BeginInvocation("shell", "Bash", `{}`); err != nil {
				t.Fatal(err)
			}
			if scenario != "prepared" {
				if _, err := manager.MarkInvocationDispatched("shell"); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "unconfirmed hook" || scenario == "unconfirmed committed hook" {
				if err := manager.RecordInvocationRuntime("shell", "post_tool", "retained-hook", false); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "prepared" && scenario != "dispatched" {
				if _, err := manager.CommitInvocation(session.InvocationResult{ID: "shell", Name: "Bash", RuntimeID: "retained-tool", Stopped: scenario != "unknown", OutcomeUnknown: scenario != "committed" && scenario != "unconfirmed committed hook"}); err != nil {
					t.Fatal(err)
				}
			}
			ledger, err := store.Ledger()
			if err != nil {
				t.Fatal(err)
			}
			before, err := ledger.Events(ctx, current.ID)
			if err != nil {
				t.Fatal(err)
			}
			gateway := admission.NewGateway(admission.NewBudget(ledger), admission.ProviderConfig{})
			err = gateway.FinishTask(ctx, actor)
			if scenario == "confirmed unknown" || scenario == "committed" {
				if err != nil {
					t.Fatalf("confirmed stopped CLI task refused explicit completion: %v", err)
				}
			} else if !errors.Is(err, session.ErrSessionStateConflict) {
				t.Fatalf("unfinished CLI execution allowed task completion: %v", err)
			}
			after, err := ledger.Events(ctx, current.ID)
			if err != nil || len(after) != len(before) || after[len(after)-1].Checksum != before[len(before)-1].Checksum {
				t.Fatal("task completion rewrote retained CLI invocation facts")
			}
		})
	}
}
