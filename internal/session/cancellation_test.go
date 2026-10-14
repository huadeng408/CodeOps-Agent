package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/tools"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestCancellationRetainsFormalProposalAfterLedgerReopen(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first proposal contract")
	}
	ctx := context.Background()
	ledgerPath := filepath.Join(t.TempDir(), "sessions.sqlite")
	ledger, err := OpenSQLiteEventLog(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ledger.Close() }()
	_, workbench, _, repo, storage, proposal := readyApplicationProposal(t, ledger, nil)
	id := proposal.Proposal.SessionID
	before, err := ledger.Events(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	conversation := &blockingConversationAdapter{started: make(chan struct{}), finished: make(chan struct{})}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{})
	defer runner.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "after-proposal", SessionID: id, OwnerID: 7, ExpectedSeq: int64(proposal.EventCount), Content: "review", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("conversation did not start")
	}
	if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel-after-proposal", SessionID: id, RunID: run.RunID, OwnerID: 7, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, runner, id, run.RunID, RunCanceled)
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	ledger, err = OpenSQLiteEventLog(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Verify(ctx, id); err != nil {
		t.Fatal(err)
	}
	after, err := ledger.Events(ctx, id)
	if err != nil || len(after) < len(before) || !reflect.DeepEqual(before, after[:len(before)]) {
		t.Fatal("proposal events changed after cancel/reopen")
	}
	retained, err := NewTaskWorkspaces(NewWorkbench(ledger, nil), taskWorkspaceOwner, repo, storage).WithBaselineRuntime(applicationRuntimeProfile()).Inspect(ctx, 7, id)
	if err != nil || retained.Proposal == nil || (retained.Proposal.State != "ready" && retained.Proposal.State != "stale") {
		t.Fatalf("retained proposal: %+v %v", retained, err)
	}
	retained.Proposal.State = proposal.Proposal.State
	if !reflect.DeepEqual(*retained.Proposal, *proposal.Proposal) {
		t.Fatal("proposal identity, diff, checksum or test receipt changed")
	}
	original, err := os.ReadFile(filepath.Join(repo, "main.go"))
	if err != nil || string(original) != "dirty" {
		t.Fatal("original repository changed")
	}
	afterIndex, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil || !reflect.DeepEqual(index, afterIndex) {
		t.Fatal("original index changed")
	}
	partial, err := os.ReadFile(filepath.Join(storage, proposal.WorkspaceID, "main.go"))
	if err != nil || string(partial) != "reviewed" {
		t.Fatal("partial task result changed")
	}
}

func TestSessionOwnerCancelsRunningConversationAndRetainsHistory(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.Create(ctx, 7, "repo", "cancel", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation := &blockingConversationAdapter{started: make(chan struct{}), finished: make(chan struct{})}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{})
	defer runner.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "work", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("conversation did not start")
	}
	before, err := workbench.Events(ctx, 7, created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel", SessionID: created.ID, RunID: run.RunID, OwnerID: 7, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop its conversation")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, err := runner.Run(ctx, created.ID, run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == RunCanceled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancellation was not durable: %+v", view)
		}
		time.Sleep(10 * time.Millisecond)
	}
	after, err := workbench.Events(ctx, 7, created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) <= len(before) {
		t.Fatal("cancellation erased history")
	}
	for i, original := range before {
		if after[i].Hash != original.Hash {
			t.Fatal("cancellation changed an earlier fact")
		}
	}
	view, err := workbench.Get(ctx, 7, created.ID)
	if err != nil || view.Status != "paused" || view.Run.Status != RunCanceled {
		t.Fatalf("canceled session cannot be reopened: %+v %v", view, err)
	}
}

func TestCancellationReachesTheWorkerHoldingAnotherRunnersLease(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.Create(ctx, 7, "repo", "leased cancellation", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation := &blockingConversationAdapter{started: make(chan struct{}), finished: make(chan struct{})}
	worker := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "owning-worker", LeaseDuration: 10 * time.Second, HeartbeatInterval: 20 * time.Millisecond})
	defer worker.Close()
	commands := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerID: "command-worker"})
	defer commands.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
	run, err := worker.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "work", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("owning worker did not start")
	}
	if _, err := commands.CancelRun(ctx, CancelRunCommand{RequestID: "cancel", SessionID: created.ID, RunID: run.RunID, OwnerID: 7, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.finished:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not reach the worker holding the lease")
	}
	deadline := time.Now().Add(time.Second)
	for {
		view, err := commands.Run(ctx, created.ID, run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == RunCanceled && view.CancellationState == "stopped" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("leased cancellation was not durable: %+v", view)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type repeatedBlockingConversation struct{ started chan string }

type modelTransportConversation struct {
	mu         sync.Mutex
	connection chan struct{}
	runID      string
	started    chan string
}

func (conversation *modelTransportConversation) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, _ orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	connection := make(chan struct{})
	conversation.mu.Lock()
	conversation.connection = connection
	conversation.runID = request.RunID
	conversation.mu.Unlock()
	conversation.started <- request.Input
	select {
	case <-ctx.Done():
		return orchestrator.ConversationResult{}, ctx.Err()
	case <-connection:
		return orchestrator.ConversationResult{}, errors.New("model transport stopped")
	}
}

func (conversation *modelTransportConversation) stopTransport(_ context.Context, command CancelRunCommand) error {
	conversation.mu.Lock()
	defer conversation.mu.Unlock()
	if conversation.connection != nil && conversation.runID == command.RunID {
		select {
		case <-conversation.connection:
		default:
			close(conversation.connection)
		}
	}
	return nil
}

func (conversation repeatedBlockingConversation) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, _ orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	conversation.started <- request.Input
	<-ctx.Done()
	return orchestrator.ConversationResult{}, ctx.Err()
}

type canceledToolConversation struct{}

type childToolCancellationConversation struct{ parentStarted chan struct{} }

func (conversation childToolCancellationConversation) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	if request.AgentTask != nil {
		return (canceledToolConversation{}).RunConversation(ctx, request, handlers)
	}
	close(conversation.parentStarted)
	<-ctx.Done()
	return orchestrator.ConversationResult{}, ctx.Err()
}

func (canceledToolConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	result := handlers.Tool(ctx, orchestrator.ToolCall{ID: "active-tool", Name: "Bash", ParametersJSON: `{"command":"approved-fixture"}`})
	return orchestrator.ConversationResult{Success: result.ExitCode == 0, Message: "tool ended"}, nil
}

type canceledTool struct {
	started     chan struct{}
	unconfirmed bool
	release     <-chan struct{}
}

func (tool canceledTool) Execute(ctx context.Context, _ identity.Actor, _ string, call orchestrator.ToolCall) orchestrator.ToolResult {
	close(tool.started)
	<-ctx.Done()
	if tool.release != nil {
		<-tool.release
	}
	return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "canceled fixture", Output: "partial output", ExitCode: 1, RuntimeID: "owned-fixture", Stopped: !tool.unconfirmed, OutcomeUnknown: true}
}

func TestCancellationRetainsDispatchedToolResultAndUnknownOutcome(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "tool outcome", "")
	if err != nil {
		t.Fatal(err)
	}
	tool := canceledTool{started: make(chan struct{})}
	runner := NewSessionRunner(workbench, canceledToolConversation{}, tool, SessionRunnerOptions{})
	defer runner.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "work", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-tool.started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not start")
	}
	if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel", SessionID: created.ID, RunID: run.RunID, OwnerID: 7, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, err := runner.Run(ctx, created.ID, run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == RunCanceled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tool cancellation was not durable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "tool/result" {
			var result struct {
				Output  string `json:"output"`
				Stopped bool   `json:"stopped"`
				Unknown bool   `json:"outcome_unknown"`
			}
			if json.Unmarshal(event.Payload, &result) != nil || result.Output != "partial output" || !result.Stopped || !result.Unknown {
				t.Fatalf("cancellation changed the tool outcome: %s", event.Payload)
			}
			return
		}
	}
	t.Fatal("cancellation lost the dispatched tool result")
}

func TestCancellationDoesNotClaimAnUnconfirmedRuntimeStopped(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.Create(ctx, 7, "repo", "unknown runtime", "")
	if err != nil {
		t.Fatal(err)
	}
	tool := canceledTool{started: make(chan struct{}), unconfirmed: true}
	runner := NewSessionRunner(workbench, canceledToolConversation{}, tool, SessionRunnerOptions{})
	defer runner.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "work", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-tool.started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not start")
	}
	if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel", SessionID: created.ID, RunID: run.RunID, OwnerID: 7, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, err := runner.Run(ctx, created.ID, run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == RunCanceled {
			if view.CancellationState != "unknown" {
				t.Fatalf("unconfirmed runtime was promoted to stopped: %+v", view)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("cancellation did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type cancelingApprovalConversation struct {
	returned chan struct{}
	release  chan struct{}
}

func (conversation cancelingApprovalConversation) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	result, err := (&approvalToolConversationAdapter{}).RunConversation(ctx, request, handlers)
	close(conversation.returned)
	<-conversation.release
	return result, err
}

func TestCancellationStopsPendingApprovalWithoutExecutingWrite(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "pending approval", "", root)
	if err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(root)
	defer executor.Close()
	conversation := cancelingApprovalConversation{returned: make(chan struct{}), release: make(chan struct{})}
	runner := NewSessionRunner(workbench, conversation, executorToolAdapter{executor: executor}, SessionRunnerOptions{Permissions: permission.NewController(map[string]permission.Level{"Write": permission.AskSession}, nil)})
	defer runner.Close()
	defer func() {
		select {
		case <-conversation.release:
		default:
			close(conversation.release)
		}
	}()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "work", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	var pending EventView
	deadline := time.Now().Add(5 * time.Second)
	for {
		events, err := workbench.Events(ctx, 7, created.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Type == approvalPendingEventType {
				pending = event
				break
			}
		}
		if pending.ID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("approval did not become pending")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel", SessionID: created.ID, RunID: run.RunID, OwnerID: 7, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.returned:
	case <-time.After(5 * time.Second):
		t.Fatal("pending approval did not stop")
	}
	if _, err := workbench.DecideToolApproval(ctx, 7, created.ID, ToolApprovalDecisionCommand{RunID: run.RunID, ToolCallID: "approval-call", PendingEventID: pending.ID, PendingSeq: pending.Seq, Decision: ApprovalApproved}); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("approval was admitted after cancellation intent: %v", err)
	}
	close(conversation.release)
	deadline = time.Now().Add(5 * time.Second)
	for {
		view, err := runner.Run(ctx, created.ID, run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == RunCanceled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("approval did not stop")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := workbench.DecideToolApproval(ctx, 7, created.ID, ToolApprovalDecisionCommand{RunID: run.RunID, ToolCallID: "approval-call", PendingEventID: pending.ID, PendingSeq: pending.Seq, Decision: ApprovalApproved}); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("canceled approval was accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "runtime", "approved.txt")); !os.IsNotExist(err) {
		t.Fatalf("canceled approval wrote a file: %v", err)
	}
}

func TestSessionCancellationRequestCannotBeReboundToAnotherRun(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.Create(ctx, 7, "repo", "request identity", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation := repeatedBlockingConversation{started: make(chan string, 2)}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{})
	defer runner.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
	first, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn-1", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "one", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not start")
	}
	command := CancelRunCommand{RequestID: "cancel-1", SessionID: created.ID, RunID: first.RunID, OwnerID: 7, Actor: actor}
	foreign := command
	foreign.OwnerID = 8
	if _, err := runner.CancelRun(ctx, foreign); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("foreign owner could cancel: %v", err)
	}
	if _, err := runner.CancelRun(ctx, command); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, err := runner.Run(ctx, created.ID, first.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == RunCanceled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first run did not stop")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := runner.CancelRun(ctx, command); err != nil {
		t.Fatalf("same request replay failed: %v", err)
	}
	view, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn-2", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(view.EventCount), Content: "two", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("second run did not start")
	}
	command.RunID = second.RunID
	if _, err := runner.CancelRun(ctx, command); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("old cancellation request canceled another run: %v", err)
	}
}

func TestOldCancellationReplayDoesNotStopANewRunsModelTransport(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.Create(ctx, 7, "repo", "cancel replay", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation := &modelTransportConversation{started: make(chan string, 2)}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{CancelModelCalls: conversation.stopTransport})
	defer runner.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
	first, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn-1", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "one", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not start")
	}
	command := CancelRunCommand{RequestID: "cancel-1", SessionID: created.ID, RunID: first.RunID, OwnerID: 7, Actor: actor}
	if _, err := runner.CancelRun(ctx, command); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, err := runner.Run(ctx, created.ID, first.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == RunCanceled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first run did not stop")
		}
		time.Sleep(10 * time.Millisecond)
	}
	view, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn-2", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(view.EventCount), Content: "two", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("second run did not start")
	}
	if _, err := runner.CancelRun(ctx, command); err != nil {
		t.Fatalf("original request replay failed: %v", err)
	}
	conversation.mu.Lock()
	connection := conversation.connection
	conversation.mu.Unlock()
	select {
	case <-connection:
		t.Fatal("old cancellation replay revoked a new model connection")
	default:
	}
	secondView, err := runner.Run(ctx, created.ID, second.RunID)
	if err != nil || secondView.Status != RunRunning {
		t.Fatalf("new run was affected: %+v %v", secondView, err)
	}
}

type delayedCancellationReply struct {
	EventLog
	persisted chan struct{}
	release   chan struct{}
}

func (log delayedCancellationReply) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	event, err := log.EventLog.Append(ctx, id, seq, kind, payload)
	if err == nil && kind == runCancelRequestedEventType {
		close(log.persisted)
		<-log.release
	}
	return event, err
}

func TestDelayedCancellationReplyCannotStopANewRunsModelTransport(t *testing.T) {
	ctx := context.Background()
	log := delayedCancellationReply{EventLog: openWorkbenchTestLedger(t), persisted: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-log.release:
		default:
			close(log.release)
		}
	}()
	workbench := NewWorkbench(log, nil)
	created, err := workbench.Create(ctx, 7, "repo", "delayed cancellation reply", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation := &modelTransportConversation{started: make(chan string, 2)}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{CancelModelCalls: conversation.stopTransport, HeartbeatInterval: 20 * time.Millisecond})
	defer runner.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
	first, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn-1", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "one", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not start")
	}
	replied := make(chan error, 1)
	go func() {
		_, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel-1", SessionID: created.ID, RunID: first.RunID, OwnerID: 7, Actor: actor})
		replied <- err
	}()
	select {
	case <-log.persisted:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel intent was not persisted")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, err := workbench.Get(ctx, 7, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Run.Status == RunCanceled {
			if _, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn-2", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(view.EventCount), Content: "two", Actor: actor}); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first run did not stop while reply was delayed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("second run did not start")
	}
	conversation.mu.Lock()
	connection := conversation.connection
	conversation.mu.Unlock()
	close(log.release)
	select {
	case err := <-replied:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel reply did not finish")
	}
	select {
	case <-connection:
		t.Fatal("delayed old cancellation revoked the new model transport")
	default:
	}
}

func TestQueuedCancellationDoesNotWaitForAnotherOwnersWorker(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	first, err := workbench.Create(ctx, 7, "repo", "other owner", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := workbench.Create(ctx, 8, "repo", "queued", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation := repeatedBlockingConversation{started: make(chan string, 2)}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{WorkerCount: 1})
	defer runner.Close()
	actor7 := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester7", TenantID: "org:test", Roles: []string{"USER"}}
	actor8 := identity.Actor{SchemaVersion: 1, ActorID: "user:8", Subject: "tester8", TenantID: "org:test", Roles: []string{"USER"}}
	active, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn-a", SessionID: first.ID, OwnerID: 7, ExpectedSeq: int64(first.EventCount), Content: "other owner", Actor: actor7})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first owner did not start")
	}
	queued, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn-b", SessionID: second.ID, OwnerID: 8, ExpectedSeq: int64(second.EventCount), Content: "queued", Actor: actor8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel-b", SessionID: second.ID, RunID: queued.RunID, OwnerID: 8, Actor: actor8}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		view, err := runner.Run(ctx, second.ID, queued.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == RunCanceled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queued cancellation waited for another owner's worker")
		}
		time.Sleep(10 * time.Millisecond)
	}
	view, err := runner.Run(ctx, first.ID, active.RunID)
	if err != nil || view.Status != RunRunning {
		t.Fatalf("another owner's run was affected: %+v %v", view, err)
	}
	select {
	case input := <-conversation.started:
		t.Fatalf("canceled queued model ran: %s", input)
	default:
	}
}

func TestRootCancellationStopsItsLinkedAgent(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "parent", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conversation := repeatedBlockingConversation{started: make(chan string, 3)}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{AgentWorkspace: func(_ context.Context, request AgentWorkspaceRequest) (string, error) {
		return request.ParentWorkingDir, nil
	}})
	defer runner.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}, SessionID: created.ID}
	parent, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "parent-turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "parent", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("parent did not start")
	}
	spawn := runner.ExecuteAgentTool(ctx, actor, created.ID, orchestrator.ToolCall{ID: "spawn-child", Name: "SpawnAgent", ParametersJSON: `{"kind":"general","title":"child","objective":"wait","parallel":true}`})
	var child pb.AgentTask
	if spawn.ExitCode != 0 || protojson.Unmarshal([]byte(spawn.Output), &child) != nil {
		t.Fatalf("spawn failed: %+v", spawn)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not start")
	}
	if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel-parent", SessionID: created.ID, RunID: parent.RunID, OwnerID: 7, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		view, err := workbench.Get(ctx, 7, child.ChildSessionId)
		if err != nil {
			t.Fatal(err)
		}
		if view.Run != nil && view.Run.Status == RunCanceled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("root cancellation left its child running")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRootCancellationAcknowledgesItsQueuedChildWorkspace(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "queued child", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	tool := canceledTool{started: make(chan struct{}), release: release}
	lifecycle := make(chan string, 2)
	runner := NewSessionRunner(workbench, childToolCancellationConversation{parentStarted: make(chan struct{})}, tool, SessionRunnerOptions{AgentWorkerCount: 1, AgentWorkspace: func(_ context.Context, request AgentWorkspaceRequest) (string, error) {
		return request.ParentWorkingDir, nil
	}, AgentLifecycle: func(_ context.Context, event *pb.AgentLifecycle) error { lifecycle <- event.ChildSessionId; return nil }})
	defer runner.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}, SessionID: created.ID}
	parent, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "parent", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "parent", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	children := []string{}
	for _, name := range []string{"active", "queued"} {
		spawn := runner.ExecuteAgentTool(ctx, actor, created.ID, orchestrator.ToolCall{ID: "spawn-" + name, Name: "SpawnAgent", ParametersJSON: `{"kind":"general","title":"child","objective":"wait","parallel":true}`})
		var child pb.AgentTask
		if spawn.ExitCode != 0 || protojson.Unmarshal([]byte(spawn.Output), &child) != nil {
			t.Fatalf("spawn failed: %+v", spawn)
		}
		children = append(children, child.ChildSessionId)
		if name == "active" {
			select {
			case <-tool.started:
			case <-time.After(5 * time.Second):
				t.Fatal("first child tool did not start")
			}
		}
	}
	if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel-parent", SessionID: created.ID, RunID: parent.RunID, OwnerID: 7, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, err := workbench.Get(ctx, 7, children[1])
		if err != nil {
			t.Fatal(err)
		}
		if view.Run != nil && view.Run.Status == RunCanceling && view.Run.Attempt == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued child did not receive its cancellation fence: %+v", view.Run)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	acknowledged := map[string]bool{}
	for len(acknowledged) < 2 {
		select {
		case id := <-lifecycle:
			acknowledged[id] = true
		case <-time.After(2 * time.Second):
			t.Fatal("queued child workspace lifecycle was not acknowledged")
		}
	}
	for _, id := range children {
		if !acknowledged[id] {
			t.Fatal("another child workspace was acknowledged")
		}
	}
}

func TestRootCancellationRetainsLinkedAgentsDispatchedToolOutcome(t *testing.T) {
	for _, variant := range []struct{ unconfirmed, legacy bool }{{}, {unconfirmed: true}, {legacy: true}, {unconfirmed: true, legacy: true}} {
		unconfirmed := variant.unconfirmed
		name := "confirmed-stop"
		if unconfirmed {
			name = "unconfirmed-stop"
		}
		if variant.legacy {
			name += "-legacy-cancel"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ledger := openWorkbenchTestLedger(t)
			workbench := NewWorkbench(ledger, nil)
			created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "child tool", "", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			conversation := childToolCancellationConversation{parentStarted: make(chan struct{})}
			tool := canceledTool{started: make(chan struct{}), unconfirmed: unconfirmed}
			lifecycle := make(chan string, 1)
			runner := NewSessionRunner(workbench, conversation, tool, SessionRunnerOptions{AgentWorkspace: func(_ context.Context, request AgentWorkspaceRequest) (string, error) {
				return request.ParentWorkingDir, nil
			}, AgentLifecycle: func(ctx context.Context, event *pb.AgentLifecycle) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				lifecycle <- event.ChildSessionId
				return nil
			}})
			defer runner.Close()
			actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}, SessionID: created.ID}
			parent, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "parent-turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "parent", Actor: actor})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-conversation.parentStarted:
			case <-time.After(5 * time.Second):
				t.Fatal("parent did not start")
			}
			spawn := runner.ExecuteAgentTool(ctx, actor, created.ID, orchestrator.ToolCall{ID: "spawn-child", Name: "SpawnAgent", ParametersJSON: `{"kind":"general","title":"child","objective":"wait","parallel":true}`})
			var child pb.AgentTask
			if spawn.ExitCode != 0 || protojson.Unmarshal([]byte(spawn.Output), &child) != nil {
				t.Fatalf("spawn failed: %+v", spawn)
			}
			select {
			case <-tool.started:
			case <-time.After(5 * time.Second):
				t.Fatal("child tool did not start")
			}
			if variant.legacy {
				arguments, _ := json.Marshal(map[string]string{"action": "cancel", "task_id": child.Id})
				result := runner.ExecuteAgentTool(ctx, actor, created.ID, orchestrator.ToolCall{ID: "cancel-child", Name: "AgentTask", ParametersJSON: string(arguments)})
				if result.ExitCode != 0 {
					t.Fatalf("legacy cancellation failed: %+v", result)
				}
			}
			if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel-parent", SessionID: created.ID, RunID: parent.RunID, OwnerID: 7, Actor: actor}); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				view, err := runner.Run(ctx, created.ID, parent.RunID)
				if err != nil {
					t.Fatal(err)
				}
				if view.Status == RunCanceled {
					expected := "stopped"
					if unconfirmed {
						expected = "unknown"
					}
					if view.CancellationState != expected {
						t.Fatalf("parent did not retain child stop state: %+v", view)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("parent did not stop")
				}
				time.Sleep(10 * time.Millisecond)
			}
			events, err := ledger.Events(ctx, child.ChildSessionId)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range events {
				if event.Type == "tool/result" {
					var result struct {
						Output  string `json:"output"`
						Stopped bool   `json:"stopped"`
						Unknown bool   `json:"outcome_unknown"`
					}
					if json.Unmarshal(event.Payload, &result) != nil || result.Output != "partial output" || result.Stopped == unconfirmed || !result.Unknown {
						t.Fatalf("child tool outcome changed: %s", event.Payload)
					}
					select {
					case id := <-lifecycle:
						if id != child.ChildSessionId {
							t.Fatal("cancellation closed another child workspace")
						}
					case <-time.After(time.Second):
						t.Fatal("cancellation omitted the owned workspace lifecycle acknowledgement")
					}
					return
				}
			}
			t.Fatal("root cancellation lost its child's dispatched tool result")
		})
	}
}

type cancelRaceConversation struct {
	started chan struct{}
	release chan struct{}
	result  chan orchestrator.ToolResult
}

type canceledSiblingTools struct {
	started chan string
	calls   atomic.Int32
}

type carriedResourceTool struct {
	session string
	owned   bool
	active  *atomic.Bool
}

func (tool carriedResourceTool) Execute(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult {
	tool.active.Store(true)
	return orchestrator.ToolResult{ToolCallID: "retained", ToolName: "Read", Output: "retained managed resource"}
}

func (tool carriedResourceTool) ReleaseSession(id string) error {
	if tool.owned && id == tool.session {
		tool.active.Store(false)
	}
	return nil
}

type carriedResourceConversation struct{ started chan struct{} }

func (conversation carriedResourceConversation) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	if request.Input == "initial" {
		result := handlers.Tool(ctx, orchestrator.ToolCall{ID: "retained", Name: "Read", ParametersJSON: `{}`})
		return orchestrator.ConversationResult{Success: result.ExitCode == 0, Message: "initial complete"}, nil
	}
	close(conversation.started)
	<-ctx.Done()
	return orchestrator.ConversationResult{}, ctx.Err()
}

func TestQueuedCancellationCannotAbandonAnotherRunnersRetainedResources(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.Create(ctx, 7, "repo", "retained resources", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := workbench.Create(ctx, 8, "repo", "worker occupied", "")
	if err != nil {
		t.Fatal(err)
	}
	active := &atomic.Bool{}
	conversation := carriedResourceConversation{started: make(chan struct{})}
	worker := NewSessionRunner(workbench, conversation, carriedResourceTool{session: created.ID, owned: true, active: active}, SessionRunnerOptions{WorkerCount: 1})
	defer worker.Close()
	commands := NewSessionRunner(workbench, conversation, carriedResourceTool{session: created.ID, active: active}, SessionRunnerOptions{})
	defer commands.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
	if _, err := worker.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "initial", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "initial", Actor: actor}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var view SessionView
	for {
		view, err = workbench.Get(ctx, 7, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Run != nil && view.Run.Status == RunCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial turn did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !active.Load() {
		t.Fatal("fixture did not retain the managed resource")
	}
	otherRun, err := worker.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "occupy", SessionID: other.ID, OwnerID: 8, ExpectedSeq: int64(other.EventCount), Content: "hold", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not become occupied")
	}
	var queued RunView
	deadline = time.Now().Add(5 * time.Second)
	for {
		view, err = workbench.Get(ctx, 7, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		queued, err = worker.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "queued", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(view.EventCount), Content: "queued", Actor: actor})
		if !errors.Is(err, ErrSequenceConflict) || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	viewRun, err := commands.CancelRun(ctx, CancelRunCommand{RequestID: "cancel-queued", SessionID: created.ID, RunID: queued.RunID, OwnerID: 7, Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	if viewRun.Status != RunCanceling || viewRun.CancellationState != "unknown" || !active.Load() {
		t.Fatalf("foreign cancel abandoned a retained resource: %+v / active=%v", viewRun, active.Load())
	}
	if _, err := worker.CancelRun(ctx, CancelRunCommand{RequestID: "cancel-other", SessionID: other.ID, RunID: otherRun.RunID, OwnerID: 8, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		viewRun, err = commands.Run(ctx, created.ID, queued.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if viewRun.Status == RunCanceled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resource owner did not finish queued cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if viewRun.CancellationState != "stopped" || active.Load() {
		t.Fatalf("owned resource stop was not acknowledged: %+v / active=%v", viewRun, active.Load())
	}
}

type blockingCancelRelease struct {
	session string
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
	fail    bool
}

func (tool *blockingCancelRelease) Execute(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult {
	return orchestrator.ToolResult{ExitCode: 1, Error: "unused fixture"}
}

func (tool *blockingCancelRelease) ReleaseSession(id string) error {
	if tool.fail {
		return errors.New("managed resource stop unconfirmed")
	}
	if id == tool.session && tool.calls.Add(1) == 1 {
		close(tool.started)
		<-tool.release
	}
	return nil
}

func TestUnconfirmedResourceCleanupFencesNewRunsAndTaskFinish(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "unconfirmed cleanup", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation := repeatedBlockingConversation{started: make(chan string, 1)}
	runner := NewSessionRunner(workbench, conversation, &blockingCancelRelease{fail: true}, SessionRunnerOptions{})
	defer runner.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}, SessionID: created.ID}
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "work", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not start")
	}
	if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel", SessionID: created.ID, RunID: run.RunID, OwnerID: 7, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var view SessionView
	for {
		view, err = workbench.Get(ctx, 7, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Run != nil && view.Run.Status == RunCanceled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancellation outcome was not durable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if view.Run.CancellationState != "unknown" {
		t.Fatalf("cleanup failure was not retained: %+v", view.Run)
	}
	if _, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "new", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(view.EventCount), Content: "new", Actor: actor}); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("unconfirmed cleanup admitted a new run: %v", err)
	}
	if err := ModelTaskIdle(ctx, ledger, actor); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("unconfirmed resources were allowed to finish: %v", err)
	}
	if _, err := runner.RequestContinuation(ctx, ContinueCommand{RequestID: "new-continuation", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(view.EventCount), CheckpointHash: view.Run.CheckpointHash, Actor: actor}); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("unconfirmed cleanup admitted a continuation: %v", err)
	}
}

func TestQueuedCancellationWaitsForItsSingleResourceFinalizer(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	other, err := workbench.Create(ctx, 8, "repo", "worker occupied", "")
	if err != nil {
		t.Fatal(err)
	}
	created, err := workbench.Create(ctx, 7, "repo", "queued release", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation := repeatedBlockingConversation{started: make(chan string, 2)}
	tool := &blockingCancelRelease{session: created.ID, started: make(chan struct{}), release: make(chan struct{})}
	runner := NewSessionRunner(workbench, conversation, tool, SessionRunnerOptions{WorkerCount: 1})
	defer runner.Close()
	defer func() {
		select {
		case <-tool.release:
		default:
			close(tool.release)
		}
	}()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
	otherRun, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "occupy", SessionID: other.ID, OwnerID: 8, ExpectedSeq: int64(other.EventCount), Content: "hold worker", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	queued, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "queued", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "queued", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	replied := make(chan error, 1)
	go func() {
		_, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel-queued", SessionID: created.ID, RunID: queued.RunID, OwnerID: 7, Actor: actor})
		replied <- err
	}()
	select {
	case <-tool.started:
	case <-time.After(5 * time.Second):
		t.Fatal("resource release did not start")
	}
	if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel-other", SessionID: other.ID, RunID: otherRun.RunID, OwnerID: 8, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		view, err := runner.Run(ctx, created.ID, queued.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == RunCanceled {
			t.Fatal("second finalizer claimed stopped while owned release was still running")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tool.calls.Load() != 1 {
		t.Fatalf("resource release ran %d times", tool.calls.Load())
	}
	close(tool.release)
	select {
	case err := <-replied:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("release did not finish")
	}
	view, err := runner.Run(ctx, created.ID, queued.RunID)
	if err != nil || view.Status != RunCanceled || view.CancellationState != "stopped" {
		t.Fatalf("release was not durably acknowledged: %+v %v", view, err)
	}
}

func TestAnotherRunnerCannotFinalizeTheOwningWorkersPendingResourceStop(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.Create(ctx, 7, "repo", "lease cleanup owner", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation := repeatedBlockingConversation{started: make(chan string, 1)}
	tool := &blockingCancelRelease{session: created.ID, started: make(chan struct{}), release: make(chan struct{})}
	options := SessionRunnerOptions{WorkerID: "reused-supervisor-label", LeaseDuration: 80 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond}
	worker := NewSessionRunner(workbench, conversation, tool, options)
	defer worker.Close()
	commands := NewSessionRunner(workbench, conversation, tool, options)
	defer commands.Close()
	defer func() {
		select {
		case <-tool.release:
		default:
			close(tool.release)
		}
	}()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
	run, err := worker.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "work", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	if _, err := commands.CancelRun(ctx, CancelRunCommand{RequestID: "cancel", SessionID: created.ID, RunID: run.RunID, OwnerID: 7, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tool.started:
	case <-time.After(5 * time.Second):
		t.Fatal("owned release did not start")
	}
	time.Sleep(250 * time.Millisecond)
	view, err := commands.Run(ctx, created.ID, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != RunCanceling || view.CancellationState != "unknown" || tool.calls.Load() != 1 {
		t.Fatalf("another runner finalized resources it did not own: %+v / releases=%d", view, tool.calls.Load())
	}
	current, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := commands.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "too-early", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(current.EventCount), Content: "new", Actor: actor}); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("unconfirmed cleanup admitted a new run: %v", err)
	}
	close(tool.release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, err = commands.Run(ctx, created.ID, run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == RunCanceled && view.CancellationState == "stopped" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("owning worker did not acknowledge stop: %+v", view)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (tool *canceledSiblingTools) Execute(ctx context.Context, actor identity.Actor, _ string, call orchestrator.ToolCall) orchestrator.ToolResult {
	first := tool.calls.Add(1) == 1
	tool.started <- actor.SessionID
	<-ctx.Done()
	return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, ExitCode: 1, Error: "canceled", Output: "partial output", RuntimeID: "owned-fixture", Stopped: !first, OutcomeUnknown: true}
}

func TestUnknownChildStopDoesNotSkipCancellationOfItsSibling(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "siblings", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conversation := childToolCancellationConversation{parentStarted: make(chan struct{})}
	tool := &canceledSiblingTools{started: make(chan string, 2)}
	runner := NewSessionRunner(workbench, conversation, tool, SessionRunnerOptions{AgentWorkspace: func(_ context.Context, request AgentWorkspaceRequest) (string, error) {
		return request.ParentWorkingDir, nil
	}})
	defer runner.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}, SessionID: created.ID}
	parent, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "parent-turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "parent", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.parentStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("parent did not start")
	}
	children := []string{}
	for _, name := range []string{"first", "second"} {
		result := runner.ExecuteAgentTool(ctx, actor, created.ID, orchestrator.ToolCall{ID: "spawn-" + name, Name: "SpawnAgent", ParametersJSON: `{"kind":"general","title":"child","objective":"wait","parallel":true}`})
		if result.ExitCode != 0 {
			t.Fatalf("spawn failed: %+v", result)
		}
		select {
		case id := <-tool.started:
			children = append(children, id)
		case <-time.After(5 * time.Second):
			t.Fatal("child tool did not start")
		}
	}
	if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel-parent", SessionID: created.ID, RunID: parent.RunID, OwnerID: 7, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		stopped := 0
		for _, id := range children {
			view, err := workbench.Get(ctx, 7, id)
			if err != nil {
				t.Fatal(err)
			}
			if view.Run != nil && view.Run.Status == RunCanceled {
				stopped++
			}
		}
		if stopped == len(children) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unknown child stop left its sibling running")
		}
		time.Sleep(10 * time.Millisecond)
	}
	view, err := runner.Run(ctx, created.ID, parent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for view.Status != RunCanceled && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		view, err = runner.Run(ctx, created.ID, parent.RunID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if view.Status != RunCanceled || view.CancellationState != "unknown" {
		t.Fatalf("child uncertainty was lost: %+v", view)
	}
}

func (conversation cancelRaceConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	close(conversation.started)
	<-conversation.release
	result := handlers.Tool(context.WithoutCancel(ctx), orchestrator.ToolCall{ID: "late-write", Name: "Write", ParametersJSON: `{"path":"late.txt","content":"late effect"}`})
	conversation.result <- result
	return orchestrator.ConversationResult{Success: true, Message: "late answer"}, nil
}

func TestSessionCancellationRefusesLateToolDispatch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "cancel race", "", root)
	if err != nil {
		t.Fatal(err)
	}
	conversation := cancelRaceConversation{started: make(chan struct{}), release: make(chan struct{}), result: make(chan orchestrator.ToolResult, 1)}
	executor := tools.NewExecutor(root)
	defer executor.Close()
	runner := NewSessionRunner(workbench, conversation, executorToolAdapter{executor: executor}, SessionRunnerOptions{})
	defer runner.Close()
	defer func() {
		select {
		case <-conversation.release:
		default:
			close(conversation.release)
		}
	}()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "tester", TenantID: "org:test", Roles: []string{"USER"}}
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "work", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("conversation did not start")
	}
	if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "cancel", SessionID: created.ID, RunID: run.RunID, OwnerID: 7, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	close(conversation.release)
	select {
	case result := <-conversation.result:
		if result.ExitCode == 0 {
			t.Fatalf("late tool was admitted: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late request did not return")
	}
	if _, err := os.Stat(filepath.Join(root, "late.txt")); !os.IsNotExist(err) {
		t.Fatalf("cancellation allowed an external write: %v", err)
	}
}
