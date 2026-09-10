package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"code-agent/internal/session"
	"code-agent/internal/worktree"
)

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
