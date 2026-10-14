package admission_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestGatewayHonorsConfiguredRequestDeadline(t *testing.T) {
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "fixture-only")
	t.Setenv("OPENAI_BASE_URL", upstream.URL)
	t.Setenv("OPENAI_MODEL", "fixture-model")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "100")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "3")
	t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
	t.Setenv("CODE_AGENT_MODEL_REQUEST_TIMEOUT_SECONDS", "1")
	gateway := admission.NewGateway(budget, admission.ProviderFromEnv())
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, "deadline-task", "deadline-session")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	channel, err := grpc.NewClient(binding.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	response, err := pb.NewModelGatewayClient(channel).Invoke(ctx, &pb.ModelCallRequest{
		Capability: binding.Capability, CallId: "deadline-call",
		PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hello"}]}`),
	})
	if err != nil || response.GetErrorCode() != "provider_timeout_unknown" || time.Since(start) > 2500*time.Millisecond {
		t.Fatalf("configured one-second request deadline was not enforced: code=%s err=%v elapsed=%v", response.GetErrorCode(), err, time.Since(start))
	}
	batch, err := budget.Inspect(context.Background(), 7)
	if err != nil || !batch.UnknownUsage || batch.ReservedTokens != 103 || batch.UsedTokens != 0 || batch.ActiveCalls != 0 {
		t.Fatal("request deadline lost the stopped call's unknown accounting")
	}
}

func TestGatewayIssuesRPCDeadlineCoveringAccountedRetries(t *testing.T) {
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	for _, scenario := range []struct {
		name           string
		requestSeconds int64
		rpcSeconds     uint32
	}{{"default", 0, 210}, {"longer", 180, 570}, {"maximum", 600, 1830}} {
		t.Run(scenario.name, func(t *testing.T) {
			gateway := admission.NewGateway(admission.NewBudget(ledger), admission.ProviderConfig{
				Protocol: "openai", BaseURL: "https://example.invalid/v1", APIKey: "fixture-only", Model: "fixture-model",
				InputLimit: 100, OutputLimit: 3, RequestTimeoutSeconds: scenario.requestSeconds,
			})
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, "deadline-task", "deadline-session")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if binding.RpcTimeoutSeconds != scenario.rpcSeconds {
				t.Fatalf("Go-issued RPC wait cannot cover the transport/retry profile: got %d, want %d", binding.RpcTimeoutSeconds, scenario.rpcSeconds)
			}
		})
	}
}

func TestGatewayRejectsInvalidRequestDeadlineBeforeDispatch(t *testing.T) {
	for _, raw := range []string{"0", "-1", "601", "invalid", "9223372036854775808"} {
		t.Run(raw, func(t *testing.T) {
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			var paidAttempts atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				paidAttempts.Add(1)
				_, _ = w.Write([]byte(`{"model":"fixture-model","usage":{"prompt_tokens":8,"completion_tokens":3}}`))
			}))
			defer upstream.Close()
			t.Setenv("LLM_PROVIDER", "openai")
			t.Setenv("OPENAI_API_KEY", "fixture-only")
			t.Setenv("OPENAI_BASE_URL", upstream.URL)
			t.Setenv("OPENAI_MODEL", "fixture-model")
			t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "100")
			t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "3")
			t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
			t.Setenv("CODE_AGENT_MODEL_REQUEST_TIMEOUT_SECONDS", raw)
			budget := admission.NewBudget(ledger)
			gateway := admission.NewGateway(budget, admission.ProviderFromEnv())
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, "deadline-task", "deadline-session")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			channel, err := grpc.NewClient(binding.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer channel.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			response, err := pb.NewModelGatewayClient(channel).Invoke(ctx, &pb.ModelCallRequest{
				Capability: binding.Capability, CallId: "invalid-deadline-call", PayloadJson: []byte(`{"model":"fixture-model","messages":[]}`),
			})
			if err != nil || response.GetErrorCode() != "model_prerequisites_missing" || paidAttempts.Load() != 0 {
				t.Fatal("invalid deadline reached upstream transport")
			}
			batch, err := budget.Inspect(context.Background(), 7)
			if err != nil || batch.ReservedTokens != 0 || batch.UsedTokens != 0 || batch.UnknownUsage {
				t.Fatal("invalid deadline invented accounting facts")
			}
		})
	}
}

type blockedFinalizationLedger struct{ session.EventLog }

func (ledger blockedFinalizationLedger) Append(ctx context.Context, id string, seq int64, eventType string, payload any) (session.Event, error) {
	if eventType == "admission/call_settled" || eventType == "admission/call_unknown" {
		<-ctx.Done()
		return session.Event{}, ctx.Err()
	}
	return ledger.EventLog.Append(ctx, id, seq, eventType, payload)
}

func TestGatewayBoundsFinalizationAfterCallerDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	// Inject a stalled database write; all prior facts use the real SQLite Ledger.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"model":"fixture-model","usage":{"prompt_tokens":8,"completion_tokens":3}}`))
	}))
	defer upstream.Close()
	budget := admission.NewBudget(blockedFinalizationLedger{ledger})
	gateway := admission.NewGateway(budget, admission.ProviderConfig{
		Protocol: "openai", BaseURL: upstream.URL, APIKey: "fixture-only", Model: "fixture-model", InputLimit: 100, OutputLimit: 3,
	})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, "deadline-task", "deadline-session")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	response, err := gateway.Invoke(ctx, &pb.ModelCallRequest{
		Capability: binding.Capability, CallId: "locked-finalization-call", PayloadJson: []byte(`{"model":"fixture-model","messages":[]}`),
	})
	elapsed := time.Since(start)
	if err != nil || response.GetErrorCode() != "provider_settlement_unknown" || elapsed > 6250*time.Millisecond {
		t.Fatalf("Ledger finalization exceeded the caller deadline and one audit window: code=%s err=%v elapsed=%v", response.GetErrorCode(), err, elapsed)
	}
	batch, err := budget.Inspect(context.Background(), 7)
	if err != nil || batch.ReservedTokens != 103 || batch.UsedTokens != 0 || batch.UnknownUsage || batch.ActiveCalls != 1 {
		t.Fatal("failed finalization lost the original reservation or invented settled usage")
	}
}
