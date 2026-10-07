package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type independentAgentFixture struct {
	mu             sync.Mutex
	requests       []orchestrator.ConversationRequest
	started        chan struct{}
	mode           string
	inputRelease   chan struct{}
	writeResult    orchestrator.ToolResult
	externalResult orchestrator.ToolResult
}

type agentResourceRecorder struct {
	released chan string
}

type agentLifecycleRecorder struct {
	mu        sync.Mutex
	attempts  []*pb.AgentLifecycle
	failCount int
	notified  chan struct{}
}

func (r *agentLifecycleRecorder) handle(_ context.Context, lifecycle *pb.AgentLifecycle) error {
	copy := proto.Clone(lifecycle).(*pb.AgentLifecycle)
	r.mu.Lock()
	r.attempts = append(r.attempts, copy)
	shouldFail := r.failCount > 0
	if shouldFail {
		r.failCount--
	}
	r.mu.Unlock()
	select {
	case r.notified <- struct{}{}:
	default:
	}
	if shouldFail {
		return errors.New("fixture lifecycle failure")
	}
	return nil
}

func (r *agentLifecycleRecorder) snapshot() []*pb.AgentLifecycle {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*pb.AgentLifecycle, len(r.attempts))
	copy(out, r.attempts)
	return out
}

func waitForAgentLifecycle(t *testing.T, recorder *agentLifecycleRecorder, taskID, status string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		for _, lifecycle := range recorder.snapshot() {
			if lifecycle.RequestId == taskID && lifecycle.ChildSessionId == taskID && lifecycle.Status == status {
				return
			}
		}
		select {
		case <-recorder.notified:
		case <-deadline.C:
			t.Fatalf("agent lifecycle %s for %s was not emitted: %+v", status, taskID, recorder.snapshot())
		}
	}
}

func (r *agentResourceRecorder) Execute(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult {
	return orchestrator.ToolResult{Error: "unexpected effect dispatch", ExitCode: 1}
}

func (r *agentResourceRecorder) ReleaseSession(sessionID string) error {
	r.released <- sessionID
	return nil
}

func waitForAgentRelease(t *testing.T, recorder *agentResourceRecorder, sessionID string) {
	t.Helper()
	select {
	case got := <-recorder.released:
		if got != sessionID {
			t.Fatalf("released session = %q, want %q", got, sessionID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("session %s resources were not released", sessionID)
	}
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
	if f.mode == "external" {
		f.externalResult = handlers.Tool(ctx, orchestrator.ToolCall{ID: "external", Name: "e2e_echo", ParametersJSON: `{"text":"hello"}`})
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

func TestIndependentAgentReleasesOnlyTerminalTaskResources(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		runner, _, actor := agentTestRunner(t, &independentAgentFixture{})
		recorder := &agentResourceRecorder{released: make(chan string, 4)}
		runner.tools = recorder
		task := agentTestCall(t, runner, actor, "complete-release", "SpawnAgent", `{"kind":"explore","title":"complete","objective":"finish"}`)
		waitForAgentRelease(t, recorder, task.ChildSessionId)
		if err := runner.Close(); err != nil {
			t.Fatal(err)
		}
		ledger := runner.workbench.ledger.(*SQLiteEventLog)
		if err := ledger.Close(); err != nil {
			t.Fatal(err)
		}
		if stats := ledger.db.Stats(); stats.OpenConnections != 0 {
			t.Fatalf("ledger connections after close: %+v", stats)
		}
	})

	t.Run("failed", func(t *testing.T) {
		runner, _, actor := agentTestRunner(t, &independentAgentFixture{mode: "failed"})
		recorder := &agentResourceRecorder{released: make(chan string, 4)}
		runner.tools = recorder
		task := agentTestCall(t, runner, actor, "failed-release", "SpawnAgent", `{"kind":"explore","title":"fail","objective":"fail"}`)
		waitForAgentRelease(t, recorder, task.ChildSessionId)
	})

	t.Run("input required then canceled", func(t *testing.T) {
		runner, _, actor := agentTestRunner(t, &independentAgentFixture{mode: "input"})
		recorder := &agentResourceRecorder{released: make(chan string, 4)}
		runner.tools = recorder
		task := agentTestCall(t, runner, actor, "input-retain", "SpawnAgent", `{"kind":"plan","title":"input","objective":"ask"}`)
		if task.Status != "input_required" {
			t.Fatalf("task status = %s", task.Status)
		}
		select {
		case released := <-recorder.released:
			t.Fatalf("input-required session released early: %s", released)
		default:
		}
		task = agentTestCall(t, runner, actor, "input-cancel", "AgentTask", `{"action":"cancel","task_id":"`+task.Id+`"}`)
		waitForAgentRelease(t, recorder, task.ChildSessionId)
	})
}

func TestIndependentAgentManagedWorkspaceLifecycle(t *testing.T) {
	t.Run("completed workspace is retained", func(t *testing.T) {
		runner, _, actor := agentTestRunner(t, &independentAgentFixture{})
		recorder := &agentLifecycleRecorder{notified: make(chan struct{}, 8)}
		runner.options.AgentWorkspace = func(_ context.Context, _ AgentWorkspaceRequest) (string, error) {
			return t.TempDir(), nil
		}
		runner.options.AgentLifecycle = recorder.handle

		task := agentTestCall(t, runner, actor, "complete-workspace", "SpawnAgent", `{"kind":"general","title":"complete","objective":"finish"}`)
		if task.Status != "completed" {
			t.Fatalf("task status = %s", task.Status)
		}
		select {
		case <-recorder.notified:
			t.Fatalf("completed retained workspace emitted cleanup lifecycle: %+v", recorder.snapshot())
		case <-time.After(100 * time.Millisecond):
		}
	})

	t.Run("failed workspace is retained for a later turn", func(t *testing.T) {
		fixture := &independentAgentFixture{mode: "failed"}
		runner, _, actor := agentTestRunner(t, fixture)
		recorder := &agentLifecycleRecorder{notified: make(chan struct{}, 8)}
		workspace := t.TempDir()
		runner.options.AgentWorkspace = func(_ context.Context, _ AgentWorkspaceRequest) (string, error) {
			return workspace, nil
		}
		runner.options.AgentLifecycle = recorder.handle

		task := agentTestCall(t, runner, actor, "failed-workspace", "SpawnAgent", `{"kind":"general","title":"fail","objective":"fail"}`)
		if task.Status != "failed" {
			t.Fatalf("task status = %s", task.Status)
		}
		select {
		case <-recorder.notified:
			t.Fatalf("failed retained workspace emitted cleanup lifecycle: %+v", recorder.snapshot())
		case <-time.After(100 * time.Millisecond):
		}
		fixture.mode = ""
		_ = agentTestCall(t, runner, actor, "retry-failed-workspace", "AgentTask", `{"action":"message","task_id":"`+task.Id+`","message":{"parts":[{"text":"retry in the same workspace"}]}}`)
		task = agentTestCall(t, runner, actor, "wait-failed-workspace", "AgentTask", `{"action":"wait","task_id":"`+task.Id+`"}`)
		if task.Status != "completed" || task.WorkingDir != workspace {
			t.Fatalf("failed task did not resume in retained workspace: %s", task)
		}
		if _, err := os.Stat(workspace); err != nil {
			t.Fatalf("retained workspace unavailable after retry: %v", err)
		}
	})

	t.Run("canceled workspace is released", func(t *testing.T) {
		runner, _, actor := agentTestRunner(t, &independentAgentFixture{mode: "input"})
		recorder := &agentLifecycleRecorder{notified: make(chan struct{}, 8)}
		runner.options.AgentWorkspace = func(_ context.Context, _ AgentWorkspaceRequest) (string, error) {
			return t.TempDir(), nil
		}
		runner.options.AgentLifecycle = recorder.handle

		task := agentTestCall(t, runner, actor, "cancel-workspace", "SpawnAgent", `{"kind":"general","title":"cancel","objective":"ask first"}`)
		if task.Status != "input_required" {
			t.Fatalf("task status = %s", task.Status)
		}
		task = agentTestCall(t, runner, actor, "cancel-workspace-task", "AgentTask", `{"action":"cancel","task_id":"`+task.Id+`"}`)
		waitForAgentLifecycle(t, recorder, task.Id, "cancelled")
	})

	t.Run("invalid assignment is rejected before workspace allocation", func(t *testing.T) {
		runner, _, actor := agentTestRunner(t, &independentAgentFixture{})
		recorder := &agentLifecycleRecorder{notified: make(chan struct{}, 8)}
		allocations := 0
		runner.options.AgentWorkspace = func(_ context.Context, _ AgentWorkspaceRequest) (string, error) {
			allocations++
			return t.TempDir(), nil
		}
		runner.options.AgentLifecycle = recorder.handle

		result := runner.ExecuteAgentTool(context.Background(), actor, actor.SessionID, orchestrator.ToolCall{
			ID: "invalid-assignment", Name: "SpawnAgent",
			ParametersJSON: `{"kind":"general","title":"invalid","objective":"use a missing file","message":{"parts":[{"file":{"path":"missing.txt"}}]}}`,
		})
		if result.Error == "" {
			t.Fatal("invalid assignment unexpectedly created a task")
		}
		if allocations != 0 || len(recorder.snapshot()) != 0 {
			t.Fatalf("invalid assignment allocated workspace: allocations=%d lifecycle=%+v", allocations, recorder.snapshot())
		}
	})

	t.Run("post-allocation failure compensates workspace", func(t *testing.T) {
		runner, view, actor := agentTestRunner(t, &independentAgentFixture{})
		recorder := &agentLifecycleRecorder{notified: make(chan struct{}, 8), failCount: 2}
		if err := os.WriteFile(filepath.Join(view.WorkingDir, "material.txt"), []byte("verified material"), 0o600); err != nil {
			t.Fatal(err)
		}
		allocations := 0
		runner.options.AgentWorkspace = func(_ context.Context, _ AgentWorkspaceRequest) (string, error) {
			allocations++
			return filepath.Join(view.WorkingDir, "missing-worktree"), nil
		}
		runner.options.AgentLifecycle = recorder.handle

		result := runner.ExecuteAgentTool(context.Background(), actor, actor.SessionID, orchestrator.ToolCall{
			ID: "post-allocation-failure", Name: "SpawnAgent",
			ParametersJSON: `{"kind":"general","title":"invalid target","objective":"copy material","message":{"parts":[{"file":{"path":"material.txt"}}]}}`,
		})
		if result.Error == "" || allocations != 1 {
			t.Fatalf("post-allocation failure was not surfaced: result=%+v allocations=%d", result, allocations)
		}
		taskID := "agent-" + agentDigest([]byte(actor.SessionID + "\x00post-allocation-failure"))[:32]
		waitForAgentLifecycle(t, recorder, taskID, "cancelled")
		if got := len(recorder.snapshot()); got != 3 {
			t.Fatalf("compensation lifecycle attempts = %d, want 3", got)
		}
	})
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

func TestIndependentAgentRequiresExplicitRegisteredExternalTool(t *testing.T) {
	fixture := &independentAgentFixture{}
	runner, _, actor := agentTestRunner(t, fixture)
	runner.options.AgentWorkspace = func(_ context.Context, request AgentWorkspaceRequest) (string, error) {
		return request.ParentWorkingDir, nil
	}
	runner.options.AgentToolBinding = func(name string) (AgentToolBinding, bool) {
		return AgentToolBinding{Name: name, Server: "fixture", InputSchemaSHA256: strings.Repeat("a", 64), ServerConfigSHA256: strings.Repeat("c", 64)}, name == "e2e_echo"
	}

	task := agentTestCall(t, runner, actor, "external", "SpawnAgent", `{"kind":"general","title":"inspect","objective":"use the delegated MCP tool","allowed_tools":["e2e_echo"]}`)
	fixture.mu.Lock()
	request := fixture.requests[0]
	fixture.mu.Unlock()
	if task.Status != "completed" || !slices.Contains(request.AllowedTools, "e2e_echo") {
		t.Fatalf("registered external tool was not delegated: task=%s tools=%v", task, request.AllowedTools)
	}

	denied := runner.ExecuteAgentTool(context.Background(), actor, actor.SessionID, orchestrator.ToolCall{
		ID: "unregistered", Name: "SpawnAgent",
		ParametersJSON: `{"kind":"explore","title":"inspect","objective":"use an unknown tool","allowed_tools":["not_registered"]}`,
	})
	if denied.ExitCode == 0 || denied.Error == "" {
		t.Fatalf("unregistered external tool was delegated: %+v", denied)
	}
	readOnlyDenied := runner.ExecuteAgentTool(context.Background(), actor, actor.SessionID, orchestrator.ToolCall{
		ID: "read-only-external", Name: "SpawnAgent",
		ParametersJSON: `{"kind":"explore","title":"inspect","objective":"use an external tool","allowed_tools":["e2e_echo"]}`,
	})
	if readOnlyDenied.ExitCode == 0 || readOnlyDenied.Error == "" {
		t.Fatalf("read-only Agent Card accepted a dynamic MCP tool: %+v", readOnlyDenied)
	}
}

func TestIndependentAgentPinsAndRevalidatesExternalToolBinding(t *testing.T) {
	fixture := &independentAgentFixture{mode: "external"}
	runner, _, actor := agentTestRunner(t, fixture)
	runner.options.AgentWorkspace = func(_ context.Context, request AgentWorkspaceRequest) (string, error) {
		return request.ParentWorkingDir, nil
	}
	calls := 0
	runner.options.AgentToolBinding = func(name string) (AgentToolBinding, bool) {
		calls++
		configChecksum := strings.Repeat("c", 64)
		if calls > 1 {
			configChecksum = strings.Repeat("d", 64)
		}
		return AgentToolBinding{Name: name, Server: "fixture", InputSchemaSHA256: strings.Repeat("a", 64), ServerConfigSHA256: configChecksum}, name == "e2e_echo"
	}

	task := agentTestCall(t, runner, actor, "binding-drift", "SpawnAgent", `{"kind":"general","title":"inspect","objective":"use MCP","allowed_tools":["e2e_echo"]}`)
	if fixture.externalResult.Error != "delegated tool binding changed" || fixture.externalResult.ExitCode == 0 {
		t.Fatalf("drifted external binding was not rejected: %+v", fixture.externalResult)
	}
	events, err := runner.workbench.ledger.Events(context.Background(), task.ChildSessionId)
	if err != nil {
		t.Fatal(err)
	}
	_, created, err := projectAgentTask(events)
	if err != nil || len(created.ExternalToolBindings) != 1 || created.ExternalToolBindings[0].ServerConfigSHA256 != strings.Repeat("c", 64) {
		t.Fatalf("task-created binding was not durably pinned: created=%+v err=%v", created, err)
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
