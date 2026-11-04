package session

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
)

type protectedInputCompactor struct {
	request chan orchestrator.ConversationRequest
}

func (c *protectedInputCompactor) RunConversation(_ context.Context, request orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	c.request <- request
	update := &pb.CompactionUpdate{Summary: "The original constraints remain in force."}
	// Match the existing Python rule: an unsourced message ends the replaceable prefix.
	for _, message := range request.History {
		if message.EventID == "" || message.EventChecksum == "" {
			break
		}
		update.SourceEvents = append(update.SourceEvents, &pb.CanonicalEventReference{EventId: message.EventID, Checksum: message.EventChecksum})
		update.RemovedMessages++
	}
	if handlers.Compaction == nil {
		return orchestrator.ConversationResult{}, errors.New("missing compaction callback")
	}
	if err := handlers.Compaction(update); err != nil {
		return orchestrator.ConversationResult{}, err
	}
	return orchestrator.ConversationResult{Success: true, Message: "Recovered without replacing current intent."}, nil
}

func TestSessionRunnerRecoveryProtectsInlineActiveInput(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	w := NewWorkbench(ledger, nil)
	created, err := w.Create(ctx, 7, "repo", "inline input", "goal")
	if err != nil {
		t.Fatal(err)
	}
	appendUser := func(content string) string {
		events, readErr := ledger.Events(ctx, created.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		message, appendErr := w.AppendUserMessage(ctx, 7, created.ID, int64(len(events)), content)
		if appendErr != nil {
			t.Fatal(appendErr)
		}
		return message.ID
	}
	appendUser("Keep the public API compatible.")
	appendUser("Preserve tool call and result pairing.")
	const intent = "Inspect the repository and keep this current request intact."
	input, err := ledger.AppendSurface(ctx, created.ID, 3, userMessageEventType, messagePayload{Author: "user", Content: intent, RequestID: "request-inline-input"}, SurfaceOperation{Op: "append"})
	if err != nil {
		t.Fatal(err)
	}
	inputID := input.EventID
	checkpoint, err := w.CreateCheckpoint(ctx, 7, created.ID, 4, inputID, "before-tool")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.UpdateStatus(ctx, 7, created.ID, 5, "paused"); err != nil {
		t.Fatal(err)
	}
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	const runID = "run-inline-input"
	payload, err := continuationRequestPayloadForTest(ctx, w, created.ID, checkpoint.Hash, "request-inline-input", runID, actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 6, continuationEventType, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 7, runLeasedEventType, runLeasePayload{RunID: runID, RequestID: "request-inline-input", LeaseID: "expired-inline", WorkerID: "old-worker", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC()}); err != nil {
		t.Fatal(err)
	}
	call := toolCallPayload{RunID: runID, ToolCallID: "read-before-recovery", ToolName: "Read", ArgumentsJSON: `{"path":"main.go"}`}
	if _, err := ledger.AppendSurface(ctx, created.ID, 8, "tool/call", call, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 9, toolDispatchedType, call); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.AppendSurface(ctx, created.ID, 10, "tool/result", toolResultPayload{RunID: runID, ToolCallID: call.ToolCallID, ToolName: "Read", Output: "package main"}, SurfaceOperation{Op: "append"}); err != nil {
		t.Fatal(err)
	}
	before, err := ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	module := &protectedInputCompactor{request: make(chan orchestrator.ConversationRequest, 1)}
	memory := &scopedRecallMemory{}
	runner := NewSessionRunner(w, module, nil, SessionRunnerOptions{WorkerID: "recovered-worker", LeaseDuration: time.Second, Memory: memory, Permissions: permission.NewController(map[string]permission.Level{"RecallMemory": permission.AutoAllow}, nil)})
	t.Cleanup(func() { _ = runner.Close() })
	if err := runner.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	terminal := waitForRunStatus(t, runner, created.ID, runID, RunCompleted)
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	if terminal.Attempt != 2 {
		t.Fatalf("recovered attempt = %d, want 2", terminal.Attempt)
	}
	request := <-module.request
	if request.Input != "" || request.NewTurn || len(request.History) != 5 {
		t.Fatalf("active intent must be inline once in a resumed turn: input=%q new_turn=%t history=%+v", request.Input, request.NewTurn, request.History)
	}
	current := request.History[2]
	if current.Role != "user" || current.Content != intent || current.EventID != "" || current.EventChecksum != "" {
		t.Fatalf("current input is not the protected inline message: %+v", current)
	}
	if len(request.History[3].ToolCalls) != 1 || request.History[4].ToolCallID != call.ToolCallID || request.MemoryContextJSON != `{"entries":[]}` || memory.query.Query != intent || memory.query.TargetSessionID != created.ID {
		t.Fatalf("tool history or original Memory query changed: request=%+v query=%+v", request, memory.query)
	}
	after, err := ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for index, event := range before.Events {
		if after.Events[index].EventID != event.EventID || after.Events[index].Checksum != event.Checksum {
			t.Fatal("recovery changed an immutable prefix event")
		}
	}
	if len(after.Surface) != 5 || after.Surface[0].Type != compactionEventType || after.Surface[1].EventID != inputID || after.Surface[2].Type != "tool/call" || after.Surface[3].Type != "tool/result" || after.Surface[4].Type != "assistant/message" {
		t.Fatalf("compaction lost intent/tool pairing: %+v", after.Surface)
	}
}
