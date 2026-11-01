package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/session"
	"google.golang.org/grpc"
)

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
				if receipt["task_state"] != "running" {
					t.Fatal("inspection changed the interrupted run")
				}
				after, err := session.ReadVerifiedSnapshot(ctx, ledger, boundary["session_id"].(string))
				if err != nil || len(after.Events) != len(before.Events) || after.Events[len(after.Events)-1].Checksum != before.Events[len(before.Events)-1].Checksum {
					t.Fatal("inspection wrote Session facts")
				}
			} else if receipt["task_state"] != "finalized" || receipt["admission_finalized"] != true {
				t.Fatal("successful abandonment was not acknowledged")
			}
		})
	}
}
