package codeagent_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/memory"
	"code-agent/internal/session"
)

func TestMemoryCompactionPreservesToolEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, operation                          string
		nested, approved, unknown, unknownResult bool
		mismatch, codeChange                     bool
	}{
		{name: "read", operation: "compact"},
		{name: "approved", operation: "compact", approved: true},
		{name: "nested", operation: "compact", nested: true},
		{name: "nested-approved", operation: "compact", nested: true, approved: true},
		{name: "unknown", operation: "compact", unknown: true},
		{name: "nested-unknown", operation: "compact", nested: true, unknown: true},
		{name: "mismatched-name", operation: "compact", mismatch: true},
		{name: "unknown-result-active", unknownResult: true},
		{name: "unknown-result-compacted", operation: "compact", unknownResult: true},
		{name: "unverified-append", operation: "append"},
		{name: "unverified-replace", operation: "replace"},
		{name: "code-change", operation: "compact", codeChange: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			created, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "repo", "compacted tool", "goal")
			if err != nil {
				t.Fatal(err)
			}
			id := created.ID
			oldUser := appendTrajectorySurfaceEvent(t, ctx, ledger, id, "user/message", map[string]any{"content": "hidden original message"})
			toolName := "Read"
			if tc.codeChange {
				toolName = "Write"
			}
			call := map[string]any{"run_id": "first", "tool_call_id": "call-1", "tool_name": toolName}
			oldCall := appendTrajectorySurfaceEvent(t, ctx, ledger, id, "tool/call", call)
			if tc.approved {
				appendTrajectoryEvent(t, ctx, ledger, id, "approval/pending", call)
				appendTrajectoryEvent(t, ctx, ledger, id, "approval/approved", call)
			}
			appendTrajectoryEvent(t, ctx, ledger, id, "tool/dispatched", call)
			if tc.unknown {
				appendTrajectoryEvent(t, ctx, ledger, id, "tool/unknown", call)
			}
			var change session.Event
			if tc.codeChange {
				appendTrajectoryEvent(t, ctx, ledger, id, "code/modified", map[string]any{
					"run_id": "first", "tool_call_id": "call-1", "tool_name": toolName,
					"path": "main.go", "operation": "modify", "summary": "retained verified code change",
					"before": "before", "after": "after", "before_sha256": sha256Hex("before"),
					"after_sha256": sha256Hex("after"), "diff_sha256": transitionSHA256("before", "after"),
				})
				events, err := ledger.Events(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				change = events[len(events)-1]
			}
			resultName := toolName
			if tc.mismatch {
				resultName = "Edit"
			}
			oldResult := appendTrajectorySurfaceEvent(t, ctx, ledger, id, "tool/result", map[string]any{
				"run_id": "first", "tool_call_id": "call-1", "tool_name": resultName,
				"output": "hidden original output", "exit_code": 0, "outcome_unknown": tc.unknownResult,
			})
			oldAnswer := appendTrajectorySurfaceEvent(t, ctx, ledger, id, "assistant/message", map[string]any{"content": "hidden original answer"})
			appendTrajectoryEvent(t, ctx, ledger, id, "session/run-completed", map[string]any{"run_id": "first"})
			hidden := []session.Event{oldUser, oldCall, oldResult, oldAnswer}
			unverified := tc.operation == "append" || tc.operation == "replace"
			if tc.operation != "" {
				operation := session.SurfaceOperation{Op: "append"}
				if tc.operation == "append" {
					operation = session.SurfaceOperation{Op: "replace", Start: oldUser.Seq, End: oldAnswer.Seq}
				}
				current := appendTrajectorySurfaceEventWithPayload(t, ctx, ledger, id, "user/message", map[string]any{"content": "continue with current goal"}, operation)
				refs := make([]map[string]string, 0, len(hidden))
				for _, event := range hidden {
					checksum := event.Checksum
					if unverified {
						checksum = strings.Repeat("f", 64)
					}
					refs = append(refs, map[string]string{"event_id": event.EventID, "checksum": checksum})
				}
				appendTrajectorySurfaceEventWithPayload(t, ctx, ledger, id, "context/compaction", map[string]any{
					"run_id": "second", "summary": "The earlier tool completed; retain current goal.",
					"input_event_id": current.EventID, "sources": refs, "reported_sources": refs,
				}, session.SurfaceOperation{Op: tc.operation, Start: oldUser.Seq, End: oldAnswer.Seq})
				appendTrajectorySurfaceEvent(t, ctx, ledger, id, "assistant/message", map[string]any{"content": "current answer"})
				appendTrajectoryEvent(t, ctx, ledger, id, "session/run-completed", map[string]any{"run_id": "second"})
			}
			if tc.nested {
				latest := appendTrajectorySurfaceEvent(t, ctx, ledger, id, "user/message", map[string]any{"content": "latest current goal"})
				surface, err := ledger.Surface(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				refs := make([]map[string]string, 0, len(surface)-1)
				for _, event := range surface[:len(surface)-1] {
					refs = append(refs, map[string]string{"event_id": event.EventID, "checksum": event.Checksum})
				}
				appendTrajectorySurfaceEventWithPayload(t, ctx, ledger, id, "context/compaction", map[string]any{
					"run_id": "third", "summary": "The two earlier turns were summarized.",
					"input_event_id": latest.EventID, "sources": refs, "reported_sources": refs,
				}, session.SurfaceOperation{Op: "compact"})
				appendTrajectorySurfaceEvent(t, ctx, ledger, id, "assistant/message", map[string]any{"content": "latest answer"})
				appendTrajectoryEvent(t, ctx, ledger, id, "session/run-completed", map[string]any{"run_id": "third"})
			}
			if _, err := session.ReadVerifiedSnapshot(ctx, ledger, id); err != nil {
				t.Fatalf("fixture must have valid native hashes and Surface: %v", err)
			}
			trajectory, err := memory.BuildSessionTrajectory(ctx, ledger, id, memory.TrajectoryOptions{})
			if tc.mismatch || unverified {
				if !errors.Is(err, memory.ErrTrajectoryIntegrity) {
					t.Fatalf("untrusted compacted tool identity or provenance accepted: %v", err)
				}
				if err := memory.NewLedgerMemory(ledger).Commit(ctx, id); !errors.Is(err, memory.ErrTrajectoryIntegrity) {
					t.Fatalf("untrusted evidence became Memory: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("completed compacted tool history must remain projectable: %v", err)
			}
			unknown := tc.unknown || tc.unknownResult
			if trajectory.Complete == unknown {
				t.Fatalf("completion=%v, unknown=%v: %+v", trajectory.Complete, unknown, trajectory)
			}
			if tc.operation != "" {
				if strings.Contains(trajectory.Overview, "hidden original") {
					t.Fatalf("hidden Surface text returned: %q", trajectory.Overview)
				}
				for _, source := range trajectory.Sources {
					for _, event := range hidden {
						if source.EventID == event.EventID {
							t.Fatal("hidden Surface event became a Memory source")
						}
					}
				}
			}
			if tc.codeChange {
				retained := false
				for _, source := range trajectory.Sources {
					if source.EventID == change.EventID && source.Checksum == change.Checksum && source.Type == "code/modified" {
						retained = true
					}
				}
				if !retained || !strings.Contains(trajectory.Overview, "retained verified code change") {
					t.Fatal("compaction discarded verified code-change evidence")
				}
			}
			err = memory.NewLedgerMemory(ledger).Commit(ctx, id)
			if unknown {
				if !errors.Is(err, memory.ErrIncompleteTrajectory) {
					t.Fatalf("unknown tool effects became Memory: %v", err)
				}
			} else if err != nil {
				t.Fatalf("completed trajectory cannot be committed: %v", err)
			}
		})
	}
}
