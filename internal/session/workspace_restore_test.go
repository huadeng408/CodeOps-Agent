package session

import (
	"context"
	"testing"
	"time"
)

func TestWorkbenchRestoreCodeChangesUsesCheckpointBoundTerminalRun(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "restore", "restore files")
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "make a change")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 2, message.ID, "before run")
	if err != nil {
		t.Fatal(err)
	}
	continuation := continuationPayload{
		RequestID: "restore-request", RunID: "restore-run", CheckpointHash: checkpoint.Hash,
		TargetEventID: message.ID, TargetSeq: message.Seq, TargetChecksum: message.Hash,
		ResumeCount: 1,
	}
	if _, err := ledger.Append(ctx, created.ID, 3, continuationEventType, continuation); err != nil {
		t.Fatal(err)
	}
	lease := runLeasePayload{RunID: "restore-run", RequestID: "restore-request", LeaseID: "restore-lease", WorkerID: "test", Attempt: 1, LeaseUntil: time.Now().Add(time.Minute).UTC()}
	if _, err := ledger.Append(ctx, created.ID, 4, runLeasedEventType, lease); err != nil {
		t.Fatal(err)
	}
	modification := codeModificationPayload{
		RunID: "restore-run", ToolCallID: "write-1", ToolName: "Write", Operation: "Write", Summary: "Write modified notes.txt", Path: "notes.txt",
		Before: "before", After: "after", BeforeSHA256: hashCodeState("before"), AfterSHA256: hashCodeState("after"), DiffSHA256: hashCodeTransition("before", "after"),
	}
	if _, err := ledger.Append(ctx, created.ID, 5, codeModifiedEventType, modification); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 6, runCompletedEventType, runTerminalPayload{RunID: "restore-run", RequestID: "restore-request", LeaseID: "restore-lease", Attempt: 1}); err != nil {
		t.Fatal(err)
	}

	var applied []CodeFileTransition
	restored, err := workbench.RestoreCodeChanges(ctx, 7, created.ID, checkpoint.Hash, "restore-run", 7, func(changes []CodeFileTransition) error {
		applied = append([]CodeFileTransition(nil), changes...)
		return nil
	})
	if err != nil {
		t.Fatalf("restore code changes: %v", err)
	}
	if len(applied) != 1 || applied[0].Path != "notes.txt" || applied[0].Before != "before" || applied[0].After != "after" {
		t.Fatalf("applied transitions = %+v", applied)
	}
	if restored.Type != workspaceRestoreCompletedEventType {
		t.Fatalf("restore receipt type = %q", restored.Type)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 9 || events[7].Type != workspaceRestoreIntentEventType || events[8].Type != workspaceRestoreCompletedEventType {
		t.Fatalf("restore ledger facts = %+v", events)
	}
}
