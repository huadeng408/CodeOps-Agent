package session_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"code-agent/internal/admission"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
)

type baselineNotification func(string)

func (notify baselineNotification) Notify(id string) { notify(id) }

func TestTaskBaselineDeletionBeforeAdmissionDoesNotAcquireTask(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows task preparation contract")
	}
	ctx := context.Background()
	repo, storage := t.TempDir(), t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.test"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("Git fixture: %s %v", output, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.test/baseline\n\ngo 1.20\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "fixture"}} {
		if err := exec.Command("git", append([]string{"-C", repo}, args...)...).Run(); err != nil {
			t.Fatal(err)
		}
	}
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	deleting := session.NewWorkbench(ledger, nil)
	armed := false
	// A second real Workbench commits the deletion before the first dispatches.
	workbench := session.NewWorkbench(ledger, baselineNotification(func(id string) {
		if !armed {
			return
		}
		armed = false
		view, err := deleting.Get(ctx, 7, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := deleting.Delete(ctx, 7, id, int64(view.EventCount)); err != nil {
			t.Fatal(err)
		}
	}))
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "deleted baseline", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	tasks := session.NewTaskWorkspaces(workbench, func(context.Context) (uint, error) { return 7, nil }, repo, storage)
	if view, err := tasks.Prepare(ctx, 7, created.ID, 1, "prepare"); err != nil || view.State != "prepared" {
		t.Fatalf("prepare: %+v %v", view, err)
	}
	budget := admission.NewBudget(ledger)
	tasks.WithBaselineRuntime(session.TaskBaselineRuntime{
		Runner: sandbox.NewOpenSandboxRunner(sandbox.Config{TrustRoot: storage}),
		Acquire: func(ctx context.Context, owner uint, id string) error {
			_, err := budget.AcquireTask(ctx, owner, id)
			return err
		},
		Finish: func(ctx context.Context, owner uint, id string) error {
			_, err := budget.FinishTask(ctx, owner, id)
			return err
		},
	})
	armed = true
	if _, err := tasks.RunBaseline(ctx, 7, created.ID, 3, "test"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("deleted Session admitted: %v", err)
	}
	batch, err := budget.Inspect(ctx, 7)
	if err != nil && !errors.Is(err, session.ErrSessionNotFound) || batch.ID != "" {
		t.Fatalf("deleted Session entered task admission: %+v %v", batch, err)
	}
}

func TestTaskBaselineRetainsBlockedOutcomeAndDoesNotReplayAfterRestart(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows task preparation contract")
	}
	ctx := context.Background()
	repo, storage := t.TempDir(), t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.test"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("Git fixture: %s %v", output, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.test/baseline\n\ngo 1.20\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "fixture"}} {
		if err := exec.Command("git", append([]string{"-C", repo}, args...)...).Run(); err != nil {
			t.Fatal(err)
		}
	}
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "baseline", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	owner := func(context.Context) (uint, error) { return 7, nil }
	tasks := session.NewTaskWorkspaces(workbench, owner, repo, storage)
	prepared, err := tasks.Prepare(ctx, 7, created.ID, 1, "prepare-1")
	if err != nil || prepared.State != "prepared" {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	budget := admission.NewBudget(ledger)
	profile := session.TaskBaselineRuntime{
		Runner: sandbox.NewOpenSandboxRunner(sandbox.Config{TrustRoot: storage}),
		Acquire: func(ctx context.Context, owner uint, id string) error {
			_, err := budget.AcquireTask(ctx, owner, id)
			return err
		},
		Finish: func(ctx context.Context, owner uint, id string) error {
			_, err := budget.FinishTask(ctx, owner, id)
			return err
		},
	}
	tasks.WithBaselineRuntime(profile)
	if _, err := tasks.RunBaseline(ctx, 8, created.ID, 3, "test-1"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatal("foreign owner admitted")
	}
	if _, err := tasks.RunBaseline(ctx, 7, created.ID, 2, "test-1"); !errors.Is(err, session.ErrSequenceConflict) {
		t.Fatal("stale cursor admitted")
	}
	view, err := tasks.RunBaseline(ctx, 7, created.ID, 3, "test-1")
	if err != nil || view.Test == nil || view.Test.State != "blocked" || view.Test.ExitCode == nil || *view.Test.ExitCode != 126 || view.EventCount != 5 {
		t.Fatalf("blocked result was lost: %+v %v", view, err)
	}
	reopened := session.NewTaskWorkspaces(session.NewWorkbench(ledger, nil), owner, repo, storage).WithBaselineRuntime(profile)
	again, err := reopened.RunBaseline(ctx, 7, created.ID, 3, "test-1")
	if err != nil || again.Test == nil || again.Test.OperationID != view.Test.OperationID || again.EventCount != 5 {
		t.Fatalf("restart replayed the operation: %+v %v", again, err)
	}
	if _, err := reopened.RunBaseline(ctx, 7, created.ID, 5, "different-request"); !errors.Is(err, session.ErrSessionStateConflict) {
		t.Fatal("new request replaced retained outcome")
	}
	batch, err := budget.Inspect(ctx, 7)
	if err != nil || batch.TaskID != "" || batch.UsedTokens != 0 || batch.ReservedTokens != 0 {
		t.Fatalf("baseline changed model budget or retained a known stopped slot: %+v %v", batch, err)
	}
}
