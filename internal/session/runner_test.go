package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/skills"
	"code-agent/internal/tools"
	"google.golang.org/protobuf/encoding/protojson"
)

type recordingConversationAdapter struct {
	mu       sync.Mutex
	requests []orchestrator.ConversationRequest
	reply    string
	err      error
}

type blockingConversationAdapter struct {
	started  chan struct{}
	finished chan struct{}
}

type crossSessionBlockingConversationAdapter struct {
	firstStarted chan struct{}
	releaseFirst chan struct{}
	mu           sync.Mutex
	calls        int
	releaseOnce  sync.Once
}

func (a *crossSessionBlockingConversationAdapter) release() {
	a.releaseOnce.Do(func() { close(a.releaseFirst) })
}

func (a *crossSessionBlockingConversationAdapter) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, _ orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	a.mu.Lock()
	a.calls++
	call := a.calls
	a.mu.Unlock()
	if call == 1 {
		close(a.firstStarted)
		select {
		case <-a.releaseFirst:
		case <-ctx.Done():
			return orchestrator.ConversationResult{}, ctx.Err()
		}
	}
	return orchestrator.ConversationResult{Success: true, Message: request.Input + " done"}, nil
}

type recoverableConversationAdapter struct {
	mu    sync.Mutex
	calls int
}

type retryToolConversationAdapter struct {
	mu          sync.Mutex
	toolResults []orchestrator.ToolResult
}

type readonlyRetryConversationAdapter struct {
	mu          sync.Mutex
	toolResults []orchestrator.ToolResult
}

// skillConversationAdapter models the provider turn that exposed the server
// wiring regression: a repository-oriented turn invokes Skill before it can
// produce an answer. A missing Skills manager must fail the run durably, while
// a later retry with the manager configured must be able to execute a fresh
// invocation and keep the failed run in its retry lineage.
type skillConversationAdapter struct {
	mu    sync.Mutex
	calls int
}

func (a *skillConversationAdapter) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	if handlers.Tool == nil {
		return orchestrator.ConversationResult{}, errors.New("tool handler is missing")
	}
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	result := handlers.Tool(ctx, orchestrator.ToolCall{
		// Providers may reuse the same logical tool call while retrying a
		// failed turn; the runner must not replay a failed receipt forever.
		ID: "skill-call", Name: "Skill", ParametersJSON: `{"name":"commit"}`,
	})
	if result.Error != "" {
		return orchestrator.ConversationResult{}, errors.New(result.Error)
	}
	return orchestrator.ConversationResult{Success: true, Message: "repository exploration continued"}, nil
}

type executorToolAdapter struct {
	executor *tools.Executor
}

func (a executorToolAdapter) Execute(ctx context.Context, _ identity.Actor, sessionID string, call orchestrator.ToolCall) orchestrator.ToolResult {
	var arguments map[string]any
	if strings.TrimSpace(call.ParametersJSON) != "" {
		if err := json.Unmarshal([]byte(call.ParametersJSON), &arguments); err != nil {
			return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "invalid tool parameters", ExitCode: 1}
		}
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	result, err := a.executor.Execute(ctx, tools.ToolRequest{Name: call.Name, Arguments: arguments, OwnerSessionID: sessionID})
	out := orchestrator.ToolResult{
		ToolCallID: call.ID, ToolName: call.Name, Output: result.Output,
		Error: result.Error, ExitCode: int32(result.ExitCode), Truncated: result.Truncated,
	}
	if err != nil && out.Error == "" {
		out.Error = err.Error()
	}
	if err != nil && out.ExitCode == 0 {
		out.ExitCode = 1
	}
	return out
}

func (a *readonlyRetryConversationAdapter) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	if handlers.Tool == nil {
		return orchestrator.ConversationResult{}, errors.New("tool handler is missing")
	}
	result := handlers.Tool(ctx, orchestrator.ToolCall{ID: "retry-readonly", Name: "Grep", ParametersJSON: "{\"glob\":\"internal/session/*.go\",\"head_limit\":5,\"pattern\":\"func\"}"})
	a.mu.Lock()
	a.toolResults = append(a.toolResults, result)
	a.mu.Unlock()
	return orchestrator.ConversationResult{Success: true, Message: "re-executed readonly result"}, nil
}

type approvalToolConversationAdapter struct {
	result chan orchestrator.ToolResult
}

type planTodoConversationAdapter struct {
	called bool
}

type progressConversationAdapter struct{}

type progressToolAdapter struct{}

func TestLatestRunSourceSeqSkipsTransportOnlyTail(t *testing.T) {
	runID := "run:source"
	events := []Event{
		{Seq: 0, Type: sessionCreatedEventType, Payload: json.RawMessage(`{"id":"session"}`)},
		{Seq: 1, Type: continuationEventType, Payload: json.RawMessage(`{"run_id":"run:source"}`)},
		{Seq: 2, Type: "execution_result", Payload: json.RawMessage(`{"run_id":"run:source","status":"turn_started"}`)},
		{Seq: 3, Type: "agent/worktree-terminal", Payload: json.RawMessage(`{"worktree":"agent-slot","status":"cleaned"}`)},
	}
	if got := latestRunSourceSeq(events, runID); got != 2 {
		t.Fatalf("latestRunSourceSeq = %d, want authoritative run event seq 2", got)
	}
}

func (progressToolAdapter) Execute(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult {
	return orchestrator.ToolResult{Output: "PRIVATE_TOOL_OUTPUT_DO_NOT_SHOW"}
}

func (progressConversationAdapter) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	if handlers.PlanTodo == nil || handlers.Tool == nil {
		return orchestrator.ConversationResult{}, errors.New("progress handlers are missing")
	}
	if err := handlers.PlanTodo(nil, &codeagentpb.TodoUpdate{
		Todos:    []*codeagentpb.TodoItem{{Content: "inspect the repository", ActiveForm: "inspecting the repository", Status: "in_progress"}},
		Revision: 1,
	}); err != nil {
		return orchestrator.ConversationResult{}, err
	}
	result := handlers.Tool(ctx, orchestrator.ToolCall{
		ID: "progress-tool", Name: "Read", ParametersJSON: `{"path":"credentials/DO_NOT_DISPLAY.txt"}`,
	})
	if result.Error != "" {
		return orchestrator.ConversationResult{}, errors.New(result.Error)
	}
	return orchestrator.ConversationResult{Success: true, Message: "progress complete"}, nil
}

func (a *planTodoConversationAdapter) RunConversation(_ context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	a.called = true
	if handlers.PlanTodo == nil {
		return orchestrator.ConversationResult{}, errors.New("plan/todo handler is missing")
	}
	if err := handlers.PlanTodo(nil, &codeagentpb.TodoUpdate{
		Todos:    []*codeagentpb.TodoItem{{Content: "inspect", ActiveForm: "inspecting", Status: "in_progress"}},
		Revision: 1,
	}); err != nil {
		return orchestrator.ConversationResult{}, err
	}
	return orchestrator.ConversationResult{Success: true, Message: "todo persisted"}, nil
}

func TestSessionRunnerPersistsPlanTodoUpdatesFromConversation(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "todo", "goal")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	conversation := &planTodoConversationAdapter{}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-plan-todo"})
	defer runner.Close()
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{
		RequestID: "req-plan-todo", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: int64(created.EventCount), Content: "implement a multi-step feature across several files",
		Actor: identity.Default(),
	})
	if err != nil {
		t.Fatalf("submit message: %v", err)
	}
	view := waitForRunStatus(t, runner, created.ID, run.RunID, RunCompleted)
	if view.Status != RunCompleted {
		t.Fatalf("run status = %s, want completed", view.Status)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var found bool
	for _, event := range events {
		if event.Type == "session/plan-todo" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("plan/todo update was not persisted: %v", events)
	}
	current, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatalf("project current session: %v", err)
	}
	if current.PlanTodo == nil || current.PlanTodo.Revision != 1 || len(current.PlanTodo.Todos) != 1 {
		t.Fatalf("plan/todo projection = %+v, want revision 1 with one todo", current.PlanTodo)
	}
	if got := current.PlanTodo.Todos[0]; got.Content != "inspect" || got.ActiveForm != "inspecting" || got.Status != "in_progress" {
		t.Fatalf("todo projection = %+v", got)
	}
}

func TestSessionRunnerPersistsSafeProgressSummariesForMultiStepRun(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	runner := NewSessionRunner(workbench, progressConversationAdapter{}, progressToolAdapter{}, SessionRunnerOptions{WorkerID: "worker-progress"})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-progress", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)

	var events []Event
	deadline := time.Now().Add(time.Second)
	for {
		events, err = ledger.Events(ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		completedProgress := false
		for _, event := range events {
			if event.Type != progressEventType {
				continue
			}
			var payload progressPayload
			if json.Unmarshal(event.Payload, &payload) == nil && payload.Title == "任务完成" {
				completedProgress = true
				break
			}
		}
		if completedProgress {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("completion progress was not appended after terminal fact: %+v", events)
		}
		time.Sleep(5 * time.Millisecond)
	}
	var progress []Event
	var completedSeq int64 = -1
	var completionProgressSeq int64 = -1
	for _, event := range events {
		if event.Type == "session/progress" {
			progress = append(progress, event)
			var payload progressPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Title == "任务完成" {
				completionProgressSeq = event.Seq
			}
		}
		if event.Type == runCompletedEventType {
			completedSeq = event.Seq
		}
	}
	if len(progress) < 4 {
		t.Fatalf("progress events = %d, want at least start, plan/todo, tool, and terminal summaries: %+v", len(progress), events)
	}
	for _, event := range progress {
		payload := string(event.Payload)
		if strings.Contains(payload, "PRIVATE_TOOL_OUTPUT_DO_NOT_SHOW") || strings.Contains(payload, "credentials/DO_NOT_DISPLAY.txt") {
			t.Fatalf("progress summary leaked tool content or arguments: %s", payload)
		}
	}
	views, err := workbench.Events(ctx, 7, created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var visible int
	for _, event := range views {
		if event.Type == "session/progress" && strings.TrimSpace(event.Content) != "" {
			visible++
		}
	}
	if visible != len(progress) {
		t.Fatalf("progress views = %d, want %d visible summaries", visible, len(progress))
	}
	if completedSeq < 0 || completionProgressSeq <= completedSeq {
		t.Fatalf("completion progress seq = %d, run completed seq = %d; progress must follow the authoritative terminal fact", completionProgressSeq, completedSeq)
	}
}

type cancelingHeartbeatEventLog struct {
	EventLog
	started chan struct{}
}

func (l *cancelingHeartbeatEventLog) Events(ctx context.Context, _ string) ([]Event, error) {
	close(l.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (a *approvalToolConversationAdapter) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	if handlers.Tool == nil {
		return orchestrator.ConversationResult{}, errors.New("tool handler is missing")
	}
	result := handlers.Tool(ctx, orchestrator.ToolCall{
		ID: "approval-call", Name: "Write",
		ParametersJSON: `{"content":"approved","path":"runtime/approved.txt"}`,
	})
	if a.result != nil {
		a.result <- result
	}
	return orchestrator.ConversationResult{Success: true, Message: "approval completed"}, nil
}

func (a *retryToolConversationAdapter) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	if handlers.Tool == nil {
		return orchestrator.ConversationResult{}, errors.New("tool handler is missing")
	}
	result := handlers.Tool(ctx, orchestrator.ToolCall{
		ID:             "retry-call",
		Name:           "Write",
		ParametersJSON: `{"content":"retry","path":"runtime/retry.txt"}`,
	})
	a.mu.Lock()
	a.toolResults = append(a.toolResults, result)
	a.mu.Unlock()
	return orchestrator.ConversationResult{Success: true, Message: "reused committed result"}, nil
}

type countingToolAdapter struct {
	mu    sync.Mutex
	calls int
}

type codeChangeToolAdapter struct{}

func (codeChangeToolAdapter) Execute(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult {
	return orchestrator.ToolResult{
		Output:  "written",
		Changes: []orchestrator.CodeChange{{Path: "src/example.txt", Before: "old-secret-content", After: "new-secret-content"}},
	}
}

func (a *countingToolAdapter) Execute(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	return orchestrator.ToolResult{Output: "unexpected external execution"}
}

func (a *blockingConversationAdapter) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, _ orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	close(a.started)
	<-ctx.Done()
	if a.finished != nil {
		close(a.finished)
	}
	return orchestrator.ConversationResult{}, ctx.Err()
}

func (a *recordingConversationAdapter) RunConversation(_ context.Context, request orchestrator.ConversationRequest, _ orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	a.mu.Lock()
	a.requests = append(a.requests, request)
	a.mu.Unlock()
	if a.err != nil {
		return orchestrator.ConversationResult{}, a.err
	}
	return orchestrator.ConversationResult{Success: true, Message: a.reply}, nil
}

func (a *recordingConversationAdapter) lastRequest(t *testing.T) orchestrator.ConversationRequest {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.requests) == 0 {
		t.Fatal("conversation adapter was not called")
	}
	return a.requests[len(a.requests)-1]
}

func (a *recoverableConversationAdapter) RunConversation(_ context.Context, _ orchestrator.ConversationRequest, _ orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.calls == 1 {
		return orchestrator.ConversationResult{}, errors.New("rpc error: code = Unavailable desc = transport is closing")
	}
	return orchestrator.ConversationResult{Success: true, Message: "resumed after transport recovery"}, nil
}

func TestSessionRunnerContinuationIsIdempotentAndCommitsAssistantTerminal(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "title", "goal")
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "continue this task")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 2, message.ID, "resume")
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.UpdateStatus(ctx, 7, created.ID, 3, "paused"); err != nil {
		t.Fatal(err)
	}

	conversation := &recordingConversationAdapter{reply: "resumed answer"}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{
		WorkerID: "worker-test", LeaseDuration: time.Second, HeartbeatInterval: 100 * time.Millisecond,
	})
	t.Cleanup(func() { _ = runner.Close() })
	command := ContinueCommand{
		RequestID: "request-1", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4,
		Actor: identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "alice", TenantID: "org:one", Roles: []string{"USER"}},
	}
	accepted, err := runner.RequestContinuation(ctx, command)
	if err != nil {
		t.Fatalf("request continuation: %v", err)
	}
	if accepted.RunID == "" || accepted.RequestID != command.RequestID || accepted.Status != RunQueued {
		t.Fatalf("accepted run = %+v", accepted)
	}

	// A retry after a lost HTTP response must return the original run even after
	// the ledger advanced; it must not append a second continuation request.
	retried, err := runner.RequestContinuation(ctx, command)
	if err != nil {
		t.Fatalf("retry continuation: %v", err)
	}
	if retried.RunID != accepted.RunID {
		t.Fatalf("retry run id = %q, want %q", retried.RunID, accepted.RunID)
	}
	if _, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-1", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: "different-checkpoint", ExpectedSeq: command.ExpectedSeq,
		Actor: command.Actor,
	}); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("request id reuse with a different checkpoint = %v, want conflict", err)
	}

	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	if terminal.Attempt != 1 || terminal.Error != "" {
		t.Fatalf("terminal run = %+v", terminal)
	}
	request := conversation.lastRequest(t)
	if request.SessionID != created.ID || request.RunID != accepted.RunID || !request.Resume || request.Input != "continue this task" {
		t.Fatalf("conversation request = %+v", request)
	}
	if request.Actor.SessionID != created.ID || request.Actor.ActorID != "user:7" {
		t.Fatalf("bound actor = %+v", request.Actor)
	}

	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Progress receipts are observational ledger facts and may be interleaved
	// with execution events; the execution ordering assertion below ignores
	// them while still requiring every durable lifecycle transition.
	eventsWithoutProgress := make([]Event, 0, len(events))
	for _, event := range events {
		if event.Type != progressEventType {
			eventsWithoutProgress = append(eventsWithoutProgress, event)
		}
	}
	wantTypes := []string{
		"session/created", "user/message", "checkpoint/create", "session/status-updated",
		continuationEventType, runLeasedEventType, "assistant/message", runCompletedEventType,
	}
	if len(eventsWithoutProgress) != len(wantTypes) {
		t.Fatalf("execution events = %d, want %d: %+v", len(eventsWithoutProgress), len(wantTypes), events)
	}
	for index, want := range wantTypes {
		if eventsWithoutProgress[index].Type != want {
			t.Fatalf("event[%d] = %q, want %q", index, eventsWithoutProgress[index].Type, want)
		}
	}
	view, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "done" || view.Run == nil || view.Run.Status != RunCompleted {
		t.Fatalf("completed session = %+v", view)
	}
	surface, err := ledger.Surface(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := surface[len(surface)-1].Type; got != "assistant/message" {
		t.Fatalf("surface tail = %q", got)
	}
}

func TestSessionRunnerContinuationCarriesCanonicalWorkingDir(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	workingDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("workspace metadata only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "title", "goal", workingDir)
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "resume")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 2, message.ID, "resume")
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.UpdateStatus(ctx, 7, created.ID, 3, "paused"); err != nil {
		t.Fatal(err)
	}
	conversation := &recordingConversationAdapter{reply: "ok"}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-working-dir"})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-working-dir", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	request := conversation.lastRequest(t)
	if got := request.WorkingDir; got != workingDir {
		t.Fatalf("conversation working dir = %q, want %q", got, workingDir)
	}
	if request.ContextEnvelope == nil || request.ContextEnvelope.SchemaVersion != contextEnvelopeVersion {
		t.Fatalf("context envelope = %+v", request.ContextEnvelope)
	}
	found := false
	for _, item := range request.ContextEnvelope.P0 {
		found = found || item.Path == "README.md"
	}
	if !found || len(request.ContextEnvelope.Events) == 0 || request.ContextEnvelope.LedgerChecksum == "" {
		t.Fatalf("context envelope missing workspace or ledger metadata: %+v", request.ContextEnvelope)
	}
}

func TestSessionRunnerSubmitMessageStartsNaturalLanguageTurnAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "natural language", "reply to users")
	if err != nil {
		t.Fatal(err)
	}
	conversation := &recordingConversationAdapter{reply: "natural language reply"}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-natural"})
	t.Cleanup(func() { _ = runner.Close() })
	command := SubmitMessageCommand{
		RequestID: "message-request-1", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: 1, Content: "please explain this repository", Actor: testRunnerActor(),
	}
	accepted, err := runner.SubmitMessage(ctx, command)
	if err != nil {
		t.Fatalf("submit message: %v", err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	if terminal.RunID != accepted.RunID {
		t.Fatalf("terminal run = %+v", terminal)
	}
	if !conversation.lastRequest(t).NewTurn {
		t.Fatal("durable user message did not assert new-turn identity")
	}
	repeated, err := runner.SubmitMessage(ctx, command)
	if err != nil || repeated.RunID != accepted.RunID {
		t.Fatalf("idempotent submit = %+v err=%v", repeated, err)
	}
	if _, err := runner.SubmitMessage(ctx, SubmitMessageCommand{
		RequestID: command.RequestID, SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: 1, Content: "different content", Actor: testRunnerActor(),
	}); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("request content conflict = %v, want ErrSessionStateConflict", err)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Type]++
	}
	if counts[userMessageEventType] != 1 || counts[checkpointEventType] != 1 || counts[continuationEventType] != 1 || counts["assistant/message"] != 1 || counts[runCompletedEventType] != 1 {
		t.Fatalf("natural language event counts = %+v", counts)
	}
	checkpoints, err := workbench.ListCheckpoints(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 0 {
		t.Fatalf("automatic checkpoints leaked into manual list: %+v", checkpoints)
	}
}

func TestSessionRunnerSubmitMessageRejectsSecondTurnWhileRunIsActive(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "active run", "serialize turns")
	if err != nil {
		t.Fatal(err)
	}
	conversation := &blockingConversationAdapter{started: make(chan struct{}), finished: make(chan struct{})}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-active"})
	t.Cleanup(func() { _ = runner.Close() })
	if _, err := runner.SubmitMessage(ctx, SubmitMessageCommand{
		RequestID: "message-active-1", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: 1, Content: "first turn", Actor: testRunnerActor(),
	}); err != nil {
		t.Fatal(err)
	}
	<-conversation.started
	view, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.SubmitMessage(ctx, SubmitMessageCommand{
		RequestID: "message-active-2", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: int64(view.EventCount), Content: "second turn", Actor: testRunnerActor(),
	}); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("active run submit error = %v, want ErrSessionStateConflict", err)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	messageCount := 0
	for _, event := range events {
		if event.Type == userMessageEventType {
			messageCount++
		}
	}
	if messageCount != 1 {
		t.Fatalf("active run accepted %d user messages, want 1", messageCount)
	}
}

func TestSessionRunnerRunsDifferentSessionsConcurrently(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	first, err := workbench.Create(ctx, 7, "repo", "first", "slow provider")
	if err != nil {
		t.Fatal(err)
	}
	second, err := workbench.Create(ctx, 7, "repo", "second", "should not wait")
	if err != nil {
		t.Fatal(err)
	}
	conversation := &crossSessionBlockingConversationAdapter{firstStarted: make(chan struct{}), releaseFirst: make(chan struct{})}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-cross-session"})
	t.Cleanup(func() { conversation.release(); _ = runner.Close() })
	firstRun, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "cross-first", SessionID: first.ID, OwnerID: 7, ExpectedSeq: 1, Content: "slow", Actor: testRunnerActor()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first conversation did not start")
	}
	secondRun, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "cross-second", SessionID: second.ID, OwnerID: 7, ExpectedSeq: 1, Content: "fast", Actor: testRunnerActor()})
	if err != nil {
		t.Fatal(err)
	}
	if terminal := waitForRunStatus(t, runner, second.ID, secondRun.RunID, RunCompleted); terminal.Status != RunCompleted {
		t.Fatalf("second session remained blocked by first: %+v", terminal)
	}
	conversation.release()
	_ = waitForRunStatus(t, runner, first.ID, firstRun.RunID, RunCompleted)
}

func TestSessionRunnerFailureWritesOneTerminalAndReturnsSessionToPaused(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	conversation := &recordingConversationAdapter{err: errors.New("orchestrator unavailable")}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-fail", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-fail", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunFailed)
	if terminal.Error != "agent continuation failed" {
		t.Fatalf("public terminal error = %q", terminal.Error)
	}
	view, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "paused" || view.Run == nil || view.Run.Status != RunFailed {
		t.Fatalf("failed session = %+v", view)
	}
}

func TestSessionRunnerSkillFailureContinuesWithSameRetryLineage(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)

	// The first attempt mirrors the pre-fix server wiring: the executor exists,
	// but no Skills manager has been installed yet.
	executor := tools.NewExecutor(t.TempDir())
	t.Cleanup(func() { _ = executor.Close() })
	conversation := &skillConversationAdapter{}
	runner := NewSessionRunner(workbench, conversation, executorToolAdapter{executor: executor}, SessionRunnerOptions{
		WorkerID: "worker-skill-retry", LeaseDuration: time.Second,
	})
	t.Cleanup(func() { _ = runner.Close() })

	first, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-skill-first", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatalf("request initial Skill continuation: %v", err)
	}
	failed := waitForRunStatus(t, runner, created.ID, first.RunID, RunFailed)
	if failed.Error != "agent continuation failed" {
		t.Fatalf("initial Skill failure = %+v, want public continuation failure", failed)
	}

	// Verify the durable failure retained its categorized runtime error and the
	// failed Skill result, rather than silently dropping the tool invocation.
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawRuntimeFailure, sawMissingManager bool
	for _, event := range events {
		switch event.Type {
		case "execution_result":
			var payload struct {
				ErrorCode string `json:"error_code"`
			}
			if json.Unmarshal(event.Payload, &payload) == nil && payload.ErrorCode == "continuation_runtime_error" {
				sawRuntimeFailure = true
			}
		case "tool/result":
			var payload toolResultPayload
			if json.Unmarshal(event.Payload, &payload) == nil && payload.Error == "skills manager not available" {
				sawMissingManager = true
			}
		}
	}
	if !sawRuntimeFailure || !sawMissingManager {
		t.Fatalf("missing Skill manager failure was not durably categorized: runtime=%v missing_manager=%v events=%+v", sawRuntimeFailure, sawMissingManager, events)
	}

	// Install the same built-in catalog used by the server's continuation
	// wiring, then let the user continue the paused task. The provider retries
	// the same invocation id, so the runner must execute it again after the
	// capability is fixed instead of reusing the failed receipt.
	executor.SetSkillsManager(skills.NewManager())
	latest, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-skill-retry", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: int64(len(latest)), Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatalf("request Skill retry: %v", err)
	}
	completed := waitForRunStatus(t, runner, created.ID, second.RunID, RunCompleted)
	if completed.Status != RunCompleted || completed.RetryOfRunID != first.RunID || len(completed.RetryOfRunIDs) != 1 || completed.RetryOfRunIDs[0] != first.RunID {
		t.Fatalf("Skill retry lineage = %+v, want completed retry of %q", completed, first.RunID)
	}

	events, err = ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var successfulSkillResults int
	for _, event := range events {
		if event.Type != "tool/result" {
			continue
		}
		var payload toolResultPayload
		if json.Unmarshal(event.Payload, &payload) == nil && payload.RunID == second.RunID && payload.Error == "" && payload.Output != "" {
			successfulSkillResults++
		}
	}
	if successfulSkillResults != 1 {
		t.Fatalf("successful Skill retry results = %d, want exactly one; events=%+v", successfulSkillResults, events)
	}
}

func TestSessionRunnerRecoverClaimsExpiredLeaseWithNextAttempt(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-recover", "run-recover", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{
		RunID: "run-recover", RequestID: "request-recover", LeaseID: "dead-lease",
		WorkerID: "dead-worker", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	conversation := &recordingConversationAdapter{reply: "recovered"}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-recovery", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	if err := runner.Recover(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, "run-recover", RunCompleted)
	if terminal.Attempt != 2 || terminal.WorkerID != "worker-recovery" {
		t.Fatalf("recovered run = %+v", terminal)
	}
}

func TestSessionRunnerRecoverDoesNotResurrectDeletedSession(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-deleted", "run-deleted", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{
		RunID: "run-deleted", RequestID: "request-deleted", LeaseID: "expired-deleted",
		WorkerID: "dead-worker", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := workbench.Delete(ctx, 7, created.ID, 6); err != nil {
		t.Fatal(err)
	}

	conversation := &recordingConversationAdapter{reply: "must not run"}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-after-delete", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	if err := runner.Recover(ctx); err != nil {
		t.Fatalf("recover deleted session: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	conversation.mu.Lock()
	calls := len(conversation.requests)
	conversation.mu.Unlock()
	if calls != 0 {
		t.Fatalf("deleted session was resurrected with %d model calls", calls)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 7 || events[len(events)-1].Type != "session/deleted" {
		t.Fatalf("deleted session history changed during recovery: %+v", events)
	}
}

func TestSessionRunnerDeletionCancelsInFlightConversation(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	blocking := &blockingConversationAdapter{started: make(chan struct{}), finished: make(chan struct{})}
	runner := NewSessionRunner(workbench, blocking, nil, SessionRunnerOptions{
		WorkerID: "worker-delete-in-flight", LeaseDuration: 10 * time.Second, HeartbeatInterval: 5 * time.Second,
	})
	t.Cleanup(func() { _ = runner.Close() })
	if _, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-delete-in-flight", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("conversation did not start")
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.Delete(ctx, 7, created.ID, int64(len(events))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocking.finished:
	case <-time.After(500 * time.Millisecond):
		history, _ := ledger.Events(ctx, created.ID)
		t.Fatalf("deleted in-flight run waited for its heartbeat instead of canceling immediately: %+v", history)
	}
	history, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != len(events)+1 || history[len(history)-1].Type != "session/deleted" {
		t.Fatalf("deletion appended execution facts after cancellation: %+v", history)
	}
}

type recoverSessionErrorLog struct {
	EventLog
	badSessionID string
	healthyID    string
	err          error
}

type legacyOnlyRecoveryLog struct {
	EventLog
	events []Event
}

func (l *legacyOnlyRecoveryLog) SessionIDs(context.Context) ([]string, error) {
	return []string{"legacy-only-session"}, nil
}

func (l *legacyOnlyRecoveryLog) Events(context.Context, string) ([]Event, error) {
	return l.events, nil
}

func TestSessionRunnerRecoverIgnoresLegacyStateEventStreams(t *testing.T) {
	legacy := &legacyOnlyRecoveryLog{events: []Event{{
		SessionID: "legacy-only-session", Seq: 0, EventID: "legacy-state-event", Type: sessionStateEventType,
	}}}
	workbench := NewWorkbench(legacy, nil)
	runner := NewSessionRunner(workbench, &recordingConversationAdapter{reply: "must not run"}, nil, SessionRunnerOptions{WorkerID: "worker-legacy-state"})
	t.Cleanup(func() { _ = runner.Close() })
	if err := runner.Recover(context.Background()); err != nil {
		t.Fatalf("legacy state streams are owned by the event-store module: %v", err)
	}
}

func TestSessionRunnerRecoverIgnoresLegacyOnlySessionIDs(t *testing.T) {
	workbench := NewWorkbench(&legacyOnlyRecoveryLog{}, nil)
	runner := NewSessionRunner(workbench, &recordingConversationAdapter{reply: "must not run"}, nil, SessionRunnerOptions{WorkerID: "worker-legacy-only"})
	t.Cleanup(func() { _ = runner.Close() })
	if err := runner.Recover(context.Background()); err != nil {
		t.Fatalf("legacy-only IDs are not schedulable recovery failures: %v", err)
	}
}

func (l *recoverSessionErrorLog) SessionIDs(context.Context) ([]string, error) {
	return []string{l.badSessionID, l.healthyID}, nil
}

func (l *recoverSessionErrorLog) Events(ctx context.Context, sessionID string) ([]Event, error) {
	if sessionID == l.badSessionID {
		return nil, l.err
	}
	return l.EventLog.Events(ctx, sessionID)
}

func TestSessionRunnerRecoverContinuesHealthySessionsAfterOneLedgerFailure(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	seedWorkbench := NewWorkbench(ledger, nil)
	_, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	if created.ID == "healthy-session" {
		t.Fatal("test fixture unexpectedly used reserved session id")
	}
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := continuationRequestPayloadForTest(ctx, seedWorkbench, created.ID, checkpoint.Hash, "request-recover-after-error", "run-recover-after-error", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{
		RunID: "run-recover-after-error", RequestID: "request-recover-after-error", LeaseID: "expired-recover-after-error",
		WorkerID: "dead-worker", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	corruptErr := fmt.Errorf("corrupt session ledger")
	recoverLedger := &recoverSessionErrorLog{EventLog: ledger, badSessionID: "bad-session", healthyID: created.ID, err: corruptErr}
	workbench := NewWorkbench(recoverLedger, nil)
	conversation := &recordingConversationAdapter{reply: "recovered despite neighboring failure"}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-recover-after-error", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	if err := runner.Recover(ctx); !errors.Is(err, corruptErr) {
		t.Fatalf("recover error = %v, want joined corrupt ledger error", err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, "run-recover-after-error", RunCompleted)
	if terminal.Attempt != 2 || terminal.WorkerID != "worker-recover-after-error" {
		t.Fatalf("healthy session was not recovered after neighboring failure: %+v", terminal)
	}
}

func TestSessionRunnerConcurrentRecoverSingleLeaseClaim(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-concurrent-recover", "run-concurrent-recover", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{
		RunID: "run-concurrent-recover", RequestID: "request-concurrent-recover", LeaseID: "expired-concurrent-lease",
		WorkerID: "dead-worker", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	firstConversation := &recordingConversationAdapter{reply: "recovered"}
	secondConversation := &recordingConversationAdapter{reply: "recovered"}
	first := NewSessionRunner(workbench, firstConversation, nil, SessionRunnerOptions{WorkerID: "worker-concurrent-1", LeaseDuration: time.Second})
	second := NewSessionRunner(workbench, secondConversation, nil, SessionRunnerOptions{WorkerID: "worker-concurrent-2", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = first.Recover(ctx) }()
	go func() { defer wg.Done(); _ = second.Recover(ctx) }()
	wg.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, readErr := ledger.Events(ctx, created.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		projection, projectErr := projectRun(events, "run-concurrent-recover")
		if projectErr != nil {
			t.Fatal(projectErr)
		}
		if projection.terminal && projection.view.Status == RunCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectRun(events, "run-concurrent-recover")
	if err != nil {
		t.Fatal(err)
	}
	if !projection.terminal || projection.view.Status != RunCompleted {
		t.Fatalf("concurrent recovery projection = %+v", projection.view)
	}
	var leases, assistantMessages int
	for _, event := range events {
		switch event.Type {
		case runLeasedEventType:
			var lease runLeasePayload
			if json.Unmarshal(event.Payload, &lease) == nil && lease.RunID == "run-concurrent-recover" && lease.Attempt == 2 {
				leases++
			}
		case "assistant/message":
			var message messagePayload
			if json.Unmarshal(event.Payload, &message) == nil && message.RunID == "run-concurrent-recover" {
				assistantMessages++
			}
		}
	}
	if leases != 1 {
		t.Fatalf("attempt-2 lease events = %d, want 1", leases)
	}
	if assistantMessages != 1 {
		t.Fatalf("assistant messages = %d, want 1", assistantMessages)
	}
	firstConversation.mu.Lock()
	firstCalls := len(firstConversation.requests)
	firstConversation.mu.Unlock()
	secondConversation.mu.Lock()
	secondCalls := len(secondConversation.requests)
	secondConversation.mu.Unlock()
	if firstCalls+secondCalls != 1 {
		t.Fatalf("conversation calls = %d, want 1", firstCalls+secondCalls)
	}
}

func TestSessionRunnerClaimUnexpiredLeaseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-unexpired-lease", "run-unexpired-lease", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, payload); err != nil {
		t.Fatal(err)
	}
	leaseUntil := time.Now().Add(time.Minute).UTC()
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{
		RunID: "run-unexpired-lease", RequestID: "request-unexpired-lease", LeaseID: "lease-unexpired",
		WorkerID: "worker-existing", Attempt: 1, LeaseUntil: leaseUntil,
	}); err != nil {
		t.Fatal(err)
	}
	runner := NewSessionRunner(workbench, &recordingConversationAdapter{reply: "unused"}, nil, SessionRunnerOptions{WorkerID: "worker-retry", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	key := runKey{sessionID: created.ID, runID: "run-unexpired-lease"}
	first, wait, err := runner.claim(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if first != (runLeasePayload{}) {
		t.Fatalf("claim returned a new lease for an unexpired run: %+v", first)
	}
	if wait.IsZero() || wait.Before(leaseUntil.Add(-time.Second)) {
		t.Fatalf("waitUntil = %v, want near existing lease expiry %v", wait, leaseUntil)
	}
	eventsBefore, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, waitAgain, err := runner.claim(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if waitAgain.IsZero() {
		t.Fatal("second claim returned zero wait for unexpired lease")
	}
	eventsAfter, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatalf("unexpired claim appended events: before=%d after=%d", len(eventsBefore), len(eventsAfter))
	}
}

func TestSessionRunnerRecoverStopsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ledger := openWorkbenchTestLedger(t)
	workbench, _, _ := pausedSessionWithCheckpoint(t, ledger)
	runner := NewSessionRunner(workbench, &recordingConversationAdapter{reply: "unused"}, nil, SessionRunnerOptions{WorkerID: "worker-canceled-recover"})
	t.Cleanup(func() { _ = runner.Close() })
	if err := runner.Recover(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("recover canceled error = %v, want context canceled", err)
	}
}

func TestSessionRunnerCloseClearsQueuedRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &SessionRunner{
		ctx: ctx, cancel: cancel, queue: make(chan runKey, 1),
		queued: make(map[runKey]struct{}),
	}
	key := runKey{sessionID: "session-close", runID: "run-close"}
	runner.queued[key] = struct{}{}
	runner.queue <- key
	if err := runner.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	runner.queuedMu.Lock()
	queued := len(runner.queued)
	runner.queuedMu.Unlock()
	if queued != 0 {
		t.Fatalf("queued runs after close = %d, want 0", queued)
	}
	if len(runner.queue) != 0 {
		t.Fatalf("buffered runs after close = %d, want 0", len(runner.queue))
	}
}

func TestSessionRunnerRejectsContinuationAfterClose(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	runner := NewSessionRunner(workbench, &recordingConversationAdapter{reply: "unused"}, nil, SessionRunnerOptions{WorkerID: "worker-closed-request"})
	if err := runner.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-after-close", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	}); !errors.Is(err, ErrSessionRunnerClosed) {
		t.Fatalf("request after close error = %v, want %v", err, ErrSessionRunnerClosed)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("request after close changed canonical ledger: %d events", len(events))
	}
}

func TestSessionRunnerRetriesTransportFailureAfterLeaseWithoutTerminalFailure(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	conversation := &recoverableConversationAdapter{}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{
		WorkerID: "worker-transport-retry", LeaseDuration: 80 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond,
	})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-transport-retry", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	if terminal.Attempt != 2 || terminal.Error != "" {
		t.Fatalf("transport-recovered run = %+v", terminal)
	}
	conversation.mu.Lock()
	calls := conversation.calls
	conversation.mu.Unlock()
	if calls != 2 {
		t.Fatalf("conversation calls = %d, want 2", calls)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == runFailedEventType {
			t.Fatalf("recoverable transport failure wrote terminal event: %+v", event)
		}
	}
}

func TestSessionRunnerRetryReusesCommittedToolResultWithoutExecutingAgain(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldPayload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-failed", "run-failed", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, oldPayload); err != nil {
		t.Fatal(err)
	}
	oldLease := runLeasePayload{
		RunID: "run-failed", RequestID: "request-failed", LeaseID: "lease-failed",
		WorkerID: "worker-failed", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, oldLease); err != nil {
		t.Fatal(err)
	}
	call := toolCallPayload{RunID: "run-failed", ToolCallID: "retry-call", ToolName: "Write", ArgumentsJSON: `{"content":"retry","path":"runtime/retry.txt"}`}
	if _, err := ledger.AppendSurface(ctx, created.ID, 6, "tool/call", call, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 7, toolDispatchedType, call); err != nil {
		t.Fatal(err)
	}
	committed := toolResultPayload{RunID: "run-failed", ToolCallID: "retry-call", ToolName: "Write", Output: "committed once"}
	if _, err := ledger.AppendSurface(ctx, created.ID, 8, "tool/result", committed, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 9, runFailedEventType, runTerminalPayload{
		RunID: "run-failed", RequestID: "request-failed", LeaseID: "lease-failed", Attempt: 1, Error: "agent continuation failed",
	}); err != nil {
		t.Fatal(err)
	}

	conversation := &retryToolConversationAdapter{}
	external := &countingToolAdapter{}
	runner := NewSessionRunner(workbench, conversation, external, SessionRunnerOptions{WorkerID: "worker-retry", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-retry", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 10, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	if terminal.Attempt != 1 {
		t.Fatalf("new retry run attempt = %d, want 1", terminal.Attempt)
	}
	external.mu.Lock()
	toolCalls := external.calls
	external.mu.Unlock()
	if toolCalls != 0 {
		t.Fatalf("retry executed external tool %d times, want 0", toolCalls)
	}
	conversation.mu.Lock()
	defer conversation.mu.Unlock()
	if len(conversation.toolResults) != 1 || conversation.toolResults[0].Output != "committed once" {
		t.Fatalf("retry tool result = %#v, want committed receipt", conversation.toolResults)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var currentResults int
	for _, event := range events {
		if event.Type != "tool/result" {
			continue
		}
		var payload toolResultPayload
		if json.Unmarshal(event.Payload, &payload) == nil && payload.RunID == accepted.RunID {
			currentResults++
			if payload.Output != "committed once" {
				t.Fatalf("retry result output = %q, want committed receipt", payload.Output)
			}
		}
	}
	if currentResults != 1 {
		t.Fatalf("current retry result receipts = %d, want 1", currentResults)
	}
}

func TestSessionRunnerWaitsForDurableApprovalBeforeToolDispatch(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	conversation := &approvalToolConversationAdapter{result: make(chan orchestrator.ToolResult, 1)}
	external := &countingToolAdapter{}
	permissions := permission.NewController(map[string]permission.Level{"Write": permission.AskSession}, nil)
	runner := NewSessionRunner(workbench, conversation, external, SessionRunnerOptions{
		WorkerID: "worker-approval", LeaseDuration: 3 * time.Second,
		HeartbeatInterval: time.Second, Permissions: permissions,
	})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-approval", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}

	var pending EventView
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events, readErr := workbench.Events(ctx, 7, created.ID, 0)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, event := range events {
			if event.Type == approvalPendingEventType {
				pending = event
				break
			}
		}
		if pending.Approval != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pending.Approval == nil || pending.Approval.RunID != accepted.RunID || pending.Approval.ToolCallID != "approval-call" {
		t.Fatalf("pending approval = %+v", pending)
	}
	external.mu.Lock()
	beforeApproval := external.calls
	external.mu.Unlock()
	if beforeApproval != 0 {
		t.Fatalf("tool executed before approval %d times", beforeApproval)
	}
	if _, err := workbench.DecideToolApproval(ctx, 7, created.ID, ToolApprovalDecisionCommand{
		RunID: accepted.RunID, ToolCallID: "approval-call",
		PendingEventID: pending.ID, PendingSeq: pending.Seq, Decision: ApprovalApproved,
	}); err != nil {
		t.Fatalf("approve pending tool: %v", err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	if terminal.Error != "" {
		t.Fatalf("completed run = %+v", terminal)
	}
	select {
	case result := <-conversation.result:
		if result.Error != "" || result.Output != "unexpected external execution" {
			t.Fatalf("approved tool result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("approved tool result was not returned")
	}
	external.mu.Lock()
	afterApproval := external.calls
	external.mu.Unlock()
	if afterApproval != 1 {
		t.Fatalf("approved tool executed %d times, want 1", afterApproval)
	}

	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"tool/call", approvalPendingEventType, approvalApprovedEventType, toolDispatchedType, "tool/result"}
	next := 0
	for _, event := range events {
		if next < len(wantOrder) && event.Type == wantOrder[next] {
			next++
		}
	}
	if next != len(wantOrder) {
		t.Fatalf("approval execution order stopped at %d of %d: %+v", next, len(wantOrder), events)
	}
}

func TestSessionRunnerPersistsContentFreeCodeModificationReceipt(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	conversation := &approvalToolConversationAdapter{result: make(chan orchestrator.ToolResult, 1)}
	runner := NewSessionRunner(workbench, conversation, codeChangeToolAdapter{}, SessionRunnerOptions{
		WorkerID: "worker-code-receipt", LeaseDuration: time.Second,
	})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-code-receipt", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	events, err := workbench.Events(ctx, 7, created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var modified EventView
	for _, event := range events {
		if event.Type == codeModifiedEventType {
			modified = event
			break
		}
	}
	if modified.CodeModification == nil {
		t.Fatalf("code modification receipt missing: %+v", events)
	}
	if modified.CodeModification.Path != "src/example.txt" || modified.CodeModification.Operation != "Write" || len(modified.CodeModification.DiffSHA256) != 64 {
		t.Fatalf("code modification receipt = %+v", modified.CodeModification)
	}
	raw, err := json.Marshal(modified.CodeModification)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "old-secret-content") || strings.Contains(string(raw), "new-secret-content") {
		t.Fatalf("code receipt leaked file content: %s", raw)
	}
}

func TestSessionRunnerDenialReturnsToolErrorWithoutDispatch(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	conversation := &approvalToolConversationAdapter{result: make(chan orchestrator.ToolResult, 1)}
	external := &countingToolAdapter{}
	permissions := permission.NewController(map[string]permission.Level{"Write": permission.AlwaysAsk}, nil)
	runner := NewSessionRunner(workbench, conversation, external, SessionRunnerOptions{
		WorkerID: "worker-denial", LeaseDuration: 3 * time.Second,
		HeartbeatInterval: time.Second, Permissions: permissions,
	})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-denial", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var pending EventView
	for time.Now().Before(deadline) {
		events, readErr := workbench.Events(ctx, 7, created.ID, 0)
		if readErr != nil {
			t.Fatal(readErr)
		}
		found := false
		for _, event := range events {
			if event.Type == approvalPendingEventType {
				pending = event
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pending.Approval == nil {
		t.Fatal("pending approval was not persisted")
	}
	if _, err := workbench.DecideToolApproval(ctx, 7, created.ID, ToolApprovalDecisionCommand{
		RunID: accepted.RunID, ToolCallID: "approval-call",
		PendingEventID: pending.ID, PendingSeq: pending.Seq, Decision: ApprovalDenied,
	}); err != nil {
		t.Fatalf("deny pending tool: %v", err)
	}
	waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	result := <-conversation.result
	if result.Error != "tool approval denied" || result.ExitCode != 1 {
		t.Fatalf("denied tool result = %+v", result)
	}
	external.mu.Lock()
	toolCalls := external.calls
	external.mu.Unlock()
	if toolCalls != 0 {
		t.Fatalf("denied tool executed %d times", toolCalls)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var denied, resultSeen bool
	for _, event := range events {
		switch event.Type {
		case approvalDeniedEventType:
			denied = true
		case toolDispatchedType:
			t.Fatalf("denied tool was marked dispatched: %+v", event)
		case "tool/result":
			resultSeen = true
		}
	}
	if !denied || !resultSeen {
		t.Fatalf("denied approval/result facts missing: %+v", events)
	}
}

func TestSessionRunnerRecoversPendingApprovalAfterLedgerReopen(t *testing.T) {
	ctx := context.Background()
	ledgerPath := filepath.Join(t.TempDir(), "approval-restart.sqlite")
	firstLedger, err := OpenSQLiteEventLog(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	firstWorkbench, created, checkpoint := pausedSessionWithCheckpoint(t, firstLedger)
	permissions := permission.NewController(map[string]permission.Level{"Write": permission.AskSession}, nil)
	firstRunner := NewSessionRunner(firstWorkbench, &approvalToolConversationAdapter{result: make(chan orchestrator.ToolResult, 1)}, &countingToolAdapter{}, SessionRunnerOptions{
		WorkerID: "worker-before-restart", LeaseDuration: 120 * time.Millisecond,
		HeartbeatInterval: 40 * time.Millisecond, Permissions: permissions,
	})
	accepted, err := firstRunner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-restart-approval", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var pending EventView
	for time.Now().Before(deadline) {
		events, readErr := firstWorkbench.Events(ctx, 7, created.ID, 0)
		if readErr != nil {
			t.Fatal(readErr)
		}
		found := false
		for _, event := range events {
			if event.Type == approvalPendingEventType {
				pending = event
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pending.Approval == nil {
		t.Fatal("pending approval was not persisted before restart")
	}
	if err := firstRunner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := firstLedger.Close(); err != nil {
		t.Fatal(err)
	}

	secondLedger, err := OpenSQLiteEventLog(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	secondWorkbench := NewWorkbench(secondLedger, nil)
	if _, err := secondWorkbench.DecideToolApproval(ctx, 7, created.ID, ToolApprovalDecisionCommand{
		RunID: accepted.RunID, ToolCallID: "approval-call",
		PendingEventID: pending.ID, PendingSeq: pending.Seq, Decision: ApprovalApproved,
	}); err != nil {
		t.Fatalf("approve restored pending tool: %v", err)
	}
	external := &countingToolAdapter{}
	secondRunner := NewSessionRunner(secondWorkbench, &approvalToolConversationAdapter{result: make(chan orchestrator.ToolResult, 1)}, external, SessionRunnerOptions{
		WorkerID: "worker-after-restart", LeaseDuration: 120 * time.Millisecond,
		HeartbeatInterval: 40 * time.Millisecond, Permissions: permissions,
	})
	t.Cleanup(func() {
		_ = secondRunner.Close()
		_ = secondLedger.Close()
	})
	if err := secondRunner.Recover(ctx); err != nil {
		t.Fatalf("recover approval run: %v", err)
	}
	terminal := waitForRunStatus(t, secondRunner, created.ID, accepted.RunID, RunCompleted)
	if terminal.RunID != accepted.RunID || terminal.Attempt < 2 {
		t.Fatalf("recovered approval lineage = %+v", terminal)
	}
	external.mu.Lock()
	toolCalls := external.calls
	external.mu.Unlock()
	if toolCalls != 1 {
		t.Fatalf("restored approval executed tool %d times, want 1", toolCalls)
	}
	events, err := secondLedger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Type]++
	}
	for _, eventType := range []string{approvalPendingEventType, approvalApprovedEventType, toolDispatchedType, "tool/result"} {
		if counts[eventType] != 1 {
			t.Fatalf("%s count = %d, want 1; counts=%+v", eventType, counts[eventType], counts)
		}
	}
}

func TestSessionRunnerHeartbeatCancellationDoesNotFailSuccessfulRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	base := openWorkbenchTestLedger(t)
	ledger := &cancelingHeartbeatEventLog{EventLog: base, started: make(chan struct{})}
	runner := &SessionRunner{
		workbench: NewWorkbench(ledger, nil),
		options: SessionRunnerOptions{
			HeartbeatInterval: time.Millisecond,
			LeaseDuration:     time.Second,
			Now:               time.Now,
		},
	}
	done := make(chan struct{})
	failures := make(chan error, 1)
	deletions := make(chan struct{})
	go runner.heartbeat(ctx, runKey{sessionID: "session-cancel", runID: "run-cancel"}, runLeasePayload{
		RunID: "run-cancel", LeaseID: "lease-cancel",
	}, deletions, done, cancel, failures)
	<-ledger.started
	cancel()
	<-done
	select {
	case err := <-failures:
		t.Fatalf("normal heartbeat cancellation reported as run failure: %v", err)
	default:
	}
}

func TestSessionRunnerEmitsOnlyOneSlowRunNarration(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	blocking := &blockingConversationAdapter{started: make(chan struct{})}
	runner := NewSessionRunner(workbench, blocking, nil, SessionRunnerOptions{
		WorkerID: "worker-single-narration", LeaseDuration: 200 * time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond, ProgressInterval: 12 * time.Millisecond,
	})
	t.Cleanup(func() { _ = runner.Close() })
	if _, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-single-narration", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("conversation did not start")
	}
	time.Sleep(70 * time.Millisecond)
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	narrations := 0
	for _, event := range events {
		if event.Type != progressEventType {
			continue
		}
		var payload progressPayload
		if json.Unmarshal(event.Payload, &payload) == nil && payload.Kind == progressNarration {
			narrations++
		}
	}
	if narrations != 1 {
		t.Fatalf("slow-run narrations = %d, want exactly 1", narrations)
	}
}

func TestSessionRunnerRetrySearchesAllFailedPredecessorsForToolReceipt(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	appendRun := func(requestID, runID string, seq int64) {
		t.Helper()
		payload, payloadErr := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, requestID, runID, actor)
		if payloadErr != nil {
			t.Fatal(payloadErr)
		}
		if _, appendErr := ledger.Append(ctx, created.ID, seq, continuationEventType, payload); appendErr != nil {
			t.Fatal(appendErr)
		}
		if _, appendErr := ledger.Append(ctx, created.ID, seq+1, runLeasedEventType, runLeasePayload{
			RunID: runID, RequestID: requestID, LeaseID: "lease-" + runID,
			WorkerID: "worker-" + runID, Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
		}); appendErr != nil {
			t.Fatal(appendErr)
		}
	}
	appendRun("request-root", "run-root", 4)
	if _, err := ledger.Append(ctx, created.ID, 6, runFailedEventType, runTerminalPayload{
		RunID: "run-root", RequestID: "request-root", LeaseID: "lease-run-root", Attempt: 1, Error: "root failed",
	}); err != nil {
		t.Fatal(err)
	}
	appendRun("request-middle", "run-middle", 7)
	call := toolCallPayload{RunID: "run-middle", ToolCallID: "retry-call", ToolName: "Write", ArgumentsJSON: `{"content":"retry","path":"runtime/retry.txt"}`}
	if _, err := ledger.AppendSurface(ctx, created.ID, 9, "tool/call", call, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 10, toolDispatchedType, call); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.AppendSurface(ctx, created.ID, 11, "tool/result", toolResultPayload{
		RunID: "run-middle", ToolCallID: "retry-call", ToolName: "Write", Output: "committed by middle",
	}, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 12, runFailedEventType, runTerminalPayload{
		RunID: "run-middle", RequestID: "request-middle", LeaseID: "lease-run-middle", Attempt: 1, Error: "middle failed",
	}); err != nil {
		t.Fatal(err)
	}

	conversation := &retryToolConversationAdapter{}
	external := &countingToolAdapter{}
	runner := NewSessionRunner(workbench, conversation, external, SessionRunnerOptions{WorkerID: "worker-latest", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-latest", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 13, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	if terminal.Status != RunCompleted {
		t.Fatalf("latest retry status = %+v", terminal)
	}
	if terminal.RetryOfRunID != "run-root" || len(terminal.RetryOfRunIDs) != 2 || terminal.RetryOfRunIDs[0] != "run-root" || terminal.RetryOfRunIDs[1] != "run-middle" {
		t.Fatalf("retry lineage = %+v, want root and middle predecessors", terminal)
	}
	conversation.mu.Lock()
	if len(conversation.toolResults) != 1 || conversation.toolResults[0].Output != "committed by middle" {
		t.Fatalf("latest retry did not reuse middle receipt: %#v", conversation.toolResults)
	}
	conversation.mu.Unlock()
	external.mu.Lock()
	if external.calls != 0 {
		t.Fatalf("latest retry executed external tool %d times, want 0", external.calls)
	}
	external.mu.Unlock()
}

func TestAgentInvocationIDUsesStableRetryRoot(t *testing.T) {
	if got := agentInvocationID("run-current", []string{"run-root", "run-middle", "run-root"}, "spawn"); got != "run-root:spawn" {
		t.Fatalf("retry agent invocation id = %q, want run-root:spawn", got)
	}
	if got := agentInvocationID("run-current", nil, "spawn"); got != "run-current:spawn" {
		t.Fatalf("ordinary agent invocation id = %q, want run-current:spawn", got)
	}
}

func TestSessionRunnerRetryRetainsFailedSideEffectReceipt(t *testing.T) {
	for _, name := range []string{"Write", "Bash", "Git", "external_mcp"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ledger := openWorkbenchTestLedger(t)
			workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
			actor, err := testRunnerActor().BindSession(created.ID)
			if err != nil {
				t.Fatal(err)
			}
			appendRun := func(requestID, runID string, seq int64) runLeasePayload {
				t.Helper()
				payload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, requestID, runID, actor)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ledger.Append(ctx, created.ID, seq, continuationEventType, payload); err != nil {
					t.Fatal(err)
				}
				lease := runLeasePayload{RunID: runID, RequestID: requestID, LeaseID: "lease-" + runID, WorkerID: "worker", Attempt: 1, LeaseUntil: time.Now().Add(time.Minute).UTC()}
				if _, err := ledger.Append(ctx, created.ID, seq+1, runLeasedEventType, lease); err != nil {
					t.Fatal(err)
				}
				return lease
			}
			old := appendRun("request-old", "run-old", 4)
			call := toolCallPayload{RunID: old.RunID, ToolCallID: "effect", ToolName: name, ArgumentsJSON: `{"path":"result.txt"}`}
			if _, err := ledger.AppendSurface(ctx, created.ID, 6, "tool/call", call, SurfaceOperation{Op: "append"}); err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Append(ctx, created.ID, 7, toolDispatchedType, call); err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.AppendSurface(ctx, created.ID, 8, "tool/result", toolResultPayload{RunID: old.RunID, ToolCallID: call.ToolCallID, ToolName: name, Error: "effect failed after partial changes", ExitCode: 1}, SurfaceOperation{Op: "append"}); err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Append(ctx, created.ID, 9, runFailedEventType, runTerminalPayload{RunID: old.RunID, RequestID: old.RequestID, LeaseID: old.LeaseID, Attempt: 1, Error: "failed"}); err != nil {
				t.Fatal(err)
			}
			lease := appendRun("request-retry", "run-retry", 10)
			external := &countingToolAdapter{}
			runner := NewSessionRunner(workbench, &recordingConversationAdapter{}, external, SessionRunnerOptions{WorkerID: "worker"})
			t.Cleanup(func() { _ = runner.Close() })
			result, unsafe := runner.executeTool(ctx, runKey{sessionID: created.ID, runID: lease.RunID}, lease, actor, created.WorkingDir, orchestrator.ToolCall{ID: call.ToolCallID, Name: name, ParametersJSON: call.ArgumentsJSON}, old.RunID)
			if unsafe || result.Error != "effect failed after partial changes" || result.ExitCode != 1 || external.calls != 0 {
				t.Fatalf("failed effect retried: result=%+v unsafe=%v calls=%d", result, unsafe, external.calls)
			}
		})
	}
}

func TestSessionRunnerRetryKeepsAgentMutationInvocationId(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "agent retry", "keep child mutations idempotent", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "spawn a child agent")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 2, message.ID, "agent retry")
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.UpdateStatus(ctx, 7, created.ID, 3, "paused"); err != nil {
		t.Fatal(err)
	}
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &independentAgentFixture{mode: "blocked", started: make(chan struct{})}
	runner := NewSessionRunner(workbench, fixture, nil, SessionRunnerOptions{WorkerID: "worker-agent-retry", AgentWorkerCount: 1, LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })

	oldPayload, err := workbench.continuationRequestPayload(ctx, created.ID, checkpoint.Hash, "request-root", "run-root", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, oldPayload); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{
		RunID: "run-root", RequestID: "request-root", LeaseID: "lease-root", WorkerID: "worker-root", Attempt: 1,
		LeaseUntil: time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	parameters := `{"kind":"explore","title":"retry child","objective":"inspect explicit sources","parallel":true}`
	first := runner.ExecuteAgentTool(ctx, actor, created.ID, orchestrator.ToolCall{
		ID: agentInvocationID("run-root", nil, "spawn"), Name: "SpawnAgent", ParametersJSON: parameters,
	})
	if first.Error != "" {
		t.Fatalf("initial child mutation failed: %+v", first)
	}
	firstTask := &codeagentpb.AgentTask{}
	if err := protojson.Unmarshal([]byte(first.Output), firstTask); err != nil {
		t.Fatal(err)
	}
	if firstTask.Id == "" {
		t.Fatal("initial child mutation returned no task id")
	}
	select {
	case <-fixture.started:
	case <-time.After(3 * time.Second):
		t.Fatal("initial child did not start")
	}

	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	baseSeq := int64(len(events))
	call := toolCallPayload{RunID: "run-root", ToolCallID: "spawn", ToolName: "SpawnAgent", ArgumentsJSON: parameters}
	if _, err := ledger.AppendSurface(ctx, created.ID, baseSeq, "tool/call", call, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, baseSeq+1, toolDispatchedType, call); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.AppendSurface(ctx, created.ID, baseSeq+2, "tool/result", toolResultPayload{
		RunID: "run-root", ToolCallID: "spawn", ToolName: "SpawnAgent", Error: "child attempt interrupted", ExitCode: 1,
	}, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, baseSeq+3, runFailedEventType, runTerminalPayload{
		RunID: "run-root", RequestID: "request-root", LeaseID: "lease-root", Attempt: 1, Error: "child attempt interrupted",
	}); err != nil {
		t.Fatal(err)
	}
	retryPayload, err := workbench.continuationRequestPayload(ctx, created.ID, checkpoint.Hash, "request-retry", "run-retry", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, baseSeq+4, continuationEventType, retryPayload); err != nil {
		t.Fatal(err)
	}
	lease := runLeasePayload{RunID: "run-retry", RequestID: "request-retry", LeaseID: "lease-retry", WorkerID: "worker-retry", Attempt: 1, LeaseUntil: time.Now().Add(time.Minute).UTC()}
	if _, err := ledger.Append(ctx, created.ID, baseSeq+5, runLeasedEventType, lease); err != nil {
		t.Fatal(err)
	}

	result, unsafe := runner.executeTool(ctx, runKey{sessionID: created.ID, runID: "run-retry"}, lease, actor, created.WorkingDir, orchestrator.ToolCall{
		ID: "spawn", Name: "SpawnAgent", ParametersJSON: parameters,
	}, "run-root")
	if unsafe || result.Error != "" {
		t.Fatalf("retry child mutation = %+v unsafe=%v", result, unsafe)
	}
	retriedTask := &codeagentpb.AgentTask{}
	if err := protojson.Unmarshal([]byte(result.Output), retriedTask); err != nil {
		t.Fatal(err)
	}
	if retriedTask.Id != firstTask.Id {
		t.Fatalf("retry created a second child task: first=%s retry=%s", firstTask.Id, retriedTask.Id)
	}
	events, err = ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	links := 0
	for _, event := range events {
		if event.Type == "agent/task-linked" {
			links++
		}
	}
	if links != 1 {
		t.Fatalf("retry appended %d child links, want one", links)
	}
}

func TestWorkbenchRunHistoryKeepsCreationOrderAfterLaterHeartbeat(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	appendContinuation := func(requestID, runID string, seq int64) {
		t.Helper()
		payload, payloadErr := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, requestID, runID, actor)
		if payloadErr != nil {
			t.Fatal(payloadErr)
		}
		if _, appendErr := ledger.Append(ctx, created.ID, seq, continuationEventType, payload); appendErr != nil {
			t.Fatal(appendErr)
		}
	}
	appendContinuation("request-old", "run-old", 4)
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{
		RunID: "run-old", RequestID: "request-old", LeaseID: "lease-old", WorkerID: "worker-old", Attempt: 1,
		LeaseUntil: time.Now().Add(time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	appendContinuation("request-new", "run-new", 6)
	if _, err := ledger.Append(ctx, created.ID, 7, runLeasedEventType, runLeasePayload{
		RunID: "run-new", RequestID: "request-new", LeaseID: "lease-new", WorkerID: "worker-new", Attempt: 1,
		LeaseUntil: time.Now().Add(time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 8, runHeartbeatEventType, runLeasePayload{
		RunID: "run-old", RequestID: "request-old", LeaseID: "lease-old", WorkerID: "worker-old", Attempt: 1,
		LeaseUntil: time.Now().Add(time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	view, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Runs) != 2 || view.Runs[0].RunID != "run-old" || view.Runs[1].RunID != "run-new" {
		t.Fatalf("run history = %+v, want creation order old,new", view.Runs)
	}
	if view.Run == nil || view.Run.RunID != "run-new" {
		t.Fatalf("current run = %+v, want run-new", view.Run)
	}
}

func TestConversationRequestDoesNotTreatFutureFailureAsRetryAncestor(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentPayload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-current", "run-current", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, currentPayload); err != nil {
		t.Fatal(err)
	}
	currentLease := runLeasePayload{
		RunID: "run-current", RequestID: "request-current", LeaseID: "lease-current",
		WorkerID: "worker-current", Attempt: 1, LeaseUntil: time.Now().Add(time.Minute).UTC(),
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, currentLease); err != nil {
		t.Fatal(err)
	}
	futurePayload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-future", "run-future", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 6, continuationEventType, futurePayload); err != nil {
		t.Fatal(err)
	}
	futureLease := runLeasePayload{
		RunID: "run-future", RequestID: "request-future", LeaseID: "lease-future",
		WorkerID: "worker-future", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
	}
	if _, err := ledger.Append(ctx, created.ID, 7, runLeasedEventType, futureLease); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 8, runFailedEventType, runTerminalPayload{
		RunID: "run-future", RequestID: "request-future", LeaseID: "lease-future", Attempt: 1, Error: "future failed",
	}); err != nil {
		t.Fatal(err)
	}

	runner := NewSessionRunner(workbench, nil, nil, SessionRunnerOptions{WorkerID: "worker-current"})
	t.Cleanup(func() { _ = runner.Close() })
	request, err := runner.conversationRequest(ctx, runKey{sessionID: created.ID, runID: "run-current"}, currentLease)
	if err != nil {
		t.Fatal(err)
	}
	if request.RetryOfRunID != "" || len(request.RetryOfRunIDs) != 0 {
		t.Fatalf("future failure leaked into retry lineage: primary=%q all=%v", request.RetryOfRunID, request.RetryOfRunIDs)
	}
}

func TestPartialRecoveryErrorBoundsHealthDetails(t *testing.T) {
	recoveryErr := &PartialRecoveryError{Err: errors.Join(
		errors.New("recover session first projection: session not found"),
		errors.New("recover session second projection: session not found"),
		errors.New("recover session third projection: session not found"),
		errors.New("recover session fourth projection: session not found"),
		errors.New(strings.Repeat("long detail ", 40)),
	)}

	message := recoveryErr.Error()
	for _, expected := range []string{"session first", "session second", "session third", "and 2 more"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("bounded recovery error %q does not contain %q", message, expected)
		}
	}
	if strings.Contains(message, "session fourth") || len([]rune(message)) > 560 {
		t.Fatalf("partial recovery error was not bounded: %q", message)
	}
	if !errors.Is(recoveryErr, recoveryErr.Err) {
		t.Fatal("bounded display must preserve the original recovery error chain")
	}
}

func TestSessionRunnerGracefulCloseLeavesRunRecoverable(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	blocking := &blockingConversationAdapter{started: make(chan struct{})}
	first := NewSessionRunner(workbench, blocking, nil, SessionRunnerOptions{
		WorkerID: "worker-first", LeaseDuration: 80 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond,
	})
	accepted, err := first.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-restart", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 4, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("first runner did not start")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	view, err := first.Run(ctx, created.ID, accepted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != RunRunning || view.CompletedAt != nil {
		t.Fatalf("graceful close wrote a terminal outcome: %+v", view)
	}

	time.Sleep(100 * time.Millisecond)
	conversation := &recordingConversationAdapter{reply: "resumed after restart"}
	second := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-second", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = second.Close() })
	if err := second.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, second, created.ID, accepted.RunID, RunCompleted)
	if terminal.Attempt != 2 || terminal.WorkerID != "worker-second" {
		t.Fatalf("restarted run = %+v", terminal)
	}
}

func TestSessionRunnerCompletesPersistedAssistantWithoutCallingModelAgain(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-output", "run-output", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{
		RunID: "run-output", RequestID: "request-output", LeaseID: "expired-output",
		WorkerID: "dead-worker", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.AppendSurface(ctx, created.ID, 6, "assistant/message", messagePayload{
		Author: "assistant", Content: "already committed", RunID: "run-output",
	}, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}

	conversation := &recordingConversationAdapter{reply: "must not run"}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-output", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	if err := runner.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, "run-output", RunCompleted)
	if terminal.Attempt != 2 {
		t.Fatalf("persisted output recovery = %+v", terminal)
	}
	conversation.mu.Lock()
	defer conversation.mu.Unlock()
	if len(conversation.requests) != 0 {
		t.Fatalf("model was called again after output commit: %#v", conversation.requests)
	}
}

func TestSessionRunnerResumesHistoryOnlyToolCheckpoint(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "history-only", "goal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "inspect the repository"); err != nil {
		t.Fatal(err)
	}
	call := toolCallPayload{RunID: "run-pending", ToolCallID: "call-1", ToolName: "Glob", ArgumentsJSON: `{"pattern":"**/*.go"}`}
	if _, err := ledger.AppendSurface(ctx, created.ID, 2, "tool/call", call, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	result := toolResultPayload{RunID: "run-pending", ToolCallID: "call-1", ToolName: "Glob", Output: "main.go"}
	resultEvent, err := ledger.AppendSurface(ctx, created.ID, 3, "tool/result", result, SurfaceOperation{Op: "append"})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 4, resultEvent.EventID, "after-tool")
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.UpdateStatus(ctx, 7, created.ID, 5, "paused"); err != nil {
		t.Fatal(err)
	}

	conversation := &recordingConversationAdapter{reply: "continued from tool history"}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "worker-history", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{
		RequestID: "request-history", SessionID: created.ID, OwnerID: 7,
		CheckpointHash: checkpoint.Hash, ExpectedSeq: 6, Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	if terminal.Status != RunCompleted {
		t.Fatalf("history-only run = %+v", terminal)
	}
	request := conversation.lastRequest(t)
	if request.Input != "" || len(request.History) != 3 {
		t.Fatalf("history-only request = input=%q history=%#v", request.Input, request.History)
	}
	if len(request.History[1].ToolCalls) != 1 || request.History[2].ToolCallID != "call-1" {
		t.Fatalf("tool pairing was not preserved: %#v", request.History)
	}
}

func pausedSessionWithCheckpoint(t *testing.T, ledger EventLog) (*Workbench, SessionView, CheckpointView) {
	t.Helper()
	ctx := context.Background()
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "title", "goal")
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "resume")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 2, message.ID, "resume")
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.UpdateStatus(ctx, 7, created.ID, 3, "paused"); err != nil {
		t.Fatal(err)
	}
	return workbench, created, checkpoint
}

func testRunnerActor() identity.Actor {
	return identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "alice", TenantID: "org:one", Roles: []string{"USER"}}
}

func TestSessionRunnerRetryReadOnlyUnknownInvocationReexecutes(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor := testRunnerActor()
	oldPayload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-readonly-failed", "run-readonly-failed", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Append(ctx, created.ID, 4, continuationEventType, oldPayload); err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{RunID: "run-readonly-failed", RequestID: "request-readonly-failed", LeaseID: "lease-readonly-failed", WorkerID: "worker-readonly-failed", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC()}); err != nil {
		t.Fatal(err)
	}
	call := toolCallPayload{RunID: "run-readonly-failed", ToolCallID: "retry-readonly", ToolName: "Grep", ArgumentsJSON: "{\"glob\":\"internal/session/*.go\",\"head_limit\":5,\"pattern\":\"func\"}"}
	if _, err = ledger.AppendSurface(ctx, created.ID, 6, "tool/call", call, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Append(ctx, created.ID, 7, toolDispatchedType, call); err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Append(ctx, created.ID, 8, runFailedEventType, runTerminalPayload{RunID: "run-readonly-failed", RequestID: "request-readonly-failed", LeaseID: "lease-readonly-failed", Attempt: 1, Error: "agent continuation failed"}); err != nil {
		t.Fatal(err)
	}
	conversation := &readonlyRetryConversationAdapter{}
	external := &countingToolAdapter{}
	runner := NewSessionRunner(workbench, conversation, external, SessionRunnerOptions{WorkerID: "worker-readonly-retry", LeaseDuration: time.Second})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, ContinueCommand{RequestID: "request-readonly-retry", SessionID: created.ID, OwnerID: 7, CheckpointHash: checkpoint.Hash, ExpectedSeq: 9, Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, accepted.RunID, RunCompleted)
	if terminal.Error != "" {
		t.Fatalf("readonly retry failed: %+v", terminal)
	}
	external.mu.Lock()
	executions := external.calls
	external.mu.Unlock()
	if executions != 1 {
		t.Fatalf("readonly retry executions = %d, want 1", executions)
	}
	conversation.mu.Lock()
	defer conversation.mu.Unlock()
	if len(conversation.toolResults) != 1 || conversation.toolResults[0].Error != "" || conversation.toolResults[0].Output != "unexpected external execution" {
		t.Fatalf("readonly retry result = %#v", conversation.toolResults)
	}
}

func continuationRequestPayloadForTest(ctx context.Context, workbench *Workbench, sessionID, checkpointHash, requestID, runID string, actor identity.Actor) (continuationPayload, error) {
	return workbench.continuationRequestPayload(ctx, sessionID, checkpointHash, requestID, runID, actor)
}

func waitForRunStatus(t *testing.T, runner *SessionRunner, sessionID, runID string, status RunStatus) RunView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, err := runner.Run(context.Background(), sessionID, runID)
		if err == nil && view.Status == status {
			return view
		}
		time.Sleep(10 * time.Millisecond)
	}
	view, err := runner.Run(context.Background(), sessionID, runID)
	t.Fatalf("run did not reach %q: view=%+v err=%v", status, view, err)
	return RunView{}
}
