package cli_test

import (
	"bufio"
	"bytes"
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
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/permission"
	"code-agent/internal/safety"
	"code-agent/internal/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type productionRecoveryTransport struct{ evaluationFileTransport }

type recoveryResultFault struct{ session.EventLog }

func (f recoveryResultFault) AppendSurface(ctx context.Context, id string, seq int64, kind string, payload any, op session.SurfaceOperation) (session.Event, error) {
	if kind == "tool/result" {
		return session.Event{}, errors.New("fixture stopped after the reconciled code projection")
	}
	return f.EventLog.AppendSurface(ctx, id, seq, kind, payload, op)
}

type recoveryPrefetchedStream struct {
	grpc.BidiStreamingServer[pb.HarnessMessage, pb.OrchestratorMessage]
	first *pb.HarnessMessage
}

func (s *recoveryPrefetchedStream) Recv() (*pb.HarnessMessage, error) {
	if s.first != nil {
		first := s.first
		s.first = nil
		return first, nil
	}
	return s.BidiStreamingServer.Recv()
}

func (f productionRecoveryTransport) Converse(stream grpc.BidiStreamingServer[pb.HarnessMessage, pb.OrchestratorMessage]) error {
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
	response, err := pb.NewModelGatewayClient(connection).Invoke(stream.Context(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "recovery-fixture-" + hex.EncodeToString(callHash[:8]), PayloadJson: []byte(`{"model":"approved-model","messages":[{"role":"user","content":"perform the fixture task"}]}`)})
	if err != nil {
		return err
	}
	if response.ErrorCode != "" {
		return fmt.Errorf("fixture model admission: %s", response.ErrorCode)
	}
	return f.evaluationFileTransport.Converse(&recoveryPrefetchedStream{BidiStreamingServer: stream, first: first})
}

func TestEvaluationProductionRecoveryProcessHelper(t *testing.T) {
	root := os.Getenv("CODE_AGENT_PRODUCTION_RECOVERY_FIXTURE_ROOT")
	if root == "" {
		t.Skip("owned subprocess protocol fixture")
	}
	cfg := config.Default(root)
	cfg.SessionDBPath = os.Getenv("CODE_AGENT_PRODUCTION_RECOVERY_FIXTURE_LEDGER")
	cfg.OrchestratorAddr = os.Getenv("CODE_AGENT_PRODUCTION_RECOVERY_FIXTURE_ORCHESTRATOR")
	cfg.OrchestratorAutoStart, cfg.RAGEnabled, cfg.MCPConfig = false, false, "missing.json"
	if err := json.Unmarshal([]byte(os.Getenv("CODE_AGENT_PRODUCTION_RECOVERY_FIXTURE_SANDBOX_JSON")), &cfg.Sandbox); err != nil {
		os.Exit(3)
	}
	if err := cli.RunEvaluation(context.Background(), cfg, os.Stdin, os.Stdout, os.Stderr); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestEvaluationProductionStoppedWriteContinuesSameSessionAndGrades(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production file recovery uses admitted Windows pins")
	}
	for _, window := range []string{"after_effect", "after_record", "before_effect"} {
		t.Run(window, func(t *testing.T) { testProductionStoppedWriteAndGrade(t, window, "") })
	}
}

func TestEvaluationProductionStoppedWriteProjectionGuards(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production file recovery uses admitted Windows pins")
	}
	for _, mode := range []string{"partial_projection", "historical_result_only"} {
		t.Run(mode, func(t *testing.T) { testProductionStoppedWriteAndGrade(t, "after_effect", mode) })
	}
}

func testProductionStoppedWriteAndGrade(t *testing.T, window, projectionMode string) {
	t.Helper()
	for key, value := range map[string]string{
		"CODE_AGENT_SESSION_LEDGER_PATH": "", "CODE_AGENT_EVAL_ALLOW_TOOLS": "Read,Write", "CODE_AGENT_EVAL_VISIBLE_VERIFICATION_COMMANDS_JSON": "[]",
		"OTEL_EXPORTER_OTLP_ENDPOINT": ":invalid", "LLM_PROVIDER": "openai", "OPENAI_MODEL": "approved-model", "OPENAI_API_KEY": "fixture-only",
		"CODE_AGENT_MODEL_INPUT_LIMIT": "200", "CODE_AGENT_MODEL_OUTPUT_LIMIT": "20", "CODE_AGENT_MODEL_TOTAL_LIMIT": "", "CODE_AGENT_MODEL_REASONING_EFFORT": "", "CODE_AGENT_MODEL_REPORTED_MODEL": "",
	} {
		t.Setenv(key, value)
	}
	var modelCalls, runtimeCalls, graderCalls atomic.Int64
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
	pb.RegisterOrchestratorServer(server, productionRecoveryTransport{})
	go server.Serve(listener)
	defer server.Stop()
	source, storage, root := productionRepository(t), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module fixture\n\ngo 1.23\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "artifact_test.go"), []byte("package fixture\nimport(\"os\";\"testing\")\nfunc TestArtifact(t *testing.T){if _,err:=os.Stat(\"effect.txt\");err!=nil{t.Fatal(err)}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(root, "fixture.sqlite")
	sandboxConfig := productionRuntimeFixture(t, storage, &runtimeCalls, func(workspace string) string {
		// This protocol fixture executes only the fixed trusted Go checker.
		// Real OpenSandbox/provider validation is a separate root-owned run.
		graderCalls.Add(1)
		command := exec.Command("go", "run", "grader.go")
		command.Dir, command.Env = workspace, safety.ScrubEnvironment(os.Environ())
		body, err := command.CombinedOutput()
		if err != nil {
			t.Errorf("trusted checker fixture failed: %v %s", err, body)
		}
		return string(body)
	})
	sandboxJSON, _ := json.Marshal(sandboxConfig)
	t.Setenv("CODE_AGENT_PRODUCTION_RECOVERY_FIXTURE_ROOT", root)
	t.Setenv("CODE_AGENT_PRODUCTION_RECOVERY_FIXTURE_LEDGER", ledgerPath)
	t.Setenv("CODE_AGENT_PRODUCTION_RECOVERY_FIXTURE_ORCHESTRATOR", listener.Addr().String())
	t.Setenv("CODE_AGENT_PRODUCTION_RECOVERY_FIXTURE_SANDBOX_JSON", string(sandboxJSON))
	request := map[string]any{"schema_version": 1, "runtime_contract": "production_opensandbox", "run_id": "production-recovery-" + window, "task_id": "create-and-grade", "workspace": source,
		"message": "Create effect.txt using the authorized Write tool", "model": "approved-model", "lifecycle": "hold", "turn_id": "turn-1",
		"budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 180},
		"policy": map[string]any{"context_strategy": "metadata_on_demand", "execution_strategy": "single", "memory_read": false, "max_tool_rounds": 8, "context_max_bytes": 12000, "fault_window": window, "fault_tool": "Write"}}
	input, _ := json.Marshal(request)
	ctx, cancel := context.WithTimeout(context.Background(), 210*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEvaluationProductionRecoveryProcessHelper$")
	child.Stdin = bytes.NewReader(input)
	var diagnostics bytes.Buffer
	child.Stderr = &diagnostics
	stdout, err := child.StdoutPipe()
	if err != nil || child.Start() != nil {
		t.Fatal("owned evaluation process did not start")
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	var boundary, started map[string]any
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event["type"] == "started" {
			started = event
		}
		if event["type"] == "fault_ready" {
			boundary = event
			break
		}
	}
	if boundary == nil || started == nil {
		t.Fatalf("no public fault boundary: %s", diagnostics.String())
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	read := func() session.LedgerSnapshot {
		ledger, err := session.OpenReadOnlySQLiteEventLog(ledgerPath)
		if err != nil {
			t.Fatal(err)
		}
		defer ledger.Close()
		snapshot, err := session.ReadVerifiedSnapshot(context.Background(), ledger, boundary["session_id"].(string))
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	call := func() map[string]any {
		input, _ := json.Marshal(request)
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEvaluationProductionRecoveryProcessHelper$")
		command.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		body, err := command.Output()
		if err != nil {
			t.Fatalf("production control failed: %v %s %s", err, body, stderr.String())
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
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
		return receipt
	}
	before := read()
	actual := started["workspace"].(string)
	if actual == source || !strings.HasPrefix(actual, storage+string(filepath.Separator)) {
		t.Fatal("fault did not occur in a retained production copy")
	}
	if projectionMode != "" {
		ledger, err := session.OpenSQLiteEventLog(ledgerPath)
		if err != nil {
			t.Fatal(err)
		}
		defer ledger.Close()
		actor, err := identity.Default().BindSession(boundary["session_id"].(string))
		if err != nil {
			t.Fatal(err)
		}
		command := session.EvaluationWriteReconcileCommand{Actor: actor, SessionID: actor.SessionID, RunID: boundary["product_run_id"].(string), ToolCallID: "write-file", ExpectedSessionHeadChecksum: before.Events[len(before.Events)-1].Checksum}
		reconcile := func(log session.EventLog) (session.EvaluationWriteReconciliation, error) {
			runner := session.NewSessionRunner(session.NewWorkbench(log, nil), nil, nil, session.SessionRunnerOptions{Permissions: permission.NewController(nil, nil)})
			defer runner.Close()
			return runner.ReconcileEvaluationWrite(context.Background(), command)
		}
		if projectionMode == "partial_projection" {
			if _, err := reconcile(recoveryResultFault{ledger}); err == nil {
				t.Fatal("fixture did not stop the partial projection")
			}
			partial := read()
			codes, results := 0, 0
			for _, event := range partial.Events {
				if event.Type == "code/modified" {
					codes++
				}
				if event.Type == "tool/result" {
					results++
				}
			}
			if codes != 1 || results != 0 {
				t.Fatal("partial projection did not retain exactly the code fact")
			}
			command.ExpectedSessionHeadChecksum = partial.Events[len(partial.Events)-1].Checksum
			result, err := reconcile(ledger)
			if err != nil || !result.AlreadyRecorded || !result.ToolResultRecorded || !result.EffectKnown || result.ReplayAllowed || result.PhysicalExecutionCount != nil {
				t.Fatalf("partial projection did not resume without replay: %+v %v", result, err)
			}
			before = read()
			command.ExpectedSessionHeadChecksum = before.Events[len(before.Events)-1].Checksum
			if _, err := reconcile(ledger); err != nil || len(read().Events) != len(before.Events) {
				t.Fatal("closed reconciliation was not idempotent")
			}
		} else {
			// Explicit legacy-history fixture: the old projection recorded a
			// successful result without the required preceding code transition.
			var preimage, pending session.Event
			var retained map[string]any
			for _, event := range before.Events {
				if event.Type == "evaluation/write-preimage" {
					preimage = event
					_ = json.Unmarshal(event.Payload, &retained)
				}
				if event.Type == "commit_pending" {
					pending = event
				}
			}
			if preimage.EventID == "" || pending.EventID == "" {
				t.Fatal("no retained preimage/pending call for the historical fixture")
			}
			proof := map[string]any{"schema_version": 1, "run_id": command.RunID, "tool_call_id": command.ToolCallID, "state": "applied", "source_head_checksum": command.ExpectedSessionHeadChecksum,
				"preimage_event_id": preimage.EventID, "preimage_checksum": preimage.Checksum, "pending_event_id": pending.EventID, "pending_checksum": pending.Checksum,
				"arguments_sha256": retained["arguments_sha256"], "actor": retained["actor"], "stopped_process": retained["process"], "physical_execution_count": nil,
				"observed": map[string]any{"exists": true, "sha256": retained["content_sha256"], "size": retained["content_size"]}}
			if _, err := ledger.Append(context.Background(), actor.SessionID, int64(len(before.Events)), "evaluation/write-reconciled", proof); err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.AppendSurface(context.Background(), actor.SessionID, int64(len(before.Events)+1), "tool/result", map[string]any{"run_id": command.RunID, "tool_call_id": command.ToolCallID, "tool_name": "Write", "output": "legacy result-only repair", "stopped": true}, session.SurfaceOperation{Op: "append"}); err != nil {
				t.Fatal(err)
			}
			historical := read()
			command.ExpectedSessionHeadChecksum = historical.Events[len(historical.Events)-1].Checksum
			if _, err := reconcile(ledger); !errors.Is(err, session.ErrSessionStateConflict) || len(read().Events) != len(historical.Events) {
				t.Fatalf("historical result-only ordering was silently tail-repaired: %v", err)
			}
			if graderCalls.Load() != 0 || modelCalls.Load() != 1 {
				t.Fatal("blocked historical fixture dispatched continuation/grading")
			}
			return
		}
	}
	request["session_id"], request["operation"] = boundary["session_id"], "continue"
	request["expected_session_head_checksum"] = before.Events[len(before.Events)-1].Checksum
	continued := call()
	if continued["task_state"] != "completed" || continued["product_run_id"] != boundary["product_run_id"] || continued["workspace"] != actual || continued["task_workspace"].(map[string]any)["admission_ready"] != true {
		t.Fatalf("stopped Write continuation: state=%v code=%v diagnostic=%v run=%v original_run=%v workspace=%v expected=%v ready=%v", continued["task_state"], continued["error_code"], continued["diagnostic"], continued["product_run_id"], boundary["product_run_id"], continued["workspace"], actual, continued["task_workspace"].(map[string]any)["admission_ready"])
	}
	after := read()
	codeSeq, resultSeq, dispatchSeq := int64(-1), int64(-1), int64(-1)
	writes, codes, proofs := 0, 0, 0
	for _, event := range after.Events {
		var value map[string]any
		_ = json.Unmarshal(event.Payload, &value)
		if event.Type == "tool/dispatched" && value["tool_name"] == "Write" {
			writes++
		}
		if value["tool_call_id"] == "write-file" {
			switch event.Type {
			case "tool/dispatched":
				dispatchSeq = event.Seq
			case "code/modified":
				codeSeq = event.Seq
			case "tool/result":
				resultSeq = event.Seq
			}
		}
		if event.Type == "code/modified" {
			codes++
		}
		if event.Type == "evaluation/write-reconciled" {
			proofs++
			if value["physical_execution_count"] != nil {
				t.Fatal("file-state proof invented a physical execution count")
			}
		}
	}
	wantWrites, wantProofs := 1, 1
	if window == "before_effect" {
		wantWrites = 2
	} else if window == "after_record" {
		wantProofs = 0
	}
	if writes != wantWrites || codes != 1 || proofs != wantProofs || window != "before_effect" && !(dispatchSeq < codeSeq && codeSeq < resultSeq) {
		t.Fatalf("missing/misordered/duplicated code projection: writes=%d codes=%d proofs=%d seq=%d/%d/%d", writes, codes, proofs, dispatchSeq, codeSeq, resultSeq)
	}
	if body, err := os.ReadFile(filepath.Join(actual, "effect.txt")); err != nil || string(body) != reconcileFixtureContent {
		t.Fatal("recovery did not preserve the proven file content")
	}
	if _, err := os.Stat(filepath.Join(source, "effect.txt")); !os.IsNotExist(err) {
		t.Fatal("recovery changed the operator source repository")
	}
	checks := []byte(`{"schema_version":1,"files":[{"path":"effect.txt","contains":"op-017 applied"}]}`)
	checksPath := filepath.Join(t.TempDir(), "hidden-checks.json")
	if err := os.WriteFile(checksPath, checks, 0600); err != nil {
		t.Fatal(err)
	}
	checkHash := sha256.Sum256(checks)
	request["operation"], request["expected_session_head_checksum"] = "grade", after.Events[len(after.Events)-1].Checksum
	request["grade_checks_path"], request["grade_checks_sha256"] = checksPath, hex.EncodeToString(checkHash[:])
	graded := call()
	if graded["task_state"] != "graded" || graded["grade"].(map[string]any)["passed"] != true || graderCalls.Load() != 1 || modelCalls.Load() != 2 {
		t.Fatal("recovered candidate could not be independently frozen and graded")
	}
	delete(request, "grade_checks_path")
	delete(request, "grade_checks_sha256")
	request["operation"], request["finalize_mode"] = "finalize", "finish"
	request["expected_session_head_checksum"] = graded["ledger"].(map[string]any)["head_checksum"]
	finalized := call()
	if finalized["task_state"] != "finalized" || finalized["admission_finalized"] != true {
		t.Fatal("known recovered/graduated task did not release normally")
	}
}
