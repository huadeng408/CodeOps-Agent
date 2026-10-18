package session

import (
	"context"
	"errors"
	"testing"
	"time"
)

type runtimeStopContentionLedger struct {
	EventLog
	conflicts int
}

func (ledger *runtimeStopContentionLedger) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	if kind == toolRuntimeStoppedEventType && ledger.conflicts > 0 {
		ledger.conflicts--
		if _, err := ledger.EventLog.Append(ctx, id, seq, "fixture/concurrent-write", map[string]bool{"concurrent": true}); err != nil {
			return Event{}, err
		}
		return Event{}, ErrSequenceConflict
	}
	return ledger.EventLog.Append(ctx, id, seq, kind, payload)
}

func TestRuntimeStopConfirmationKeepsIdentityTerminalAndCASFences(t *testing.T) {
	for _, scenario := range []string{"running", "foreign actor", "premature proof", "missing dispatch", "wrong dispatch", "wrong dispatch proof", "CAS retry", "CAS exhausted"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			ledger := &runtimeStopContentionLedger{EventLog: openWorkbenchTestLedger(t)}
			workbench := NewWorkbench(ledger, nil)
			created, err := workbench.Create(ctx, 7, "repo", "stop proof", "")
			if err != nil {
				t.Fatal(err)
			}
			actor, err := testRunnerActor().BindSession(created.ID)
			if err != nil {
				t.Fatal(err)
			}
			message, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "run tests")
			if err != nil {
				t.Fatal(err)
			}
			checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 2, message.ID, "tests")
			if err != nil {
				t.Fatal(err)
			}
			continued, err := continuationRequestPayloadForTest(ctx, workbench, created.ID, checkpoint.Hash, "request-stop", "run-stop", actor)
			if err != nil {
				t.Fatal(err)
			}
			seq := int64(3)
			appendFact := func(kind string, payload any) Event {
				t.Helper()
				event, err := ledger.Append(ctx, created.ID, seq, kind, payload)
				if err != nil {
					t.Fatal(err)
				}
				seq++
				return event
			}
			appendFact(continuationEventType, continued)
			appendFact(runLeasedEventType, runLeasePayload{RunID: "run-stop", RequestID: "request-stop", LeaseID: "lease", WorkerID: "worker", Attempt: 1, LeaseUntil: time.Now().Add(time.Minute)})
			dispatchName := "Bash"
			if scenario == "wrong dispatch" || scenario == "wrong dispatch proof" {
				dispatchName = "Write"
			}
			if scenario != "missing dispatch" {
				appendFact(toolDispatchedType, toolCallPayload{RunID: "run-stop", ToolCallID: "shell", ToolName: dispatchName})
			}
			result := appendFact("tool/result", toolResultPayload{RunID: "run-stop", ToolCallID: "shell", ToolName: "Bash", RuntimeID: "retained-runtime", OutcomeUnknown: true, ExitCode: 126})
			if scenario == "premature proof" {
				appendFact(toolRuntimeStoppedEventType, runtimeStopsPayload{SchemaVersion: 1, Results: []runtimeStopReference{{EventID: result.EventID, Checksum: result.Checksum, RuntimeID: "retained-runtime"}}})
			}
			if scenario != "running" {
				appendFact(runFailedEventType, runTerminalPayload{RunID: "run-stop", RequestID: "request-stop", LeaseID: "lease", Attempt: 1, Error: "unknown execution"})
			}
			if scenario == "wrong dispatch proof" {
				appendFact(toolRuntimeStoppedEventType, runtimeStopsPayload{SchemaVersion: 1, Results: []runtimeStopReference{{EventID: result.EventID, Checksum: result.Checksum, RuntimeID: "retained-runtime"}}})
				if err := ModelTaskIdle(ctx, ledger, actor); !errors.Is(err, ErrEventIntegrity) {
					t.Fatalf("replayed proof accepted another tool dispatch: %v", err)
				}
			}
			if scenario == "foreign actor" {
				actor.Subject = "another-subject"
			}
			if scenario == "CAS retry" {
				ledger.conflicts = 1
			}
			if scenario == "CAS exhausted" {
				ledger.conflicts = 8
			}
			checks := 0
			err = ConfirmTaskRuntimeStops(ctx, ledger, actor, func(_ context.Context, id string) error {
				if id != "retained-runtime" {
					t.Fatal("stop observer received a foreign identity")
				}
				checks++
				return nil
			})
			if scenario == "CAS retry" {
				if err != nil || checks != 2 || ModelTaskIdle(ctx, ledger, actor) != nil {
					t.Fatalf("bounded contention prevented a verified stop: checks=%d err=%v", checks, err)
				}
			} else if scenario == "CAS exhausted" {
				if !errors.Is(err, ErrSequenceConflict) || checks != 8 {
					t.Fatalf("CAS exhaustion was hidden: checks=%d err=%v", checks, err)
				}
			} else if err == nil || checks != 0 {
				t.Fatalf("unbound or active source was observed as stopped: checks=%d err=%v", checks, err)
			}
			snapshot, err := ReadVerifiedSnapshot(ctx, ledger, created.ID)
			if err != nil || snapshot.Events[result.Seq].Checksum != result.Checksum {
				t.Fatal("stop recovery rewrote the original unknown result")
			}
		})
	}
}
