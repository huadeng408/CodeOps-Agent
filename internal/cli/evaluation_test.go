package cli_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/memory"
	"code-agent/internal/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestEvaluationRefusesModelSubstitutionBeforeStartingProduct(t *testing.T) {
	t.Setenv("CODE_AGENT_SESSION_LEDGER_PATH", "")
	t.Setenv("OPENAI_MODEL", "approved-model")
	t.Setenv("LLM_PROVIDER", "openai")
	cfg := config.Default(t.TempDir())
	var output strings.Builder
	request := `{"schema_version":1,"run_id":"experiment-1","task_id":"task-1","workspace":"` + strings.ReplaceAll(cfg.ProjectRoot, `\`, `\\`) + `","message":"Inspect the source","model":"another-model","budget":{"max_tokens":1000,"wall_clock_seconds":10}}`
	if err := cli.RunEvaluation(context.Background(), cfg, strings.NewReader(request), &output, &strings.Builder{}); err == nil {
		t.Fatal("a different model was admitted")
	}
	var receipt map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output.String())), &receipt); err != nil {
		t.Fatalf("expected one JSON refusal receipt: %v", err)
	}
	if receipt["type"] != "receipt" || receipt["task_state"] != "blocked" || receipt["error_code"] != "model_mismatch" || receipt["session_id"] != "" {
		t.Fatalf("unexpected refusal receipt: %+v", receipt)
	}
}

func TestEvaluationUsesActualPythonOrchestratorBeforeModelDispatch(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			t.Setenv("CODE_AGENT_MODEL_RESPONSE_MODE", "")
			testEvaluationActualPythonTransport(t, protocol)
		})
	}
}

func TestEvaluationActualPythonProductionKeepsPreparedInventory(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production task preparation uses Windows native pins")
	}
	t.Setenv("CODE_AGENT_MODEL_RESPONSE_MODE", "")
	testEvaluationActualPythonTransport(t, "openai", true)
}

func TestEvaluationActualPythonProductionReadOnlyChild(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production task preparation uses Windows native pins")
	}
	t.Setenv("CODE_AGENT_MODEL_RESPONSE_MODE", "")
	testEvaluationActualPythonTransport(t, "openai", true, true)
}

func TestEvaluationActualPythonSSEProductionReadOnlyChild(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production task preparation uses Windows native pins")
	}
	t.Setenv("CODE_AGENT_MODEL_RESPONSE_MODE", "sse")
	testEvaluationActualPythonTransport(t, "openai", true, true)
}

func testEvaluationActualPythonTransport(t *testing.T, protocol string, productionMode ...bool) {
	production := len(productionMode) > 0 && productionMode[0]
	child := len(productionMode) > 1 && productionMode[1]
	streaming := os.Getenv("CODE_AGENT_MODEL_RESPONSE_MODE") == "sse"
	model := "approved-model"
	if protocol == "anthropic" {
		model = "grok-4.6"
	}
	t.Setenv("CODE_AGENT_SESSION_LEDGER_PATH", "")
	t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "Read,Write,Edit")
	if production {
		t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "Read,Write,Edit,Bash")
	}
	if child {
		t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "Read,Write,Bash,SpawnAgent,AgentTask,PublishArtifact")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:6006")
	t.Setenv("LLM_PROVIDER", protocol)
	t.Setenv("OPENAI_MODEL", model)
	t.Setenv("OPENAI_API_KEY", "fixture-only")
	t.Setenv("ANTHROPIC_MODEL", model)
	t.Setenv("ANTHROPIC_API_KEY", "fixture-only")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "32768")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "8192")
	if protocol == "anthropic" {
		t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "16384")
	}
	t.Setenv("CODE_AGENT_MODEL_REASONING_EFFORT", "")
	t.Setenv("CODE_AGENT_MODEL_REPORTED_MODEL", "")
	t.Setenv("CODE_AGENT_MODEL_PROVIDER_SESSION_ID", "")
	if child {
		t.Setenv("CODE_AGENT_MODEL_PROVIDER_SESSION_ID", "fixture-child-cohort")
	}
	if protocol == "openai" {
		t.Setenv("CODE_AGENT_MODEL_REASONING_EFFORT", "xhigh")
	}
	t.Setenv("CODE_AGENT_MODEL_REQUEST_TIMEOUT_SECONDS", "60")
	t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
	t.Setenv("CODE_AGENT_EVAL_ORCHESTRATOR_STARTUP_SECONDS", "30")
	t.Setenv("CODE_AGENT_EVAL_CONTEXT_WINDOW", "256000")
	var calls atomic.Int64
	var rootCalls, childCalls atomic.Int64
	emitOpenAI := func(w http.ResponseWriter, message map[string]any, n int64) {
		id := fmt.Sprintf("fixture-%d", n)
		usage := map[string]int{"prompt_tokens": 100, "completion_tokens": 10}
		if streaming {
			w.Header().Set("Content-Type", "text/event-stream")
			if tools, ok := message["tool_calls"].([]map[string]any); ok {
				for index, tool := range tools {
					tool["index"] = index
				}
			}
			chunk, _ := json.Marshal(map[string]any{"id": id, "model": model, "choices": []any{map[string]any{"index": 0, "delta": message, "finish_reason": "stop"}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			chunk, _ = json.Marshal(map[string]any{"id": id, "model": model, "choices": []any{}, "usage": usage})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "model": model, "choices": []any{map[string]any{"message": message, "finish_reason": "stop"}}, "usage": usage})
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if child && r.Header.Get("X-Trace-Id") != "fixture-child-cohort" {
			t.Error("parent, child or auxiliary call changed its frozen provider session")
		}
		n := calls.Add(1)
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid upstream payload")
		}
		if protocol == "openai" && payload["reasoning_effort"] != "xhigh" {
			t.Error("actual Python request bypassed the Go effort profile")
		}
		if protocol == "openai" && payload["stream"] != streaming {
			t.Error("Go did not apply the trusted response mode")
		}
		if protocol == "anthropic" {
			content := []map[string]any{{"type": "text", "text": "Completed the task"}}
			stop := "end_turn"
			if n == 1 {
				content = []map[string]any{{"type": "tool_use", "id": "actual-python-write", "name": "Write", "input": map[string]string{"path": "result.txt", "content": "actual Python transport\n"}}}
				stop = "tool_use"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("fixture-%d", n), "type": "message", "role": "assistant", "model": model, "content": content, "stop_reason": stop, "usage": map[string]int{"input_tokens": 100, "output_tokens": 10}})
			return
		}
		message := map[string]any{"role": "assistant", "content": "Completed the task"}
		if child {
			body, _ := json.Marshal(payload)
			text := string(body)
			name, arguments := "", ""
			switch {
			case strings.Contains(text, "Extract reusable memories"):
				message["content"] = `{"candidates":[]}`
			case strings.Contains(text, "You are an independent child agent"):
				if strings.Contains(text, "PARENT_PRIVATE_HISTORY") || !strings.Contains(text, "CHILD_EXPLICIT_MATERIAL") {
					t.Error("child inherited parent conversation or lost its explicit assignment")
				}
				switch childCalls.Add(1) {
				case 1:
					name, arguments = "Read", `{"path":"child-input.txt"}`
				case 2:
					name, arguments = "PublishArtifact", `{"artifact":{"name":"bounded-exploration","parts":[{"data_json":"{\"observed\":\"PUBLIC_INPUT\"}"}]}}`
				}
			default:
				switch rootCalls.Add(1) {
				case 1:
					name, arguments = "SpawnAgent", `{"kind":"explore","title":"Bounded read-only exploration","objective":"CHILD_EXPLICIT_MATERIAL: Read child-input.txt and publish the observed data as a JSON artifact.","parallel":false,"allowed_tools":["Read","PublishArtifact"]}`
				case 2:
					if !strings.Contains(text, "bounded-exploration") {
						t.Error("parent resumed without the actual child artifact")
					}
					taskID := regexp.MustCompile(`agent-[0-9a-f]{32}`).FindString(text)
					if taskID == "" {
						t.Error("parent did not receive the real child identity")
					}
					name = "AgentTask"
					encoded, _ := json.Marshal(map[string]any{"action": "wait", "task_id": taskID, "message": map[string]any{}})
					arguments = string(encoded)
				case 3:
					name, arguments = "Write", `{"path":"result.txt","content":"actual Python transport\n"}`
				case 4:
					name, arguments = "Bash", `{"command":"go test ./... -count=1"}`
				}
			}
			if name != "" {
				message["content"] = ""
				message["tool_calls"] = []map[string]any{{"id": fmt.Sprintf("actual-child-%d", n), "type": "function", "function": map[string]string{"name": name, "arguments": arguments}}}
			}
			emitOpenAI(w, message, n)
			return
		}
		if n == 1 {
			message["content"] = ""
			message["tool_calls"] = []map[string]any{{"id": "actual-python-write", "type": "function", "function": map[string]string{"name": "Write", "arguments": `{"path":"result.txt","content":"actual Python transport\n"}`}}}
		}
		if production && n == 2 {
			message["content"] = ""
			message["tool_calls"] = []map[string]any{{"id": "actual-python-check", "type": "function", "function": map[string]string{"name": "Bash", "arguments": `{"command":"go test ./... -count=1"}`}}}
		}
		emitOpenAI(w, message, n)
	}))
	defer provider.Close()
	t.Setenv("OPENAI_BASE_URL", provider.URL)
	t.Setenv("ANTHROPIC_BASE_URL", provider.URL)
	_, sourceFile, _, _ := runtime.Caller(0)
	repo := filepath.Dir(filepath.Dir(filepath.Dir(sourceFile)))
	cfg, err := config.Load(repo)
	if err != nil {
		t.Fatal("load product configuration")
	}
	cfg.SessionDBPath = filepath.Join(t.TempDir(), "sessions.sqlite")
	cfg.OrchestratorAutoStart = true
	cfg.OrchestratorConversationTimeout = 20
	cfg.RAGEnabled = false
	cfg.MCPConfig = "missing.json"
	cfg.OrchestratorArgs = []string{"-m", "orchestrator.server"}
	var runtimeCalls atomic.Int64
	if production {
		cfg.Sandbox = productionRuntimeFixture(t, t.TempDir(), &runtimeCalls)
	} else {
		cfg.Sandbox = config.SandboxConfig{}
	}
	workspace := t.TempDir()
	if child {
		if err := os.WriteFile(filepath.Join(workspace, "child-input.txt"), []byte("PUBLIC_INPUT\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "initial"}} {
		if body, err := exec.Command("git", append([]string{"-C", workspace}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, body)
		}
	}
	if child {
		for _, args := range [][]string{{"add", "child-input.txt"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "public child input"}} {
			if body, err := exec.Command("git", append([]string{"-C", workspace}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git fixture: %v %s", err, body)
			}
		}
	}
	requestBody := map[string]any{"schema_version": 1, "run_id": "real-python-fixture", "task_id": "write-result", "workspace": workspace, "message": "Create result.txt with the authorized file tool", "model": model, "budget": map[string]any{"max_tokens": 120000, "wall_clock_seconds": 45}}
	if production {
		requestBody["runtime_contract"] = "production_opensandbox"
	}
	if child {
		requestBody["message"] = "PARENT_PRIVATE_HISTORY: Create result.txt after delegating bounded repository exploration with SpawnAgent."
		requestBody["policy"] = map[string]string{"execution_strategy": "subagent"}
		requestBody["budget"] = map[string]any{"max_tokens": 600000, "wall_clock_seconds": 60}
	}
	request, _ := json.Marshal(requestBody)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var output, diagnostics strings.Builder
	err = cli.RunEvaluation(ctx, cfg, strings.NewReader(string(request)), &output, &diagnostics)
	if err != nil || calls.Load() == 0 {
		if child {
			t.Fatalf("actual child path failed: calls=%d child_calls=%d err=%v", calls.Load(), childCalls.Load(), err)
		}
		t.Fatalf("actual Python failed before model dispatch: calls=%d err=%v protocol=%s diagnostics=%s", calls.Load(), err, output.String(), diagnostics.String())
	}
	var started map[string]any
	if err := json.NewDecoder(strings.NewReader(output.String())).Decode(&started); err != nil {
		t.Fatal(err)
	}
	actual := workspace
	if production {
		actual = started["workspace"].(string)
		if actual == workspace || runtimeCalls.Load() != 1 {
			t.Fatal("production runtime was not exercised")
		}
		if _, err := os.Stat(filepath.Join(actual, ".agent")); !os.IsNotExist(err) {
			t.Fatal("Python bootstrap polluted the prepared task inventory")
		}
		if _, err := os.Stat(filepath.Join(cfg.Sandbox.TrustRoot, ".runtime", "evaluation-memory")); !os.IsNotExist(err) {
			t.Fatal("Go-managed Python created a competing Memory directory")
		}
	}
	if body, err := os.ReadFile(filepath.Join(actual, "result.txt")); err != nil || string(body) != "actual Python transport\n" {
		t.Fatalf("actual Python/Go tool path did not complete: %q %v", body, err)
	}
	if child {
		decoder := json.NewDecoder(strings.NewReader(output.String()))
		var receipt map[string]any
		for decoder.More() {
			if decoder.Decode(&receipt) != nil {
				t.Fatal("invalid child receipt")
			}
		}
		strategy := receipt["strategy_evidence"].(map[string]any)
		ids := strategy["child_session_ids"].([]any)
		if len(ids) != 1 || ids[0] == started["session_id"] || strategy["child_model_call_count"].(float64) < 3 || childCalls.Load() != 3 {
			t.Fatalf("real child execution was not attributable: %+v", strategy)
		}
		usage := receipt["usage"].(map[string]any)
		if usage["call_count"] != float64(calls.Load()) || usage["confirmed_tokens"] != float64(calls.Load()*110) || strings.Contains(output.String(), "fixture-child-cohort") {
			t.Fatal("shared admission accounting or safe provider-session binding was lost")
		}
	}
	runtime := started["product_runtime"].(map[string]any)
	if runtime["orchestrator_startup_seconds"] != float64(30) || runtime["orchestrator_conversation_seconds"] != float64(20) || runtime["context_window"] != float64(256000) {
		t.Fatalf("trusted runtime overrides were not recorded: %+v", runtime)
	}
	if protocol == "openai" {
		if runtime["model_profile"].(map[string]any)["reasoning_effort"] != "xhigh" {
			t.Fatal("runtime did not freeze the Go effort profile")
		}
		ledger, err := session.OpenReadOnlySQLiteEventLog(cfg.SessionDBPath)
		if err != nil {
			t.Fatal(err)
		}
		defer ledger.Close()
		events, err := ledger.Events(ctx, started["session_id"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if streaming {
			binding, err := session.ReadEvaluation(events)
			if err != nil || binding == nil || binding.SchemaVersion != 2 || binding.Runtime.ModelProfile.ResponseMode != "sse" {
				t.Fatal("SSE evaluation binding lost its versioned transport pin")
			}
		}
		observations := 0
		for _, event := range events {
			if event.Type != "evaluation/model-response" {
				continue
			}
			var fact map[string]any
			if json.Unmarshal(event.Payload, &fact) != nil || fact["effective_request_profile"].(map[string]any)["reasoning_effort"] != "xhigh" {
				t.Fatal("model response audit lost the effective request profile")
			}
			if streaming && fact["effective_request_profile"].(map[string]any)["response_mode"] != "sse" {
				t.Fatal("parent or child response lost the SSE request profile")
			}
			observations++
		}
		if observations == 0 {
			t.Fatal("no model response profile was audited")
		}
	}
}

type evaluationTransport struct {
	pb.UnimplementedOrchestratorServer
	memoryResult         chan *pb.ToolResult
	expectedMemory       string
	rejectBeforeDispatch bool
	terminalCode         string
	bashAfterWrite       bool
	onHealth             func()
}

func (fixture evaluationTransport) Health(context.Context, *pb.Empty) (*pb.HealthResponse, error) {
	if fixture.onHealth != nil {
		fixture.onHealth()
	}
	return &pb.HealthResponse{Status: "ok"}, nil
}

func (evaluationTransport) ReflectMemory(_ context.Context, request *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
	return &pb.MemoryReflectionResponse{SourceChecksum: request.SourceChecksum}, nil
}

func (fixture evaluationTransport) Converse(stream grpc.BidiStreamingServer[pb.HarnessMessage, pb.OrchestratorMessage]) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	if fixture.rejectBeforeDispatch {
		return status.Error(codes.InvalidArgument, "fixture-private-upstream-detail")
	}
	if fixture.terminalCode != "" {
		return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: false, ErrorCode: fixture.terminalCode, Message: "fixture-private-upstream-detail"}}})
	}
	if fixture.memoryResult != nil {
		if fixture.expectedMemory == "" && message.GetUserInput().MemoryContextJson != "" {
			return errors.New("Memory-off still pre-injected history")
		}
		if fixture.expectedMemory != "" && (!strings.Contains(message.GetUserInput().MemoryContextJson, fixture.expectedMemory) || strings.Contains(message.GetUserInput().MemoryContextJson, "FUTURE_ANCHOR")) {
			return errors.New("Frozen Memory pre-injection was not pinned")
		}
		for _, call := range []*pb.ToolRequest{{ToolCallId: "history-read", ToolName: "RecallMemory", ParametersJson: `{"query":"prior history"}`}, {ToolCallId: "history-list", ToolName: "ManageMemory", ParametersJson: `{"action":"list"}`}, {ToolCallId: "history-get", ToolName: "ManageMemory", ParametersJson: `{"action":"read","id":"prior"}`}} {
			if err := stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_ToolRequest{ToolRequest: call}}); err != nil {
				return err
			}
			response, err := stream.Recv()
			if err != nil {
				return err
			}
			fixture.memoryResult <- response.GetToolResult()
		}
	}
	binding := message.GetUserInput().GetModelGateway()
	inputHash := sha256.Sum256([]byte(message.GetUserInput().Text))
	connection, err := grpc.NewClient(binding.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer connection.Close()
	response, err := pb.NewModelGatewayClient(connection).Invoke(stream.Context(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: message.GetUserInput().SessionId + "-" + hex.EncodeToString(inputHash[:8]), PayloadJson: []byte(`{"model":"approved-model","messages":[{"role":"user","content":"perform the task"}]}`)})
	if err != nil {
		return err
	}
	if response.ErrorCode != "" {
		return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{ErrorCode: response.ErrorCode}}})
	}
	if err := stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_ToolRequest{ToolRequest: &pb.ToolRequest{ToolCallId: "write-result", ToolName: "Write", ParametersJson: `{"path":"result.txt","content":"product tool completed\n"}`}}}); err != nil {
		return err
	}
	message, err = stream.Recv()
	if err != nil {
		return err
	}
	result := message.GetToolResult()
	if fixture.bashAfterWrite && result.Error == "" {
		if err := stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_ToolRequest{ToolRequest: &pb.ToolRequest{ToolCallId: "public-check", ToolName: "Bash", ParametersJson: `{"command":"go test ./... -count=1"}`}}}); err != nil {
			return err
		}
		message, err = stream.Recv()
		if err != nil {
			return err
		}
		result = message.GetToolResult()
	}
	return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: result.Error == "" && result.ExitCode == 0, Message: "task finished"}}})
}

func TestEvaluationReportsSafePreDispatchDiagnostic(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprint("terminal=", terminal), func(t *testing.T) { testEvaluationPreDispatchDiagnostic(t, terminal) })
	}
}

func testEvaluationPreDispatchDiagnostic(t *testing.T, terminal bool) {
	t.Setenv("CODE_AGENT_SESSION_LEDGER_PATH", "")
	t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "Read,Write,Edit")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "approved-model")
	t.Setenv("OPENAI_API_KEY", "fixture-only")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "200")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "20")
	t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	fixture := evaluationTransport{rejectBeforeDispatch: !terminal}
	if terminal {
		fixture.terminalCode = "model_payload_mismatch"
	}
	pb.RegisterOrchestratorServer(server, fixture)
	go server.Serve(listener)
	defer server.Stop()
	cfg := config.Default(t.TempDir())
	cfg.OrchestratorAddr = listener.Addr().String()
	cfg.Sandbox.Enabled = false
	cfg.OrchestratorAutoStart = false
	cfg.RAGEnabled = false
	cfg.MCPConfig = "missing.json"
	workspace := t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "initial"}} {
		if body, err := exec.Command("git", append([]string{"-C", workspace}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, body)
		}
	}
	request, _ := json.Marshal(map[string]any{"schema_version": 1, "run_id": "pre-dispatch", "task_id": "unstarted-task", "workspace": workspace, "message": "Perform the task", "model": "approved-model", "budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 15}})
	var output strings.Builder
	if err := cli.RunEvaluation(context.Background(), cfg, strings.NewReader(string(request)), &output, io.Discard); err == nil {
		t.Fatal("pre-dispatch transport failure was accepted")
	}
	var started, receipt map[string]any
	decoder := json.NewDecoder(strings.NewReader(output.String()))
	if err := decoder.Decode(&started); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&receipt); err != nil {
		t.Fatal(err)
	}
	wantCode, wantStage, wantStatus := "orchestrator_pre_dispatch_failed", "orchestrator_receive", "InvalidArgument"
	if terminal {
		wantCode, wantStage, wantStatus = "model_payload_mismatch", "orchestrator_terminal", "Unknown"
	}
	if receipt["task_state"] != "infra_error" || receipt["error_code"] != wantCode {
		t.Fatalf("wrong failure classification: %s", output.String())
	}
	diagnostic, _ := receipt["diagnostic"].(map[string]any)
	if diagnostic["stage"] != wantStage || diagnostic["grpc_status"] != wantStatus {
		t.Fatalf("missing safe diagnostic: %+v", diagnostic)
	}
	if terminal && diagnostic["gateway_grpc_status"] != "InvalidArgument" {
		t.Fatalf("gateway status lost: %+v", diagnostic)
	}
	if receipt["usage"].(map[string]any)["call_count"] != float64(0) {
		t.Fatal("a model call was recorded before dispatch")
	}
	if strings.Contains(output.String(), "fixture-private-upstream-detail") {
		t.Fatal("raw upstream error leaked into protocol")
	}
	var inspection map[string]any
	if json.Unmarshal(request, &inspection) != nil {
		t.Fatal("invalid fixture request")
	}
	inspection["operation"], inspection["session_id"] = "inspect", receipt["session_id"]
	inspectionInput, _ := json.Marshal(inspection)
	var inspectionOutput strings.Builder
	if err := cli.RunEvaluation(context.Background(), cfg, strings.NewReader(string(inspectionInput)), &inspectionOutput, io.Discard); err != nil {
		t.Fatalf("successful read replayed the historical run failure: %v", err)
	}
	inspectionDecoder := json.NewDecoder(strings.NewReader(inspectionOutput.String()))
	var ignored, inspected map[string]any
	if inspectionDecoder.Decode(&ignored) != nil || inspectionDecoder.Decode(&inspected) != nil || inspected["error_code"] != "" || inspected["run_error_code"] != wantCode || inspected["run_state"] != "failed" {
		t.Fatal("inspection lost the historical failure or failed the control read")
	}
	ledger, err := session.OpenSQLiteEventLog(cfg.SessionDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	events, err := ledger.Events(context.Background(), receipt["session_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.Contains(string(event.Payload), "fixture-private-upstream-detail") {
			t.Fatal("raw upstream error leaked into Ledger")
		}
	}
}

func TestEvaluationRunsProductToolsAndKeepsCanonicalLedger(t *testing.T) {
	t.Setenv("CODE_AGENT_SESSION_LEDGER_PATH", "")
	t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "Read,Write,Edit")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
	var providerCalls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-only" {
			t.Error("Go gateway did not own credentials")
		}
		io.WriteString(w, `{"id":"fixture-response","model":"approved-model","choices":[{"message":{"role":"assistant","content":"continue"}}],"usage":{"prompt_tokens":100,"completion_tokens":5}}`)
	}))
	defer provider.Close()
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "approved-model")
	t.Setenv("OPENAI_BASE_URL", provider.URL)
	t.Setenv("OPENAI_API_KEY", "fixture-only")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "200")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "20")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	memoryResult := make(chan *pb.ToolResult, 6)
	pb.RegisterOrchestratorServer(server, evaluationTransport{memoryResult: memoryResult})
	go server.Serve(listener)
	defer server.Stop()
	cfg := config.Default(t.TempDir())
	cfg.OrchestratorAddr = listener.Addr().String()
	cfg.Sandbox.Enabled = false
	cfg.OrchestratorAutoStart = false
	cfg.RAGEnabled = false
	cfg.MCPConfig = "missing.json"
	workspace := t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "initial"}} {
		if data, err := exec.Command("git", append([]string{"-C", workspace}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, data)
		}
	}
	request, _ := json.Marshal(map[string]any{"schema_version": 1, "run_id": "integration-1", "task_id": "write-result", "workspace": workspace, "message": "Write result.txt", "model": "approved-model", "budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 15}})
	var output strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := cli.RunEvaluation(ctx, cfg, strings.NewReader(string(request)), &output, io.Discard); err != nil {
		t.Fatalf("product trial failed: %v %s", err, output.String())
	}
	var started, receipt map[string]any
	decoder := json.NewDecoder(strings.NewReader(output.String()))
	if err := decoder.Decode(&started); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&receipt); err != nil {
		t.Fatal(err)
	}
	if started["type"] != "started" || receipt["type"] != "receipt" || receipt["task_state"] != "completed" {
		t.Fatalf("unexpected protocol: %s", output.String())
	}
	if body, err := os.ReadFile(filepath.Join(workspace, "result.txt")); err != nil || string(body) != "product tool completed\n" {
		t.Fatalf("real tool result missing: %q %v", body, err)
	}
	ledgerInfo := receipt["ledger"].(map[string]any)
	if ledgerInfo["path"] != cfg.SessionDBPath {
		t.Fatal("trial replaced the canonical Ledger")
	}
	ledger, err := session.OpenSQLiteEventLog(cfg.SessionDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if err := ledger.Verify(ctx, receipt["session_id"].(string)); err != nil {
		t.Fatal(err)
	}
	usage := receipt["usage"].(map[string]any)
	if usage["confirmed_tokens"] != float64(105) || usage["cost_status"] != "unknown" {
		t.Fatalf("usage was not attributable: %+v", usage)
	}
	memoryEvidence := receipt["execution_evidence"].(map[string]any)["memory_reads"].(map[string]any)
	if memoryEvidence["read_count"] != float64(0) || memoryEvidence["injected_count"] != float64(0) || memoryEvidence["returned_bytes"] != float64(0) {
		t.Fatalf("off-arm performed historical reads: %+v", memoryEvidence)
	}
	if strings.Contains(output.String(), "fixture-only") {
		t.Fatal("credentials escaped into protocol")
	}
	for i := 0; i < 3; i++ {
		select {
		case result := <-memoryResult:
			if result.Error == "" {
				t.Fatal("Memory-off permitted explicit historical reading")
			}
		default:
			t.Fatal("historical read channel was not exercised")
		}
	}
	var inspectRequest map[string]any
	if err := json.Unmarshal(request, &inspectRequest); err != nil {
		t.Fatal(err)
	}
	inspectRequest["operation"] = "inspect"
	inspectRequest["session_id"] = receipt["session_id"]
	inspectInput, _ := json.Marshal(inspectRequest)
	var inspected strings.Builder
	if err := cli.RunEvaluation(ctx, cfg, strings.NewReader(string(inspectInput)), &inspected, io.Discard); err != nil {
		t.Fatalf("public inspect failed: %v", err)
	}
	inspectDecoder := json.NewDecoder(strings.NewReader(inspected.String()))
	var inspectStarted, inspectReceipt map[string]any
	if err := inspectDecoder.Decode(&inspectStarted); err != nil {
		t.Fatal(err)
	}
	if err := inspectDecoder.Decode(&inspectReceipt); err != nil {
		t.Fatal(err)
	}
	inspectedLedger := inspectReceipt["ledger"].(map[string]any)
	if inspectedLedger["verified"] != true || inspectedLedger["head_checksum"] != ledgerInfo["head_checksum"] || inspectedLedger["event_count"] != ledgerInfo["event_count"] {
		t.Fatal("inspect did not verify the same immutable Ledger boundary")
	}
	limitedWorkspace := t.TempDir()
	if data, err := exec.Command("git", "clone", "--no-hardlinks", workspace, limitedWorkspace).CombinedOutput(); err != nil {
		t.Fatalf("clone budget fixture: %v %s", err, data)
	}
	inspectRequest["operation"] = "run"
	delete(inspectRequest, "session_id")
	inspectRequest["run_id"] = "budget-1"
	inspectRequest["workspace"] = limitedWorkspace
	inspectRequest["budget"] = map[string]any{"max_tokens": 100, "wall_clock_seconds": 15}
	limitedInput, _ := json.Marshal(inspectRequest)
	var limited strings.Builder
	if err := cli.RunEvaluation(ctx, cfg, strings.NewReader(string(limitedInput)), &limited, io.Discard); err == nil {
		t.Fatal("a call larger than the remaining trial budget was dispatched")
	}
	if providerCalls.Load() != 1 || !strings.Contains(limited.String(), `"error_code":"token_budget_exceeded"`) {
		t.Fatalf("budget was not refused before upstream dispatch: %s", limited.String())
	}
	if _, err := os.Stat(filepath.Join(limitedWorkspace, "result.txt")); !os.IsNotExist(err) {
		t.Fatal("budget-exhausted trial executed a mutating tool")
	}

	// Build attributable prior experience through the public Memory API, then
	// advance its live source before exercising the frozen evaluation reader.
	store := session.NewSQLiteEventStore(cfg.SessionDBPath)
	manager := session.NewManager(store)
	source := manager.NewSession(workspace)
	sourceActor, err := identity.Default().BindSession(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetActor(sourceActor)
	manager.Append(session.RoleUser, "prior history FROZEN_ANCHOR")
	sourceLedger, err := store.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	module := memory.NewLedgerMemory(sourceLedger)
	if err := module.CommitCLIOutcome(ctx, source.ID, "prior history FROZEN_ANCHOR"); err != nil {
		t.Fatal(err)
	}
	frozenSnapshot, err := session.ReadVerifiedSnapshot(ctx, sourceLedger, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	pin := frozenSnapshot.Events[len(frozenSnapshot.Events)-1]
	manager.Append(session.RoleUser, "prior history FUTURE_ANCHOR")
	if err := module.CommitCLIOutcome(ctx, source.ID, "prior history FUTURE_ANCHOR"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	frozenListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	frozenServer := grpc.NewServer()
	frozenResults := make(chan *pb.ToolResult, 3)
	pb.RegisterOrchestratorServer(frozenServer, evaluationTransport{memoryResult: frozenResults, expectedMemory: "FROZEN_ANCHOR"})
	go frozenServer.Serve(frozenListener)
	defer frozenServer.Stop()
	frozenCfg := cfg
	frozenCfg.OrchestratorAddr = frozenListener.Addr().String()
	t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "Read,Write,Edit,RecallMemory")
	frozenWorkspace := t.TempDir()
	if data, err := exec.Command("git", "clone", "--no-hardlinks", workspace, frozenWorkspace).CombinedOutput(); err != nil {
		t.Fatalf("clone Frozen Memory fixture: %v %s", err, data)
	}
	inspectRequest["run_id"] = "frozen-1"
	inspectRequest["workspace"] = frozenWorkspace
	inspectRequest["message"] = "Write result.txt using prior history"
	inspectRequest["budget"] = map[string]any{"max_tokens": 1000, "wall_clock_seconds": 15}
	inspectRequest["policy"] = map[string]any{"context_strategy": "metadata_on_demand", "memory_read": true, "execution_strategy": "single", "frozen_memory": []map[string]string{{"session_id": source.ID, "event_id": pin.EventID, "checksum": pin.Checksum}}}
	frozenInput, _ := json.Marshal(inspectRequest)
	var frozenOutput strings.Builder
	if err := cli.RunEvaluation(ctx, frozenCfg, strings.NewReader(string(frozenInput)), &frozenOutput, io.Discard); err != nil {
		t.Fatalf("frozen trial failed: %v %s", err, frozenOutput.String())
	}
	result := <-frozenResults
	if result.Error != "" || !strings.Contains(result.Output, "FROZEN_ANCHOR") || strings.Contains(result.Output, "FUTURE_ANCHOR") {
		t.Fatalf("historical read escaped its pinned prefix: %+v", result)
	}
}
