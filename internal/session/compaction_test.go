package session

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"
)

type compactingConversation struct{ update *pb.CompactionUpdate }

func (a compactingConversation) RunConversation(_ context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	if handlers.Compaction == nil {
		return orchestrator.ConversationResult{}, errors.New("missing compaction handler")
	}
	if err := handlers.Compaction(a.update); err != nil {
		return orchestrator.ConversationResult{}, err
	}
	return orchestrator.ConversationResult{Success: true, Message: "continued after summary"}, nil
}

func TestRunnerCommitsCompactionAndRecoversCanonicalSurface(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	w := NewWorkbench(ledger, nil)
	created, err := w.Create(ctx, 7, "repo", "compaction", "goal")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"original constraint", "revised constraint"} {
		events, _ := ledger.Events(ctx, created.ID)
		if _, err := w.AppendUserMessage(ctx, 7, created.ID, int64(len(events)), text); err != nil {
			t.Fatal(err)
		}
	}
	oldSurface, _ := ledger.Surface(ctx, created.ID)
	update := &pb.CompactionUpdate{Summary: "constraints retained", RemovedMessages: 2, KeepRecentMessages: 1}
	for _, event := range oldSurface {
		update.SourceEvents = append(update.SourceEvents, &pb.CanonicalEventReference{EventId: event.EventID, Checksum: event.Checksum})
	}
	runner := NewSessionRunner(w, compactingConversation{update}, nil, SessionRunnerOptions{WorkerID: "compact-worker"})
	defer runner.Close()
	events, _ := ledger.Events(ctx, created.ID)
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "compact-request", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(len(events)), Content: "continue", Actor: testRunnerActor()})
	if err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, runner, created.ID, run.RunID, RunCompleted)
	events, _ = ledger.Events(ctx, created.ID)
	surface, err := ledger.Surface(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(surface) != 3 || surface[0].Type != compactionEventType {
		t.Fatalf("unexpected compacted surface: %+v", surface)
	}
	history, _, err := conversationHistory(surface, events, run.RunID, "")
	if err != nil || history[0].Role != "system" || history[0].Content != "constraints retained" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	projection, _ := projectRun(events, run.RunID)
	checkpoint, _ := surfaceAt(events, projection.targetSeq)
	expanded, err := expandContinuationCompactions(checkpoint, surface, events, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateContinuationSurface(checkpoint, expanded, run.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := expandContinuationCompactions(checkpoint, surface, events, "foreign-run"); err == nil {
		t.Fatal("foreign compaction accepted")
	}
	if err := ledger.Verify(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCompactionPrefixRejectsIncompleteOrForeignSources(t *testing.T) {
	surface := []Event{{EventID: "a", Checksum: "ha", Type: userMessageEventType}, {EventID: "b", Checksum: "hb", Type: userMessageEventType}, {EventID: "input", Checksum: "hi", Type: userMessageEventType}}
	for _, sources := range [][]canonicalSource{
		nil, {{"b", "hb"}}, {{"a", "wrong"}}, {{"a", "ha"}, {"input", "hi"}},
		{{"a", "ha"}, {"a", "ha"}}, {{"a", "ha"}, {"b", "hb"}, {"input", "hi"}},
	} {
		if _, err := compactionPrefix(surface, sources, "input"); err == nil {
			t.Fatalf("accepted %+v", sources)
		}
	}
	if n, err := compactionPrefix(surface, []canonicalSource{{"a", "ha"}, {"b", "hb"}}, "input"); err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestCompactionKeepsToolPairsAndRepeatedSummaryOrder(t *testing.T) {
	call, _ := json.Marshal(toolCallPayload{RunID: "r", ToolCallID: "t", ToolName: "Read"})
	result, _ := json.Marshal(toolResultPayload{RunID: "r", ToolCallID: "t", ToolName: "Read"})
	surface := []Event{{EventID: "a", Checksum: "ha", Type: "tool/call", Payload: call}, {EventID: "b", Checksum: "hb", Type: "tool/result", Payload: result}, {EventID: "input", Checksum: "hi", Type: userMessageEventType}}
	if _, err := compactionPrefix(surface, []canonicalSource{{"a", "ha"}}, "input"); err == nil {
		t.Fatal("split tool pair accepted")
	}
	refs := []canonicalSource{{"a", "ha"}, {"b", "hb"}}
	payload, _ := json.Marshal(compactionPayload{RunID: "r", Summary: "summary", Sources: refs, ReportedSources: refs, InputEventID: "input"})
	first := Event{EventID: "summary-1", Checksum: "hs", Type: compactionEventType, Payload: payload}
	projected, err := applyCompaction(surface, first)
	if err != nil {
		t.Fatal(err)
	}
	for _, reported := range [][]canonicalSource{refs, {{"summary-1", "hs"}}} {
		payload, _ = json.Marshal(compactionPayload{RunID: "r", Summary: "second", Sources: []canonicalSource{{"summary-1", "hs"}}, ReportedSources: reported, InputEventID: "input"})
		second := Event{EventID: "summary-2", Checksum: "hs2", Type: compactionEventType, Payload: payload}
		got, err := applyCompaction(projected, second)
		if err != nil || len(got) != 2 || got[0].EventID != "summary-2" || got[1].EventID != "input" {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	}
}
