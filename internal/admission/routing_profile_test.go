package admission_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/session"
)

func TestGatewayRejectsUnsafeProviderSessionBeforeReservation(t *testing.T) {
	for _, value := range []string{"opaque\r\ninjected", strings.Repeat("x", 129), "has space", "fixture-only"} {
		t.Run(strings.ReplaceAll(value[:min(len(value), 12)], "\r\n", "-"), func(t *testing.T) {
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			defer upstream.Close()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			budget := admission.NewBudget(ledger)
			gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 64, ProviderSessionID: value})
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, "routing-task", "routing-session")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			response, err := gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "routing-call", PayloadJson: []byte(`{"model":"fixture-model","messages":[]}`)})
			if err != nil || response.GetErrorCode() != "model_prerequisites_missing" || calls.Load() != 0 {
				t.Fatal("unsafe provider session reached the upstream")
			}
			batch, err := budget.Inspect(context.Background(), 7)
			if err != nil || batch.ActiveCalls != 0 || batch.ReservedTokens != 0 {
				t.Fatal("unsafe routing profile reserved tokens")
			}
		})
	}
}
