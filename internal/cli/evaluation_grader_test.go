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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/safety"
	"google.golang.org/grpc"
)

func TestEvaluationProductionGradeUsesIndependentGoCheckerAndCanonicalResult(t *testing.T) {
	testEvaluationProductionGrade(t, evaluationGradeScenario{})
}

func TestEvaluationProductionGradeAcceptsEquivalentWindowsRootSeparators(t *testing.T) {
	testEvaluationProductionGrade(t, evaluationGradeScenario{slashRoot: true})
}

func TestEvaluationProductionGradeStreamsBeforeSlowCheckerCompletes(t *testing.T) {
	testEvaluationProductionGrade(t, evaluationGradeScenario{checkerDelay: 11 * time.Second})
}

func TestEvaluationProductionGradeAcceptsCompletedCandidateWithoutChanges(t *testing.T) {
	testEvaluationProductionGrade(t, evaluationGradeScenario{noChanges: true})
}

func TestEvaluationProductionGradeRecordsFailedAssertionWithoutChanges(t *testing.T) {
	testEvaluationProductionGrade(t, evaluationGradeScenario{noChanges: true, wrongAssertion: true})
}

func TestEvaluationProductionGradeRecordsFailureForEmptyCandidate(t *testing.T) {
	testEvaluationProductionGrade(t, evaluationGradeScenario{noChanges: true, wrongAssertion: true, emptyRepository: true})
}

func TestEvaluationProductionGradeRejectsUnapprovedToolWithoutWaiting(t *testing.T) {
	testEvaluationProductionGrade(t, evaluationGradeScenario{noChanges: true, unapprovedTool: "Skill"})
}

type evaluationGradeScenario struct {
	unapprovedTool                                        string
	slashRoot, noChanges, wrongAssertion, emptyRepository bool
	checkerDelay                                          time.Duration
}

func testEvaluationProductionGrade(t *testing.T, scenario evaluationGradeScenario) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("production protocol fixture uses admitted Windows paths")
	}
	for key, value := range map[string]string{
		"CODE_AGENT_SESSION_LEDGER_PATH": "", "CODE_AGENT_EVAL_ALLOW_TOOLS": "Read,Write", "CODE_AGENT_EVAL_VISIBLE_VERIFICATION_COMMANDS_JSON": "[]",
		"OTEL_EXPORTER_OTLP_ENDPOINT": ":invalid", "LLM_PROVIDER": "openai", "OPENAI_MODEL": "approved-model", "OPENAI_API_KEY": "fixture-only",
		"CODE_AGENT_MODEL_INPUT_LIMIT": "200", "CODE_AGENT_MODEL_OUTPUT_LIMIT": "20", "CODE_AGENT_MODEL_TOTAL_LIMIT": "", "CODE_AGENT_MODEL_REASONING_EFFORT": "", "CODE_AGENT_MODEL_REPORTED_MODEL": "",
	} {
		t.Setenv(key, value)
	}
	var modelCalls, runtimeCalls, checkerCalls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		io.WriteString(w, `{"model":"approved-model","usage":{"prompt_tokens":100,"completion_tokens":5}}`)
	}))
	defer provider.Close()
	t.Setenv("OPENAI_BASE_URL", provider.URL)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterOrchestratorServer(server, evaluationTransport{skipWrite: scenario.noChanges, unapprovedTool: scenario.unapprovedTool})
	go server.Serve(listener)
	defer server.Stop()
	source, storage := productionRepository(t), t.TempDir()
	if scenario.noChanges && !scenario.emptyRepository {
		if err := os.WriteFile(filepath.Join(source, "baseline.txt"), []byte("baseline preserved\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "baseline.txt"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "baseline"}} {
			if body, err := exec.Command("git", append([]string{"-C", source}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git baseline: %v %s", err, body)
			}
		}
	}
	cfg := config.Default(t.TempDir())
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.MCPConfig = listener.Addr().String(), false, "missing.json"
	// Only the fixed trusted checker executes in this local protocol fixture.
	// Real OpenSandbox and provider acceptance are separate runtime validations.
	cfg.Sandbox = productionRuntimeFixture(t, storage, &runtimeCalls, func(workspace string) string {
		checkerCalls.Add(1)
		if scenario.checkerDelay > 0 {
			time.Sleep(scenario.checkerDelay)
		}
		command := exec.Command("go", "run", "grader.go")
		command.Dir, command.Env = workspace, safety.ScrubEnvironment(os.Environ())
		body, err := command.CombinedOutput()
		if err != nil {
			t.Errorf("trusted checker fixture failed: %v %s", err, body)
		}
		return string(body)
	})
	if scenario.slashRoot {
		// Config JSON commonly uses forward slashes; canonical preparation
		// resolves the same approved Windows root with native separators.
		cfg.Sandbox.TrustRoot = filepath.ToSlash(cfg.Sandbox.TrustRoot)
	}
	request := map[string]any{"schema_version": 1, "runtime_contract": "production_opensandbox", "run_id": "production-grader-fixture", "task_id": "write-and-grade", "workspace": source,
		"message": "Write result.txt using the authorized tool", "model": "approved-model", "lifecycle": "hold", "turn_id": "turn-1", "budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 90}}
	if scenario.noChanges {
		request["message"] = "Acknowledge the task without modifying any file"
	}
	if scenario.unapprovedTool != "" {
		request["budget"] = map[string]any{"max_tokens": 1000, "wall_clock_seconds": 20}
	}
	call := func() map[string]any {
		input, _ := json.Marshal(request)
		var output strings.Builder
		if err := cli.RunEvaluation(context.Background(), cfg, strings.NewReader(string(input)), &output, io.Discard); err != nil {
			t.Fatalf("public production control failed: %v %s", err, output.String())
		}
		var receipt map[string]any
		decoder := json.NewDecoder(strings.NewReader(output.String()))
		for {
			var event map[string]any
			if decoder.Decode(&event) != nil {
				break
			}
			if event["type"] == "receipt" {
				receipt = event
			}
		}
		return receipt
	}
	run := call()
	if run["task_state"] != "completed" || run["admission_finalized"] != false || modelCalls.Load() != 1 {
		t.Fatal("model candidate was not retained under hold")
	}
	checks := []byte(`{"schema_version":1,"files":[{"path":"result.txt","contains":"product tool completed"}]}`)
	if scenario.noChanges {
		checks = []byte(`{"schema_version":1,"files":[{"path":"baseline.txt","contains":"baseline preserved"}]}`)
	}
	if scenario.wrongAssertion {
		checks = []byte(`{"schema_version":1,"files":[{"path":"baseline.txt","contains":"private incorrect hidden expectation"}]}`)
	}
	checksPath := filepath.Join(t.TempDir(), "operator-hidden-checks.json")
	if err := os.WriteFile(checksPath, checks, 0600); err != nil {
		t.Fatal(err)
	}
	checksHash := sha256.Sum256(checks)
	modelHead := run["ledger"].(map[string]any)["head_checksum"]
	request["operation"], request["session_id"] = "grade", run["session_id"]
	request["expected_session_head_checksum"] = modelHead
	request["grade_checks_path"], request["grade_checks_sha256"] = checksPath, hex.EncodeToString(checksHash[:])
	graded := call()
	grade := graded["grade"].(map[string]any)
	if graded["task_state"] != "graded" || graded["error_code"] != "" || grade["state"] != "completed" || grade["passed"] != !scenario.wrongAssertion || grade["stopped"] != true || grade["exit_code"] != float64(0) || grade["checks_total"] != float64(1) || checkerCalls.Load() != 1 || modelCalls.Load() != 1 {
		t.Fatalf("independent Go grading did not complete: %+v", graded)
	}
	if graded["turn_id"] != "turn-1" || graded["opensandbox"] == nil || graded["workspace"] != run["workspace"] || graded["source_workspace"] != source || graded["task_workspace"].(map[string]any)["verified"] != true || graded["task_workspace"].(map[string]any)["admission_ready"] != false {
		t.Fatal("grade control lost its frozen product identity")
	}
	ledger := graded["ledger"].(map[string]any)
	if ledger["head_event_id"] != grade["result_event_id"] || ledger["head_checksum"] != grade["result_event_checksum"] {
		t.Fatal("grade does not identify its native canonical result head")
	}
	encoded, _ := json.Marshal(graded)
	if strings.Contains(string(encoded), checksPath) || strings.Contains(string(encoded), "product tool completed") || strings.Contains(string(encoded), "private incorrect hidden expectation") || strings.Contains(string(encoded), "contains") {
		t.Fatal("hidden assertions leaked into ordinary control output")
	}
	beforeRuntime := runtimeCalls.Load()
	again := call()
	if again["grade"].(map[string]any)["already_recorded"] != true || again["ledger"].(map[string]any)["head_checksum"] != ledger["head_checksum"] || runtimeCalls.Load() != beforeRuntime || checkerCalls.Load() != 1 || modelCalls.Load() != 1 {
		t.Fatal("duplicate grade invoked the runtime or model")
	}
	delete(request, "grade_checks_path")
	delete(request, "grade_checks_sha256")
	delete(request, "expected_session_head_checksum")
	request["operation"] = "inspect"
	inspected := call()
	if inspected["ledger"].(map[string]any)["head_checksum"] != ledger["head_checksum"] || runtimeCalls.Load() != beforeRuntime || modelCalls.Load() != 1 {
		t.Fatal("independent inspection changed grading evidence")
	}
	request["operation"], request["finalize_mode"] = "finalize", "finish"
	request["expected_session_head_checksum"] = ledger["head_checksum"]
	finalized := call()
	if finalized["task_state"] != "finalized" || finalized["admission_finalized"] != true || checkerCalls.Load() != 1 || modelCalls.Load() != 1 {
		t.Fatal("known stopped grader could not finish its retained task")
	}
	if scenario.noChanges {
		workspace := run["workspace"].(string)
		if !scenario.emptyRepository {
			if body, err := os.ReadFile(filepath.Join(workspace, "baseline.txt")); err != nil || string(body) != "baseline preserved\n" {
				t.Fatal("grading changed the candidate baseline")
			}
		}
		if _, err := os.Stat(filepath.Join(workspace, "result.txt")); !os.IsNotExist(err) {
			t.Fatal("grading manufactured a file change")
		}
	}
}
