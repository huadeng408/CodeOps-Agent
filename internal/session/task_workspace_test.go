package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"code-agent/internal/safety"
	"code-agent/internal/worktree"
)

func taskWorkspaceRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.test"}} {
		taskWorkspaceGit(t, root, args...)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("committed"), 0600); err != nil {
		t.Fatal(err)
	}
	taskWorkspaceGit(t, root, "add", ".")
	taskWorkspaceGit(t, root, "commit", "-m", "fixture")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("dirty"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func taskWorkspaceGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", safety.HardenedGitArgs(root, args[0], args[1:])...)
	command.Env = safety.ScrubGitEnvironment(os.Environ())
	if err := command.Run(); err != nil {
		t.Fatalf("fixture Git %s failed", args[0])
	}
}

func taskWorkspaceOwner(context.Context) (uint, error) { return 7, nil }

func TestTaskWorkspaceLedgerOwnsPreparationAndRecovery(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first native Git directory pin contract")
	}
	ctx := context.Background()
	root, storage := taskWorkspaceRepository(t), t.TempDir()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "prepare", "", root)
	if err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskWorkspaces(workbench, taskWorkspaceOwner, root, storage)
	if _, err := tasks.Prepare(ctx, 8, created.ID, 1, "request-1"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("foreign owner not denied")
	}
	if _, err := tasks.Prepare(ctx, 7, created.ID, 0, "request-1"); !errors.Is(err, ErrSequenceConflict) {
		t.Fatal("stale cursor not denied")
	}
	view, err := tasks.Prepare(ctx, 7, created.ID, 1, "request-1")
	if err != nil || view.State != "prepared" || view.WorkspaceID == "" || view.EventCount != 3 {
		t.Fatalf("preparation failed: %v %+v", err, view)
	}
	again, err := NewTaskWorkspaces(NewWorkbench(ledger, nil), taskWorkspaceOwner, root, storage).Prepare(ctx, 7, created.ID, 1, "request-1")
	if err != nil || again.WorkspaceID != view.WorkspaceID || again.EventCount != 3 {
		t.Fatal("restart/idempotency changed lease or events")
	}
	if _, err := tasks.Prepare(ctx, 7, created.ID, 3, "different-request"); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatal("new request replaced retained workspace")
	}
	if err := ledger.Verify(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storage, view.WorkspaceID, "main.go"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := tasks.Inspect(ctx, 7, created.ID)
	if err != nil || changed.State != "blocked" {
		t.Fatal("changed workspace reported prepared")
	}
}

func TestTaskWorkspaceRequiresHostApprovalAndPreservesInterruptedIntent(t *testing.T) {
	ctx := context.Background()
	root, storage := taskWorkspaceRepository(t), t.TempDir()
	ledger := openWorkbenchTestLedger(t)
	w := NewWorkbench(ledger, nil)
	created, err := w.CreateWithWorkingDir(ctx, 7, "repo", "blocked", "", root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewTaskWorkspaces(w, taskWorkspaceOwner, t.TempDir(), storage).Prepare(ctx, 7, created.ID, 1, "denied"); !errors.Is(err, ErrInvalidSessionInput) {
		t.Fatal("unapproved repository admitted")
	}
	plan, err := worktree.NewManager(root, "HEAD").PlanTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 1, taskPrepareIntentEvent, taskPreparePayload{RequestID: "crashed", Task: plan}); err != nil {
		t.Fatal(err)
	}
	view, err := NewTaskWorkspaces(NewWorkbench(ledger, nil), taskWorkspaceOwner, root, storage).Prepare(ctx, 7, created.ID, 1, "crashed")
	if err != nil || view.State != "unknown" || view.WorkspaceID != plan.LeaseID {
		t.Fatal("interrupted intent was silently retried or discarded")
	}
	if _, err := os.Lstat(filepath.Join(storage, plan.LeaseID)); !os.IsNotExist(err) {
		t.Fatal("interrupted intent created a new checkout")
	}
}
