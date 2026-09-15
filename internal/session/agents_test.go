package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"google.golang.org/protobuf/encoding/protojson"
)

type independentAgentFixture struct {
	mu           sync.Mutex
	requests     []orchestrator.ConversationRequest
	started      chan struct{}
	mode         string
	inputRelease chan struct{}
	writeResult  orchestrator.ToolResult
}

func (f *independentAgentFixture) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	f.mu.Lock()
	f.requests = append(f.requests, request)
	n := len(f.requests)
	f.mu.Unlock()
	if f.started != nil && n == 1 {
		close(f.started)
	}
	if f.mode == "blocked" {
		<-ctx.Done()
		return orchestrator.ConversationResult{}, ctx.Err()
	}
	if f.mode == "failed" {
		return orchestrator.ConversationResult{}, errors.New("fixture failure")
	}
	if f.mode == "input" && n == 1 {
		result := handlers.Tool(ctx, orchestrator.ToolCall{ID: "ask-input", Name: "AskUser", ParametersJSON: `{"questions":[{"question":"Which branch?"}]}`})
		if result.Error != "" {
			return orchestrator.ConversationResult{}, errors.New(result.Error)
		}
		if f.inputRelease != nil {
			select {
			case <-f.inputRelease:
			case <-ctx.Done():
				return orchestrator.ConversationResult{}, ctx.Err()
			}
		}
		return orchestrator.ConversationResult{Success: true, Message: "Need branch input"}, nil
	}
	if f.mode == "forbidden-write" || f.mode == "approved-write" {
		f.writeResult = handlers.Tool(ctx, orchestrator.ToolCall{ID: "write", Name: "Write", ParametersJSON: `{"path":"forbidden.txt","content":"should never run"}`})
		if f.mode == "approved-write" && f.writeResult.Error != "" {
			return orchestrator.ConversationResult{}, errors.New("write rejected")
		}
	}
	result := handlers.Tool(ctx, orchestrator.ToolCall{ID: "artifact", Name: "PublishArtifact", ParametersJSON: `{"artifact":{"name":"Analysis","parts":[{"data_json":"{\"finding\":\"source verified\"}"}]}}`})
	if result.Error != "" {
		return orchestrator.ConversationResult{}, errors.New(result.Error)
	}
	return orchestrator.ConversationResult{Success: true, Message: "Independent task finished"}, nil
}

func agentTestRunner(t *testing.T, fixture *independentAgentFixture) (*SessionRunner, SessionView, identity.Actor) {
	t.Helper()
	ledger, err := OpenSQLiteEventLog(filepath.Join(t.TempDir(), "agents.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	workbench := NewWorkbench(ledger, nil)
	view, err := workbench.CreateWithWorkingDir(context.Background(), 7, "repo", "parent", "parent objective", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = workbench.AppendUserMessage(context.Background(), 7, view.ID, 1, "PARENT_PRIVATE_HISTORY_NOT_SHARED")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	runner := NewSessionRunner(workbench, fixture, ToolExecutionFunc(func(_ context.Context, _ identity.Actor, _ string, call orchestrator.ToolCall) orchestrator.ToolResult {
		t.Errorf("unexpected effect dispatch: %s", call.Name)
		return orchestrator.ToolResult{Error: "forbidden effect", ExitCode: 1}
	}), SessionRunnerOptions{WorkerCount: 1, AgentWorkerCount: 1, HeartbeatInterval: 100 * time.Millisecond, LeaseDuration: 2 * time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	return runner, view, actor
}

func agentTestCall(t *testing.T, runner *SessionRunner, actor identity.Actor, id, name, args string) *pb.AgentTask {
	t.Helper()
	result := runner.ExecuteAgentTool(context.Background(), actor, actor.SessionID, orchestrator.ToolCall{ID: id, Name: name, ParametersJSON: args})
	if result.Error != "" {
		t.Fatalf("%s = %+v", name, result)
	}
	task := &pb.AgentTask{}
	if err := protojson.Unmarshal([]byte(result.Output), task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestIndependentAgentTaskContextIdentityAndArtifacts(t *testing.T) {
	fixture := &independentAgentFixture{}
	runner, view, actor := agentTestRunner(t, fixture)
	task := agentTestCall(t, runner, actor, "spawn", "SpawnAgent", `{"kind":"explore","title":"inspect","objective":"inspect explicit sources","context":{"material":"EXPLICIT_CHILD_MATERIAL"}}`)
	if task.Status != "completed" || task.ChildSessionId == view.ID || task.Card.SchemaVersion != "agent.v2" || len(task.Artifacts) == 0 {
		t.Fatalf("task = %s", task)
	}
	fixture.mu.Lock()
	request := fixture.requests[0]
	count := len(fixture.requests)
	fixture.mu.Unlock()
	if count != 1 || len(request.History) != 0 || request.State != nil || request.Actor.SessionID != task.ChildSessionId || request.AgentTask == nil || request.MemoryContextJSON != "" {
		t.Fatalf("child inherited parent context: %+v", request)
	}
	if strings.Contains(request.Input, "PARENT_PRIVATE_HISTORY") || !strings.Contains(request.Input, "EXPLICIT_CHILD_MATERIAL") {
		t.Fatal("task materials not isolated")
	}
	for _, artifact := range task.Artifacts {
		if len(artifact.Checksum) != 64 {
			t.Fatal("artifact missing checksum")
		}
	}
	duplicate := agentTestCall(t, runner, actor, "spawn", "SpawnAgent", `{"kind":"explore","title":"inspect","objective":"inspect explicit sources","context":{"material":"EXPLICIT_CHILD_MATERIAL"}}`)
	if duplicate.Id != task.Id {
		t.Fatal("spawn was not idempotent")
	}
	foreign, err := runner.workbench.Create(context.Background(), 8, "repo", "foreign", "goal")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := identity.Default().BindSession(foreign.ID)
	result := runner.ExecuteAgentTool(context.Background(), other, other.SessionID, orchestrator.ToolCall{ID: "query", Name: "AgentTask", ParametersJSON: `{"action":"get","task_id":"` + task.Id + `"}`})
	if result.Error == "" {
		t.Fatal("foreign task was exposed")
	}
}

func TestIndependentAgentArtifactReplayAtCapacity(t *testing.T) {
	runner, _, actor := agentTestRunner(t, &independentAgentFixture{})
	task := agentTestCall(t, runner, actor, "spawn", "SpawnAgent", `{"kind":"explore","title":"inspect","objective":"inspect"}`)
	view, err := runner.workbench.Get(context.Background(), 7, task.ChildSessionId)
	if err != nil || view.Run == nil {
		t.Fatalf("completed child run unavailable: %v", err)
	}
	// Materialize the terminal report before filling the remaining capacity.
	runner.completeAgentTurn(context.Background(), runKey{sessionID: task.ChildSessionId, runID: view.Run.RunID})
	task, _, err = runner.readAgentTask(context.Background(), 7, task.Id, actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	child := actor
	child.SessionID = ""
	child, err = child.BindSession(task.ChildSessionId)
	if err != nil {
		t.Fatal(err)
	}
	args := `{"artifact":{"name":"Report","parts":[{"text":"bounded result"}]}}`
	var last orchestrator.ToolResult
	for i := len(task.Artifacts); i < 16; i++ {
		last = runner.ExecuteAgentTool(context.Background(), child, child.SessionID, orchestrator.ToolCall{ID: fmt.Sprintf("report-%d", i), Name: "PublishArtifact", ParametersJSON: args})
		if last.ExitCode != 0 {
			t.Fatalf("publish #%d: %+v", i, last)
		}
	}
	before, _, err := runner.readAgentTask(context.Background(), 7, task.Id, actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	replay := runner.ExecuteAgentTool(context.Background(), child, child.SessionID, orchestrator.ToolCall{ID: last.ToolCallID, Name: "PublishArtifact", ParametersJSON: args})
	if replay.ExitCode != 0 || replay.Output != last.Output {
		t.Fatalf("idempotent replay rejected at capacity: %+v", replay)
	}
	result := runner.ExecuteAgentTool(context.Background(), child, child.SessionID, orchestrator.ToolCall{ID: "overflow", Name: "PublishArtifact", ParametersJSON: args})
	if result.ExitCode == 0 {
		t.Fatal("new artifact must still respect capacity")
	}
	after, _, err := runner.readAgentTask(context.Background(), 7, task.Id, actor.SessionID)
	if err != nil || len(after.Artifacts) != 16 || after.Revision != before.Revision {
		t.Fatalf("replay changed durable task: %s, %v", after, err)
	}
}

func TestIndependentAgentLegacyKindsHaveReadOnlyCards(t *testing.T) {
	runner, _, actor := agentTestRunner(t, &independentAgentFixture{})
	result := runner.ExecuteAgentTool(context.Background(), actor, actor.SessionID, orchestrator.ToolCall{ID: "cards", Name: "AgentTask", ParametersJSON: `{"action":"cards"}`})
	if result.ExitCode != 0 {
		t.Fatal(result.Error)
	}
	for _, kind := range []string{"deep", "review", "security"} {
		card, allowed, err := agentCard(kind)
		if err != nil || card.Id != kind || !strings.Contains(result.Output, `"id":"`+kind+`"`) {
			t.Fatalf("legacy card %s unavailable: %s, %v", kind, result.Output, err)
		}
		for _, name := range allowed {
			if name == "Bash" || name == "Write" || name == "Edit" || name == "Git" {
				t.Fatalf("read-only card %s exposed %s", kind, name)
			}
		}
	}
}

func TestIndependentAgentConcurrentArtifactsRespectCapacity(t *testing.T) {
	runner, _, actor := agentTestRunner(t, &independentAgentFixture{})
	task := agentTestCall(t, runner, actor, "spawn", "SpawnAgent", `{"kind":"explore","title":"inspect","objective":"inspect"}`)
	child := actor
	child.SessionID = ""
	child, err := child.BindSession(task.ChildSessionId)
	if err != nil {
		t.Fatal(err)
	}
	args := `{"artifact":{"name":"Report","parts":[{"text":"concurrent result"}]}}`
	var wg sync.WaitGroup
	results := make(chan orchestrator.ToolResult, 24)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- runner.ExecuteAgentTool(context.Background(), child, child.SessionID, orchestrator.ToolCall{ID: fmt.Sprintf("concurrent-%d", i), Name: "PublishArtifact", ParametersJSON: args})
		}(i)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for result := range results {
		if result.ExitCode == 0 {
			accepted++
		}
	}
	final, _, err := runner.readAgentTask(context.Background(), 7, task.Id, actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	durable := 0
	for _, artifact := range final.Artifacts {
		if strings.HasPrefix(artifact.Id, "concurrent-") {
			durable++
		}
	}
	if len(final.Artifacts) != 16 || durable != accepted {
		t.Fatalf("concurrent capacity: accepted=%d task=%s err=%v", accepted, final, err)
	}
}

func TestIndependentAgentInputMessageAndFailureStates(t *testing.T) {
	fixture := &independentAgentFixture{mode: "input", inputRelease: make(chan struct{})}
	runner, _, actor := agentTestRunner(t, fixture)
	task := agentTestCall(t, runner, actor, "spawn", "SpawnAgent", `{"kind":"plan","title":"plan","objective":"choose branch"}`)
	if task.Status != "input_required" || len(task.Messages) != 2 {
		t.Fatalf("input state = %s", task)
	}
	_ = agentTestCall(t, runner, actor, "supplement", "AgentTask", `{"action":"message","task_id":"`+task.Id+`","message":{"parts":[{"text":"Use branch main"},{"data_json":"{\"scope\":\"tests\"}"}]}}`)
	close(fixture.inputRelease)
	task = agentTestCall(t, runner, actor, "wait", "AgentTask", `{"action":"wait","task_id":"`+task.Id+`"}`)
	if task.Status != "completed" {
		t.Fatalf("supplement did not resume child: %s", task)
	}
	fixture.mu.Lock()
	requests := append([]orchestrator.ConversationRequest(nil), fixture.requests...)
	fixture.mu.Unlock()
	if len(requests) != 2 || requests[0].SessionID != requests[1].SessionID || !strings.Contains(requests[1].Input, "Use branch main") || len(requests[1].History) == 0 {
		t.Fatal("child turn history was not independently resumed")
	}
	failure := &independentAgentFixture{mode: "failed"}
	failRunner, _, failActor := agentTestRunner(t, failure)
	failed := agentTestCall(t, failRunner, failActor, "fail-spawn", "SpawnAgent", `{"kind":"explore","title":"fail","objective":"fail explicitly"}`)
	if failed.Status != "failed" || failed.ErrorCode == "" {
		t.Fatalf("failed state = %s", failed)
	}
}

func TestIndependentAgentCancellationAndReadOnlyPolicy(t *testing.T) {
	fixture := &independentAgentFixture{mode: "blocked", started: make(chan struct{})}
	runner, _, actor := agentTestRunner(t, fixture)
	task := agentTestCall(t, runner, actor, "spawn", "SpawnAgent", `{"kind":"explore","title":"blocked","objective":"wait","parallel":true}`)
	select {
	case <-fixture.started:
	case <-time.After(3 * time.Second):
		t.Fatal("child never started")
	}
	task = agentTestCall(t, runner, actor, "cancel", "AgentTask", `{"action":"cancel","task_id":"`+task.Id+`"}`)
	if task.Status != "canceled" {
		t.Fatalf("cancel state = %s", task)
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := runner.workbench.ledger.Events(context.Background(), task.ChildSessionId)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := projectRuns(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if !run.terminal || run.view.Status != RunCanceled {
			t.Fatal("canceled task left a recoverable run")
		}
	}
	readonly := &independentAgentFixture{mode: "forbidden-write"}
	readRunner, _, readActor := agentTestRunner(t, readonly)
	_ = agentTestCall(t, readRunner, readActor, "read-only", "SpawnAgent", `{"kind":"explore","title":"inspect","objective":"read only"}`)
	if readonly.writeResult.Error == "" {
		t.Fatal("read-only child was allowed to write")
	}
}

func TestAgentArtifactFileSnapshotAndTraversalRejection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.patch")
	if err := os.WriteFile(path, []byte("source patch"), 0600); err != nil {
		t.Fatal(err)
	}
	parts := []*pb.AgentPart{{Payload: &pb.AgentPart_File{File: &pb.AgentFile{Path: "report.patch", MediaType: "text/x-diff"}}}}
	if err := validateAgentParts(parts, dir, dir); err != nil {
		t.Fatal(err)
	}
	if err := pinAgentFiles(parts, dir, ".agent/artifacts"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed after publish"), 0600); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, parts[0].GetFile().Path))
	if err != nil || string(body) != "source patch" || agentDigest(body) != parts[0].GetFile().Sha256 {
		t.Fatal("artifact did not preserve published file")
	}
	escape := []*pb.AgentPart{{Payload: &pb.AgentPart_File{File: &pb.AgentFile{Path: "../outside"}}}}
	if validateAgentParts(escape, dir, dir) == nil {
		t.Fatal("artifact path escaped workspace")
	}
}
