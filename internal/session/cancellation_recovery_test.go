package session

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCanceledRunRecoveryRequiresOwnerStopProofAndExactHead(t *testing.T) {
	for _, scenario := range []string{"nil-verifier", "wrong-owner", "wrong-actor", "wrong-cancel", "stale-head", "missing-tool-result", "unknown-tool-result", "pending-commit", "unresolved-approval", "independent-agent", "verifier-refused", "late-append", "stopped"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			ledger := openWorkbenchTestLedger(t)
			workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
			actor, err := testRunnerActor().BindSession(created.ID)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "original-request", "original-run", actor)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, payload); err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{RunID: "original-run", RequestID: "original-request", LeaseID: "old-lease", WorkerID: "old-worker", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC()}); err != nil {
				t.Fatal(err)
			}
			cancel := runCancelPayload{RunID: "original-run", RequestID: "original-cancel", Actor: actor, CleanupOwner: "original-owner"}
			if _, err := ledger.Append(ctx, created.ID, 6, runCancelRequestedEventType, cancel); err != nil {
				t.Fatal(err)
			}
			appendFixture := func(eventType string, payload any) {
				events, err := ledger.Events(ctx, created.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ledger.Append(ctx, created.ID, int64(len(events)), eventType, payload); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "missing-tool-result", "unknown-tool-result":
				call := toolCallPayload{RunID: "original-run", ToolCallID: "real-write", ToolName: "Write", ArgumentsJSON: `{}`}
				appendFixture("tool/call", call)
				appendFixture(toolDispatchedType, call)
				if scenario == "unknown-tool-result" {
					appendFixture("tool/result", toolResultPayload{RunID: call.RunID, ToolCallID: call.ToolCallID, ToolName: call.ToolName, OutcomeUnknown: true})
				}
			case "pending-commit":
				appendFixture(commitPendingEventType, commitPayload{CommitID: "original-commit", RunID: "original-run", ToolCallID: "pending-write", ToolName: "Write"})
			case "unresolved-approval":
				appendFixture(approvalPendingEventType, toolApprovalPayload{RunID: "original-run", ToolCallID: "pending-write", ToolName: "Write", ArgumentsJSON: `{}`, Decision: ApprovalPending})
			case "independent-agent":
				appendFixture("agent/task-linked", map[string]string{"task_id": "unconfirmed-child"})
			}
			prefix, err := ledger.Events(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			command := CanceledRunRecoveryCommand{CancelRunCommand: CancelRunCommand{RequestID: cancel.RequestID, SessionID: created.ID, RunID: cancel.RunID, OwnerID: created.UserID, Actor: actor}, ExpectedSeq: int64(len(prefix)), ExpectedHeadChecksum: prefix[len(prefix)-1].Checksum, ProofDigest: strings.Repeat("a", 64)}
			verify := func(context.Context) error { return nil }
			switch scenario {
			case "nil-verifier":
				verify = nil
			case "wrong-owner":
				command.OwnerID++
			case "wrong-actor":
				command.Actor.Subject = "another-subject"
			case "wrong-cancel":
				command.RequestID = "another-cancel"
			case "stale-head":
				command.ExpectedHeadChecksum = strings.Repeat("b", 64)
			case "verifier-refused":
				verify = func(context.Context) error { return ErrSessionStateConflict }
			case "late-append":
				verify = func(ctx context.Context) error {
					_, err := ledger.Append(ctx, created.ID, int64(len(prefix)), "recovery/test-note", map[string]string{"observation": "concurrent append"})
					return err
				}
			}
			view, err := workbench.RecoverCanceledRun(ctx, command, verify)
			after, readErr := ledger.Events(ctx, created.ID)
			if readErr != nil || !reflect.DeepEqual(prefix, after[:len(prefix)]) {
				t.Fatalf("original prefix changed: %v", readErr)
			}
			if scenario != "stopped" {
				if err == nil {
					t.Fatal("recovery accepted an unverified transition")
				}
				for _, event := range after {
					if event.Type == runCanceledEventType {
						t.Fatal("rejected recovery wrote a terminal")
					}
				}
				return
			}
			if err != nil || view.Status != RunCanceled || view.CancellationState != "stopped" || view.Attempt != 1 || len(after) != len(prefix)+1 {
				t.Fatalf("recovery = %+v %v", view, err)
			}
			if _, err := workbench.RecoverCanceledRun(ctx, command, verify); err != nil {
				t.Fatalf("idempotent recovery: %v", err)
			}
			again, err := ledger.Events(ctx, created.ID)
			if err != nil || len(again) != len(after) {
				t.Fatal("recovery duplicated its terminal")
			}
			if err := ledger.Verify(ctx, created.ID); err != nil {
				t.Fatalf("recovery broke the native chain: %v", err)
			}
		})
	}
}

func TestCanceledRunRecoveryResumesKnownCommitClosuresWithoutReplay(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench, created, checkpoint := pausedSessionWithCheckpoint(t, ledger)
	actor, err := testRunnerActor().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	continued, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "original-request", "original-run", actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, continuationEventType, continued); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 5, runLeasedEventType, runLeasePayload{RunID: "original-run", RequestID: "original-request", LeaseID: "old-lease", WorkerID: "old-worker", Attempt: 1, LeaseUntil: time.Now().Add(-time.Minute).UTC()}); err != nil {
		t.Fatal(err)
	}
	appendFact := func(eventType string, payload any, surface bool) {
		events, err := ledger.Events(ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if surface {
			_, err = ledger.AppendSurface(ctx, created.ID, int64(len(events)), eventType, payload, SurfaceOperation{Op: "append"})
		} else {
			_, err = ledger.Append(ctx, created.ID, int64(len(events)), eventType, payload)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Read", "Write"} {
		call := toolCallPayload{RunID: "original-run", ToolCallID: "known-" + name, ToolName: name, ArgumentsJSON: `{}`}
		appendFact("tool/call", call, true)
		appendFact(commitPendingEventType, commitPayload{CommitID: commitIDFor(call.RunID, call.ToolCallID), RunID: call.RunID, ToolCallID: call.ToolCallID, ToolName: call.ToolName, ArgumentsJSON: call.ArgumentsJSON}, false)
		appendFact(toolDispatchedType, call, false)
		appendFact("tool/result", toolResultPayload{RunID: call.RunID, ToolCallID: call.ToolCallID, ToolName: name, Output: "known " + name}, true)
	}
	appendFact(runCancelRequestedEventType, runCancelPayload{RunID: "original-run", RequestID: "original-cancel", Actor: actor, CleanupOwner: "original-owner"}, false)
	prefix, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending := pendingCommits(prefix, "original-run")
	if len(pending) != 2 || !knownRecoveryCommit(prefix, pending[0]) || !knownRecoveryCommit(prefix, pending[1]) {
		t.Fatal("fixture did not retain two known original commits")
	}
	bad := pending[0]
	bad.PendingChecksum = strings.Repeat("b", 64)
	if knownRecoveryCommit(prefix, bad) {
		t.Fatal("accepted a mismatched original pending checksum")
	}
	command := CanceledRunRecoveryCommand{CancelRunCommand: CancelRunCommand{RequestID: "original-cancel", SessionID: created.ID, RunID: "original-run", OwnerID: created.UserID, Actor: actor}, ExpectedSeq: int64(len(prefix)), ExpectedHeadChecksum: prefix[len(prefix)-1].Checksum, ProofDigest: strings.Repeat("a", 64)}
	proofCalls := 0
	_, err = workbench.RecoverCanceledRun(ctx, command, func(context.Context) error {
		proofCalls++
		if proofCalls == 2 {
			return ErrSessionStateConflict
		}
		return nil
	})
	if err == nil {
		t.Fatal("interrupted recovery pretended the Run was terminal")
	}
	partial, err := ledger.Events(ctx, created.ID)
	if err != nil || len(partial) != len(prefix)+1 || partial[len(prefix)].Type != commitDoneEventType || !reflect.DeepEqual(prefix, partial[:len(prefix)]) {
		t.Fatalf("partial closure was lost or changed its prefix: %v", err)
	}
	view, err := workbench.RecoverCanceledRun(ctx, command, func(context.Context) error { return nil })
	if err != nil || view.Status != RunCanceled || view.CancellationState != "stopped" || view.Attempt != 1 {
		t.Fatalf("resume=%+v %v", view, err)
	}
	after, err := ledger.Events(ctx, created.ID)
	if err != nil || len(after) != len(prefix)+3 || !reflect.DeepEqual(prefix, after[:len(prefix)]) || len(pendingCommits(after, "original-run")) != 0 {
		t.Fatalf("recovery lost or duplicated an original fact: %v", err)
	}
	if _, err := workbench.RecoverCanceledRun(ctx, command, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	again, err := ledger.Events(ctx, created.ID)
	if err != nil || len(again) != len(after) {
		t.Fatal("idempotent resume replayed tools/closures/terminal")
	}
}
