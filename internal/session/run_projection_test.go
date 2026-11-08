package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"code-agent/internal/identity"
)

func retryProjectionEvents(runCount, checkpointCount int) []Event {
	events := make([]Event, 0, runCount*3)
	appendEvent := func(eventType string, payload any) {
		body, _ := json.Marshal(payload)
		events = append(events, Event{SessionID: "session", Seq: int64(len(events)), Type: eventType, Payload: body, CreatedAt: time.Unix(int64(len(events)+1), 0).UTC()})
	}
	for i := 0; i < runCount; i++ {
		requestID, runID := fmt.Sprintf("request-%d", i), fmt.Sprintf("run-%d", i)
		appendEvent(continuationEventType, continuationPayload{
			RequestID: requestID, RunID: runID, CheckpointHash: fmt.Sprintf("checkpoint-%d", i%checkpointCount),
			TargetEventID: "target", TargetSeq: 0, TargetChecksum: "checksum", ResumeCount: 1,
			Actor: identity.Actor{SchemaVersion: 1, ActorID: fmt.Sprintf("actor-%d", i%3), Subject: "subject", TenantID: "tenant", SessionID: "session"},
		})
		leaseID := "lease-" + runID
		appendEvent(runLeasedEventType, runLeasePayload{RunID: runID, RequestID: requestID, LeaseID: leaseID, WorkerID: "worker", Attempt: 1, LeaseUntil: time.Unix(1, 0).UTC()})
		if i%3 == 0 {
			appendEvent(runFailedEventType, runTerminalPayload{RunID: runID, RequestID: requestID, LeaseID: leaseID, Attempt: 1, Error: "failed"})
		}
	}
	return events
}

func TestRunProjectionRetainsCheckpointAndActorScopedRetryLineage(t *testing.T) {
	actorA := identity.Actor{SchemaVersion: 1, ActorID: "actor-a", Subject: "a", TenantID: "tenant", SessionID: "session"}
	actorB := identity.Actor{SchemaVersion: 1, ActorID: "actor-b", Subject: "b", TenantID: "tenant", SessionID: "session"}
	var events []Event
	appendEvent := func(eventType string, payload any) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, Event{SessionID: "session", Seq: int64(len(events)), Type: eventType, Payload: body, CreatedAt: time.Unix(int64(len(events)+1), 0).UTC()})
	}
	appendRun := func(runID, checkpoint string, actor identity.Actor, failed bool) {
		t.Helper()
		requestID, leaseID := "request-"+runID, "lease-"+runID
		appendEvent(continuationEventType, continuationPayload{RequestID: requestID, RunID: runID, CheckpointHash: checkpoint,
			TargetEventID: "target", TargetSeq: 0, TargetChecksum: "checksum", ResumeCount: 1, Actor: actor})
		appendEvent(runLeasedEventType, runLeasePayload{RunID: runID, RequestID: requestID, LeaseID: leaseID, WorkerID: "worker", Attempt: 1, LeaseUntil: time.Unix(1, 0).UTC()})
		if failed {
			appendEvent(runFailedEventType, runTerminalPayload{RunID: runID, RequestID: requestID, LeaseID: leaseID, Attempt: 1, Error: "failed"})
		}
	}
	appendRun("a-first", "checkpoint-a", actorA, true)
	appendRun("b-first", "checkpoint-a", actorB, true)
	appendRun("a-other-checkpoint", "checkpoint-b", actorA, true)
	appendRun("a-second", "checkpoint-a", actorA, true)
	appendRun("a-current", "checkpoint-a", actorA, false)
	appendRun("b-current", "checkpoint-a", actorB, true)

	runs, err := projectRuns(events)
	if err != nil {
		t.Fatal(err)
	}
	for runID, want := range map[string][]string{
		"a-current": {"a-first", "a-second"},
		"b-current": {"b-first"},
		"b-first":   nil,
	} {
		got := runs[runID].view.RetryOfRunIDs
		if len(got) != len(want) {
			t.Fatalf("%s lineage = %v, want %v", runID, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s lineage = %v, want %v", runID, got, want)
			}
		}
		wantFirst := ""
		if len(want) > 0 {
			wantFirst = want[0]
		}
		if runs[runID].view.RetryOfRunID != wantFirst {
			t.Fatalf("%s first retry = %q, want %q", runID, runs[runID].view.RetryOfRunID, wantFirst)
		}
	}
}

func TestRunProjectionStillRejectsInvalidEventStream(t *testing.T) {
	events := retryProjectionEvents(1, 1)
	events = append(events, Event{SessionID: "session", Seq: int64(len(events)), Type: runLeasedEventType, Payload: json.RawMessage(`{"run_id":"unrelated"}`)})
	_, err := projectRun(events, "run-0")
	if !errors.Is(err, ErrEventIntegrity) {
		t.Fatalf("invalid event stream error = %v, want ErrEventIntegrity", err)
	}
}

func TestRunProjectionRetainsLateFailureInEarlierRunLineage(t *testing.T) {
	events := retryProjectionEvents(4, 1)
	lateFailure := events[2]
	events = append(events[:2:2], events[3:]...)
	events = append(events, lateFailure)
	for i := range events {
		events[i].Seq = int64(i)
	}
	run, err := projectRun(events, "run-3")
	if err != nil {
		t.Fatal(err)
	}
	if run.view.RetryOfRunID != "run-0" || len(run.view.RetryOfRunIDs) != 1 || run.view.RetryOfRunIDs[0] != "run-0" {
		t.Fatalf("late failure lost its creation lineage: %+v", run.view)
	}
}

func BenchmarkRunProjectionIndependentRetryGroups(b *testing.B) {
	events := retryProjectionEvents(600, 120)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := projectRuns(events); err != nil {
			b.Fatal(err)
		}
	}
}
