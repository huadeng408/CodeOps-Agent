package codeagent_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/memory"
	"code-agent/internal/session"
)

func TestBuildSessionTrajectoryBuildsStableOverviewAndPromotesCompletedRun(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer ledger.Close()

	appendTrajectoryEvent(t, ctx, ledger, "trajectory-1", "user/message", map[string]any{
		"content": "Implement a durable session memory trajectory",
	})
	appendTrajectoryEvent(t, ctx, ledger, "trajectory-1", "tool/call", map[string]any{
		"run_id": "run-1", "tool_call_id": "call-1", "tool_name": "Read",
		"arguments_json": `{"path":"internal/memory/manager.go"}`,
	})
	appendTrajectoryEvent(t, ctx, ledger, "trajectory-1", "tool/result", map[string]any{
		"run_id": "run-1", "tool_call_id": "call-1", "tool_name": "Read",
		"output": "repository evidence", "exit_code": 0,
	})
	appendTrajectoryEvent(t, ctx, ledger, "trajectory-1", "tool/call", map[string]any{
		"run_id": "run-1", "tool_call_id": "call-2", "tool_name": "Edit",
	})
	appendTrajectoryEvent(t, ctx, ledger, "trajectory-1", "code/modified", map[string]any{
		"run_id": "run-1", "tool_call_id": "call-2", "tool_name": "Edit",
		"path": "internal/memory/trajectory.go", "operation": "Edit",
		"summary":       "Edit modified internal/memory/trajectory.go",
		"before_sha256": strings.Repeat("a", 64),
		"after_sha256":  strings.Repeat("b", 64),
		"diff_sha256":   strings.Repeat("c", 64),
	})
	appendTrajectoryEvent(t, ctx, ledger, "trajectory-1", "tool/result", map[string]any{
		"run_id": "run-1", "tool_call_id": "call-2", "tool_name": "Edit", "exit_code": 0,
	})
	appendTrajectoryEvent(t, ctx, ledger, "trajectory-1", "assistant/message", map[string]any{
		"content": "The trajectory is ready for verification",
	})
	appendTrajectoryEvent(t, ctx, ledger, "trajectory-1", "session/run-completed", map[string]any{
		"run_id": "run-1", "request_id": "request-1", "lease_id": "lease-1", "attempt": 1,
	})

	first, err := memory.BuildSessionTrajectory(ctx, ledger, "trajectory-1", memory.TrajectoryOptions{})
	if err != nil {
		t.Fatalf("build trajectory: %v", err)
	}
	second, err := memory.BuildSessionTrajectory(ctx, ledger, "trajectory-1", memory.TrajectoryOptions{})
	if err != nil {
		t.Fatalf("rebuild trajectory: %v", err)
	}
	if !first.Complete || first.Outcome != "completed" {
		t.Fatalf("trajectory completion = %+v", first)
	}
	if first.SourceChecksum == "" || first.SourceChecksum != second.SourceChecksum || first.Overview != second.Overview {
		t.Fatalf("trajectory is not deterministic: first=%+v second=%+v", first, second)
	}
	if len(first.Sources) != 8 || first.FirstSeq != 0 || first.LastSeq != 7 {
		t.Fatalf("unexpected trajectory sources: %+v", first)
	}
	if !strings.Contains(first.Overview, "durable session memory trajectory") || !strings.Contains(first.Overview, "code/modified") {
		t.Fatalf("overview lost useful evidence: %q", first.Overview)
	}

	manager := memory.NewManager(t.TempDir())
	trajectory, saved, err := manager.SaveSessionTrajectory(ctx, ledger, "trajectory-1", memory.TrajectoryOptions{})
	if err != nil {
		t.Fatalf("save trajectory: %v", err)
	}
	if saved == nil || !trajectory.Complete {
		t.Fatalf("save result = trajectory=%+v memory=%+v", trajectory, saved)
	}
	if saved.Kind != "trajectories" || saved.Detail != "overview" || saved.SessionID != "trajectory-1" {
		t.Fatalf("saved memory metadata = %+v", saved)
	}
	if saved.SourceChecksum != first.SourceChecksum || saved.SourceURI == "" {
		t.Fatalf("saved provenance = %+v", saved)
	}
	_, repeated, err := manager.SaveSessionTrajectory(ctx, ledger, "trajectory-1", memory.TrajectoryOptions{})
	if err != nil {
		t.Fatalf("repeat save trajectory: %v", err)
	}
	if repeated == nil || repeated.ID != saved.ID || len(manager.List()) != 1 {
		t.Fatalf("repeat save was not idempotent: repeated=%+v memories=%+v", repeated, manager.List())
	}
}

func TestSaveSessionTrajectoryReturnsIncompleteWithoutWritingMemory(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer ledger.Close()
	appendTrajectoryEvent(t, ctx, ledger, "incomplete", "user/message", map[string]any{"content": "keep working"})

	manager := memory.NewManager(t.TempDir())
	trajectory, saved, err := manager.SaveSessionTrajectory(ctx, ledger, "incomplete", memory.TrajectoryOptions{})
	if err != nil {
		t.Fatalf("incomplete trajectory should be inspectable: %v", err)
	}
	if trajectory.Complete || trajectory.Outcome != "incomplete" || saved != nil {
		t.Fatalf("incomplete trajectory result = trajectory=%+v memory=%+v", trajectory, saved)
	}
	if len(manager.List()) != 0 {
		t.Fatalf("incomplete trajectory wrote memory: %+v", manager.List())
	}
}

func TestBuildSessionTrajectoryDoesNotPromoteOpenToolCall(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer ledger.Close()
	appendTrajectoryEvent(t, ctx, ledger, "open-tool", "user/message", map[string]any{"content": "run a tool"})
	appendTrajectoryEvent(t, ctx, ledger, "open-tool", "tool/call", map[string]any{
		"run_id": "run-1", "tool_call_id": "call-1", "tool_name": "Write",
	})
	appendTrajectoryEvent(t, ctx, ledger, "open-tool", "session/run-completed", map[string]any{
		"run_id": "run-1", "request_id": "request-1", "lease_id": "lease-1", "attempt": 1,
	})

	trajectory, err := memory.BuildSessionTrajectory(ctx, ledger, "open-tool", memory.TrajectoryOptions{})
	if err != nil {
		t.Fatalf("build open-tool trajectory: %v", err)
	}
	if trajectory.Complete || trajectory.Outcome != "incomplete" {
		t.Fatalf("open tool was promoted: %+v", trajectory)
	}
}

func TestBuildSessionTrajectoryKeepsLegacyOpenCallFailClosed(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer ledger.Close()
	appendTrajectoryEvent(t, ctx, ledger, "legacy-open-tool", "user/message", map[string]any{"content": "legacy adapter"})
	appendTrajectoryEvent(t, ctx, ledger, "legacy-open-tool", "tool/call", map[string]any{
		"run_id": "run-failed", "tool_call_id": "call-1", "tool_name": "Write",
	})
	appendTrajectoryEvent(t, ctx, ledger, "legacy-open-tool", "session/run-failed", map[string]any{"run_id": "run-failed"})
	appendTrajectoryEvent(t, ctx, ledger, "legacy-open-tool", "user/message", map[string]any{"content": "continue"})
	appendTrajectoryEvent(t, ctx, ledger, "legacy-open-tool", "session/run-completed", map[string]any{"run_id": "run-complete"})

	trajectory, err := memory.BuildSessionTrajectory(ctx, ledger, "legacy-open-tool", memory.TrajectoryOptions{})
	if err != nil {
		t.Fatalf("build trajectory: %v", err)
	}
	if trajectory.Complete || trajectory.Outcome != "incomplete" {
		t.Fatalf("legacy open call was treated as safely abandoned: %+v", trajectory)
	}
}

func TestBuildSessionTrajectoryDropsUnapprovedCallAfterFailedRun(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer ledger.Close()

	appendTrajectoryEvent(t, ctx, ledger, "abandoned-call", "user/message", map[string]any{"content": "first attempt"})
	appendTrajectoryEvent(t, ctx, ledger, "abandoned-call", "tool/call", map[string]any{
		"run_id": "run-failed", "tool_call_id": "call-unapproved", "tool_name": "Write",
	})
	appendTrajectoryEvent(t, ctx, ledger, "abandoned-call", "approval/pending", map[string]any{
		"run_id": "run-failed", "tool_call_id": "call-unapproved", "tool_name": "Write", "decision": "pending",
	})
	appendTrajectoryEvent(t, ctx, ledger, "abandoned-call", "session/run-failed", map[string]any{"run_id": "run-failed"})
	appendTrajectoryEvent(t, ctx, ledger, "abandoned-call", "user/message", map[string]any{"content": "retry safely"})
	appendTrajectoryEvent(t, ctx, ledger, "abandoned-call", "tool/call", map[string]any{
		"run_id": "run-complete", "tool_call_id": "call-read", "tool_name": "Read",
	})
	appendTrajectoryEvent(t, ctx, ledger, "abandoned-call", "tool/result", map[string]any{
		"run_id": "run-complete", "tool_call_id": "call-read", "tool_name": "Read", "exit_code": 0,
	})
	appendTrajectoryEvent(t, ctx, ledger, "abandoned-call", "session/run-completed", map[string]any{"run_id": "run-complete"})

	trajectory, err := memory.BuildSessionTrajectory(ctx, ledger, "abandoned-call", memory.TrajectoryOptions{})
	if err != nil {
		t.Fatalf("build trajectory: %v", err)
	}
	if !trajectory.Complete || trajectory.Outcome != "completed" {
		t.Fatalf("unapproved call from failed run blocked later completion: %+v", trajectory)
	}
}

func TestBuildSessionTrajectoryKeepsApprovedOrDispatchedCallFailClosed(t *testing.T) {
	for _, marker := range []string{"approval/approved", "tool/dispatched"} {
		t.Run(marker, func(t *testing.T) {
			ctx := context.Background()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
			if err != nil {
				t.Fatalf("open ledger: %v", err)
			}
			defer ledger.Close()

			appendTrajectoryEvent(t, ctx, ledger, "uncertain-call", "user/message", map[string]any{"content": "first attempt"})
			appendTrajectoryEvent(t, ctx, ledger, "uncertain-call", "tool/call", map[string]any{
				"run_id": "run-failed", "tool_call_id": "call-uncertain", "tool_name": "Write",
			})
			if marker == "approval/approved" {
				appendTrajectoryEvent(t, ctx, ledger, "uncertain-call", "approval/approved", map[string]any{
					"run_id": "run-failed", "tool_call_id": "call-uncertain", "tool_name": "Write", "decision": "approved",
				})
			} else {
				appendTrajectoryEvent(t, ctx, ledger, "uncertain-call", "tool/dispatched", map[string]any{
					"run_id": "run-failed", "tool_call_id": "call-uncertain", "tool_name": "Write",
				})
			}
			appendTrajectoryEvent(t, ctx, ledger, "uncertain-call", "session/run-failed", map[string]any{"run_id": "run-failed"})
			appendTrajectoryEvent(t, ctx, ledger, "uncertain-call", "user/message", map[string]any{"content": "retry safely"})
			appendTrajectoryEvent(t, ctx, ledger, "uncertain-call", "session/run-completed", map[string]any{"run_id": "run-complete"})

			trajectory, err := memory.BuildSessionTrajectory(ctx, ledger, "uncertain-call", memory.TrajectoryOptions{})
			if err != nil {
				t.Fatalf("build trajectory: %v", err)
			}
			if trajectory.Complete || trajectory.Outcome != "incomplete" {
				t.Fatalf("uncertain side effect was promoted: %+v", trajectory)
			}
		})
	}
}

func TestBuildSessionTrajectoryOmitsSensitiveOutput(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer ledger.Close()
	appendTrajectoryEvent(t, ctx, ledger, "secret-output", "user/message", map[string]any{"content": "inspect the service"})
	appendTrajectoryEvent(t, ctx, ledger, "secret-output", "tool/call", map[string]any{
		"run_id": "run-1", "tool_call_id": "call-1", "tool_name": "Read",
	})
	appendTrajectoryEvent(t, ctx, ledger, "secret-output", "tool/result", map[string]any{
		"run_id": "run-1", "tool_call_id": "call-1", "tool_name": "Read",
		"output": "password=super-secret-value", "exit_code": 0,
	})
	appendTrajectoryEvent(t, ctx, ledger, "secret-output", "session/run-failed", map[string]any{
		"run_id": "run-1", "request_id": "request-1", "lease_id": "lease-1", "attempt": 1,
		"error": "failed after password=super-secret-value",
	})

	trajectory, err := memory.BuildSessionTrajectory(ctx, ledger, "secret-output", memory.TrajectoryOptions{})
	if err != nil {
		t.Fatalf("build secret-output trajectory: %v", err)
	}
	if !trajectory.Complete || trajectory.Outcome != "failed" {
		t.Fatalf("failed terminal trajectory = %+v", trajectory)
	}
	if strings.Contains(trajectory.Overview, "super-secret-value") || strings.Contains(trajectory.Overview, "password=") {
		t.Fatalf("sensitive output leaked into overview: %q", trajectory.Overview)
	}
}

func TestTrajectoryRedactsCredentialsInMessageFormats(t *testing.T) {
	for _, content := range []string{
		`{"api_key": "fixture-private-marker"}`,
		`'password': 'fixture-private-marker'`,
		`password=fixture-private-marker`,
		"-----BEGIN PRIVATE KEY-----\nfixture-private-marker\n-----END PRIVATE KEY-----",
	} {
		t.Run(fmt.Sprintf("format-%x", sha256.Sum256([]byte(content))), func(t *testing.T) {
			ctx := context.Background()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			appendTrajectoryEvent(t, ctx, ledger, "redacted", "user/message", map[string]any{"content": content})
			appendTrajectoryEvent(t, ctx, ledger, "redacted", "session/run-completed", map[string]any{"run_id": "run"})
			trajectory, err := memory.BuildSessionTrajectory(ctx, ledger, "redacted", memory.TrajectoryOptions{})
			if err != nil || !trajectory.Complete || strings.Contains(trajectory.Overview, "fixture-private-marker") || !strings.Contains(trajectory.Overview, "[redacted]") {
				t.Fatal("credential-like message text was not redacted")
			}
		})
	}
}

func TestTrajectoryRejectsOrphanCodeModificationReceipt(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	appendTrajectoryEvent(t, ctx, ledger, "orphan-diff", "user/message", map[string]any{"content": "edit a file"})
	appendTrajectoryEvent(t, ctx, ledger, "orphan-diff", "code/modified", map[string]any{
		"run_id": "run", "tool_call_id": "unpaired", "tool_name": "Write", "path": "file.txt",
		"before_sha256": sha256Hex(""), "after_sha256": sha256Hex("after"), "diff_sha256": transitionSHA256("", "after"),
	})
	appendTrajectoryEvent(t, ctx, ledger, "orphan-diff", "session/run-completed", map[string]any{"run_id": "run"})
	if _, err := memory.BuildSessionTrajectory(ctx, ledger, "orphan-diff", memory.TrajectoryOptions{}); !errors.Is(err, memory.ErrTrajectoryIntegrity) {
		t.Fatalf("orphan code modification receipt accepted: %v", err)
	}
}

func TestBuildSessionTrajectoryUsesCurrentSurfaceAfterCompaction(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer ledger.Close()

	oldUser := appendTrajectorySurfaceEvent(t, ctx, ledger, "compacted", "user/message", map[string]any{
		"content": "old private context that must not return",
	})
	oldAssistant := appendTrajectorySurfaceEvent(t, ctx, ledger, "compacted", "assistant/message", map[string]any{
		"content": "old assistant answer that was compacted",
	})
	current := appendTrajectorySurfaceEvent(t, ctx, ledger, "compacted", "user/message", map[string]any{
		"content": "continue from the compacted summary",
	})
	compactionPayload := map[string]any{
		"run_id":         "run-1",
		"summary":        "The earlier context was safely summarized.",
		"input_event_id": current.EventID,
		"sources": []map[string]string{
			{"event_id": oldUser.EventID, "checksum": oldUser.Checksum},
			{"event_id": oldAssistant.EventID, "checksum": oldAssistant.Checksum},
		},
		"reported_sources": []map[string]string{
			{"event_id": oldUser.EventID, "checksum": oldUser.Checksum},
			{"event_id": oldAssistant.EventID, "checksum": oldAssistant.Checksum},
		},
	}
	appendTrajectorySurfaceEventWithPayload(t, ctx, ledger, "compacted", "context/compaction", compactionPayload, session.SurfaceOperation{Op: "compact"})
	appendTrajectorySurfaceEvent(t, ctx, ledger, "compacted", "assistant/message", map[string]any{
		"content": "the current branch is complete",
	})
	appendTrajectoryEvent(t, ctx, ledger, "compacted", "session/run-completed", map[string]any{"run_id": "run-1"})

	trajectory, err := memory.BuildSessionTrajectory(ctx, ledger, "compacted", memory.TrajectoryOptions{})
	if err != nil {
		t.Fatalf("build compacted trajectory: %v", err)
	}
	if !trajectory.Complete {
		t.Fatalf("compacted trajectory was not complete: %+v", trajectory)
	}
	if strings.Contains(trajectory.Overview, "old private context") || strings.Contains(trajectory.Overview, "old assistant answer") {
		t.Fatalf("hidden surface events leaked into overview: %q", trajectory.Overview)
	}
	if !strings.Contains(trajectory.Overview, "earlier context was safely summarized") || !strings.Contains(trajectory.Overview, "current branch is complete") {
		t.Fatalf("current surface evidence missing: %q", trajectory.Overview)
	}
}

func TestBuildSessionTrajectoryRejectsDuplicateRunTerminal(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer ledger.Close()
	appendTrajectoryEvent(t, ctx, ledger, "duplicate-terminal", "user/message", map[string]any{"content": "finish once"})
	appendTrajectoryEvent(t, ctx, ledger, "duplicate-terminal", "session/run-completed", map[string]any{"run_id": "run-1"})
	appendTrajectoryEvent(t, ctx, ledger, "duplicate-terminal", "session/run-completed", map[string]any{"run_id": "run-1"})

	if _, err := memory.BuildSessionTrajectory(ctx, ledger, "duplicate-terminal", memory.TrajectoryOptions{}); !errors.Is(err, memory.ErrTrajectoryIntegrity) {
		t.Fatalf("duplicate terminal error = %v, want ErrTrajectoryIntegrity", err)
	}
}

func TestBuildSessionTrajectoryRejectsMismatchedCodeReceiptBodies(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer ledger.Close()
	appendTrajectoryEvent(t, ctx, ledger, "bad-receipt", "user/message", map[string]any{"content": "edit the file"})
	appendTrajectoryEvent(t, ctx, ledger, "bad-receipt", "tool/call", map[string]any{
		"run_id": "run-1", "tool_call_id": "call-1", "tool_name": "Edit",
	})
	appendTrajectoryEvent(t, ctx, ledger, "bad-receipt", "code/modified", map[string]any{
		"run_id": "run-1", "tool_call_id": "call-1", "tool_name": "Edit",
		"path": "internal/memory/trajectory.go", "operation": "Edit", "summary": "edit",
		"before": "before", "after": "after",
		"before_sha256": strings.Repeat("a", 64),
		"after_sha256":  strings.Repeat("b", 64),
		"diff_sha256":   strings.Repeat("c", 64),
	})
	appendTrajectoryEvent(t, ctx, ledger, "bad-receipt", "session/run-completed", map[string]any{"run_id": "run-1"})

	if _, err := memory.BuildSessionTrajectory(ctx, ledger, "bad-receipt", memory.TrajectoryOptions{}); !errors.Is(err, memory.ErrTrajectoryIntegrity) {
		t.Fatalf("mismatched receipt error = %v, want ErrTrajectoryIntegrity", err)
	}
}

func TestBuildSessionTrajectoryAcceptsEmptyCodeReceiptSide(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer ledger.Close()
	appendTrajectoryEvent(t, ctx, ledger, "empty-receipt-side", "user/message", map[string]any{"content": "create the file"})
	appendTrajectoryEvent(t, ctx, ledger, "empty-receipt-side", "tool/call", map[string]any{
		"run_id": "run-1", "tool_call_id": "call-1", "tool_name": "Write",
	})
	after := "new file contents"
	appendTrajectoryEvent(t, ctx, ledger, "empty-receipt-side", "code/modified", map[string]any{
		"run_id": "run-1", "tool_call_id": "call-1", "tool_name": "Write",
		"path": "new.txt", "operation": "Write", "summary": "create new.txt",
		"after":         after,
		"before_sha256": sha256Hex(""),
		"after_sha256":  sha256Hex(after),
		"diff_sha256":   transitionSHA256("", after),
	})
	appendTrajectoryEvent(t, ctx, ledger, "empty-receipt-side", "tool/result", map[string]any{
		"run_id": "run-1", "tool_call_id": "call-1", "tool_name": "Write", "exit_code": 0,
	})
	appendTrajectoryEvent(t, ctx, ledger, "empty-receipt-side", "session/run-completed", map[string]any{"run_id": "run-1"})

	trajectory, err := memory.BuildSessionTrajectory(ctx, ledger, "empty-receipt-side", memory.TrajectoryOptions{})
	if err != nil || !trajectory.Complete {
		t.Fatalf("empty receipt side should be accepted: trajectory=%+v err=%v", trajectory, err)
	}
}

func TestSaveSessionTrajectoryRollsBackWhenAuditSinkFails(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer ledger.Close()
	appendTrajectoryEvent(t, ctx, ledger, "audit-failure", "user/message", map[string]any{"content": "finish"})
	appendTrajectoryEvent(t, ctx, ledger, "audit-failure", "session/run-completed", map[string]any{"run_id": "run-1"})

	manager := memory.NewManagerWithAudit(t.TempDir(), func(memory.MemoryEvent) error {
		return errors.New("audit unavailable")
	})
	_, saved, err := manager.SaveSessionTrajectory(ctx, ledger, "audit-failure", memory.TrajectoryOptions{})
	if err == nil || saved != nil {
		t.Fatalf("audit failure result = saved=%+v err=%v", saved, err)
	}
	if len(manager.List()) != 0 {
		t.Fatalf("audit failure left derived memory: %+v", manager.List())
	}
}

func TestTrajectoryRejectsMismatchedToolName(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	appendTrajectoryEvent(t, ctx, ledger, "mismatch", "tool/call", map[string]any{"run_id": "run", "tool_call_id": "call", "tool_name": "Write"})
	appendTrajectoryEvent(t, ctx, ledger, "mismatch", "tool/result", map[string]any{"run_id": "run", "tool_call_id": "call", "tool_name": "Read", "exit_code": 0})
	if _, err := memory.BuildSessionTrajectory(ctx, ledger, "mismatch", memory.TrajectoryOptions{}); !errors.Is(err, memory.ErrTrajectoryIntegrity) {
		t.Fatalf("mismatched tool name accepted: %v", err)
	}
}

func TestTrajectoryNewUserOrLeaseMakesOldTerminalIncomplete(t *testing.T) {
	for _, eventType := range []string{"user/message", "session/run-leased"} {
		t.Run(eventType, func(t *testing.T) {
			ctx := context.Background()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			appendTrajectoryEvent(t, ctx, ledger, "new-turn", "user/message", map[string]any{"content": "finish"})
			appendTrajectoryEvent(t, ctx, ledger, "new-turn", "session/run-completed", map[string]any{"run_id": "old"})
			appendTrajectoryEvent(t, ctx, ledger, "new-turn", eventType, map[string]any{"run_id": "new", "content": "still pending"})
			trajectory, err := memory.BuildSessionTrajectory(ctx, ledger, "new-turn", memory.TrajectoryOptions{})
			if err != nil || trajectory.Complete {
				t.Fatalf("old terminal promoted a new turn: %+v, %v", trajectory, err)
			}
		})
	}
}

func TestTrajectoryRewindDropsAbandonedUnknownAndTerminal(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	anchor := appendTrajectorySurfaceEvent(t, ctx, ledger, "rewound", "user/message", map[string]any{"content": "current task"})
	appendTrajectoryEvent(t, ctx, ledger, "rewound", "tool/unknown", map[string]any{"run_id": "old"})
	appendTrajectoryEvent(t, ctx, ledger, "rewound", "session/run-failed", map[string]any{"run_id": "old"})
	if _, err := ledger.Rewind(ctx, "rewound", anchor.Seq); err != nil {
		t.Fatal(err)
	}
	appendTrajectorySurfaceEvent(t, ctx, ledger, "rewound", "assistant/message", map[string]any{"content": "current answer"})
	appendTrajectoryEvent(t, ctx, ledger, "rewound", "session/run-completed", map[string]any{"run_id": "new"})
	trajectory, err := memory.BuildSessionTrajectory(ctx, ledger, "rewound", memory.TrajectoryOptions{})
	if err != nil || !trajectory.Complete || strings.Contains(trajectory.Overview, "unknown") || strings.Contains(trajectory.Overview, "failed") {
		t.Fatalf("abandoned log-only facts resurfaced: %+v, %v", trajectory, err)
	}
}

func TestTrajectoryOverviewRetainsLatestOutcomeUnderBudget(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	appendTrajectoryEvent(t, ctx, ledger, "budget", "user/message", map[string]any{"content": "initial task " + strings.Repeat("earlier detail ", 100)})
	appendTrajectoryEvent(t, ctx, ledger, "budget", "assistant/message", map[string]any{"content": "latest outcome anchor"})
	appendTrajectoryEvent(t, ctx, ledger, "budget", "session/run-completed", map[string]any{"run_id": "run"})
	trajectory, err := memory.BuildSessionTrajectory(ctx, ledger, "budget", memory.TrajectoryOptions{MaxOverviewChars: 256})
	if err != nil || len([]rune(trajectory.Overview)) > 256 || !strings.Contains(trajectory.Overview, "initial task") || !strings.Contains(trajectory.Overview, "latest outcome anchor") {
		t.Fatalf("overview lost latest evidence: %+v, %v", trajectory, err)
	}
}

func appendTrajectoryEvent(t *testing.T, ctx context.Context, ledger session.EventLog, sessionID, eventType string, payload any) {
	t.Helper()
	events, err := ledger.Events(ctx, sessionID)
	if err != nil {
		t.Fatalf("read events before %s: %v", eventType, err)
	}
	if _, err := ledger.Append(ctx, sessionID, int64(len(events)), eventType, payload); err != nil {
		t.Fatalf("append %s: %v", eventType, err)
	}
}

func appendTrajectorySurfaceEvent(t *testing.T, ctx context.Context, ledger session.EventLog, sessionID, eventType string, payload any) session.Event {
	return appendTrajectorySurfaceEventWithPayload(t, ctx, ledger, sessionID, eventType, payload, session.SurfaceOperation{Op: "append"})
}

func appendTrajectorySurfaceEventWithPayload(t *testing.T, ctx context.Context, ledger session.EventLog, sessionID, eventType string, payload any, operation session.SurfaceOperation) session.Event {
	t.Helper()
	events, err := ledger.Events(ctx, sessionID)
	if err != nil {
		t.Fatalf("read events before surface %s: %v", eventType, err)
	}
	event, err := ledger.AppendSurface(ctx, sessionID, int64(len(events)), eventType, payload, operation)
	if err != nil {
		t.Fatalf("append surface %s: %v", eventType, err)
	}
	return event
}

func sha256Hex(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func transitionSHA256(before, after string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(before))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(after))
	return fmt.Sprintf("%x", hash.Sum(nil))
}
