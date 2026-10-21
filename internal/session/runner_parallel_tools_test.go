package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
)

type parallelReadonlyConversation struct {
	results chan []orchestrator.ToolResult
}

type queuedResultAuditLog struct {
	EventLog
	toolStarted chan struct{}
	blocked     chan struct{}
	release     chan struct{}
	once        sync.Once
}

func (log *queuedResultAuditLog) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	if kind == runHeartbeatEventType {
		select {
		case <-log.toolStarted:
			log.once.Do(func() {
				close(log.blocked)
				select {
				case <-log.release:
				case <-ctx.Done():
				}
			})
		default:
		}
	}
	return log.EventLog.Append(ctx, id, seq, kind, payload)
}

func TestSessionRunnerAuditsResultsAfterWaitingForAnotherWriter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	base := openWorkbenchTestLedger(t)
	ledger := &queuedResultAuditLog{EventLog: base, toolStarted: make(chan struct{}), blocked: make(chan struct{}), release: make(chan struct{})}
	var started, release sync.Once
	t.Cleanup(func() { release.Do(func() { close(ledger.release) }) })
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "queued audits", "Read sources")
	if err != nil {
		t.Fatal(err)
	}
	conversation := parallelReadonlyConversation{results: make(chan []orchestrator.ToolResult, 1)}
	runner := NewSessionRunner(workbench, conversation, ToolExecutionFunc(func(ctx context.Context, _ identity.Actor, _ string, call orchestrator.ToolCall) orchestrator.ToolResult {
		started.Do(func() { close(ledger.toolStarted) })
		select {
		case <-ledger.blocked:
			return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Output: "Source read successfully"}
		case <-ctx.Done():
			return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "read canceled", ExitCode: 1}
		}
	}), SessionRunnerOptions{WorkerID: "queued-audit", LeaseDuration: time.Minute, HeartbeatInterval: 20 * time.Millisecond})
	t.Cleanup(func() { _ = runner.Close() })
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "queued-audit", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: int64(created.EventCount), Content: "Read sources", Actor: testRunnerActor()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ledger.blocked:
	case <-ctx.Done():
		t.Fatal("fixture never blocked the audit writer")
	}
	time.Sleep(6 * time.Second)
	release.Do(func() { close(ledger.release) })
	select {
	case results := <-conversation.results:
		for _, result := range results {
			if result.Error != "" || result.ExitCode != 0 {
				t.Fatalf("known read result lost while waiting for another writer: %+v", result)
			}
		}
	case <-ctx.Done():
		t.Fatal("queued results did not finish")
	}
	_ = waitForRunStatus(t, runner, created.ID, run.RunID, RunCompleted)
	snapshot, err := ReadVerifiedSnapshot(ctx, base, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	results := 0
	for _, event := range snapshot.Events {
		if event.Type == toolUnknownEventType {
			t.Fatal("a known result became unknown while waiting for a writer")
		}
		if event.Type == "tool/result" {
			results++
		}
	}
	if results != 5 {
		t.Fatalf("native results=%d, want five without replay", results)
	}
}

func (c parallelReadonlyConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	results := make([]orchestrator.ToolResult, 5)
	start := make(chan struct{})
	var pending sync.WaitGroup
	for index := range results {
		pending.Add(1)
		go func(index int) {
			defer pending.Done()
			<-start
			results[index] = handlers.Tool(ctx, orchestrator.ToolCall{
				ID: fmt.Sprintf("read-%d", index), Name: "Read",
				ParametersJSON: `{"limit":90,"path":"schedule/__init__.py","start":471}`,
			})
		}(index)
	}
	close(start)
	pending.Wait()
	c.results <- results
	return orchestrator.ConversationResult{Success: true, Message: "Repository analysis completed"}, nil
}

func TestSessionRunnerCompletesParallelReadonlyCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "parallel reads", "Inspect public API")
	if err != nil {
		t.Fatal(err)
	}
	conversation := parallelReadonlyConversation{results: make(chan []orchestrator.ToolResult, 1)}
	runner := NewSessionRunner(workbench, conversation, ToolExecutionFunc(func(_ context.Context, _ identity.Actor, _ string, call orchestrator.ToolCall) orchestrator.ToolResult {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Output: "Source read successfully"}
	}), SessionRunnerOptions{WorkerID: "parallel-reader", LeaseDuration: time.Minute})
	t.Cleanup(func() { _ = runner.Close() })
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{
		RequestID: "parallel-read", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: int64(created.EventCount), Content: "Inspect the scheduling API", Actor: testRunnerActor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case results := <-conversation.results:
		for _, result := range results {
			if result.Error != "" || result.ExitCode != 0 {
				t.Fatalf("parallel read %s failed: %+v", result.ToolCallID, result)
			}
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	terminal := waitForRunStatus(t, runner, created.ID, run.RunID, RunCompleted)
	if terminal.Status != RunCompleted {
		t.Fatalf("terminal run = %+v", terminal)
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil {
		t.Fatalf("verify completed read-only Session: %v", err)
	}
	calls, results := make(map[string]int), make(map[string]int)
	for _, event := range snapshot.Events {
		if event.Type != "tool/call" && event.Type != "tool/result" {
			continue
		}
		var payload struct {
			ToolCallID string `json:"tool_call_id"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if event.Type == "tool/call" {
			calls[payload.ToolCallID]++
		} else {
			results[payload.ToolCallID]++
		}
	}
	for _, id := range []string{"read-0", "read-1", "read-2", "read-3", "read-4"} {
		if calls[id] != 1 || results[id] != 1 {
			t.Fatalf("durable %s: calls=%d results=%d, want one pair", id, calls[id], results[id])
		}
	}
}
