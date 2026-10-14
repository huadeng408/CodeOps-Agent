package session

import (
	"code-agent/internal/sandbox"
	"code-agent/internal/tools"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The external runtime fixture returns a known test result; application still
// uses real preparation, Go edits, frozen input, Ledger and filesystem.
type proposalRuntimeFixture struct{}

type applicationNotification func(string)

func (notify applicationNotification) Notify(id string) { notify(id) }

func (proposalRuntimeFixture) Run(_ context.Context, req sandbox.Request) (sandbox.Result, error) {
	return sandbox.Result{ExitCode: 0, Output: "ok fixture", Stopped: true, RuntimeID: "fixture-runtime", InputSHA256: req.FrozenInput.SourceSHA256}, nil
}

func TestProposalRejectionRetainsIsolatedResultAndOwnerBoundary(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first application contract")
	}
	ctx := context.Background()
	tasks, _, _, repo, _, view := readyApplicationProposal(t, nil, nil)
	sid := view.Proposal.SessionID
	if _, err := tasks.DecideProposal(ctx, 8, sid, int64(view.EventCount), "reject-1", view.Proposal.ID, "reject"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("foreign owner decided proposal")
	}
	if _, err := tasks.DecideProposal(ctx, 7, sid, int64(view.EventCount-1), "reject-1", view.Proposal.ID, "reject"); !errors.Is(err, ErrSequenceConflict) {
		t.Fatal("stale cursor decided proposal")
	}
	rejected, err := tasks.DecideProposal(ctx, 7, sid, int64(view.EventCount), "reject-1", view.Proposal.ID, "reject")
	if err != nil || rejected.Application == nil || rejected.Application.State != "rejected" || rejected.Proposal == nil || rejected.Proposal.ID != view.Proposal.ID {
		t.Fatalf("reject: %+v %v", rejected, err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, "main.go"))
	if string(data) != "dirty" {
		t.Fatal("rejection changed original")
	}
	if _, err := tasks.DecideProposal(ctx, 7, sid, int64(rejected.EventCount), "another-approval", view.Proposal.ID, "approve"); err == nil {
		t.Fatal("rejected proposal applied with a new request")
	}
}

type unavailableApplicationOutcome struct{ EventLog }

func (l unavailableApplicationOutcome) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	if kind == "workspace/application-outcome" {
		return Event{}, errors.New("ledger outcome unavailable")
	}
	return l.EventLog.Append(ctx, id, seq, kind, payload)
}

func TestProposalUnknownOutcomeNeverRetriesWrite(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first application contract")
	}
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	tasks, _, _, repo, storage, view := readyApplicationProposal(t, unavailableApplicationOutcome{ledger}, nil)
	sid := view.Proposal.SessionID
	if _, err := tasks.DecideProposal(ctx, 7, sid, int64(view.EventCount), "apply-lost", view.Proposal.ID, "approve"); err == nil {
		t.Fatal("lost outcome reported success")
	}
	pending, err := tasks.Inspect(ctx, 7, sid)
	if err != nil || pending.Application == nil || pending.Application.State != "unknown" {
		t.Fatalf("pending intent lost: %+v %v", pending, err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, "main.go"))
	if string(data) != "reviewed" {
		t.Fatal("fixture did not reach lost-outcome boundary")
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("later edit"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened := NewTaskWorkspaces(NewWorkbench(ledger, nil), taskWorkspaceOwner, repo, storage).WithBaselineRuntime(applicationRuntimeProfile())
	again, err := reopened.DecideProposal(ctx, 7, sid, int64(view.EventCount), "apply-lost", view.Proposal.ID, "approve")
	data, _ = os.ReadFile(filepath.Join(repo, "main.go"))
	if err != nil || again.Application == nil || again.Application.State != "unknown" || string(data) != "later edit" {
		t.Fatal("unknown result repeated a write")
	}
	if _, err := reopened.ModelWorkspace(ctx, 7, sid); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatal("unknown application admitted model tools")
	}
}

func applicationRuntimeProfile() TaskBaselineRuntime {
	return TaskBaselineRuntime{
		Runner: proposalRuntimeFixture{}, Ready: func(context.Context) error { return nil },
		Acquire: func(context.Context, uint, string) error { return nil }, Finish: func(context.Context, uint, string) error { return nil },
		BeginProposal: func(context.Context, uint, string) (func() error, error) { return func() error { return nil }, nil },
	}
}

func readyApplicationProposal(t *testing.T, ledger EventLog, notify EventNotifier) (*TaskWorkspaces, *Workbench, EventLog, string, string, TaskWorkspaceView) {
	t.Helper()
	ctx := context.Background()
	repo, storage := taskWorkspaceRepository(t), t.TempDir()
	if ledger == nil {
		ledger = openWorkbenchTestLedger(t)
	}
	w := NewWorkbench(ledger, notify)
	created, err := w.CreateWithWorkingDir(ctx, 7, "repo", "review", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskWorkspaces(w, taskWorkspaceOwner, repo, storage).WithBaselineRuntime(applicationRuntimeProfile())
	view, err := tasks.Prepare(ctx, 7, created.ID, 1, "prepare")
	if err != nil || view.State != "prepared" {
		t.Fatalf("prepare: %+v %v", view, err)
	}
	executor := tools.NewExecutor(filepath.Join(storage, view.WorkspaceID))
	defer executor.Close()
	seq := int64(view.EventCount)
	appendFact := func(kind string, payload any) {
		t.Helper()
		if _, err := ledger.Append(ctx, created.ID, seq, kind, payload); err != nil {
			t.Fatal(err)
		}
		seq++
	}
	appendFact("tool/dispatched", map[string]any{"run_id": "fixture-run", "tool_call_id": "edit", "tool_name": "Edit"})
	result, err := executor.Execute(ctx, tools.ToolRequest{Name: "Edit", Arguments: map[string]any{"path": "main.go", "old_string": "dirty", "new_string": "reviewed"}})
	if err != nil || result.ExitCode != 0 || len(result.Changes) != 1 {
		t.Fatalf("edit: %+v %v", result, err)
	}
	change := result.Changes[0]
	appendFact("code/modified", map[string]any{"run_id": "fixture-run", "tool_call_id": "edit", "tool_name": "Edit", "operation": "Edit", "summary": "edit", "path": change.Path, "before": change.Before, "after": change.After, "before_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(change.Before))), "after_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(change.After))), "diff_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(change.Before+"\x00"+change.After)))})
	appendFact("tool/result", map[string]any{"run_id": "fixture-run", "tool_call_id": "edit", "tool_name": "Edit", "exit_code": 0})
	view, err = tasks.GenerateProposal(ctx, 7, created.ID, seq, "proposal")
	if err != nil || view.Proposal == nil || view.Proposal.State != "ready" {
		t.Fatalf("proposal: %+v %v", view, err)
	}
	return tasks, w, ledger, repo, storage, view
}

func TestProposalApprovalReplaysAfterRestartWithoutAnotherWrite(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first exclusive application contract")
	}
	ctx := context.Background()
	tasks, _, ledger, repo, storage, view := readyApplicationProposal(t, nil, nil)
	sid := view.Proposal.SessionID
	index, _ := os.ReadFile(filepath.Join(repo, ".git", "index"))
	applied, err := tasks.DecideProposal(ctx, 7, sid, int64(view.EventCount), "apply-1", view.Proposal.ID, "approve")
	if err != nil || applied.Application == nil || applied.Application.State != "applied" {
		t.Fatalf("approve: %+v %v", applied, err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, "main.go"))
	afterIndex, _ := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if string(data) != "reviewed" || string(index) != string(afterIndex) {
		t.Fatal("approved content or index mismatch")
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("later user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened := NewTaskWorkspaces(NewWorkbench(ledger, nil), taskWorkspaceOwner, repo, storage)
	again, err := reopened.DecideProposal(ctx, 7, sid, int64(view.EventCount), "apply-1", view.Proposal.ID, "approve")
	data, _ = os.ReadFile(filepath.Join(repo, "main.go"))
	if err != nil || again.Application == nil || again.Application.State != "applied" || again.EventCount != applied.EventCount || string(data) != "later user edit" {
		t.Fatal("replay lost known result or overwrote later edits")
	}
	if _, err := reopened.DecideProposal(ctx, 7, sid, int64(again.EventCount), "apply-1", view.Proposal.ID, "reject"); err == nil {
		t.Fatal("request identity accepted different decision")
	}
}

type refuseApplicationIntent struct{ EventLog }

func (l refuseApplicationIntent) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	if kind == "workspace/application-intent" {
		return Event{}, ErrSequenceConflict
	}
	return l.EventLog.Append(ctx, id, seq, kind, payload)
}

func TestProposalIntentCASRefusalNeverWrites(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first application contract")
	}
	tasks, _, _, repo, _, view := readyApplicationProposal(t, refuseApplicationIntent{openWorkbenchTestLedger(t)}, nil)
	if _, err := tasks.DecideProposal(context.Background(), 7, view.Proposal.SessionID, int64(view.EventCount), "refused", view.Proposal.ID, "approve"); !errors.Is(err, ErrSequenceConflict) {
		t.Fatalf("CAS refusal: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, "main.go"))
	if string(data) != "dirty" {
		t.Fatal("CAS refusal changed original")
	}
}

func TestProposalApprovalRechecksAuthorizationAfterIntent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first application contract")
	}
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	armed, approved := false, true
	notify := applicationNotification(func(id string) {
		if !armed {
			return
		}
		events, err := ledger.Events(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if events[len(events)-1].Type == "workspace/application-intent" {
			approved = false
			armed = false
		}
	})
	_, w, _, repo, storage, view := readyApplicationProposal(t, ledger, notify)
	tasks := NewTaskWorkspaces(w, func(context.Context) (uint, error) {
		if approved {
			return 7, nil
		}
		return 0, errors.New("approval revoked")
	}, repo, storage)
	armed = true
	result, err := tasks.DecideProposal(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "revoked", view.Proposal.ID, "approve")
	if err != nil || result.Application == nil || result.Application.State != "conflict" {
		t.Fatalf("revocation: %+v %v", result, err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, "main.go"))
	if string(data) != "dirty" {
		t.Fatal("revocation before first write was ignored")
	}
}
