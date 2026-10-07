package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"google.golang.org/protobuf/encoding/protojson"
)

type agentAdmissionFault struct {
	EventLog
	enabled atomic.Bool
}

func (f *agentAdmissionFault) AppendSurface(ctx context.Context, id string, seq int64, kind string, payload any, op SurfaceOperation) (Event, error) {
	if f.enabled.Load() && strings.HasPrefix(id, "agent-") && kind == userMessageEventType {
		return Event{}, errors.New("fixture admission failure")
	}
	return f.EventLog.AppendSurface(ctx, id, seq, kind, payload, op)
}

func TestIndependentAgentRecoversCreatedTaskAndQueuedMessage(t *testing.T) {
	ctx := context.Background()
	fixture := &independentAgentFixture{}
	runner, _, actor := agentTestRunner(t, fixture)
	ledger := runner.workbench.ledger
	fault := &agentAdmissionFault{EventLog: ledger}
	fault.enabled.Store(true)
	runner.workbench.ledger = fault
	result := runner.ExecuteAgentTool(ctx, actor, actor.SessionID, orchestrator.ToolCall{ID: "spawn-crash", Name: "SpawnAgent", ParametersJSON: `{"kind":"explore","title":"recover","objective":"explicit recovery materials","parallel":true}`})
	if result.Error == "" {
		t.Fatal("admission failure was hidden")
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	childID := "agent-" + agentDigest([]byte(actor.SessionID + "\x00spawn-crash"))[:32]
	snapshot, err := ReadVerifiedSnapshot(ctx, ledger, childID)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := projectAgentTask(snapshot.Events)
	if err != nil || task.Status != "submitted" {
		t.Fatalf("created crash window = %v %v", task, err)
	}
	recovered := NewSessionRunner(NewWorkbench(ledger, nil), fixture, nil, SessionRunnerOptions{AgentWorkerCount: 1})
	t.Cleanup(func() { _ = recovered.Close() })
	if err := recovered.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	completed := agentTestCall(t, recovered, actor, "wait", "AgentTask", `{"action":"wait","task_id":"`+childID+`"}`)
	if completed.Status != "completed" {
		t.Fatalf("created task not recovered: %s", completed)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	message, _ := agentJSON.Marshal(&pb.AgentMessage{Id: "crash-message", Role: "user", Parts: []*pb.AgentPart{{Payload: &pb.AgentPart_Text{Text: "RESUME_CHILD_AFTER_RESTART"}}}})
	snapshot, _ = ReadVerifiedSnapshot(ctx, ledger, childID)
	if _, err := ledger.Append(ctx, childID, int64(len(snapshot.Events)), "agent/task-message", agentTaskMutation{RequestID: "crash-message", Message: message}); err != nil {
		t.Fatal(err)
	}
	second := NewSessionRunner(NewWorkbench(ledger, nil), fixture, nil, SessionRunnerOptions{AgentWorkerCount: 1})
	t.Cleanup(func() { _ = second.Close() })
	if err := second.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	completed = agentTestCall(t, second, actor, "wait-again", "AgentTask", `{"action":"wait","task_id":"`+childID+`"}`)
	if completed.Status != "completed" {
		t.Fatalf("queued message not recovered: %s", completed)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.requests) != 2 || len(fixture.requests[1].History) == 0 || !strings.Contains(fixture.requests[1].Input, "RESUME_CHILD_AFTER_RESTART") {
		t.Fatalf("recovered child histories = %+v", fixture.requests)
	}
}

func TestIndependentAgentApprovalIsHumanScopedAndNotInherited(t *testing.T) {
	ctx := context.Background()
	fixture := &independentAgentFixture{mode: "approved-write"}
	runner, _, actor := agentTestRunner(t, fixture)
	controller := permission.NewController(nil, nil)
	controller.ApproveSessionFor(actor, "Write")
	runner.options.Permissions = controller
	runner.options.AgentWorkspace = func(context.Context, AgentWorkspaceRequest) (string, error) { return t.TempDir(), nil }
	var effects atomic.Int32
	runner.tools = ToolExecutionFunc(func(_ context.Context, child identity.Actor, id string, call orchestrator.ToolCall) orchestrator.ToolResult {
		if child.SessionID != id || id == actor.SessionID {
			t.Error("effect actor not bound to child")
		}
		effects.Add(1)
		return orchestrator.ToolResult{Output: "authorized child effect"}
	})
	task := agentTestCall(t, runner, actor, "spawn-approval", "SpawnAgent", `{"kind":"general","title":"approve","objective":"explicit write","parallel":true}`)
	task = agentTestCall(t, runner, actor, "wait-approval", "AgentTask", `{"action":"wait","task_id":"`+task.Id+`"}`)
	if len(task.PendingApprovals) != 1 || effects.Load() != 0 {
		t.Fatalf("child inherited parent approval: %s effects=%d", task, effects.Load())
	}
	pending := task.PendingApprovals[0]
	result := runner.ExecuteAgentTool(ctx, actor, actor.SessionID, orchestrator.ToolCall{ID: "model-approval", Name: "AgentTask", ParametersJSON: `{"action":"approve","task_id":"` + task.Id + `"}`})
	if result.Error == "" || effects.Load() != 0 {
		t.Fatal("model approved a child tool")
	}
	command := ToolApprovalDecisionCommand{RunID: pending.RunId, ToolCallID: pending.ToolCallId, PendingEventID: pending.PendingEventId, PendingSeq: pending.PendingSeq + 1, Decision: ApprovalApproved}
	if _, err := runner.DecideAgentToolApproval(ctx, actor, task.Id, command); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("approval entity CAS = %v", err)
	}
	command.PendingSeq = pending.PendingSeq
	if _, err := runner.DecideAgentToolApproval(ctx, actor, task.Id, command); err != nil {
		t.Fatal(err)
	}
	task = agentTestCall(t, runner, actor, "wait-completed", "AgentTask", `{"action":"wait","task_id":"`+task.Id+`"}`)
	if task.Status != "completed" || effects.Load() != 1 {
		t.Fatalf("approved task = %s effects=%d", task, effects.Load())
	}
}

func TestIndependentAgentRejectsMutatedPublishedArtifactAndRequestReplay(t *testing.T) {
	fixture := &independentAgentFixture{mode: "blocked", started: make(chan struct{})}
	runner, _, actor := agentTestRunner(t, fixture)
	task := agentTestCall(t, runner, actor, "spawn-file", "SpawnAgent", `{"kind":"explore","title":"files","objective":"deliver pinned report","parallel":true}`)
	child := actor
	child.SessionID = task.ChildSessionId
	view, err := runner.workbench.Get(context.Background(), 7, child.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(view.WorkingDir, "report.txt"), []byte("verified file"), 0600); err != nil {
		t.Fatal(err)
	}
	result := runner.ExecuteAgentTool(context.Background(), child, child.SessionID, orchestrator.ToolCall{ID: "file", Name: "PublishArtifact", ParametersJSON: `{"artifact":{"name":"Report","parts":[{"file":{"path":"report.txt"}}]}}`})
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	artifact := &pb.AgentArtifact{}
	if err := protojson.Unmarshal([]byte(result.Output), artifact); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(view.WorkingDir, artifact.Parts[0].GetFile().Path), []byte("mutated pinned file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runner.readAgentTask(context.Background(), 7, task.Id, actor.SessionID); !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("artifact mutation accepted: %v", err)
	}
	mutation := agentTaskMutation{RequestID: "replayed-message", Message: json.RawMessage(`{"parts":[{"text":"first"}]}`)}
	if err := runner.appendAgentFact(context.Background(), child.SessionID, "agent/task-message", mutation, agentMutationExists("agent/task-message", mutation.RequestID)); err != nil {
		t.Fatal(err)
	}
	mutation.Message = json.RawMessage(`{"parts":[{"text":"conflicting"}]}`)
	if err := runner.appendAgentFact(context.Background(), child.SessionID, "agent/task-message", mutation, agentMutationExists("agent/task-message", mutation.RequestID)); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("conflicting request replay accepted: %v", err)
	}
}

func TestIndependentAgentRecoveryCompletesCancellationFence(t *testing.T) {
	ctx := context.Background()
	fixture := &independentAgentFixture{mode: "blocked", started: make(chan struct{})}
	runner, _, actor := agentTestRunner(t, fixture)
	task := agentTestCall(t, runner, actor, "spawn-cancel-crash", "SpawnAgent", `{"kind":"explore","title":"cancel","objective":"wait","parallel":true}`)
	select {
	case <-fixture.started:
	case <-time.After(3 * time.Second):
		t.Fatal("child did not start")
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	ledger := runner.workbench.ledger
	snapshot, _ := ReadVerifiedSnapshot(ctx, ledger, task.ChildSessionId)
	if _, err := ledger.Append(ctx, task.ChildSessionId, int64(len(snapshot.Events)), "agent/task-canceled", agentTaskMutation{RequestID: "cancel-crash"}); err != nil {
		t.Fatal(err)
	}
	recovered := NewSessionRunner(NewWorkbench(ledger, nil), fixture, nil, SessionRunnerOptions{})
	t.Cleanup(func() { _ = recovered.Close() })
	if err := recovered.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	events, _ := ledger.Events(ctx, task.ChildSessionId)
	runs, err := projectRuns(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if !run.terminal || run.view.Status != RunCanceled {
			t.Fatal("cancellation crash left a runnable child")
		}
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	sqliteLedger := ledger.(*SQLiteEventLog)
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	if stats := sqliteLedger.db.Stats(); stats.OpenConnections != 0 {
		t.Fatalf("ledger connections after close: %+v", stats)
	}
}

func TestIndependentAgentRecoveryRetriesWorkspaceLifecycleUntilAcknowledged(t *testing.T) {
	ctx := context.Background()
	fixture := &independentAgentFixture{mode: "input"}
	runner, _, actor := agentTestRunner(t, fixture)
	runner.options.AgentWorkspace = func(context.Context, AgentWorkspaceRequest) (string, error) {
		return t.TempDir(), nil
	}
	var lifecycleCalls atomic.Int32
	lifecycle := func(_ context.Context, event *pb.AgentLifecycle) error {
		if event.GetStatus() != "cancelled" {
			t.Fatalf("lifecycle status = %q", event.GetStatus())
		}
		if lifecycleCalls.Add(1) <= 2 {
			return errors.New("fixture lifecycle failure")
		}
		return nil
	}
	runner.options.AgentLifecycle = lifecycle
	task := agentTestCall(t, runner, actor, "spawn-lifecycle-recovery", "SpawnAgent", `{"kind":"general","title":"recover cleanup","objective":"ask before cleanup"}`)
	result := runner.ExecuteAgentTool(ctx, actor, actor.SessionID, orchestrator.ToolCall{
		ID: "cancel-lifecycle-recovery", Name: "AgentTask",
		ParametersJSON: `{"action":"cancel","task_id":"` + task.Id + `"}`,
	})
	if result.Error == "" || lifecycleCalls.Load() != 1 {
		t.Fatalf("initial lifecycle failure = result=%+v calls=%d", result, lifecycleCalls.Load())
	}
	retry := runner.ExecuteAgentTool(ctx, actor, actor.SessionID, orchestrator.ToolCall{
		ID: "cancel-lifecycle-recovery-retry", Name: "AgentTask",
		ParametersJSON: `{"action":"cancel","task_id":"` + task.Id + `"}`,
	})
	if retry.Error == "" || lifecycleCalls.Load() != 2 {
		t.Fatalf("cancel retry did not retry failed lifecycle: result=%+v calls=%d", retry, lifecycleCalls.Load())
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}

	recovered := NewSessionRunner(NewWorkbench(runner.workbench.ledger, nil), fixture, nil, SessionRunnerOptions{AgentLifecycle: lifecycle})
	t.Cleanup(func() { _ = recovered.Close() })
	if err := recovered.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if lifecycleCalls.Load() != 3 {
		t.Fatalf("recovered lifecycle calls = %d, want 3", lifecycleCalls.Load())
	}
	if err := recovered.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if lifecycleCalls.Load() != 3 {
		t.Fatalf("acknowledged lifecycle was repeated: %d calls", lifecycleCalls.Load())
	}
	events, err := recovered.workbench.ledger.Events(ctx, task.ChildSessionId)
	if err != nil {
		t.Fatal(err)
	}
	acknowledgements := 0
	for _, event := range events {
		if event.Type == agentWorkspaceLifecycleAcknowledgedEventType {
			acknowledgements++
		}
	}
	if acknowledgements != 1 {
		t.Fatalf("workspace lifecycle acknowledgements = %d, want 1", acknowledgements)
	}
}
