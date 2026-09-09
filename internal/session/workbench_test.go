package session

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openWorkbenchTestLedger(t *testing.T) *SQLiteEventLog {
	t.Helper()
	ledger, err := OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatalf("open canonical session ledger: %v", err)
	}
	t.Cleanup(func() {
		if err := ledger.Close(); err != nil {
			t.Errorf("close canonical session ledger: %v", err)
		}
	})
	return ledger
}

func TestWorkbenchAppendsMessageOnlyToCanonicalLedger(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)

	created, err := workbench.Create(ctx, 7, "repo", "title", "goal")
	if err != nil {
		t.Fatalf("create workbench session: %v", err)
	}
	message, err := workbench.AppendUserMessage(ctx, 7, created.ID, int64(created.EventCount), "hello")
	if err != nil {
		t.Fatalf("append workbench message: %v", err)
	}

	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatalf("read canonical events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("canonical event count = %d, want 2: %+v", len(events), events)
	}
	if events[0].Type != "session/created" || events[1].Type != "user/message" {
		t.Fatalf("canonical event types = %q, %q", events[0].Type, events[1].Type)
	}
	if message.Seq != 1 || message.ID != events[1].EventID || message.Hash != events[1].Checksum {
		t.Fatalf("message view = %+v, canonical event = %+v", message, events[1])
	}
	if err := ledger.Verify(ctx, created.ID); err != nil {
		t.Fatalf("verify canonical event chain: %v", err)
	}
}

func TestWorkbenchEventsAfterRejectsNegativeLimit(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.Create(ctx, 7, "repo", "pagination", "goal")
	if err != nil {
		t.Fatalf("create workbench session: %v", err)
	}
	if _, err := workbench.EventsAfter(ctx, 7, created.ID, -1, -2); !errors.Is(err, ErrInvalidSessionInput) {
		t.Fatalf("negative event limit error = %v, want ErrInvalidSessionInput", err)
	}
}

func TestWorkbenchForeignOwnerIsIndistinguishableFromMissing(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.Create(ctx, 7, "repo", "title", "goal")
	if err != nil {
		t.Fatalf("create workbench session: %v", err)
	}

	_, foreignErr := workbench.Get(ctx, 8, created.ID)
	_, missingErr := workbench.Get(ctx, 8, "missing-session")
	if !errors.Is(foreignErr, ErrSessionNotFound) || !errors.Is(missingErr, ErrSessionNotFound) {
		t.Fatalf("foreign error = %v, missing error = %v; both must hide existence", foreignErr, missingErr)
	}
}

func TestWorkbenchConcurrentExpectedSequenceHasOneWinner(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.Create(ctx, 7, "repo", "title", "goal")
	if err != nil {
		t.Fatalf("create workbench session: %v", err)
	}

	const writers = 2
	errorsByWriter := make([]error, writers)
	var wg sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			_, errorsByWriter[writer] = workbench.AppendUserMessage(
				ctx, 7, created.ID, int64(created.EventCount), "concurrent message",
			)
		}(writer)
	}
	wg.Wait()

	successes, conflicts := 0, 0
	for _, appendErr := range errorsByWriter {
		switch {
		case appendErr == nil:
			successes++
		case errors.Is(appendErr, ErrSequenceConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent append error: %v", appendErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results: successes=%d conflicts=%d errors=%v", successes, conflicts, errorsByWriter)
	}
}

func TestWorkbenchRestoreAppendsRewindAndKeepsHistory(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "title", "goal")
	if err != nil {
		t.Fatalf("create workbench session: %v", err)
	}
	first, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "first")
	if err != nil {
		t.Fatalf("append first message: %v", err)
	}
	if _, err := workbench.AppendUserMessage(ctx, 7, created.ID, 2, "second"); err != nil {
		t.Fatalf("append second message: %v", err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 3, first.ID, "after first")
	if err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}

	before, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatalf("read history before restore: %v", err)
	}
	originalIDs := make([]string, len(before))
	originalChecksums := make([]string, len(before))
	for index, event := range before {
		originalIDs[index] = event.EventID
		originalChecksums[index] = event.Checksum
	}

	rewind, err := workbench.RestoreCheckpoint(ctx, 7, created.ID, checkpoint.Hash, 4)
	if err != nil {
		t.Fatalf("restore checkpoint: %v", err)
	}
	after, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatalf("read history after restore: %v", err)
	}
	if len(after) != len(before)+1 || after[len(after)-1].Type != "session/rewind" {
		t.Fatalf("restore history = %+v", after)
	}
	if rewind.Seq != 4 || rewind.ID != after[4].EventID || rewind.Hash != after[4].Checksum {
		t.Fatalf("rewind view = %+v, canonical event = %+v", rewind, after[4])
	}
	if rewind.RewindTargetSeq == nil || *rewind.RewindTargetSeq != 1 {
		t.Fatalf("rewind target = %+v, want 1", rewind.RewindTargetSeq)
	}
	for index := range before {
		if after[index].EventID != originalIDs[index] || after[index].Checksum != originalChecksums[index] {
			t.Fatalf("restore rewrote event %d: before=%+v after=%+v", index, before[index], after[index])
		}
	}
	surface, err := ledger.Surface(ctx, created.ID)
	if err != nil {
		t.Fatalf("derive restored surface: %v", err)
	}
	if len(surface) != 1 || surface[0].EventID != first.ID {
		t.Fatalf("restored surface = %+v, want only first message", surface)
	}
	if err := ledger.Verify(ctx, created.ID); err != nil {
		t.Fatalf("verify restored ledger: %v", err)
	}

	secondRewind, err := workbench.RestoreCheckpoint(ctx, 7, created.ID, checkpoint.Hash, 5)
	if err != nil {
		t.Fatalf("repeat checkpoint restore: %v", err)
	}
	if secondRewind.Seq != 5 {
		t.Fatalf("repeated rewind seq = %d, want 5", secondRewind.Seq)
	}
	afterRepeat, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatalf("read history after repeated restore: %v", err)
	}
	if len(afterRepeat) != len(before)+2 || afterRepeat[5].Type != "session/rewind" {
		t.Fatalf("repeated restore history = %+v", afterRepeat)
	}
	for index := range before {
		if afterRepeat[index].EventID != originalIDs[index] || afterRepeat[index].Checksum != originalChecksums[index] {
			t.Fatalf("repeated restore rewrote event %d", index)
		}
	}
	if _, err := workbench.RestoreCheckpoint(ctx, 7, created.ID, checkpoint.Hash, 5); !errors.Is(err, ErrSequenceConflict) {
		t.Fatalf("stale restore error = %v, want ErrSequenceConflict", err)
	}
	afterStale, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatalf("read history after stale restore: %v", err)
	}
	if len(afterStale) != len(afterRepeat) {
		t.Fatalf("stale restore appended an event: before=%d after=%d", len(afterRepeat), len(afterStale))
	}
	if err := ledger.Verify(ctx, created.ID); err != nil {
		t.Fatalf("verify repeated restore ledger: %v", err)
	}
}

func TestWorkbenchRestoreProjectsPausedForContinuation(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "resume", "goal")
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "continue after restore")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 2, message.ID, "restore point")
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.UpdateStatus(ctx, 7, created.ID, 3, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := workbench.RestoreCheckpoint(ctx, 7, created.ID, checkpoint.Hash, 4); err != nil {
		t.Fatal(err)
	}
	restored, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != "paused" {
		t.Fatalf("restored status = %q, want paused", restored.Status)
	}
	continued, err := workbench.ContinueFromCheckpoint(ctx, 7, created.ID, checkpoint.Hash, 5)
	if err != nil {
		t.Fatalf("continue after restore: %v", err)
	}
	if continued.Type != continuationEventType {
		t.Fatalf("continued event type = %q, want %q", continued.Type, continuationEventType)
	}
}

func TestWorkbenchReadsPreRunnerContinuationTargetHash(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "legacy continuation", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "resume me")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 2, target.ID, "legacy anchor")
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.UpdateStatus(ctx, 7, created.ID, 3, "paused"); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, map[string]any{
		"request_id":      "legacy-request",
		"checkpoint_hash": checkpoint.Hash,
		"target_event_id": target.ID,
		"target_seq":      target.Seq,
		"target_hash":     target.Hash,
		"resume_count":    1,
	}); err != nil {
		t.Fatal(err)
	}

	view, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatalf("read legacy continuation: %v", err)
	}
	if view.Status != "running" || view.Run != nil {
		t.Fatalf("legacy continuation projection = %+v", view)
	}
	events, err := workbench.Events(ctx, 7, created.ID, 0)
	if err != nil {
		t.Fatalf("render legacy continuation event: %v", err)
	}
	continued := events[len(events)-1]
	if continued.Continuation == nil || continued.Continuation.TargetHash != target.Hash {
		t.Fatalf("legacy continuation view = %+v", continued)
	}
	if continued.Continuation.RequestID != "legacy-request" || continued.Continuation.RunID != "" {
		t.Fatalf("continuation lineage = %+v", continued.Continuation)
	}

	runner := NewSessionRunner(workbench, nil, nil, SessionRunnerOptions{})
	defer runner.Close()
	if err := runner.Recover(ctx); err != nil {
		t.Fatalf("recover with pre-runner continuation: %v", err)
	}
}

func TestEventToViewPreservesContinuationLineage(t *testing.T) {
	payload, err := json.Marshal(continuationPayload{
		RequestID: "request-production", RunID: "run-production",
		CheckpointHash: "checkpoint-hash", TargetEventID: "event-target",
		TargetSeq: 2, TargetChecksum: "target-hash", ResumeCount: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := eventToView(Event{
		EventID: "event-continuation", SessionID: "session-1", Seq: 3,
		Type: continuationEventType, Payload: payload, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("event projection: %v", err)
	}
	if view.Continuation == nil || view.Continuation.RequestID != "request-production" || view.Continuation.RunID != "run-production" {
		t.Fatalf("continuation lineage = %+v", view.Continuation)
	}
}

func TestWorkbenchDeleteHidesSessionButKeepsLedgerHistory(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "title", "goal")
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.Delete(ctx, 7, created.ID, 1); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := workbench.Get(ctx, 7, created.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("deleted session Get error = %v, want not found", err)
	}
	listed, err := workbench.List(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("deleted session remained in surface: %+v", listed)
	}
	if _, err := workbench.AppendUserMessage(ctx, 7, created.ID, 2, "after delete"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("deleted session accepted a message: %v", err)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Type != "session/deleted" {
		t.Fatalf("delete did not preserve its canonical history: %+v", events)
	}
}

func TestWorkbenchCheckpointRejectsNonSurfaceEvent(t *testing.T) {
	ctx := context.Background()
	workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
	created, err := workbench.Create(ctx, 7, "repo", "title", "goal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 1, created.ID, "invalid lifecycle target"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("lifecycle checkpoint error = %v, want ErrSessionNotFound", err)
	}
}
