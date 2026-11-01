package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/session"
	"google.golang.org/grpc"
)

func TestEvaluationHoldsTurnsAndFinalizesWithoutResettingBudget(t *testing.T) {
	t.Setenv("CODE_AGENT_SESSION_LEDGER_PATH", "")
	t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "Read,Write,Edit")
	t.Setenv("CODE_AGENT_EVAL_VISIBLE_VERIFICATION_COMMANDS_JSON", "[]")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "approved-model")
	t.Setenv("OPENAI_API_KEY", "fixture-only")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "200")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "20")
	t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
	var modelCalls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		io.WriteString(w, `{"id":"fixture","model":"approved-model","choices":[{"message":{"role":"assistant","content":"continue"}}],"usage":{"prompt_tokens":100,"completion_tokens":5}}`)
	}))
	defer provider.Close()
	t.Setenv("OPENAI_BASE_URL", provider.URL)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterOrchestratorServer(server, evaluationTransport{})
	go server.Serve(listener)
	defer server.Stop()
	cfg := config.Default(t.TempDir())
	cfg.Sandbox.Enabled = false
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart = listener.Addr().String(), false
	cfg.MCPConfig = "missing.json"
	workspace := t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "initial"}} {
		if body, err := exec.Command("git", append([]string{"-C", workspace}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, body)
		}
	}
	request := map[string]any{"schema_version": 1, "run_id": "multi-turn-fixture", "task_id": "requirements-update", "workspace": workspace, "message": "Write the initial result", "model": "approved-model", "lifecycle": "hold", "turn_id": "turn-1", "budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 45}}
	call := func() (map[string]any, error) {
		input, _ := json.Marshal(request)
		var output strings.Builder
		err := cli.RunEvaluation(context.Background(), cfg, strings.NewReader(string(input)), &output, io.Discard)
		decoder := json.NewDecoder(strings.NewReader(output.String()))
		var receipt map[string]any
		for {
			var event map[string]any
			if decoder.Decode(&event) != nil {
				break
			}
			if event["type"] == "receipt" {
				receipt = event
			}
		}
		return receipt, err
	}
	first, err := call()
	if err != nil || first["task_state"] != "completed" {
		t.Fatalf("held first turn failed: %v %+v", err, first)
	}
	ledger, err := session.OpenReadOnlySQLiteEventLog(cfg.SessionDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	batch, err := admission.NewBudget(ledger).Inspect(context.Background(), 1)
	if err != nil || batch.TaskID != first["session_id"] {
		t.Fatal("hold finalized the root admission")
	}
	request["session_id"], request["operation"], request["turn_id"], request["message"] = first["session_id"], "run", "turn-2", "Apply updated requirements"
	request["budget"] = map[string]any{"max_tokens": 2000, "wall_clock_seconds": 45}
	if _, err := call(); err == nil {
		t.Fatal("a later turn reset the frozen budget")
	}
	request["budget"] = map[string]any{"max_tokens": 1000, "wall_clock_seconds": 45}
	second, err := call()
	if err != nil || second["task_state"] != "completed" || second["session_id"] != first["session_id"] || second["product_run_id"] == first["product_run_id"] {
		t.Fatalf("updated turn failed: %v %+v", err, second)
	}
	usage := second["usage"].(map[string]any)
	if usage["confirmed_tokens"] != float64(210) || usage["call_count"] != float64(2) {
		t.Fatalf("multi-turn accounting reset: %+v", usage)
	}
	request["operation"], request["finalize_mode"] = "finalize", "finish"
	final, err := call()
	if err != nil || final["task_state"] != "finalized" || modelCalls.Load() != 2 {
		t.Fatalf("explicit finalize failed or called a model: %v %+v", err, final)
	}
	batch, err = admission.NewBudget(ledger).Inspect(context.Background(), 1)
	if err != nil || batch.TaskID != "" || batch.UsedTokens != 210 {
		t.Fatal("finalize did not release the existing batch with usage retained")
	}
	request["operation"], request["turn_id"], request["message"] = "run", "turn-3", "Another update"
	delete(request, "finalize_mode")
	if _, err := call(); err == nil || modelCalls.Load() != 2 {
		t.Fatal("finalized trial accepted a new turn")
	}
}
