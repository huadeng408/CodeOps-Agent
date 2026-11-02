package cli_test

import (
	"bufio"
	"bytes"
	"context"
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
	"code-agent/internal/session"
	"google.golang.org/grpc"
)

func TestEvaluationControlDoesNotStartUnavailableOrchestrator(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("production task preparation uses Windows native pins")
	}
	for key, value := range map[string]string{
		"CODE_AGENT_SESSION_LEDGER_PATH": "", "CODE_AGENT_EVAL_ALLOW_TOOLS": "Read,Write", "CODE_AGENT_EVAL_VISIBLE_VERIFICATION_COMMANDS_JSON": "[]",
		"OTEL_EXPORTER_OTLP_ENDPOINT": ":invalid", "LLM_PROVIDER": "openai", "OPENAI_MODEL": "approved-model", "OPENAI_API_KEY": "fixture-only",
		"CODE_AGENT_MODEL_INPUT_LIMIT": "200", "CODE_AGENT_MODEL_OUTPUT_LIMIT": "20", "CODE_AGENT_MODEL_TOTAL_LIMIT": "", "CODE_AGENT_MODEL_REASONING_EFFORT": "", "CODE_AGENT_MODEL_REPORTED_MODEL": "",
	} {
		t.Setenv(key, value)
	}
	var modelCalls, runtimeCalls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	pb.RegisterOrchestratorServer(server, evaluationTransport{})
	go server.Serve(listener)
	defer server.Stop()
	cfg := config.Default(t.TempDir())
	cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.MCPConfig = listener.Addr().String(), false, "missing.json"
	cfg.OrchestratorCommand, cfg.OrchestratorArgs = os.Args[0], []string{"-test.run=^TestEvaluationControlStartupTrap$"}
	cfg.Sandbox = productionRuntimeFixture(t, t.TempDir(), &runtimeCalls)
	request := map[string]any{"schema_version": 1, "runtime_contract": "production_opensandbox", "run_id": "control-without-strategy", "task_id": "write-result", "workspace": productionRepository(t),
		"message": "Write result.txt", "model": "approved-model", "lifecycle": "hold", "turn_id": "turn-1", "budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 90}}
	call := func() (map[string]any, error) {
		input, _ := json.Marshal(request)
		var output strings.Builder
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := cli.RunEvaluation(ctx, cfg, bytes.NewReader(input), &output, io.Discard)
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
	receipt, err := call()
	if err != nil || receipt["task_state"] != "completed" || modelCalls.Load() != 1 {
		t.Fatalf("completed candidate missing: %v state=%v code=%v", err, receipt["task_state"], receipt["error_code"])
	}
	request["session_id"] = receipt["session_id"]
	server.Stop()
	trap := filepath.Join(t.TempDir(), "unexpected-runtime-start")
	t.Setenv("CODE_AGENT_EVAL_CONTROL_START_TRAP", trap)
	cfg.OrchestratorAutoStart = true
	for _, operation := range []string{"inspect", "finalize", "inspect"} {
		request["operation"] = operation
		before := receipt["ledger"].(map[string]any)["head_checksum"]
		started := time.Now()
		control, err := call()
		if err != nil || control["error_code"] != "" || control["ledger"].(map[string]any)["verified"] != true {
			t.Fatalf("%s depends on unavailable strategy runtime: %v state=%v code=%v", operation, err, control["task_state"], control["error_code"])
		}
		if operation == "inspect" && control["ledger"].(map[string]any)["head_checksum"] != before {
			t.Fatal("inspection changed canonical facts")
		}
		if control["run_state"] != "completed" || control["session_id"] != receipt["session_id"] || control["usage"].(map[string]any)["confirmed_tokens"] != float64(105) {
			t.Fatal("control lost the completed candidate or its usage")
		}
		if operation == "finalize" && control["admission_finalized"] != true {
			t.Fatal("control did not finalize admission")
		}
		t.Logf("%s completed without strategy startup in %s", operation, time.Since(started))
		receipt = control
	}
	if _, err := os.Stat(trap); !os.IsNotExist(err) || modelCalls.Load() != 1 || runtimeCalls.Load() != 0 {
		t.Fatal("control started strategy work or executed a new model/tool call")
	}
}

func TestEvaluationControlStartupTrap(t *testing.T) {
	path := os.Getenv("CODE_AGENT_EVAL_CONTROL_START_TRAP")
	if path == "" {
		t.Skip("subprocess startup trap")
	}
	_ = os.WriteFile(path, []byte("started"), 0600)
	os.Exit(17)
}

func TestEvaluationInspectAndAbandonReportControlSuccess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("plain Write reconciliation uses admitted Windows pins")
	}
	for _, operation := range []string{"inspect", "finalize"} {
		t.Run(operation, func(t *testing.T) {
			t.Setenv("CODE_AGENT_SESSION_LEDGER_PATH", "")
			t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "Read,Write")
			t.Setenv("CODE_AGENT_EVAL_VISIBLE_VERIFICATION_COMMANDS_JSON", "[]")
			t.Setenv("CODE_AGENT_MODEL_REASONING_EFFORT", "")
			t.Setenv("CODE_AGENT_MODEL_REPORTED_MODEL", "")
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ":invalid")
			t.Setenv("LLM_PROVIDER", "openai")
			t.Setenv("OPENAI_MODEL", "approved-model")
			t.Setenv("OPENAI_API_KEY", "fixture-only")
			t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1")
			t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "200")
			t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "20")
			t.Setenv("CODE_AGENT_MODEL_TOTAL_LIMIT", "")
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer()
			pb.RegisterOrchestratorServer(server, evaluationFileTransport{})
			go server.Serve(listener)
			defer server.Stop()
			root, workspace := t.TempDir(), t.TempDir()
			for _, args := range [][]string{{"init"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "initial"}} {
				if body, err := exec.Command("git", append([]string{"-C", workspace}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("git fixture: %v %s", err, body)
				}
			}
			cfg := config.Default(root)
			cfg.Sandbox.Enabled = false
			cfg.SessionDBPath = filepath.Join(root, "canonical.sqlite")
			cfg.OrchestratorAddr, cfg.OrchestratorAutoStart, cfg.MCPConfig = listener.Addr().String(), false, "missing.json"
			t.Setenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_ROOT", root)
			t.Setenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_LEDGER", cfg.SessionDBPath)
			t.Setenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_ORCHESTRATOR", cfg.OrchestratorAddr)
			request := map[string]any{"schema_version": 1, "run_id": "control-response", "task_id": "interrupted-write", "workspace": workspace,
				"message": "Create effect.txt with the approved file tool", "model": "approved-model", "budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 90},
				"policy": map[string]any{"context_strategy": "metadata_on_demand", "execution_strategy": "single", "max_tool_rounds": 8, "context_max_bytes": 12000, "memory_read": false, "fault_window": "after_effect", "fault_tool": "Write"}}
			input, _ := json.Marshal(request)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEvaluationFileReconcileProcessHelper$")
			child.Stdin, child.Stderr = bytes.NewReader(input), io.Discard
			stdout, err := child.StdoutPipe()
			if err != nil || child.Start() != nil {
				t.Fatal("cannot start the owned fixture process")
			}
			t.Cleanup(func() { _ = child.Process.Kill() })
			var boundary map[string]any
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				var event map[string]any
				if json.Unmarshal(scanner.Bytes(), &event) == nil && event["type"] == "fault_ready" {
					boundary = event
					break
				}
			}
			if boundary == nil {
				t.Fatal("no admitted fault boundary")
			}
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = child.Wait()
			ledger, err := session.OpenReadOnlySQLiteEventLog(cfg.SessionDBPath)
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			before, err := session.ReadVerifiedSnapshot(ctx, ledger, boundary["session_id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			request["session_id"], request["operation"] = boundary["session_id"], operation
			if operation == "finalize" {
				request["finalize_mode"], request["expected_session_head_checksum"] = "abandon", before.Events[len(before.Events)-1].Checksum
			}
			input, _ = json.Marshal(request)
			var output strings.Builder
			controlErr := cli.RunEvaluation(ctx, cfg, strings.NewReader(string(input)), &output, io.Discard)
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
			if controlErr != nil || receipt["error_code"] != "" || receipt["ledger"].(map[string]any)["verified"] != true {
				t.Fatalf("verified control was reported as failure: %v state=%v code=%v finalized=%v", controlErr, receipt["task_state"], receipt["error_code"], receipt["admission_finalized"])
			}
			if operation == "inspect" {
				if receipt["task_state"] != "running" || receipt["admission_finalized"] != false {
					t.Fatal("inspection changed the interrupted run")
				}
				after, err := session.ReadVerifiedSnapshot(ctx, ledger, boundary["session_id"].(string))
				if err != nil || len(after.Events) != len(before.Events) || after.Events[len(after.Events)-1].Checksum != before.Events[len(before.Events)-1].Checksum {
					t.Fatal("inspection wrote Session facts")
				}
				writer, err := session.OpenSQLiteEventLog(cfg.SessionDBPath)
				if err != nil {
					t.Fatal(err)
				}
				invalid, appendErr := writer.Append(ctx, boundary["session_id"].(string), int64(len(after.Events)), "evaluation/finalized", map[string]string{"mode": "unknown"})
				if closeErr := writer.Close(); appendErr != nil || closeErr != nil {
					t.Fatalf("invalid finalization fixture: %v %v", appendErr, closeErr)
				}
				var rejectedOutput strings.Builder
				rejectedErr := cli.RunEvaluation(ctx, cfg, strings.NewReader(string(input)), &rejectedOutput, io.Discard)
				var rejected map[string]any
				if err := json.Unmarshal([]byte(rejectedOutput.String()), &rejected); err != nil || rejectedErr == nil || rejected["error_code"] != "evidence_unavailable" || rejected["admission_finalized"] == true {
					t.Fatalf("unknown finalization was accepted: %v %v %+v", err, rejectedErr, rejected)
				}
				unchanged, err := session.ReadVerifiedSnapshot(ctx, ledger, boundary["session_id"].(string))
				if err != nil || len(unchanged.Events) != len(after.Events)+1 || unchanged.Events[len(unchanged.Events)-1].Checksum != invalid.Checksum {
					t.Fatal("rejected finalization inspection rewrote Session facts")
				}
			} else {
				if receipt["task_state"] != "finalized" || receipt["admission_finalized"] != true {
					t.Fatal("successful abandonment was not acknowledged")
				}
				request["operation"] = "inspect"
				delete(request, "finalize_mode")
				delete(request, "expected_session_head_checksum")
				input, _ = json.Marshal(request)
				for repeat := 0; repeat < 2; repeat++ {
					var readbackOutput strings.Builder
					readbackErr := cli.RunEvaluation(ctx, cfg, strings.NewReader(string(input)), &readbackOutput, io.Discard)
					readbackDecoder := json.NewDecoder(strings.NewReader(readbackOutput.String()))
					var readback map[string]any
					for {
						var event map[string]any
						if readbackDecoder.Decode(&event) != nil {
							break
						}
						if event["type"] == "receipt" {
							readback = event
						}
					}
					if readbackErr != nil || readback["task_state"] != "finalized" || readback["admission_finalized"] != true || readback["run_state"] != receipt["run_state"] || readback["ledger"].(map[string]any)["head_checksum"] != receipt["ledger"].(map[string]any)["head_checksum"] {
						t.Fatalf("abandoned trial readback lost canonical finalization: %v state=%v run=%v finalized=%v", readbackErr, readback["task_state"], readback["run_state"], readback["admission_finalized"])
					}
				}
			}
		})
	}
}
