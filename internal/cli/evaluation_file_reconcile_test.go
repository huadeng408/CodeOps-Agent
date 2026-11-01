package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

const reconcileFixtureContent = "op-017 applied\n"

type evaluationFileTransport struct {
	pb.UnimplementedOrchestratorServer
	acknowledged chan *pb.ToolResult
}

func (evaluationFileTransport) Health(context.Context, *pb.Empty) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok"}, nil
}

func (evaluationFileTransport) ReflectMemory(_ context.Context, request *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
	return &pb.MemoryReflectionResponse{SourceChecksum: request.SourceChecksum}, nil
}

func (f evaluationFileTransport) Converse(stream grpc.BidiStreamingServer[pb.HarnessMessage, pb.OrchestratorMessage]) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	callID := "write-file"
	for _, previous := range message.GetUserInput().History {
		if previous.Role != "tool" || previous.ToolCallId != "write-file" {
			continue
		}
		if !previous.IsError {
			return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: true, Message: "The reconciled file is complete."}}})
		}
		callID = "write-after-proven-absence"
	}
	arguments, _ := json.Marshal(map[string]string{"path": "effect.txt", "content": reconcileFixtureContent})
	if err := stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_ToolRequest{ToolRequest: &pb.ToolRequest{ToolName: "Write", ToolCallId: callID, ParametersJson: string(arguments)}}}); err != nil {
		return err
	}
	response, err := stream.Recv()
	if err != nil {
		return err
	}
	if f.acknowledged != nil {
		select {
		case f.acknowledged <- response.GetToolResult():
		default:
		}
	}
	return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: response.GetToolResult().Error == "", Message: "The approved file task finished."}}})
}

// The subprocess runs the public Go entry point; the parent kills that exact process.
func TestEvaluationFileReconcileProcessHelper(t *testing.T) {
	root := os.Getenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_ROOT")
	if root == "" {
		t.Skip("subprocess fixture")
	}
	cfg := config.Default(root)
	cfg.Sandbox.Enabled = false
	cfg.SessionDBPath = os.Getenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_LEDGER")
	cfg.OrchestratorAddr = os.Getenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_ORCHESTRATOR")
	cfg.OrchestratorAutoStart = false
	cfg.MCPConfig = "missing.json"
	if subject := os.Getenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_SUBJECT"); subject != "" {
		cfg.ActorSubject = subject
	}
	if os.Getenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_DENY_READ") == "1" {
		cfg.Permissions.Deny = append(cfg.Permissions.Deny, config.PermissionRule{Tool: "Read"})
	}
	if err := cli.RunEvaluation(context.Background(), cfg, os.Stdin, os.Stdout, os.Stderr); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestEvaluationReconcilesInterruptedPlainWriteFromCanonicalEvidence(t *testing.T) {
	for _, test := range []struct {
		name, window, initial, mutation, state string
		writes                                 int
		delay                                  time.Duration
	}{
		{name: "after_effect", window: "after_effect", state: "applied", writes: 1},
		{name: "before_effect", window: "before_effect", state: "not_applied", writes: 2},
		{name: "after_record", window: "after_record", writes: 1},
		{name: "after_record_delayed_parent", window: "after_record", writes: 1, delay: 6 * time.Second},
		{name: "identical_preimage_and_postimage", window: "after_effect", initial: reconcileFixtureContent, state: "unknown", writes: 1},
		{name: "external_state_changed", window: "after_effect", mutation: "a different controlled fixture state\n", state: "unknown", writes: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			testEvaluationFileReconcile(t, test.window, test.initial, test.mutation, test.state, test.writes, test.name == "after_effect", test.delay)
		})
	}
}

func testEvaluationFileReconcile(t *testing.T, window, initial, mutation, expectedState string, expectedWrites int, checkGuards bool, delay time.Duration) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("file-state reconciliation requires the admitted Windows directory/file pins")
	}
	t.Setenv("CODE_AGENT_SESSION_LEDGER_PATH", "")
	t.Setenv("CODE_AGENT_EVAL_ALLOW_TOOLS", "Read,Write")
	t.Setenv("CODE_AGENT_EVAL_VISIBLE_VERIFICATION_COMMANDS_JSON", "[]")
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
	acknowledged := make(chan *pb.ToolResult, 1)
	pb.RegisterOrchestratorServer(server, evaluationFileTransport{acknowledged: acknowledged})
	go server.Serve(listener)
	defer server.Stop()
	root, workspace := t.TempDir(), t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "initial"}} {
		if body, err := exec.Command("git", append([]string{"-C", workspace}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, body)
		}
	}
	if initial != "" {
		if err := os.WriteFile(filepath.Join(workspace, "effect.txt"), []byte(initial), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ledgerPath := filepath.Join(root, "canonical.sqlite")
	t.Setenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_ROOT", root)
	t.Setenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_LEDGER", ledgerPath)
	t.Setenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_ORCHESTRATOR", listener.Addr().String())
	t.Setenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_SUBJECT", "")
	t.Setenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_DENY_READ", "")
	request := map[string]any{
		"schema_version": 1, "run_id": "file-reconcile-fixture", "task_id": "create-file", "workspace": workspace,
		"message": "Create effect.txt with the approved Write tool", "model": "approved-model",
		"budget": map[string]any{"max_tokens": 1000, "wall_clock_seconds": 90},
		"policy": map[string]any{"context_strategy": "metadata_on_demand", "execution_strategy": "single", "max_tool_rounds": 8,
			"context_max_bytes": 12000, "memory_read": false, "fault_window": window, "fault_tool": "Write"},
	}
	input, _ := json.Marshal(request)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEvaluationFileReconcileProcessHelper$")
	child.Stdin = bytes.NewReader(input)
	var diagnostics bytes.Buffer
	child.Stderr = &diagnostics
	stdout, err := child.StdoutPipe()
	if err != nil || child.Start() != nil {
		t.Fatal("could not start the evaluation fixture process")
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	var boundary map[string]any
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var event map[string]any
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.UseNumber()
		if decoder.Decode(&event) == nil && event["type"] == "fault_ready" {
			boundary = event
			break
		}
	}
	if boundary == nil {
		t.Fatalf("no public fault boundary: %s", diagnostics.String())
	}
	if delay > 0 {
		time.Sleep(delay)
		select {
		case <-acknowledged:
			t.Fatal("tool response crossed the admitted fault boundary before the owned process was terminated")
		default:
		}
	}
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
	call := func() ([]byte, error) {
		input, _ := json.Marshal(request)
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEvaluationFileReconcileProcessHelper$")
		command.Stdin = bytes.NewReader(input)
		return command.CombinedOutput()
	}
	request["session_id"], request["operation"] = boundary["session_id"], "continue"
	if checkGuards {
		// No snapshot pin here: exercise the exact live-process fence, without
		// accidentally passing on a heartbeat/head race instead.
		if body, err := call(); err == nil || !strings.Contains(string(body), `"error_code":"recovery_process_stop_unconfirmed"`) {
			t.Fatalf("live original process was not refused at its process fence: %v %s", err, body)
		}
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if delay > 0 {
		request["operation"] = "inspect"
		body, _ := call()
		var evidence session.EvaluationExecutionEvidence
		for _, line := range bytes.Split(body, []byte("\n")) {
			var receipt struct {
				Type     string                              `json:"type"`
				Code     string                              `json:"error_code"`
				Evidence session.EvaluationExecutionEvidence `json:"execution_evidence"`
			}
			if json.Unmarshal(line, &receipt) == nil && receipt.Type == "receipt" && receipt.Code == "" {
				evidence = receipt.Evidence
			}
		}
		var expected session.EvaluationBoundary
		encoded, _ := json.Marshal(boundary["fault_boundary"])
		if json.Unmarshal(encoded, &expected) != nil || len(evidence.FaultBoundaries) != 1 {
			t.Fatalf("canonical held-window evidence missing after the owned kill: %s", body)
		}
		proof := evidence.FaultBoundaries[0]
		if proof.EvaluationBoundary != expected || !proof.Verified || !proof.ResultOrderVerified || !proof.ProcessStopped || proof.Released || proof.Release != nil || proof.RunTerminal || proof.CancelRequested {
			t.Fatalf("owned termination was not bound to the exact still-held canonical window: %+v", proof)
		}
		request["operation"] = "continue"
	}
	before := read()
	preimages := 0
	for _, event := range before.Events {
		if event.Type == "evaluation/write-preimage" {
			preimages++
			var image map[string]any
			if json.Unmarshal(event.Payload, &image) != nil || image["before"].(map[string]any)["exists"] != (initial != "") || image["process"].(map[string]any)["pid"] != float64(child.Process.Pid) {
				t.Fatalf("preimage did not bind the actual file state and killed process: %s", event.Payload)
			}
		}
	}
	expectedPreimages := 1
	if window == "after_record" {
		expectedPreimages = 0
	}
	if preimages != expectedPreimages {
		t.Fatalf("interrupted approved Write has no unique canonical preimage: %d", preimages)
	}
	if checkGuards {
		request["expected_session_head_checksum"] = strings.Repeat("0", 64)
		if body, err := call(); err == nil || !strings.Contains(string(body), `"error_code":"evaluation_session_changed"`) {
			t.Fatalf("stale expected head was not refused: %v %s", err, body)
		}
		afterRefusal := read()
		if afterRefusal.Events[len(afterRefusal.Events)-1].Checksum != before.Events[len(before.Events)-1].Checksum {
			t.Fatal("stale-head refusal changed the Session Ledger")
		}
		t.Setenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_SUBJECT", "other-fixture-actor")
		request["expected_session_head_checksum"] = before.Events[len(before.Events)-1].Checksum
		if body, err := call(); err == nil || strings.Contains(string(body), `"state":"applied"`) {
			t.Fatalf("a different actor reconciled the old Write: %v %s", err, body)
		}
		t.Setenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_SUBJECT", "")
		afterRefusal = read()
		if afterRefusal.Events[len(afterRefusal.Events)-1].Checksum != before.Events[len(before.Events)-1].Checksum {
			t.Fatal("wrong-actor refusal changed the Session Ledger")
		}
		otherWorkspace := filepath.Join(t.TempDir(), "same-head-clone")
		if body, err := exec.Command("git", "clone", "--quiet", workspace, otherWorkspace).CombinedOutput(); err != nil {
			t.Fatalf("clone guard fixture: %v %s", err, body)
		}
		request["workspace"] = otherWorkspace
		if body, err := call(); err == nil || !strings.Contains(string(body), `"error_code":"session_scope_mismatch"`) {
			t.Fatalf("a different same-HEAD workspace reconciled the Write: %v %s", err, body)
		}
		request["workspace"] = workspace
		t.Setenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_DENY_READ", "1")
		if body, err := call(); err == nil || !strings.Contains(string(body), `"error_code":"policy_binding_mismatch"`) {
			t.Fatalf("changed Read authorization bypassed the frozen binding: %v %s", err, body)
		}
		t.Setenv("CODE_AGENT_FILE_RECONCILE_FIXTURE_DENY_READ", "")
		afterRefusal = read()
		if afterRefusal.Events[len(afterRefusal.Events)-1].Checksum != before.Events[len(before.Events)-1].Checksum {
			t.Fatal("workspace or Read-policy refusal changed the Session Ledger")
		}
	}
	if mutation != "" {
		if err := os.WriteFile(filepath.Join(workspace, "effect.txt"), []byte(mutation), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	request["expected_session_head_checksum"] = before.Events[len(before.Events)-1].Checksum
	body, err := call()
	if (err == nil) == (expectedState == "unknown") {
		t.Fatalf("public continuation did not reconcile the stopped Write: %v %s", err, body)
	}
	after := read()
	proofs, writes := 0, 0
	for _, event := range after.Events {
		if event.Type == "evaluation/write-reconciled" {
			var proof map[string]any
			if json.Unmarshal(event.Payload, &proof) != nil || proof["state"] != expectedState || proof["physical_execution_count"] != nil {
				t.Fatalf("reconciliation overstated file-state proof: %s", event.Payload)
			}
			if _, exists := proof["physical_execution_count"]; !exists {
				t.Fatal("physical execution count must be explicitly unknown")
			}
			proofs++
		}
		if expectedState == "unknown" && (event.Type == "tool/result" || event.Type == "commit_done") {
			t.Fatal("ambiguous state produced a settled tool result or commit")
		}
		if event.Type == "tool/dispatched" {
			var call map[string]any
			_ = json.Unmarshal(event.Payload, &call)
			if call["tool_name"] == "Write" {
				writes++
			}
		}
	}
	expectedProofs := 1
	if window == "after_record" {
		expectedProofs = 0
	}
	if proofs != expectedProofs || writes != expectedWrites {
		t.Fatalf("missing reconciliation or replayed the applied Write: proofs=%d dispatches=%d", proofs, writes)
	}
	expectedContent := reconcileFixtureContent
	if mutation != "" {
		expectedContent = mutation
	}
	if body, err := os.ReadFile(filepath.Join(workspace, "effect.txt")); err != nil || string(body) != expectedContent {
		t.Fatalf("file state changed during reconciliation: %q %v", body, err)
	}
	if expectedState != "unknown" && !strings.Contains(string(body), `"task_state":"completed"`) {
		t.Fatalf("continuation did not finish the same Session: %s", body)
	}
	if expectedState == "unknown" && (!strings.Contains(string(body), `"task_state":"infra_error"`) || !strings.Contains(string(body), `"error_code":"tool_outcome_unknown"`) || !strings.Contains(string(body), `"effect_known":false`)) {
		t.Fatalf("ambiguous file state was upgraded: %s", body)
	}
	if expectedState == "unknown" {
		request["expected_session_head_checksum"] = after.Events[len(after.Events)-1].Checksum
		if body, err := call(); err == nil || !strings.Contains(string(body), `"already_recorded":true`) || !strings.Contains(string(body), `"effect_known":false`) {
			t.Fatalf("repeated reconciliation changed the unknown outcome: %v %s", err, body)
		}
		repeated := read()
		if repeated.Events[len(repeated.Events)-1].Checksum != after.Events[len(after.Events)-1].Checksum {
			t.Fatal("repeating an unknown reconciliation appended competing evidence")
		}
	}
}
