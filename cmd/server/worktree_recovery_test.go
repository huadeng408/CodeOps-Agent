package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/session"
	"code-agent/internal/worktree"
)

type failTerminalAppendLog struct {
	session.EventLog
	failed atomic.Bool
}

func (l *failTerminalAppendLog) Append(ctx context.Context, sessionID string, expectedSeq int64, eventType string, payload any) (session.Event, error) {
	if eventType == persistedWorktreeTerminalEvent && l.failed.CompareAndSwap(false, true) {
		return session.Event{}, errors.New("fixture terminal append failure")
	}
	return l.EventLog.Append(ctx, sessionID, expectedSeq, eventType, payload)
}

func TestRestorePersistedWorktreesReplaysOnlyActiveLeases(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "browser", "resume")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	active := worktree.Worktree{
		Name: "browser-agent", Path: filepath.Join(workspaceRoot, ".agent", "worktrees", "browser-agent"),
		BaseRef: "HEAD", Active: true, RequestID: "spawn-1", ParentSessionID: created.ID,
		ChildSessionID: "child-1", LeaseID: "lease-11111111111111111111111111111111", LeaseExpiresAt: time.Now().Add(time.Hour), Status: worktree.AgentWorktreeActive,
	}
	if err := appendPersistedWorktreeEvent(ctx, ledger, created.ID, persistedWorktreeActiveEvent, active, "spawned"); err != nil {
		t.Fatal(err)
	}

	manager := worktree.NewManager(workspaceRoot, "HEAD")
	if err := restorePersistedWorktrees(ctx, ledger, manager); err != nil {
		t.Fatal(err)
	}
	got := manager.List()
	if len(got) != 1 || got[0].RequestID != active.RequestID || got[0].LeaseID != active.LeaseID {
		t.Fatalf("restored worktrees = %#v", got)
	}

	if err := appendPersistedWorktreeEvent(ctx, ledger, created.ID, persistedWorktreeTerminalEvent, active, "completed"); err != nil {
		t.Fatal(err)
	}
	manager = worktree.NewManager(workspaceRoot, "HEAD")
	if err := restorePersistedWorktrees(ctx, ledger, manager); err != nil {
		t.Fatal(err)
	}
	if got := manager.List(); len(got) != 0 {
		t.Fatalf("terminal worktree was restored: %#v", got)
	}
}

func TestAgentLifecycleRetriesTerminalAppendAfterCleanup(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	created, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "repo", "browser", "retry cleanup")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	tree := worktree.Worktree{
		Name: "retry-agent", Path: filepath.Join(root, ".agent", "worktrees", "retry-agent"), BaseRef: "HEAD", Active: true,
		RequestID: "request-retry", ParentSessionID: created.ID, ChildSessionID: "child-retry",
		LeaseID: "lease-66666666666666666666666666666666", LeaseExpiresAt: time.Now().Add(time.Hour), Status: worktree.AgentWorktreeActive,
	}
	if err := appendPersistedWorktreeEvent(ctx, ledger, created.ID, persistedWorktreeActiveEvent, tree, "spawned"); err != nil {
		t.Fatal(err)
	}
	manager := worktree.NewManager(root, "HEAD")
	if err := manager.RestoreChecked([]worktree.Worktree{tree}); err != nil {
		t.Fatal(err)
	}
	fault := &failTerminalAppendLog{EventLog: ledger}
	lifecycle := &codeagentpb.AgentLifecycle{RequestId: tree.RequestID, ChildSessionId: tree.ChildSessionID, LeaseId: tree.LeaseID, Status: "cancelled", Reason: "cancel"}
	if err := handlePersistedAgentLifecycle(ctx, fault, manager, lifecycle); err == nil {
		t.Fatal("terminal append failure was hidden")
	}
	if got := manager.List(); len(got) != 0 {
		t.Fatalf("cleanup did not run before injected persistence failure: %#v", got)
	}
	active, err := persistedActiveWorktrees(ctx, ledger)
	if err != nil || len(active) != 1 || active[0].RequestID != tree.RequestID {
		t.Fatalf("durable active fact was not recoverable: active=%#v err=%v", active, err)
	}
	if err := handlePersistedAgentLifecycle(ctx, fault, manager, lifecycle); err != nil {
		t.Fatalf("terminal retry: %v", err)
	}
	if err := handlePersistedAgentLifecycle(ctx, fault, manager, lifecycle); err != nil {
		t.Fatalf("idempotent terminal retry: %v", err)
	}
	active, err = persistedActiveWorktrees(ctx, ledger)
	if err != nil || len(active) != 0 {
		t.Fatalf("terminal retry left active worktree: active=%#v err=%v", active, err)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	terminals := 0
	for _, event := range events {
		if event.Type == persistedWorktreeTerminalEvent {
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("terminal events = %d, want 1", terminals)
	}
}

func TestRestorePersistedWorktreesKeepsFailedWorkspaceManaged(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	created, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "repo", "browser", "retain failure")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	tree := worktree.Worktree{
		Name: "failed-agent", Path: filepath.Join(root, ".agent", "worktrees", "failed-agent"), BaseRef: "HEAD", Active: true,
		RequestID: "request-failed", ParentSessionID: created.ID, ChildSessionID: "child-failed",
		LeaseID: "lease-77777777777777777777777777777777", LeaseExpiresAt: time.Now().Add(time.Hour), Status: worktree.AgentWorktreeActive,
	}
	if err := appendPersistedWorktreeEvent(ctx, ledger, created.ID, persistedWorktreeActiveEvent, tree, "spawned"); err != nil {
		t.Fatal(err)
	}
	tree.Active = false
	tree.Status = "failed"
	if err := appendPersistedWorktreeEvent(ctx, ledger, created.ID, persistedWorktreeTerminalEvent, tree, "failed"); err != nil {
		t.Fatal(err)
	}
	manager := worktree.NewManager(root, "HEAD")
	if err := restorePersistedWorktrees(ctx, ledger, manager); err != nil {
		t.Fatal(err)
	}
	got := manager.List()
	if len(got) != 1 || got[0].RequestID != tree.RequestID || got[0].Status != worktree.AgentWorktreeActive {
		t.Fatalf("failed workspace was not retained for recovery: %#v", got)
	}
}

func TestRestorePersistedWorktreesRejectsCorruptEvent(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "browser", "resume")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 1, persistedWorktreeActiveEvent, map[string]any{"request_id": "missing-fields"}); err != nil {
		t.Fatal(err)
	}
	err = restorePersistedWorktrees(ctx, ledger, worktree.NewManager(t.TempDir(), "HEAD"))
	if !errors.Is(err, session.ErrEventIntegrity) {
		t.Fatalf("restore corrupt event error = %v, want ErrEventIntegrity", err)
	}
}

func TestPersistedAgentLifecycleRejectsUnknownOrMismatchedIdentity(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	created, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "repo", "browser", "identity")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	tree := worktree.Worktree{
		Name: "identity-agent", Path: filepath.Join(root, ".agent", "worktrees", "identity-agent"), BaseRef: "HEAD", Active: true,
		RequestID: "request-identity", ParentSessionID: created.ID, ChildSessionID: "child-identity",
		LeaseID: "lease-88888888888888888888888888888888", LeaseExpiresAt: time.Now().Add(time.Hour), Status: worktree.AgentWorktreeActive,
	}
	if err := appendPersistedWorktreeEvent(ctx, ledger, created.ID, persistedWorktreeActiveEvent, tree, "spawned"); err != nil {
		t.Fatal(err)
	}
	manager := worktree.NewManager(root, "HEAD")
	if err := manager.RestoreChecked([]worktree.Worktree{tree}); err != nil {
		t.Fatal(err)
	}
	for _, lifecycle := range []*codeagentpb.AgentLifecycle{
		{RequestId: "missing", ChildSessionId: tree.ChildSessionID, Status: "cancelled"},
		{RequestId: tree.RequestID, ChildSessionId: "other-child", Status: "cancelled"},
		{RequestId: tree.RequestID, ChildSessionId: tree.ChildSessionID, LeaseId: "wrong-lease", Status: "cancelled"},
	} {
		if err := handlePersistedAgentLifecycle(ctx, ledger, manager, lifecycle); err == nil {
			t.Fatalf("mismatched lifecycle unexpectedly succeeded: %#v", lifecycle)
		}
	}
	if got := len(manager.List()); got != 1 {
		t.Fatalf("rejected lifecycle changed managed worktrees: %d", got)
	}
}

func TestRestorePersistedWorktreesRejectsCrossSessionParent(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "browser", "resume")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 1, persistedWorktreeActiveEvent, persistedWorktreeEvent{
		Name: "cross-session", Path: filepath.Join(t.TempDir(), ".agent", "worktrees", "cross-session"), BaseRef: "HEAD", Active: true,
		RequestID: "request-cross-session", ParentSessionID: "other-session", ChildSessionID: "child-session",
		LeaseID: "lease-22222222222222222222222222222222", LeaseExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Status: worktree.AgentWorktreeActive,
	}); err != nil {
		t.Fatal(err)
	}
	err = restorePersistedWorktrees(ctx, ledger, worktree.NewManager(t.TempDir(), "HEAD"))
	if !errors.Is(err, session.ErrEventIntegrity) {
		t.Fatalf("restore cross-session parent error = %v, want ErrEventIntegrity", err)
	}
}

func TestRestorePersistedWorktreesDoesNotResurrectDeletedSession(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "deleted", "do not restore")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	tree := worktree.Worktree{
		Name: "deleted-agent", Path: filepath.Join(workspaceRoot, ".agent", "worktrees", "deleted-agent"),
		BaseRef: "HEAD", Active: true, RequestID: "spawn-deleted", ParentSessionID: created.ID,
		ChildSessionID: "child-deleted", LeaseID: "lease-33333333333333333333333333333333",
		LeaseExpiresAt: time.Now().Add(time.Hour), Status: worktree.AgentWorktreeActive,
	}
	if err := appendPersistedWorktreeEvent(ctx, ledger, created.ID, persistedWorktreeActiveEvent, tree, "spawned"); err != nil {
		t.Fatal(err)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.Delete(ctx, 7, created.ID, int64(len(events))); err != nil {
		t.Fatal(err)
	}

	manager := worktree.NewManager(workspaceRoot, "HEAD")
	if err := restorePersistedWorktrees(ctx, ledger, manager); err != nil {
		t.Fatal(err)
	}
	if got := manager.List(); len(got) != 0 {
		t.Fatalf("deleted session worktree was resurrected: %#v", got)
	}
}

func TestAppendPersistedWorktreeEventRejectsDeletedSession(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "deleted", "reject late worktree facts")
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.Delete(ctx, 7, created.ID, 1); err != nil {
		t.Fatal(err)
	}
	tree := worktree.Worktree{
		Name: "late-agent", Path: filepath.Join(t.TempDir(), ".agent", "worktrees", "late-agent"),
		BaseRef: "HEAD", Active: true, RequestID: "spawn-late", ParentSessionID: created.ID,
		ChildSessionID: "child-late", LeaseID: "lease-44444444444444444444444444444444",
		LeaseExpiresAt: time.Now().Add(time.Hour), Status: worktree.AgentWorktreeActive,
	}

	err = appendPersistedWorktreeEvent(ctx, ledger, created.ID, persistedWorktreeActiveEvent, tree, "spawned after delete")
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("append after delete error = %v, want ErrSessionNotFound", err)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[len(events)-1].Type != "session/deleted" {
		t.Fatalf("deleted session history changed: %+v", events)
	}
}

func TestAppendPersistedWorktreeEventRejectsMissingSession(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	tree := worktree.Worktree{
		Name: "orphan-agent", Path: filepath.Join(t.TempDir(), ".agent", "worktrees", "orphan-agent"),
		BaseRef: "HEAD", Active: true, RequestID: "spawn-orphan", ParentSessionID: "missing-session",
		ChildSessionID: "child-orphan", LeaseID: "lease-55555555555555555555555555555555",
		LeaseExpiresAt: time.Now().Add(time.Hour), Status: worktree.AgentWorktreeActive,
	}

	err = appendPersistedWorktreeEvent(ctx, ledger, tree.ParentSessionID, persistedWorktreeActiveEvent, tree, "spawned without parent")
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("append without parent error = %v, want ErrSessionNotFound", err)
	}
	ids, err := ledger.SessionIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("missing parent created orphan session histories: %v", ids)
	}
}
