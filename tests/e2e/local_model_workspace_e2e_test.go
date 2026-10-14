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
	"os/exec"
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
	testProductionLocalCoreModelTaskCopy(t, false, false, "")
}

func TestProductionLocalCoreModelRejectsUnknownShellOutcome(t *testing.T) {
	testProductionLocalCoreModelTaskCopy(t, true, false, "")
}

func TestProductionLocalCoreGoFixProducesTestedProposal(t *testing.T) {
	testProductionLocalCoreModelTaskCopy(t, false, true, "")
}

func TestProductionLocalCoreBrowserApprovesProposal(t *testing.T) {
	testProductionLocalCoreModelTaskCopy(t, false, true, "approve")
}
func TestProductionLocalCoreBrowserRejectsProposal(t *testing.T) {
	testProductionLocalCoreModelTaskCopy(t, false, true, "reject")
}

func testProductionLocalCoreModelTaskCopy(t *testing.T, unknownShell, proposal bool, decision string) {
	if runtime.GOOS != "windows" || os.Getenv("CODE_AGENT_RUN_LOCAL_MODEL_WORKSPACE_E2E") != "1" {
		t.Skip("requires the approved Windows OpenSandbox integration profile")
	}
	repository, storage := os.Getenv("CODE_AGENT_REPOSITORY_ROOT"), os.Getenv("CODE_AGENT_TASK_WORKSPACE_ROOT")
	if proposal {
		repository = t.TempDir()
		for _, args := range [][]string{{"init"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.test"}} {
			if output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("fixture Git: %s %v", output, err)
			}
		}
		for path, content := range map[string]string{
			"go.mod":       "module example.test/codeops-proposal\n\ngo 1.20\n",
			"calc.go":      "package calc\n\nfunc Add(left, right int) int { return left - right }\n",
			"calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if got := Add(2, 3); got != 5 { t.Fatalf(\"Add(2, 3) = %d, want 5\", got) } }\n",
		} {
			if err := os.WriteFile(filepath.Join(repository, path), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
		for _, args := range [][]string{{"add", "."}, {"commit", "-m", "fixture"}} {
			if output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("fixture commit: %s %v", output, err)
			}
		}
	}
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
	if proposal {
		fixture.client.Timeout = 180 * time.Second
	}
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
	pb.RegisterOrchestratorServer(transport, localWorkspaceTransport{captured: captured, failures: failures, unknownShell: unknownShell, proposal: proposal})
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
	request := func(method, path string, payload, target any, expected int) int {
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
		if expected != 0 && response.StatusCode != expected || expected == 0 && response.StatusCode != http.StatusOK && response.StatusCode != http.StatusConflict {
			var failure struct{ Message string }
			_ = json.NewDecoder(response.Body).Decode(&failure)
			t.Fatalf("%s %s = %d, want %d (%s)", method, path, response.StatusCode, expected, failure.Message)
		}
		if target != nil && json.NewDecoder(response.Body).Decode(target) != nil {
			t.Fatal("invalid product response")
		}
		return response.StatusCode
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
	request(http.MethodPost, path+"/messages", map[string]any{"requestId": "not-prepared", "expectedSeq": created.Data.EventCount, "content": "keep this draft until the task copy is ready"}, nil, http.StatusConflict)
	var rejected struct{ Data session.SessionView }
	request(http.MethodGet, path, nil, &rejected, http.StatusOK)
	if rejected.Data.EventCount != created.Data.EventCount || rejected.Data.Run != nil || modelCalls.Load() != 0 {
		t.Fatal("workspace refusal persisted a message, started a run or called the provider")
	}
	var prepared struct{ Data session.TaskWorkspaceView }
	request(http.MethodPost, path+"/task-workspace/prepare", map[string]any{"requestId": "prepare", "expectedSeq": created.Data.EventCount}, &prepared, http.StatusOK)
	if prepared.Data.State != "prepared" {
		t.Fatal("approved copy was not prepared")
	}
	if proposal {
		request(http.MethodPost, path+"/task-workspace/test", map[string]any{"requestId": "red-baseline", "expectedSeq": prepared.Data.EventCount}, &prepared, http.StatusOK)
		if prepared.Data.Test == nil || prepared.Data.Test.State != "failed" || prepared.Data.Test.ExitCode == nil || *prepared.Data.Test.ExitCode == 0 {
			t.Fatal("original Go bug did not retain a failing real baseline")
		}
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
	changedPath, expectedContent := "codeops-integration.txt", "after"
	if proposal {
		changedPath, expectedContent = "calc.go", "package calc\n\nfunc Add(left, right int) int { return left + right }\n"
	}
	data, err := os.ReadFile(filepath.Join(copyRoot, changedPath))
	if err != nil || string(data) != expectedContent {
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
	if proposal {
		var generated struct {
			Data struct {
				EventCount int `json:"eventCount"`
				Proposal   struct {
					ID, SessionID, WorkspaceID, BaselineChecksum, FinalStateSHA256, State string
					Files                                                                 []struct {
						Path, Diff                string
						BeforeExists, AfterExists bool
					}
					Test struct {
						State, InputSHA256, RuntimeID string
						ExitCode                      *int
						Stopped                       bool
					}
				}
			}
		}
		for attempt := range 3 {
			request(http.MethodGet, path+"/task-workspace", nil, &prepared, http.StatusOK)
			if request(http.MethodPost, path+"/task-workspace/proposal", map[string]any{"requestId": "review-fix", "expectedSeq": prepared.Data.EventCount}, &generated, 0) == http.StatusOK {
				break
			}
			t.Logf("proposal cursor changed at attempt %d; refreshing with the same request identity", attempt+1)
		}
		got := generated.Data.Proposal
		if len(got.ID) != 64 || got.SessionID != created.Data.ID || got.WorkspaceID != prepared.Data.WorkspaceID || got.BaselineChecksum != baseline.Checksum || len(got.FinalStateSHA256) != 64 || got.State != "ready" {
			t.Fatalf("proposal is not bound to the reviewed task: %+v", got)
		}
		if len(got.Files) != 1 || got.Files[0].Path != "calc.go" || !got.Files[0].BeforeExists || !got.Files[0].AfterExists || !strings.Contains(got.Files[0].Diff, "-func Add(left, right int) int { return left - right }") || !strings.Contains(got.Files[0].Diff, "+func Add(left, right int) int { return left + right }") {
			t.Fatal("proposal lacks an accurate diff against the initial working copy")
		}
		if got.Test.State != "passed" || got.Test.ExitCode == nil || *got.Test.ExitCode != 0 || got.Test.InputSHA256 != got.FinalStateSHA256 || got.Test.RuntimeID == "" || !got.Test.Stopped {
			t.Fatal("proposal test does not prove the final frozen input")
		}
		var reopened = generated
		request(http.MethodGet, path+"/task-workspace", nil, &reopened, http.StatusOK)
		if reopened.Data.Proposal.ID != got.ID {
			t.Fatal("refresh lost the retained proposal")
		}
		request(http.MethodPost, path+"/task-workspace/proposal", map[string]any{"requestId": "review-fix", "expectedSeq": 0}, &reopened, http.StatusOK)
		if reopened.Data.EventCount != generated.Data.EventCount || reopened.Data.Proposal.ID != got.ID {
			t.Fatal("idempotent proposal delivery repeated execution")
		}
		after, err := worktree.NewManager(repository, "HEAD").CaptureBaseline(context.Background())
		if err != nil || after.Checksum != baseline.Checksum {
			t.Fatal("proposal generation or viewing applied the original repository")
		}
		if decision != "" {
			command := exec.Command("node", "tests/e2e/proposal_decision_browser.mjs")
			command.Dir = e2ERepositoryRoot(t)
			command.Env = append(os.Environ(), "CODE_AGENT_DECISION_URL="+fixture.base, "CODE_AGENT_DECISION_PASSWORD="+fixture.password,
				"CODE_AGENT_DECISION_SESSION="+created.Data.ID, "CODE_AGENT_DECISION_PROPOSAL="+got.ID, "CODE_AGENT_DECISION_ACTION="+decision,
				"CODE_AGENT_DECISION_ARTIFACT="+os.Getenv("CODE_AGENT_LOCAL_WORKSPACE_ARTIFACT")+".browser.json")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("browser decision failed: %v: %s", err, output)
			}
			original, err := os.ReadFile(filepath.Join(repository, changedPath))
			want := "package calc\n\nfunc Add(left, right int) int { return left - right }\n"
			if decision == "approve" {
				want = expectedContent
			}
			if err != nil || string(original) != want {
				t.Fatal("browser decision changed unexpected original content")
			}
			after, err := worktree.NewManager(repository, "HEAD").CaptureBaseline(context.Background())
			if err != nil || after.HeadCommit != baseline.HeadCommit || after.IndexSHA256 != baseline.IndexSHA256 {
				t.Fatal("browser decision changed HEAD or index")
			}
			isolated, err := os.ReadFile(filepath.Join(copyRoot, changedPath))
			if err != nil || string(isolated) != expectedContent {
				t.Fatal("decision lost isolated result")
			}
			request(http.MethodPost, path+"/task/finish", map[string]any{}, nil, http.StatusOK)
			return
		}
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
		budget := admission.NewBudget(ledger)
		if _, err := budget.Reserve(context.Background(), batch.TaskOwnerID, created.Data.ID, "unresolved-after-turn", 103); err != nil {
			t.Fatal(err)
		}
		request(http.MethodGet, path, nil, &rejected, http.StatusOK)
		cursor := rejected.Data.EventCount
		request(http.MethodPost, path+"/messages", map[string]any{"requestId": "blocked-active-call", "expectedSeq": cursor, "content": "keep this draft while the call is active"}, nil, http.StatusConflict)
		request(http.MethodGet, path, nil, &rejected, http.StatusOK)
		if rejected.Data.EventCount != cursor || modelCalls.Load() != wantCalls {
			t.Fatal("active-call refusal persisted a new turn or called the provider")
		}
		if _, err := budget.MarkUnknown(context.Background(), batch.TaskOwnerID, "unresolved-after-turn"); err != nil {
			t.Fatal(err)
		}
		var usageCapabilities struct {
			Data struct {
				Budget struct{ State, Reason string }
			}
		}
		request(http.MethodGet, "/api/v1/capabilities", nil, &usageCapabilities, http.StatusOK)
		if usageCapabilities.Data.Budget.State != "ready" || usageCapabilities.Data.Budget.Reason != "provider enforces budget; some usage is unconfirmed" {
			t.Fatal("terminal unknown accounting blocked product readiness")
		}
		// A previously accepted request remains readable even after admission changes.
		request(http.MethodPost, path+"/messages", map[string]any{"requestId": "edit", "expectedSeq": 0, "content": "exercise the isolated copy"}, &accepted, http.StatusAccepted)
		retained, err := budget.Inspect(context.Background(), batch.TaskOwnerID)
		if err != nil || !retained.UnknownUsage || retained.ReservedTokens != 103 || retained.UsedTokens != batch.UsedTokens {
			t.Fatal("idempotent delivery cleared unknown usage or changed confirmed consumption")
		}
		if proposal {
			var regenerated struct {
				Data struct {
					EventCount int
					Proposal   struct{ ID, State string }
				}
			}
			for attempt := range 3 {
				request(http.MethodGet, path+"/task-workspace", nil, &prepared, http.StatusOK)
				if request(http.MethodPost, path+"/task-workspace/proposal", map[string]any{"requestId": "review-again", "expectedSeq": prepared.Data.EventCount}, &regenerated, 0) == http.StatusOK {
					break
				}
				t.Logf("regeneration cursor changed at attempt %d; retaining the request identity", attempt+1)
			}
			if regenerated.Data.Proposal.State != "ready" {
				t.Fatal("explicit regeneration did not retain a tested proposal")
			}
			var retried = regenerated
			request(http.MethodPost, path+"/task-workspace/proposal", map[string]any{"requestId": "review-fix", "expectedSeq": regenerated.Data.EventCount}, &retried, http.StatusOK)
			if retried.Data.EventCount != regenerated.Data.EventCount || retried.Data.Proposal != regenerated.Data.Proposal {
				t.Fatal("retrying a historic request re-executed or replaced the latest proposal")
			}
			snapshot, err := session.ReadVerifiedSnapshot(context.Background(), ledger, created.Data.ID)
			if err != nil {
				t.Fatal(err)
			}
			var changed map[string]any
			for _, event := range snapshot.Events {
				if event.Type == "workspace/proposal-outcome" {
					if err := json.Unmarshal(event.Payload, &changed); err != nil {
						t.Fatal(err)
					}
				}
			}
			if changed == nil {
				t.Fatal("completed proposal is missing")
			}
			files := changed["view"].(map[string]any)["files"].([]any)
			files[0].(map[string]any)["diff"] = "unreviewed content unrelated to the tested input"
			if _, err := ledger.Append(context.Background(), created.Data.ID, int64(len(snapshot.Events)), "workspace/proposal-outcome", changed); err != nil {
				t.Fatal(err)
			}
			request(http.MethodGet, path+"/task-workspace", nil, nil, http.StatusInternalServerError)
		}
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
	proposal     bool
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
	wantModule := "github.com/go-chi/chi/v5"
	if fixture.proposal {
		calls[0].ParametersJson = `{"path":"calc.go","content":"package calc\n\nfunc Add(left, right int) int { return left - right }\n"}`
		calls[1].ParametersJson = `{"path":"calc.go","old":"return left - right","new":"return left + right"}`
		wantModule = "example.test/codeops-proposal"
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
				if result.ExitCode != 0 || result.Error != "" || result.Truncated || index == 2 && !strings.Contains(result.Output, wantModule) {
					return fmt.Errorf("isolated tool %s failed: exit=%d truncated=%t error=%q output=%q", calls[index].ToolName, result.ExitCode, result.Truncated, result.Error, result.Output)
				}
				break
			}
		}
	}
	return stream.Send(&pb.OrchestratorMessage{Payload: &pb.OrchestratorMessage_Done{Done: &pb.Done{Success: true, Message: "isolated tools completed"}}})
}
