package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"code-agent/internal/safety"
	"code-agent/internal/sandbox"
	"code-agent/internal/tools"
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

func TestTaskWorkspaceAdmitsConcurrentDispatchedReads(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first native Git directory pin contract")
	}
	ctx := context.Background()
	repo, storage := taskWorkspaceRepository(t), t.TempDir()
	ledger := openWorkbenchTestLedger(t)
	w := NewWorkbench(ledger, nil)
	created, err := w.CreateWithWorkingDir(ctx, 7, "repo", "parallel reads", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskWorkspaces(w, taskWorkspaceOwner, repo, storage).WithBaselineRuntime(TaskBaselineRuntime{
		Runner:  sandbox.NewOpenSandboxRunner(sandbox.Config{TrustRoot: storage}),
		Ready:   func(context.Context) error { return nil },
		Acquire: func(context.Context, uint, string) error { return nil },
		Finish:  func(context.Context, uint, string) error { return nil },
	})
	prepared, err := tasks.Prepare(ctx, 7, created.ID, 1, "prepare")
	if err != nil || prepared.State != "prepared" {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	message, err := w.AppendUserMessage(ctx, 7, created.ID, int64(prepared.EventCount), "read concurrently")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := w.CreateCheckpoint(ctx, 7, created.ID, int64(prepared.EventCount+1), message.ID, "reads")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := continuationRequestPayloadForTest(ctx, w, created.ID, checkpoint.Hash, "request-reads", "run-reads", testRunnerActor())
	if err != nil {
		t.Fatal(err)
	}
	seq := int64(prepared.EventCount + 2)
	appendFact := func(kind string, payload any) {
		t.Helper()
		if _, err := ledger.Append(ctx, created.ID, seq, kind, payload); err != nil {
			t.Fatal(err)
		}
		seq++
	}
	appendFact(continuationEventType, payload)
	appendFact(runLeasedEventType, runLeasePayload{RunID: "run-reads", RequestID: "request-reads", LeaseID: "lease-reads", WorkerID: "worker", Attempt: 1, LeaseUntil: time.Now().Add(time.Minute).UTC()})
	for index, name := range []string{"Read", "Grep"} {
		call := toolCallPayload{RunID: "run-reads", ToolCallID: fmt.Sprintf("read-%d", index), ToolName: name, ArgumentsJSON: `{"path":"main.go"}`}
		appendFact("tool/call", call)
		appendFact(commitPendingEventType, call)
		appendFact(toolDispatchedType, call)
	}
	for _, callID := range []string{"read-0", "read-1"} {
		workspace, err := tasks.ToolWorkspace(ctx, 7, created.ID, callID)
		if err != nil || workspace.WorkingDir != filepath.Join(storage, prepared.WorkspaceID) {
			t.Fatalf("concurrent authorized %s refused: %+v %v", callID, workspace, err)
		}
	}
	if _, err := tasks.ModelWorkspace(ctx, 7, created.ID); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatal("new model work was admitted while tools were still pending")
	}
	if _, err := tasks.ToolWorkspace(ctx, 8, created.ID, "read-0"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("parallel reads exposed a foreign owner's workspace")
	}
	if _, err := tasks.ToolWorkspace(ctx, 7, created.ID, "not-dispatched"); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatal("a call without its Go dispatch was admitted")
	}
	write := toolCallPayload{RunID: "run-reads", ToolCallID: "write", ToolName: "Write", ArgumentsJSON: `{"path":"main.go","content":"change"}`}
	appendFact(commitPendingEventType, write)
	appendFact(toolDispatchedType, write)
	if _, err := tasks.ToolWorkspace(ctx, 7, created.ID, "read-0"); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatal("pending mutation did not fence parallel reads")
	}
	appendFact("tool/result", toolResultPayload{RunID: "run-reads", ToolCallID: "write", ToolName: "Write", Error: "denied", ExitCode: 1})
	if _, err := tasks.ToolWorkspace(ctx, 7, created.ID, "read-0"); err != nil {
		t.Fatal("a confirmed refused mutation blocked readonly work")
	}
	appendFact("tool/result", toolResultPayload{RunID: "run-reads", ToolCallID: "read-1", ToolName: "Grep", OutcomeUnknown: true})
	if _, err := tasks.ToolWorkspace(ctx, 7, created.ID, "read-0"); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatal("unknown tool outcome was silently admitted")
	}
}

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

func TestTaskWorkspaceContinuesOnlyAfterRecordedGoEdits(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first native Git directory pin contract")
	}
	ctx := context.Background()
	repo, storage := taskWorkspaceRepository(t), t.TempDir()
	ledger := openWorkbenchTestLedger(t)
	w := NewWorkbench(ledger, nil)
	created, err := w.CreateWithWorkingDir(ctx, 7, "repo", "edit copy", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskWorkspaces(w, taskWorkspaceOwner, repo, storage)
	prepared, err := tasks.Prepare(ctx, 7, created.ID, 1, "prepare")
	if err != nil || prepared.State != "prepared" {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	executor := tools.NewExecutor(filepath.Join(storage, prepared.WorkspaceID))
	defer executor.Close()
	seq := int64(prepared.EventCount)
	appendFact := func(kind string, payload any) {
		t.Helper()
		if _, err := ledger.Append(ctx, created.ID, seq, kind, payload); err != nil {
			t.Fatal(err)
		}
		seq++
	}
	for index, path := range []string{"main.go", "src/new.go", "main.go"} {
		callID := fmt.Sprintf("edit-%d", index)
		appendFact("tool/dispatched", map[string]any{"run_id": "run", "tool_call_id": callID, "tool_name": "Write"})
		result, err := executor.Execute(ctx, tools.ToolRequest{Name: "Write", Arguments: map[string]any{"path": path, "content": fmt.Sprintf("change %d", index)}})
		if err != nil || result.ExitCode != 0 || len(result.Changes) != 1 {
			t.Fatalf("Go Write failed: %+v %v", result, err)
		}
		change := result.Changes[0]
		appendFact("code/modified", map[string]any{
			"run_id": "run", "tool_call_id": callID, "tool_name": "Write", "operation": "Write", "summary": "Go Write",
			"path": change.Path, "before": change.Before, "after": change.After,
			"before_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(change.Before))),
			"after_sha256":  fmt.Sprintf("%x", sha256.Sum256([]byte(change.After))),
			"diff_sha256":   fmt.Sprintf("%x", sha256.Sum256([]byte(change.Before+"\x00"+change.After))),
		})
		view, err := tasks.Inspect(ctx, 7, created.ID)
		if err != nil || view.State != "blocked" {
			t.Fatal("unconfirmed file effect was admitted")
		}
		appendFact("tool/result", map[string]any{"run_id": "run", "tool_call_id": callID, "tool_name": "Write", "exit_code": 0})
		view, err = NewTaskWorkspaces(w, taskWorkspaceOwner, repo, storage).Inspect(ctx, 7, created.ID)
		if err != nil || view.State != "prepared" {
			t.Fatalf("recorded Go edit cannot continue after restart: %+v %v", view, err)
		}
	}
	original, err := os.ReadFile(filepath.Join(repo, "main.go"))
	if err != nil || string(original) != "dirty" {
		t.Fatal("task edit changed the original repository")
	}
	if err := os.WriteFile(filepath.Join(storage, prepared.WorkspaceID, "src", "new.go"), []byte("external change"), 0644); err != nil {
		t.Fatal(err)
	}
	view, err := tasks.Inspect(ctx, 7, created.ID)
	if err != nil || view.State != "blocked" {
		t.Fatal("external edit was admitted as a recorded Go edit")
	}
	if err := os.WriteFile(filepath.Join(storage, prepared.WorkspaceID, "src", "new.go"), []byte("change 1"), 0644); err != nil {
		t.Fatal(err)
	}
	appendFact("tool/dispatched", map[string]any{"run_id": "run", "tool_call_id": "shell", "tool_name": "Bash"})
	appendFact("tool/result", map[string]any{"run_id": "run", "tool_call_id": "shell", "tool_name": "Bash", "exit_code": 126, "outcome_unknown": true, "stopped": true})
	view, err = tasks.Inspect(ctx, 7, created.ID)
	if err != nil || view.State != "blocked" {
		t.Fatal("unknown shell outcome was admitted because it produced no file change")
	}
}
