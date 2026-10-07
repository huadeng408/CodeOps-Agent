package memory

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/session"
	"google.golang.org/protobuf/encoding/protojson"
)

type memoryConversationFixture struct {
	toolArgs string
	result   orchestrator.ToolResult
	calls    atomic.Int32
}

func (f *memoryConversationFixture) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	f.calls.Add(1)
	if f.toolArgs != "" {
		f.result = handlers.Tool(ctx, orchestrator.ToolCall{ID: "recall", Name: "RecallMemory", ParametersJSON: f.toolArgs})
	}
	return orchestrator.ConversationResult{Success: true, Message: "Completed: " + request.Input}, nil
}

func openMemoryLedger(t *testing.T) *session.SQLiteEventLog {
	t.Helper()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	return ledger
}

func runMemoryFixture(t *testing.T, ledger session.EventLog, owner uint, input string, module session.SessionMemory, fixture *memoryConversationFixture, permissions ...*permission.Controller) string {
	t.Helper()
	ctx := context.Background()
	workbench := session.NewWorkbench(ledger, nil)
	view, err := workbench.Create(ctx, owner, "repo", "memory task", input)
	if err != nil {
		t.Fatal(err)
	}
	options := session.SessionRunnerOptions{Memory: module}
	if len(permissions) > 0 {
		options.Permissions = permissions[0]
	}
	runner := session.NewSessionRunner(workbench, fixture, nil, options)
	t.Cleanup(func() { _ = runner.Close() })
	_, err = runner.SubmitMessage(ctx, session.SubmitMessageCommand{
		RequestID: "request", SessionID: view.ID, OwnerID: owner, ExpectedSeq: 1, Content: input, Actor: identity.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, err := workbench.Get(ctx, owner, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Run != nil && current.Run.Status == session.RunCompleted {
			if err := runner.Close(); err != nil {
				t.Fatal(err)
			}
			return view.ID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fixture run did not reach completed terminal")
	return ""
}

func decodeMemoryRecall(t *testing.T, module *LedgerMemory, owner uint, query string, budget int) RecallResult {
	t.Helper()
	text, err := module.Recall(context.Background(), owner, query, budget)
	if err != nil {
		t.Fatal(err)
	}
	var result RecallResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestLedgerMemoryAutomaticCommitScopeBudgetAndIdempotency(t *testing.T) {
	ledger := openMemoryLedger(t)
	module := NewLedgerMemory(ledger)
	sessionID := runMemoryFixture(t, ledger, 7, "durable memory anchor", module, &memoryConversationFixture{})
	runMemoryFixture(t, ledger, 8, "durable foreign private anchor", module, &memoryConversationFixture{})
	first := decodeMemoryRecall(t, module, 7, "durable", 1200)
	if len(first.Entries) != 1 || first.Entries[0].Memory.SessionID != sessionID || first.Entries[0].Memory.SourceChecksum == "" || first.Entries[0].Memory.Checksum == "" {
		t.Fatalf("scoped recall = %+v", first)
	}
	if tiny := decodeMemoryRecall(t, module, 7, "durable", 1); tiny.Stats.Returned != 0 || tiny.Stats.Dropped != 1 || tiny.Stats.UsedTokens > 1 {
		t.Fatalf("budget was not enforced: %+v", tiny)
	}
	before, _ := ledger.Events(context.Background(), sessionID)
	if err := module.Commit(context.Background(), sessionID); err != nil {
		t.Fatal(err)
	}
	after, _ := ledger.Events(context.Background(), sessionID)
	if len(before) != len(after) {
		t.Fatal("duplicate memory commit appended another fact")
	}
	if _, err := module.Recall(context.Background(), 0, "durable", 1200); !errors.Is(err, session.ErrSessionOwnerRequired) {
		t.Fatalf("ownerless recall was not refused: %v", err)
	}
}

type recoveredMemory struct {
	*LedgerMemory
	commits chan error
}

func (m *recoveredMemory) Commit(ctx context.Context, sessionID string) error {
	err := m.LedgerMemory.Commit(ctx, sessionID)
	m.commits <- err
	return err
}

func awaitRecoveredMemory(t *testing.T, commits <-chan error) {
	t.Helper()
	select {
	case err := <-commits:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovered memory commit did not finish")
	}
}

func TestLedgerMemoryRecoveryCommitsMissedTerminalWithoutRerunningModel(t *testing.T) {
	ledger := openMemoryLedger(t)
	sessionID := runMemoryFixture(t, ledger, 7, "restart memory anchor", nil, &memoryConversationFixture{})
	original, err := ledger.Events(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	module := NewLedgerMemory(ledger)
	observed := &recoveredMemory{LedgerMemory: module, commits: make(chan error, 4)}
	conversation := &memoryConversationFixture{}
	runner := session.NewSessionRunner(session.NewWorkbench(ledger, nil), conversation, nil, session.SessionRunnerOptions{Memory: observed})
	defer runner.Close()
	if err := runner.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitRecoveredMemory(t, observed.commits)
	if result := decodeMemoryRecall(t, module, 7, "restart", 1200); len(result.Entries) != 1 || result.Entries[0].Memory.SessionID != sessionID {
		t.Fatalf("missed commit was not recovered: %+v", result)
	}
	before, _ := ledger.Events(context.Background(), sessionID)
	if len(before) != len(original)+1 || before[len(before)-1].Type != trajectoryCommittedEvent {
		t.Fatal("terminal recovery appended execution facts instead of only the missing memory commit")
	}
	if err := runner.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitRecoveredMemory(t, observed.commits)
	after, _ := ledger.Events(context.Background(), sessionID)
	if len(before) != len(after) {
		t.Fatal("recovery was not idempotent")
	}
	if conversation.calls.Load() != 0 {
		t.Fatal("terminal memory recovery reran the conversation model")
	}
}

func TestLedgerMemoryRecoveryDoesNotBlockContinuationAttachOnReflection(t *testing.T) {
	ledger := openMemoryLedger(t)
	sessionID := runMemoryFixture(t, ledger, 7, "restart reflection anchor", nil, &memoryConversationFixture{})
	started, release := make(chan struct{}), make(chan struct{})
	var reflections atomic.Int32
	module := NewLedgerMemory(ledger, func(ctx context.Context, request *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
		close(started)
		select {
		case <-release:
			return reflectionFixture(&reflections, false)(ctx, request)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	observed := &recoveredMemory{LedgerMemory: module, commits: make(chan error, 4)}
	conversation := &memoryConversationFixture{}
	runner := session.NewSessionRunner(session.NewWorkbench(ledger, nil), conversation, nil, session.SessionRunnerOptions{Memory: observed, WorkerCount: 1, AgentWorkerCount: 1})
	defer runner.Close()
	recoverCtx, cancelRecover := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelRecover()
	if err := runner.Recover(recoverCtx); err != nil || recoverCtx.Err() != nil {
		t.Fatalf("reflection blocked startup recovery: err=%v context=%v", err, recoverCtx.Err())
	}
	cancelRecover()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal reflection was not queued")
	}
	slot := session.NewContinuationSlot()
	supervisor := session.NewContinuationSupervisor(slot, 100*time.Millisecond, func(context.Context) (session.ContinuationModule, error) {
		return runner, nil
	})
	supervisor.Start(context.Background())
	defer supervisor.Close()
	deadline := time.Now().Add(time.Second)
	for !slot.Available() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !slot.Available() || !supervisor.Status().Attached {
		t.Fatal("blocked reflection prevented continuation attachment")
	}
	close(release)
	awaitRecoveredMemory(t, observed.commits)
	awaitRecoveredMemory(t, observed.commits)
	if conversation.calls.Load() != 0 || reflections.Load() != 1 {
		t.Fatalf("terminal replay called models unexpectedly: conversation=%d reflection=%d", conversation.calls.Load(), reflections.Load())
	}
	if result := decodeMemoryRecall(t, module, 7, "restart", 1200); len(result.Entries) != 1 || result.Entries[0].Memory.SessionID != sessionID {
		t.Fatalf("missed commit was not recovered after reflection unblocked: %+v", result)
	}
}

func TestLedgerMemoryRecoveryDoesNotDelayNewConversationBehindReflection(t *testing.T) {
	ctx := context.Background()
	ledger := openMemoryLedger(t)
	oldSession := runMemoryFixture(t, ledger, 7, "old reflection anchor", nil, &memoryConversationFixture{})
	original, err := ledger.Events(ctx, oldSession)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var reflections atomic.Int32
	module := NewLedgerMemory(ledger, func(ctx context.Context, request *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
		if request.SessionId == oldSession {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return reflectionFixture(&reflections, false)(ctx, request)
	})
	observed := &recoveredMemory{LedgerMemory: module, commits: make(chan error, 8)}
	conversation := &memoryConversationFixture{}
	workbench := session.NewWorkbench(ledger, nil)
	runner := session.NewSessionRunner(workbench, conversation, nil, session.SessionRunnerOptions{Memory: observed, WorkerCount: 1, AgentWorkerCount: 1})
	defer runner.Close()
	if err := runner.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old reflection did not start")
	}
	view, err := workbench.Create(ctx, 7, "repo", "interactive task", "new interactive request")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.SubmitMessage(ctx, session.SubmitMessageCommand{
		RequestID: "interactive-request", SessionID: view.ID, OwnerID: 7, ExpectedSeq: 1,
		Content: "new interactive request", Actor: identity.Default(),
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	completed := false
	for time.Now().Before(deadline) {
		current, err := workbench.Get(ctx, 7, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Run != nil && current.Run.Status == session.RunCompleted {
			completed = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !completed {
		t.Fatal("new conversation waited behind blocked historical reflection")
	}
	awaitRecoveredMemory(t, observed.commits)
	close(release)
	awaitRecoveredMemory(t, observed.commits)
	before, err := ledger.Events(ctx, oldSession)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range before[len(original):] {
		if !strings.HasPrefix(event.Type, "memory/") {
			t.Fatalf("recovery appended execution fact %q", event.Type)
		}
	}
	if result := decodeMemoryRecall(t, module, 7, "old reflection", 1200); len(result.Entries) != 1 || result.Entries[0].Memory.SessionID != oldSession {
		t.Fatalf("historical memory recovery was lost: %+v", result)
	}
	if err := runner.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	awaitRecoveredMemory(t, observed.commits)
	awaitRecoveredMemory(t, observed.commits)
	after, err := ledger.Events(ctx, oldSession)
	if err != nil || len(before) != len(after) || conversation.calls.Load() != 1 || reflections.Load() != 2 {
		t.Fatalf("recovery was not idempotent: facts=%d/%d conversation=%d reflection=%d err=%v", len(before), len(after), conversation.calls.Load(), reflections.Load(), err)
	}
}

func TestLedgerMemoryRecoveryDoesNotDelayNewChildAgentBehindReflection(t *testing.T) {
	ctx := context.Background()
	ledger := openMemoryLedger(t)
	oldSession := runMemoryFixture(t, ledger, 7, "old child reflection anchor", nil, &memoryConversationFixture{})
	started, release := make(chan struct{}), make(chan struct{})
	var reflections atomic.Int32
	module := NewLedgerMemory(ledger, func(ctx context.Context, request *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
		if request.SessionId == oldSession {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return reflectionFixture(&reflections, false)(ctx, request)
	})
	observed := &recoveredMemory{LedgerMemory: module, commits: make(chan error, 8)}
	conversation := &memoryConversationFixture{}
	workbench := session.NewWorkbench(ledger, nil)
	parent, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "parent", "delegate a new task", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	runner := session.NewSessionRunner(workbench, conversation, nil, session.SessionRunnerOptions{Memory: observed, WorkerCount: 1, AgentWorkerCount: 1})
	defer runner.Close()
	if err := runner.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old reflection did not start")
	}
	result := runner.ExecuteAgentTool(ctx, actor, parent.ID, orchestrator.ToolCall{
		ID: "interactive-child", Name: "SpawnAgent",
		ParametersJSON: `{"kind":"explore","title":"new child task","objective":"inspect explicit repository materials","parallel":true}`,
	})
	if result.Error != "" || result.ExitCode != 0 {
		t.Fatalf("SpawnAgent failed: %+v", result)
	}
	var task pb.AgentTask
	if err := protojson.Unmarshal([]byte(result.Output), &task); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	completed := false
	for time.Now().Before(deadline) {
		view, err := workbench.Get(ctx, 7, task.ChildSessionId)
		if err != nil {
			t.Fatal(err)
		}
		if view.Run != nil && view.Run.Status == session.RunCompleted {
			completed = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !completed {
		t.Fatal("new child agent waited behind blocked historical reflection")
	}
	awaitRecoveredMemory(t, observed.commits)
	close(release)
	awaitRecoveredMemory(t, observed.commits)
	if conversation.calls.Load() != 1 || reflections.Load() != 2 {
		t.Fatalf("unexpected model calls: conversation=%d reflection=%d", conversation.calls.Load(), reflections.Load())
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Verify(ctx, task.ChildSessionId); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerMemoryRecallToolKeepsHarnessCallResultAndRejectsOwnerInjection(t *testing.T) {
	ledger := openMemoryLedger(t)
	module := NewLedgerMemory(ledger)
	runMemoryFixture(t, ledger, 7, "recall source anchor", module, &memoryConversationFixture{})
	fixture := &memoryConversationFixture{toolArgs: `{"query":"source","max_tokens":1200}`}
	sessionID := runMemoryFixture(t, ledger, 7, "recall historical source", module, fixture)
	if fixture.result.ExitCode != 0 || !strings.Contains(fixture.result.Output, "recall source anchor") {
		t.Fatalf("RecallMemory = %+v", fixture.result)
	}
	events, _ := ledger.Events(context.Background(), sessionID)
	calls, results := 0, 0
	for _, event := range events {
		if event.Type == "tool/call" {
			calls++
		}
		if event.Type == "tool/result" {
			results++
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("memory bypassed tool audit: calls=%d results=%d", calls, results)
	}
	fixture = &memoryConversationFixture{toolArgs: `{"query":"source","owner_id":8}`}
	runMemoryFixture(t, ledger, 7, "reject owner injection", module, fixture)
	if fixture.result.ExitCode == 0 || fixture.result.Output != "" || fixture.result.Error != "invalid memory recall parameters" {
		t.Fatalf("owner injection accepted: %+v", fixture.result)
	}
}

func TestLedgerMemorySourceSessionScopeExcludesSiblingFacts(t *testing.T) {
	ledger := openMemoryLedger(t)
	var reflections atomic.Int32
	module := NewLedgerMemory(ledger, reflectionFixture(&reflections, false))
	parentID := runMemoryFixture(t, ledger, 7, "PARENT_PRIVATE_HISTORY", module, &memoryConversationFixture{})
	childID := runMemoryFixture(t, ledger, 7, "CHILD_PRIVATE_HISTORY", module, &memoryConversationFixture{})
	if parentID == childID || reflections.Load() != 2 {
		t.Fatalf("fixture sessions/reflections = %q %q %d", parentID, childID, reflections.Load())
	}

	encoded, err := module.RecallWithOptions(context.Background(), 7, session.MemoryQuery{
		Query: "PRIVATE_HISTORY", MaxTokens: 8000, SourceSessionID: childID,
	})
	if err != nil || !strings.Contains(encoded, "CHILD_PRIVATE_HISTORY") || strings.Contains(encoded, "PARENT_PRIVATE_HISTORY") {
		t.Fatalf("scoped trajectory recall = %s, err=%v", encoded, err)
	}

	encoded, err = module.RecallWithOptions(context.Background(), 7, session.MemoryQuery{
		Query: "source verified ledger testing", Detail: "full", MaxTokens: 8000, SourceSessionID: childID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var result RecallResult
	if json.Unmarshal([]byte(encoded), &result) != nil || len(result.Entries) != 0 {
		t.Fatalf("mixed-origin experience crossed child scope: %s", encoded)
	}
}

type memoryRecallSpy struct {
	session.SessionMemory
	calls int
}

func (m *memoryRecallSpy) Recall(ctx context.Context, owner uint, query string, budget int) (string, error) {
	m.calls++
	return m.SessionMemory.Recall(ctx, owner, query, budget)
}

func TestLedgerMemoryDeniedToolNeverReadsHistoricalMemory(t *testing.T) {
	ledger := openMemoryLedger(t)
	module := &memoryRecallSpy{SessionMemory: NewLedgerMemory(ledger)}
	fixture := &memoryConversationFixture{toolArgs: `{"query":"private anchor"}`}
	permissions := permission.NewControllerWithRules(nil, nil, []permission.AllowRule{{Tool: "RecallMemory"}})
	if permissions.Level("RecallMemory") != permission.AskSession {
		t.Fatal("memory recall must require session approval by default")
	}
	sessionID := runMemoryFixture(t, ledger, 7, "deny historical recall", module, fixture, permissions)
	if module.calls != 0 || fixture.result.Output != "" || fixture.result.Error != "tool approval denied" || fixture.result.ExitCode == 0 {
		t.Fatalf("denied recall accessed memory: calls=%d result=%+v", module.calls, fixture.result)
	}
	events, err := ledger.Events(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	calls, results := 0, 0
	for _, event := range events {
		switch event.Type {
		case "tool/call":
			calls++
		case "tool/result":
			results++
		case "tool/dispatched":
			t.Fatal("denied recall was dispatched")
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("denied recall lost its audit pair: calls=%d results=%d", calls, results)
	}
}

func TestLedgerMemoryRewindDeletesButPendingTurnRetainsCompletedCommit(t *testing.T) {
	for _, mutation := range []string{"rewind", "pending", "delete"} {
		t.Run(mutation, func(t *testing.T) {
			ledger := openMemoryLedger(t)
			module := NewLedgerMemory(ledger)
			sessionID := runMemoryFixture(t, ledger, 7, "obsolete memory anchor", module, &memoryConversationFixture{})
			ctx := context.Background()
			events, _ := ledger.Events(ctx, sessionID)
			switch mutation {
			case "rewind":
				if _, err := ledger.Rewind(ctx, sessionID, 1); err != nil {
					t.Fatal(err)
				}
			case "pending":
				if _, err := session.NewWorkbench(ledger, nil).AppendUserMessage(ctx, 7, sessionID, int64(len(events)), "not completed"); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := session.NewWorkbench(ledger, nil).Delete(ctx, 7, sessionID, int64(len(events))); err != nil {
					t.Fatal(err)
				}
			}
			expected := 0
			if mutation == "pending" {
				expected = 1
			}
			if result := decodeMemoryRecall(t, module, 7, "obsolete", 1200); len(result.Entries) != expected {
				t.Fatalf("stale commit resurfaced: %+v", result)
			}
			if err := module.Commit(ctx, sessionID); err == nil {
				t.Fatal("unfinished/deleted branch promoted")
			}
		})
	}
}

func TestLedgerMemoryMalformedOwnedCommitFailsClosed(t *testing.T) {
	ledger := openMemoryLedger(t)
	module := NewLedgerMemory(ledger)
	sessionID := runMemoryFixture(t, ledger, 7, "verified source", module, &memoryConversationFixture{})
	events, _ := ledger.Events(context.Background(), sessionID)
	if _, err := ledger.Append(context.Background(), sessionID, int64(len(events)), trajectoryCommittedEvent, map[string]any{"schema_version": 1, "owner_id": 7}); err != nil {
		t.Fatal(err)
	}
	if text, err := module.Recall(context.Background(), 7, "source", 1200); !errors.Is(err, ErrTrajectoryIntegrity) || text != "" {
		t.Fatalf("invalid commit accepted: %q, %v", text, err)
	}
}

func TestMalformedTrajectorySourceSequenceFailsClosed(t *testing.T) {
	events := []session.Event{{Seq: 0, EventID: "event-0", Checksum: "checksum-0", Type: "user/message"}}
	_, err := resolveTrajectorySource(events, TrajectorySource{
		Seq: 99, EventID: "event-99", Checksum: "checksum-99", Type: "user/message",
	})
	if !errors.Is(err, ErrTrajectoryIntegrity) {
		t.Fatalf("out-of-range source was not rejected: %v", err)
	}
	_, err = resolveTrajectorySource(events, TrajectorySource{
		Seq: 0, EventID: "different-event", Checksum: "checksum-0", Type: "user/message",
	})
	if !errors.Is(err, ErrTrajectoryIntegrity) {
		t.Fatalf("mismatched source identity was not rejected: %v", err)
	}
}

type unavailableMemoryFixture struct{}

func (unavailableMemoryFixture) Commit(context.Context, string) error {
	return errors.New("fixture raw error must never be persisted")
}

func (unavailableMemoryFixture) Recall(context.Context, uint, string, int) (string, error) {
	return "", errors.New("fixture memory unavailable")
}

func TestLedgerMemoryFailureDoesNotOverwriteSuccessfulTaskTerminal(t *testing.T) {
	ledger := openMemoryLedger(t)
	sessionID := runMemoryFixture(t, ledger, 7, "task succeeded", unavailableMemoryFixture{}, &memoryConversationFixture{})
	events, err := ledger.Events(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	completed, blocked := 0, 0
	for _, event := range events {
		if strings.Contains(string(event.Payload), "fixture raw error") {
			t.Fatal("raw memory error leaked")
		}
		if event.Type == "session/run-completed" {
			completed++
		}
		if event.Type == "memory/commit-blocked" {
			blocked++
		}
		if event.Type == "session/run-failed" || event.Type == trajectoryCommittedEvent {
			t.Fatal("memory failure changed completion or fabricated a commit")
		}
	}
	if completed != 1 || blocked != 1 {
		t.Fatalf("completed=%d blocked=%d", completed, blocked)
	}
}

func TestLedgerMemoryIgnoresForeignMalformedCommit(t *testing.T) {
	ledger := openMemoryLedger(t)
	module := NewLedgerMemory(ledger)
	runMemoryFixture(t, ledger, 7, "owned source", module, &memoryConversationFixture{})
	foreignID := runMemoryFixture(t, ledger, 8, "foreign source", module, &memoryConversationFixture{})
	events, _ := ledger.Events(context.Background(), foreignID)
	if _, err := ledger.Append(context.Background(), foreignID, int64(len(events)), trajectoryCommittedEvent, map[string]any{"invalid": true}); err != nil {
		t.Fatal(err)
	}
	if result := decodeMemoryRecall(t, module, 7, "source", 1200); len(result.Entries) != 1 {
		t.Fatalf("foreign corruption affected owned recall: %+v", result)
	}
}
