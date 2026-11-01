package cli_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type evaluationRetryTransport struct {
	pb.UnimplementedOrchestratorServer
	batches   [][]*pb.ToolRequest
	responses chan *pb.ToolResult
}

func (evaluationRetryTransport) Health(context.Context, *pb.Empty) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok"}, nil
}

func (evaluationRetryTransport) ReflectMemory(_ context.Context, request *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
	return &pb.MemoryReflectionResponse{SourceChecksum: request.SourceChecksum}, nil
}

func (f evaluationRetryTransport) Converse(stream grpc.BidiStreamingServer[pb.HarnessMessage, pb.OrchestratorMessage]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	binding := first.GetUserInput().GetModelGateway()
	connection, err := grpc.NewClient(binding.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer connection.Close()
	callHash := sha256.Sum256(binding.Capability)
	response, err := pb.NewModelGatewayClient(connection).Invoke(stream.Context(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "retry-fixture-" + hex.EncodeToString(callHash[:8]), PayloadJson: []byte(`{"model":"approved-model","messages":[{"role":"user","content":"perform the fixture task"}]}`)})
	if err != nil {
		return err
	}
	if response.ErrorCode != "" {
		return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: false, Message: "fixture model admission failed"}}})
	}
	for _, requests := range f.batches {
		if len(requests) == 1 {
			err = stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_ToolRequest{ToolRequest: requests[0]}})
		} else {
			err = stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_ToolRequestBatch{ToolRequestBatch: &pb.ToolRequestBatch{Requests: requests, Parallel: true}}})
		}
		if err != nil {
			return err
		}
		for range requests {
			message, err := stream.Recv()
			if err != nil {
				return err
			}
			f.responses <- message.GetToolResult()
		}
	}
	return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: true, Message: "The scripted candidate is complete."}}})
}

func TestEvaluationBoundedRetryClosesCandidateAfterFirstPass(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production retry fixture uses admitted Windows paths")
	}
	first := &pb.ToolRequest{ToolName: "Write", ToolCallId: "candidate", ParametersJson: `{"path":"result.txt","content":"first candidate\n"}`}
	validate := &pb.ToolRequest{ToolName: "Bash", ToolCallId: "validate-1", ParametersJson: `{"command":"go test ./... -count=1"}`}
	late := &pb.ToolRequest{ToolName: "Write", ToolCallId: "late-repair", ParametersJson: `{"path":"result.txt","content":"forbidden repair\n"}`}
	receipt, responses, calls := runEvaluationRetryFixture(t, [][]*pb.ToolRequest{{first}, {validate}, {late}})
	if len(responses) != 3 || responses[2].GetError() == "" || calls != 1 {
		t.Fatalf("first-pass candidate remained open for repair: responses=%v runtime_calls=%d", responses, calls)
	}
	actual := receipt["workspace"].(string)
	if body, err := os.ReadFile(filepath.Join(actual, "result.txt")); err != nil || string(body) != "first candidate\n" {
		t.Fatal("late repair changed the validated candidate")
	}
	retry, ok := receipt["strategy_evidence"].(map[string]any)["retry_evidence"].(map[string]any)
	if !ok || retry["attempt_count"] != float64(1) || retry["retry_observed"] != false || retry["bound_enforced"] != true {
		t.Fatalf("first-pass evidence invented a repair: %+v", retry)
	}
}

func retryTool(name, id, arguments string) *pb.ToolRequest {
	return &pb.ToolRequest{ToolName: name, ToolCallId: id, ParametersJson: arguments}
}

func TestEvaluationBoundedRetryPreservesOneRepairPhaseAndBothOutcomes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production retry fixture uses admitted Windows paths")
	}
	for _, outcome := range []string{"passed", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			batches := [][]*pb.ToolRequest{
				{retryTool("Write", "candidate", `{"path":"result.txt","content":"first candidate\n"}`)},
				{retryTool("Bash", "validate-1", `{"command":"go test ./... -count=1"}`)},
				{retryTool("Write", "repair-a", `{"path":"result.txt","content":"repaired candidate\n"}`)},
				{retryTool("Write", "repair-b", `{"path":"notes.txt","content":"same repair phase\n"}`)},
				{retryTool("Bash", "validate-2", `{"command":"go test ./... -count=1"}`)},
			}
			receipt, responses, calls := runEvaluationRetryFixture(t, batches, "failed", outcome)
			retry := receipt["strategy_evidence"].(map[string]any)["retry_evidence"].(map[string]any)
			if receipt["task_state"] != "completed" || len(responses) != 5 || responses[2].GetError() != "" || responses[3].GetError() != "" || calls != 2 || retry["attempt_count"] != float64(2) || retry["retry_observed"] != true || retry["bound_enforced"] != true {
				t.Fatalf("one repair phase was not preserved: receipt=%+v responses=%v calls=%d", receipt, responses, calls)
			}
			attempts := retry["attempts"].([]any)
			for i, raw := range attempts {
				attempt := raw.(map[string]any)
				wantCode, wantState := float64(1), "failed"
				if i == 1 && outcome == "passed" {
					wantCode, wantState = 0, "passed"
				}
				if attempt["exit_code"] != wantCode || attempt["state"] != wantState || attempt["pending_event_id"] == "" || attempt["dispatch_event_id"] == "" || attempt["result_event_id"] == "" || len(attempt["result_checksum"].(string)) != 64 {
					t.Fatalf("attempt outcome or canonical refs missing: %+v", attempt)
				}
			}
		})
	}
}

func TestEvaluationBoundedRetryRejectsThirdValidationAndLateRepair(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production retry fixture uses admitted Windows paths")
	}
	for _, outcome := range []string{"passed", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			batches := [][]*pb.ToolRequest{
				{retryTool("Write", "candidate", `{"path":"result.txt","content":"first candidate\n"}`)},
				{retryTool("Bash", "validate-1", `{"command":"go test ./... -count=1"}`)},
				{retryTool("Write", "repair", `{"path":"result.txt","content":"repaired candidate\n"}`)},
				{retryTool("Bash", "validate-2", `{"command":"go test ./... -count=1"}`)},
				{retryTool("Bash", "validate-3", `{"command":"go test ./... -count=1"}`)},
				{retryTool("Write", "late-repair", `{"path":"result.txt","content":"forbidden repair\n"}`)},
			}
			receipt, responses, calls := runEvaluationRetryFixture(t, batches, "failed", outcome)
			if len(responses) != 6 || responses[4].GetError() == "" || responses[5].GetError() == "" || calls != 2 {
				t.Fatalf("post-second validation was not closed: responses=%v calls=%d", responses, calls)
			}
			if body, err := os.ReadFile(filepath.Join(receipt["workspace"].(string), "result.txt")); err != nil || string(body) != "repaired candidate\n" {
				t.Fatal("late repair changed the twice-validated candidate")
			}
		})
	}
}

func TestEvaluationBoundedRetryReservesConcurrentValidationBeforeDispatch(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production retry fixture uses admitted Windows paths")
	}
	batches := [][]*pb.ToolRequest{{retryTool("Write", "candidate", `{"path":"result.txt","content":"candidate\n"}`)}, {
		retryTool("Bash", "parallel-1", `{"command":"go test ./... -count=1"}`),
		retryTool("Bash", "parallel-2", `{"command":"go test ./... -count=1"}`),
		retryTool("Bash", "parallel-3", `{"command":"go test ./... -count=1"}`),
	}}
	receipt, responses, calls := runEvaluationRetryFixture(t, batches)
	denied := 0
	for _, response := range responses[1:] {
		if response.GetError() != "" {
			denied++
		}
	}
	retry := receipt["strategy_evidence"].(map[string]any)["retry_evidence"].(map[string]any)
	if calls != 1 || denied != 2 || retry["attempt_count"] != float64(1) || retry["bound_enforced"] != true {
		t.Fatalf("concurrent model batch bypassed the pending reservation: calls=%d denied=%d evidence=%+v", calls, denied, retry)
	}
}

func TestEvaluationBoundedRetryKeepsUnknownValidationClosed(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production retry fixture uses admitted Windows paths")
	}
	batches := [][]*pb.ToolRequest{
		{retryTool("Write", "candidate", `{"path":"result.txt","content":"first candidate\n"}`)},
		{retryTool("Bash", "validate-1", `{"command":"go test ./... -count=1"}`)},
		{retryTool("Write", "repair", `{"path":"result.txt","content":"forbidden repair\n"}`)},
		{retryTool("Bash", "validate-2", `{"command":"go test ./... -count=1"}`)},
	}
	receipt, responses, calls := runEvaluationRetryFixture(t, batches, "unknown")
	retry := receipt["strategy_evidence"].(map[string]any)["retry_evidence"].(map[string]any)
	attempt := retry["attempts"].([]any)[0].(map[string]any)
	if len(responses) != 4 || responses[2].GetError() == "" || responses[3].GetError() == "" || calls != 1 || retry["attempt_count"] != float64(1) || retry["retry_observed"] != false || attempt["state"] != "unknown" || attempt["exit_code"] != nil || receipt["task_state"] == "completed" {
		t.Fatalf("unknown validation became a known repair opportunity: receipt=%+v responses=%v calls=%d", receipt, responses, calls)
	}
}

func TestEvaluationBoundedRetryCannotFinishWithoutRequiredValidation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production retry fixture uses admitted Windows paths")
	}
	for _, first := range []string{"missing", "failed", "passed"} {
		t.Run(first, func(t *testing.T) {
			batches := [][]*pb.ToolRequest{{retryTool("Write", "candidate", `{"path":"result.txt","content":"candidate\n"}`)}}
			if first != "missing" {
				batches = append(batches, []*pb.ToolRequest{retryTool("Bash", "validate-1", `{"command":"go test ./... -count=1","cwd":".","working_dir":"."}`)})
			}
			receipt, _, _ := runEvaluationRetryFixture(t, batches, first)
			if (receipt["task_state"] == "completed") != (first == "passed") {
				t.Fatalf("completion did not require the declared validation sequence: %+v", receipt)
			}
		})
	}
}

func TestEvaluationBoundedRetryDoesNotAcceptAnUndeclaredBashAsValidation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production retry fixture uses admitted Windows paths")
	}
	batches := [][]*pb.ToolRequest{{retryTool("Bash", "different-command", `{"command":"go test ./... -count=2"}`)}}
	receipt, responses, calls := runEvaluationRetryFixture(t, batches)
	retry := receipt["strategy_evidence"].(map[string]any)["retry_evidence"].(map[string]any)
	if calls != 0 || len(responses) != 1 || responses[0].GetError() == "" || retry["attempt_count"] != float64(0) || receipt["task_state"] == "completed" {
		t.Fatalf("an undeclared command bypassed frozen validation: %+v %v", receipt, responses)
	}
}

func TestEvaluationBoundedRetryDoesNotAcceptSubdirectoryValidation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production retry fixture uses admitted Windows paths")
	}
	for _, field := range []string{"cwd", "working_dir"} {
		t.Run(field, func(t *testing.T) {
			arguments, _ := json.Marshal(map[string]string{"command": "go test ./... -count=1", field: "subdir"})
			batches := [][]*pb.ToolRequest{
				{retryTool("Write", "candidate", `{"path":"subdir/result.txt","content":"candidate\n"}`)},
				{retryTool("Bash", "subdirectory-validation", string(arguments))},
			}
			receipt, responses, calls := runEvaluationRetryFixture(t, batches)
			retry := receipt["strategy_evidence"].(map[string]any)["retry_evidence"].(map[string]any)
			if calls != 0 || len(responses) != 2 || responses[1].GetError() == "" || retry["attempt_count"] != float64(0) || receipt["task_state"] == "completed" {
				t.Fatalf("subdirectory validation was counted as whole-candidate verification: %+v %v calls=%d", receipt, responses, calls)
			}
		})
	}
}

func TestEvaluationBoundedRetryRequiresVisibleCommandBeforeModel(t *testing.T) {
	for key, value := range map[string]string{
		"CODE_AGENT_SESSION_LEDGER_PATH": "", "CODE_AGENT_EVAL_VISIBLE_VERIFICATION_COMMANDS_JSON": `[]`,
		"LLM_PROVIDER": "openai", "OPENAI_MODEL": "approved-model", "OPENAI_API_KEY": "fixture-only",
		"CODE_AGENT_MODEL_INPUT_LIMIT": "200", "CODE_AGENT_MODEL_OUTPUT_LIMIT": "20", "CODE_AGENT_MODEL_TOTAL_LIMIT": "", "CODE_AGENT_MODEL_REASONING_EFFORT": "", "CODE_AGENT_MODEL_REPORTED_MODEL": "",
	} {
		t.Setenv(key, value)
	}
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer provider.Close()
	t.Setenv("OPENAI_BASE_URL", provider.URL)
	cfg := config.Default(t.TempDir())
	request := map[string]any{"schema_version": 1, "run_id": "missing-visible-command", "task_id": "validation", "workspace": productionRepository(t), "message": "Verify the candidate", "model": "approved-model", "budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 30},
		"policy": map[string]any{"context_strategy": "metadata_on_demand", "execution_strategy": "verify_retry", "max_tool_rounds": 8, "context_max_bytes": 12000}}
	input, _ := json.Marshal(request)
	var output strings.Builder
	if err := cli.RunEvaluation(context.Background(), cfg, strings.NewReader(string(input)), &output, io.Discard); err == nil {
		t.Fatal("verify_retry accepted no declared validation")
	}
	var receipt map[string]any
	if json.Unmarshal([]byte(output.String()), &receipt) != nil || receipt["error_code"] != "visible_verification_required" || receipt["session_id"] != "" || calls.Load() != 0 {
		t.Fatalf("empty validation configuration reached model execution: %s calls=%d", output.String(), calls.Load())
	}
}

// The local HTTP proxy changes only fixture exit/status observations; it never
// launches a real container or executes a candidate on the host.
func retryRuntimeOutcomes(t *testing.T, cfg *config.SandboxConfig, outcomes []string) {
	t.Helper()
	if len(outcomes) == 0 {
		return
	}
	data, err := os.ReadFile(cfg.OpenSandboxManifest)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]string
	if json.Unmarshal(data, &manifest) != nil {
		t.Fatal("invalid fixture manifest")
	}
	endpoint := strings.TrimSuffix(manifest["endpoint"], "/v1")
	var mu sync.Mutex
	index, outcome := 0, "passed"
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/command") {
			if index < len(outcomes) {
				outcome = outcomes[index]
			}
			index++
		}
		forward, err := http.NewRequestWithContext(r.Context(), r.Method, endpoint+r.URL.RequestURI(), r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		forward.Header = r.Header.Clone()
		response, err := http.DefaultClient.Do(forward)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		defer response.Body.Close()
		w.WriteHeader(response.StatusCode)
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/command/status/") && outcome == "failed" {
			io.Copy(io.Discard, response.Body)
			io.WriteString(w, `{"id":"command-1","running":false,"exit_code":1}`)
		} else if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/command") && outcome == "unknown" {
			io.Copy(io.Discard, response.Body)
			io.WriteString(w, "{\"type\":\"init\",\"text\":\"command-1\"}\n")
		} else {
			io.Copy(w, response.Body)
		}
	}))
	t.Cleanup(proxy.Close)
	manifest["endpoint"] = proxy.URL + "/v1"
	data, _ = json.Marshal(manifest)
	if err := os.WriteFile(cfg.OpenSandboxManifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256(data)
	cfg.OpenSandboxManifestSHA256 = hex.EncodeToString(pin[:])
}

func runEvaluationRetryFixture(t *testing.T, batches [][]*pb.ToolRequest, outcomes ...string) (map[string]any, []*pb.ToolResult, int64) {
	t.Helper()
	for key, value := range map[string]string{
		"CODE_AGENT_SESSION_LEDGER_PATH": "", "CODE_AGENT_EVAL_ALLOW_TOOLS": "Read,Write,Edit,Bash", "CODE_AGENT_EVAL_VISIBLE_VERIFICATION_COMMANDS_JSON": `["go test ./... -count=1"]`,
		"OTEL_EXPORTER_OTLP_ENDPOINT": ":invalid", "LLM_PROVIDER": "openai", "OPENAI_MODEL": "approved-model", "OPENAI_API_KEY": "fixture-only",
		"CODE_AGENT_MODEL_INPUT_LIMIT": "200", "CODE_AGENT_MODEL_OUTPUT_LIMIT": "20", "CODE_AGENT_MODEL_TOTAL_LIMIT": "", "CODE_AGENT_MODEL_REASONING_EFFORT": "", "CODE_AGENT_MODEL_REPORTED_MODEL": "",
	} {
		t.Setenv(key, value)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"model":"approved-model","usage":{"prompt_tokens":100,"completion_tokens":5}}`)
	}))
	defer provider.Close()
	t.Setenv("OPENAI_BASE_URL", provider.URL)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	responses := make(chan *pb.ToolResult, 32)
	pb.RegisterOrchestratorServer(server, evaluationRetryTransport{batches: batches, responses: responses})
	go server.Serve(listener)
	defer server.Stop()
	cfg := config.Default(t.TempDir())
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.MCPConfig = listener.Addr().String(), false, "missing.json"
	var runtimeCalls atomic.Int64
	cfg.Sandbox = productionRuntimeFixture(t, t.TempDir(), &runtimeCalls)
	retryRuntimeOutcomes(t, &cfg.Sandbox, outcomes)
	request := map[string]any{"schema_version": 1, "runtime_contract": "production_opensandbox", "run_id": "bounded-retry-fixture", "task_id": "visible-verification", "workspace": productionRepository(t),
		"message": "Create and visibly verify the candidate", "model": "approved-model", "lifecycle": "hold", "turn_id": "turn-1", "budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 90},
		"policy": map[string]any{"context_strategy": "metadata_on_demand", "execution_strategy": "verify_retry", "memory_read": false, "max_tool_rounds": 16, "context_max_bytes": 12000}}
	input, _ := json.Marshal(request)
	var output strings.Builder
	_ = cli.RunEvaluation(context.Background(), cfg, strings.NewReader(string(input)), &output, io.Discard)
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
	if receipt == nil || receipt["session_id"] == "" {
		t.Fatalf("no owned public retry receipt: %s", output.String())
	}
	observed := make([]*pb.ToolResult, 0, len(responses))
	for len(responses) > 0 {
		observed = append(observed, <-responses)
	}
	return receipt, observed, runtimeCalls.Load()
}
