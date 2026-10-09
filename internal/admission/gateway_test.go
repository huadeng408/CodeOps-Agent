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
	"code-agent/internal/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

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
		_, _ = w.Write([]byte(`{"model":"fixture-model","choices":[{"message":{"content":"\u0066ixture-provider-authority"}}],"usage":{"prompt_tokens":8,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":2}}}`))
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
		calls.Add(1)
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
	if err != nil || result.GetErrorCode() != "token_usage_unknown" || calls.Load() != 1 {
		t.Fatal("unknown usage admitted another upstream call")
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
		_, _ = w.Write([]byte(`{"model":"fixture-model","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":8,"output_tokens":3,"cache_read_input_tokens":4,"cache_creation_input_tokens":5}}`))
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
	ids, err := ledger.SessionIDs(context.Background())
	if err != nil || len(ids) != 0 {
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
