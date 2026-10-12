package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/session"
	"code-agent/internal/worktree"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// This opt-in uses the approved live OpenSandbox profile. Only model HTTP and
// orchestrator transport are external fixtures; all Harness modules are real.
func TestProductionLocalCoreModelUsesRecordedTaskCopy(t *testing.T) {
	testProductionLocalCoreModelTaskCopy(t, false)
}

func TestProductionLocalCoreModelRejectsUnknownShellOutcome(t *testing.T) {
	testProductionLocalCoreModelTaskCopy(t, true)
}

func testProductionLocalCoreModelTaskCopy(t *testing.T, unknownShell bool) {
	if runtime.GOOS != "windows" || os.Getenv("CODE_AGENT_RUN_LOCAL_MODEL_WORKSPACE_E2E") != "1" {
		t.Skip("requires the approved Windows OpenSandbox integration profile")
	}
	repository, storage := os.Getenv("CODE_AGENT_REPOSITORY_ROOT"), os.Getenv("CODE_AGENT_TASK_WORKSPACE_ROOT")
	baseline, err := worktree.NewManager(repository, "HEAD").CaptureBaseline(context.Background())
	if err != nil {
		t.Fatal("approved source baseline unavailable")
	}
	fixture := startProductionLocalCore(t)
	if artifact := os.Getenv("CODE_AGENT_LOCAL_WORKSPACE_ARTIFACT"); artifact != "" {
		defer func() {
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(fixture.dir, "sessions.sqlite"))
			if err != nil {
				t.Error("could not retain the integration Ledger")
				return
			}
			defer ledger.Close()
			ids, err := ledger.SessionIDs(context.Background())
			facts := map[string][]session.Event{}
			for _, id := range ids {
				events, readErr := ledger.Events(context.Background(), id)
				if readErr != nil {
					err = readErr
				}
				facts[id] = events
			}
			file, writeErr := os.OpenFile(artifact, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil || writeErr != nil {
				t.Error("could not retain immutable integration facts")
				return
			}
			defer file.Close()
			if json.NewEncoder(file).Encode(map[string]any{"test_failed": t.Failed(), "paid_model_calls": 0, "source_baseline": baseline, "facts": facts}) != nil {
				t.Error("could not write immutable integration facts")
			}
		}()
	}
	fixture.client.Timeout = 10 * time.Second
	var modelCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer fixture-authority" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		modelCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"fixture-model","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`)
	}))
	defer upstream.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	transport := grpc.NewServer()
	captured := make(chan *pb.UserInput, 1)
	failures := make(chan error, 1)
	pb.RegisterOrchestratorServer(transport, localWorkspaceTransport{captured: captured, failures: failures, unknownShell: unknownShell})
	go transport.Serve(listener)
	defer transport.Stop()
	fixture.stop(t)
	config, err := os.ReadFile(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := json.Marshal(filepath.ToSlash(repository))
	store, _ := json.Marshal(filepath.ToSlash(storage))
	config = bytes.Replace(config, []byte("harness:\n"), []byte("harness:\n  repository_root: "+string(paths)+"\n  task_workspace_root: "+string(store)+"\n"), 1)
	if err := os.WriteFile(fixture.config, config, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"CODE_AGENT_TASK_IMAGE", "CODE_AGENT_OPENSANDBOX_MANIFEST", "CODE_AGENT_OPENSANDBOX_MANIFEST_SHA256", "CODE_AGENT_OPENSANDBOX_API_KEY", "CODE_AGENT_OPENSANDBOX_EXECD_TOKEN"} {
		if os.Getenv(name) == "" {
			t.Fatalf("approved runtime input %s is missing", name)
		}
		fixture.environment = append(fixture.environment, name+"="+os.Getenv(name))
	}
	fixture.environment = append(fixture.environment, "CODE_AGENT_TASK_RUNTIME=opensandbox", "CODE_AGENT_TASK_TOOLCHAIN=go", "CODE_AGENT_ORCHESTRATOR_ADDR="+listener.Addr().String(),
		"LLM_PROVIDER=openai", "OPENAI_API_KEY=fixture-authority", "OPENAI_MODEL=fixture-model", "OPENAI_BASE_URL="+upstream.URL,
		"CODE_AGENT_MODEL_INPUT_LIMIT=100", "CODE_AGENT_MODEL_OUTPUT_LIMIT=3")
	fixture.start(t)
	fixture.authenticate(t)
	request := func(method, path string, payload, target any, expected int) {
		t.Helper()
		body, _ := json.Marshal(payload)
		req, err := http.NewRequest(method, fixture.base+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := fixture.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != expected {
			t.Fatalf("%s %s = %d, want %d", method, path, response.StatusCode, expected)
		}
		if target != nil && json.NewDecoder(response.Body).Decode(target) != nil {
			t.Fatal("invalid product response")
		}
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		var health struct {
			Continuation struct{ Attached bool }
		}
		request(http.MethodGet, "/healthz", nil, &health, http.StatusOK)
		if health.Continuation.Attached {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("approved continuation did not attach")
		}
		time.Sleep(50 * time.Millisecond)
	}
	var created struct{ Data session.SessionView }
	request(http.MethodPost, "/api/v1/sessions", map[string]any{"projectName": "approved-repo", "title": "edit isolated copy", "workingDir": repository}, &created, http.StatusOK)
	path := "/api/v1/sessions/" + created.Data.ID
	var prepared struct{ Data session.TaskWorkspaceView }
	request(http.MethodPost, path+"/task-workspace/prepare", map[string]any{"requestId": "prepare", "expectedSeq": created.Data.EventCount}, &prepared, http.StatusOK)
	if prepared.Data.State != "prepared" {
		t.Fatal("approved copy was not prepared")
	}
	var accepted struct{ Data session.RunView }
	request(http.MethodPost, path+"/messages", map[string]any{"requestId": "edit", "expectedSeq": prepared.Data.EventCount, "content": "exercise the isolated copy"}, &accepted, http.StatusAccepted)
	deadline = time.Now().Add(180 * time.Second)
	approved := map[string]bool{}
	for {
		var events struct{ Data []session.EventView }
		request(http.MethodGet, path+"/events", nil, &events, http.StatusOK)
		for _, event := range events.Data {
			if event.Type == "approval/pending" && event.Approval != nil && !approved[event.ID] {
				decisionPath := path + "/approvals/" + event.Approval.RunID + "/" + event.Approval.ToolCallID
				request(http.MethodPost, decisionPath, map[string]any{"decision": "approved", "pendingEventId": event.ID, "pendingSeq": event.Seq}, nil, http.StatusOK)
				approved[event.ID] = true
			}
		}
		var current struct{ Data session.SessionView }
		request(http.MethodGet, path, nil, &current, http.StatusOK)
		if current.Data.Run != nil && (current.Data.Run.Status == session.RunCompleted || current.Data.Run.Status == session.RunFailed) {
			want := session.RunCompleted
			if unknownShell {
				want = session.RunFailed
			}
			if current.Data.Run.Status != want || len(failures) != 0 {
				select {
				case failure := <-failures:
					t.Logf("external transport: %v", failure)
				default:
				}
				t.Fatalf("public model/tool run failed: %+v", current.Data.Run)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("public model/tool run did not finish")
		}
		time.Sleep(50 * time.Millisecond)
	}
	input := <-captured
	copyRoot := filepath.Join(storage, prepared.Data.WorkspaceID)
	if input.WorkingDir != copyRoot || !input.HarnessManaged || input.ContextEnvelope == nil || input.ModelGateway == nil {
		t.Fatal("public continuation bypassed the admitted task Context")
	}
	data, err := os.ReadFile(filepath.Join(copyRoot, "codeops-integration.txt"))
	if err != nil || string(data) != "after" {
		t.Fatal("Go edits did not reach the isolated copy")
	}
	request(http.MethodGet, path+"/task-workspace", nil, &prepared, http.StatusOK)
	wantWorkspace := "prepared"
	if unknownShell {
		wantWorkspace = "blocked"
	}
	if prepared.Data.State != wantWorkspace {
		t.Fatal("completed Go edits lost workspace admission")
	}
	after, err := worktree.NewManager(repository, "HEAD").CaptureBaseline(context.Background())
	if err != nil || after.Checksum != baseline.Checksum {
		t.Fatal("model tools changed the original repository or index")
	}
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(fixture.dir, "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	batch, err := admission.NewBudget(ledger).Inspect(context.Background(), created.Data.UserID)
	wantCalls := int64(4)
	if unknownShell {
		wantCalls = 3
		events, err := ledger.Events(context.Background(), created.Data.ID)
		if err != nil {
			t.Fatal(err)
		}
		retained := false
		for _, event := range events {
			if event.Type != "tool/result" {
				continue
			}
			var result struct {
				ToolName       string `json:"tool_name"`
				OutcomeUnknown bool   `json:"outcome_unknown"`
			}
			if json.Unmarshal(event.Payload, &result) != nil {
				t.Fatal("invalid retained tool result")
			}
			retained = retained || result.ToolName == "Bash" && result.OutcomeUnknown
		}
		if !retained {
			t.Fatal("unknown shell outcome was not retained in the Ledger")
		}
	}
	if err != nil || modelCalls.Load() != wantCalls || batch.UsedTokens != wantCalls*10 || batch.ReservedTokens != 0 || batch.TaskID != created.Data.ID || batch.CostStatus != "unknown" {
		t.Fatalf("public admission lost accounting: calls=%d batch=%+v err=%v", modelCalls.Load(), batch, err)
	}
	if !unknownShell {
		ids, err := ledger.SessionIDs(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if id == created.Data.ID {
				continue
			}
			events, err := ledger.Events(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			// A hash-valid but contradictory imported fact must still refuse readiness.
			if _, err := ledger.Append(context.Background(), id, int64(len(events)), "admission/task_started", map[string]any{"owner_id": created.Data.UserID, "task_id": "foreign-task"}); err != nil {
				t.Fatal(err)
			}
		}
		var capabilities struct {
			Data map[string]struct{ State string }
		}
		request(http.MethodGet, "/api/v1/capabilities", nil, &capabilities, http.StatusOK)
		if capabilities.Data["execution"].State != "blocked" {
			t.Fatal("unreadable token Ledger was advertised as ready")
		}
	}
}

type localWorkspaceTransport struct {
	pb.UnimplementedOrchestratorServer
	captured     chan *pb.UserInput
	failures     chan error
	unknownShell bool
}

func (localWorkspaceTransport) Health(context.Context, *pb.Empty) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok"}, nil
}

func (fixture localWorkspaceTransport) Converse(stream grpc.BidiStreamingServer[pb.HarnessMessage, pb.OrchestratorMessage]) (err error) {
	defer func() {
		if err != nil {
			fixture.failures <- err
		}
	}()
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	input := message.GetUserInput()
	fixture.captured <- input
	binding := input.GetModelGateway()
	if binding == nil {
		return fmt.Errorf("admitted gateway binding is missing")
	}
	connection, err := grpc.NewClient(binding.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer connection.Close()
	model := pb.NewModelGatewayClient(connection)
	calls := []*pb.ToolRequest{
		{ToolName: "Write", ToolCallId: "write-copy", ParametersJson: `{"path":"codeops-integration.txt","content":"before"}`},
		{ToolName: "Edit", ToolCallId: "edit-copy", ParametersJson: `{"path":"codeops-integration.txt","old":"before","new":"after"}`},
		{ToolName: "Bash", ToolCallId: "test-copy", ParametersJson: `{"command":"go test ./... -count=1","timeout_seconds":120}`},
	}
	if fixture.unknownShell {
		calls[2].ParametersJson = `{"command":"sleep 20; go test ./... -count=1","timeout_seconds":15}`
	}
	for index := range 4 {
		payload, _ := json.Marshal(map[string]any{"model": binding.Model, "messages": []map[string]string{{"role": "user", "content": "public transport fixture"}}})
		response, err := model.Invoke(stream.Context(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: fmt.Sprintf("call-%s-%d", input.SessionId, index), PayloadJson: payload, Purpose: "foreground"})
		if index == 3 && fixture.unknownShell {
			if err != nil || response.GetErrorCode() != "task_prerequisites_missing" {
				return fmt.Errorf("unknown tool result did not refuse the next paid attempt")
			}
			return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: false, ErrorCode: "task_prerequisites_missing"}}})
		}
		if err != nil || response.GetErrorCode() != "" {
			return fmt.Errorf("model admission failed at step %d: %s", index, response.GetErrorCode())
		}
		if index == len(calls) {
			break
		}
		if err := stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_ToolRequest{ToolRequest: calls[index]}}); err != nil {
			return err
		}
		for {
			message, err := stream.Recv()
			if err != nil {
				return err
			}
			if result := message.GetToolResult(); result != nil {
				if index == 2 && fixture.unknownShell {
					if result.ExitCode != 126 || result.Error == "" {
						return fmt.Errorf("timeout did not retain a refused shell outcome")
					}
					break
				}
				if result.ExitCode != 0 || result.Error != "" || result.Truncated || index == 2 && !strings.Contains(result.Output, "github.com/go-chi/chi/v5") {
					return fmt.Errorf("isolated tool %s failed: exit=%d truncated=%t error=%q output=%q", calls[index].ToolName, result.ExitCode, result.Truncated, result.Error, result.Output)
				}
				break
			}
		}
	}
	return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: true, Message: "isolated tools completed"}}})
}
