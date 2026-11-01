package admission_test

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/session"
)

func TestGatewayRetainsOnlySafeUpstreamFailureLabels(t *testing.T) {
	for _, scenario := range []struct {
		name, key, body, expected string
	}{
		{"known labels", "private-provider-key", `{"error":{"type":"server_error","code":"no_available_channel","param":"model","message":"private-provider-key should never leave the body"}}`, "type=server_error code=no_available_channel param=model"},
		{"numeric upstream code", "private-provider-key", `{"error":{"type":"server_error","code":503,"param":"model","message":"private-provider-key should never leave the body"}}`, "type=server_error code=unknown param=model"},
		{"untrusted labels", "private-provider-key", `{"error":{"type":"private-provider-key","code":"other-secret-value","param":"https://private.invalid","message":"private-provider-key"}}`, "type=unknown code=unknown param=unknown"},
		{"credential equals allowed label", "server_error", `{"error":{"type":"server_error","code":"no_available_channel","param":"model"}}`, "type=unknown code=no_available_channel param=model"},
		{"malformed body", "private-provider-key", `not-json private-provider-key`, "type=unknown code=unknown param=unknown"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(previous)
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			budget := admission.NewBudget(ledger)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, scenario.body)
			}))
			defer upstream.Close()
			gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model", APIKey: scenario.key, InputLimit: 100, OutputLimit: 3})
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, "diagnostic-task", "diagnostic-session")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			result, err := gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "diagnostic-call", PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"private task text"}]}`)})
			if err != nil || result.GetErrorCode() != "provider_transient_unknown" || result.GetHttpStatus() != 503 || len(result.GetPayloadJson()) != 0 {
				t.Fatal("safe diagnostic changed the provider outcome")
			}
			text := output.String()
			if !strings.Contains(text, "http_status=503") || !strings.Contains(text, scenario.expected) {
				t.Fatal("upstream failure lost its safe status and labels")
			}
			for _, forbidden := range []string{scenario.key, "other-secret-value", "https://private.invalid", "private task text", "should never leave the body"} {
				if strings.Contains(text, forbidden) {
					t.Fatal("diagnostic exposed provider content or credentials")
				}
			}
			state, err := budget.Inspect(context.Background(), 7)
			if err != nil || state.UsedTokens != 0 || state.ReservedTokens != 103 || !state.UnknownUsage || state.ActiveCalls != 0 {
				t.Fatal("diagnostic changed retained unknown accounting")
			}
		})
	}
}
