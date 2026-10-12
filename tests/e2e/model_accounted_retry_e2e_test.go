package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/session"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestGoAccountedRetryReachesActualPythonClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"usage":{"prompt_tokens":8,"completion_tokens":0}}`)
			return
		}
		fmt.Fprint(w, `{"model":"fixture-model","choices":[{"message":{"content":"acknowledged"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
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
	encoded, err := protojson.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"binding": json.RawMessage(encoded), "purposes": []string{"foreground"}})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "python", "-m", "tests.e2e.model_gateway_probe")
	command.Dir, command.Stdin = e2ERepositoryRoot(t), bytes.NewReader(input)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		name = strings.ToUpper(name)
		if strings.Contains(name, "API_KEY") || name == "ANTHROPIC_AUTH_TOKEN" || name == "CODE_AGENT_PROVIDER_CONFIG" || name == "CODE_AGENT_MODEL_ADMISSION" {
			continue
		}
		command.Env = append(command.Env, entry)
	}
	command.Env = append(command.Env, "CODE_AGENT_MODEL_ADMISSION=required", "OTEL_SDK_DISABLED=true", "THINKING_ENABLED=false")
	output, runErr := command.Output()
	var observed struct {
		Passed  bool
		Results []struct {
			Passed     bool
			Input      int64  `json:"input_tokens"`
			Output     int64  `json:"output_tokens"`
			CostStatus string `json:"cost_status"`
		}
	}
	if json.Unmarshal(output, &observed) != nil || runErr != nil || !observed.Passed || len(observed.Results) != 1 || !observed.Results[0].Passed {
		t.Fatal("actual Python client did not complete the Go-admitted fixture retry; raw output suppressed")
	}
	if observed.Results[0].Input != 11 || observed.Results[0].Output != 2 || observed.Results[0].CostStatus != "unknown" || attempts.Load() != 2 {
		t.Fatal("Python omitted the settled failed attempt or performed its own unmetered retry")
	}
	batch, err := budget.Inspect(ctx, 7)
	if err != nil || batch.UsedTokens != 13 || batch.ReservedTokens != 0 || batch.UnknownUsage || batch.CostStatus != "unknown" {
		t.Fatal("cross-process retry changed retained accounting")
	}
}
