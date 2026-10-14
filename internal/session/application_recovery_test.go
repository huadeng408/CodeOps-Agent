package session

import (
	"bufio"
	"code-agent/internal/identity"
	"code-agent/internal/worktree"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type applicationCrashInput struct {
	Database, Repository, Storage, SessionID, ProposalID string
	Phase                                                string
	Index                                                int
	Seq                                                  int64
}

type applicationCrashLedger struct {
	EventLog
	phase string
	index int
}

func (l applicationCrashLedger) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	var item struct {
		Index int `json:"index"`
	}
	data, _ := json.Marshal(payload)
	_ = json.Unmarshal(data, &item)
	matches := (l.phase == "parent-intent-before" || l.phase == "parent-intent-after") && kind == proposalApplicationIntent ||
		(l.phase == "parent-outcome-before" || l.phase == "parent-outcome-after") && kind == proposalApplicationOutcome ||
		l.phase == "item-intent-after" && kind == proposalItemIntent && item.Index == l.index ||
		(l.phase == "item-outcome-before" || l.phase == "item-outcome-after") && kind == proposalItemOutcome && item.Index == l.index
	pause := func() {
		fmt.Println("APPLICATION_EFFECT_READY")
		select {}
	}
	if matches && strings.HasSuffix(l.phase, "before") {
		pause()
	}
	event, err := l.EventLog.Append(ctx, id, seq, kind, payload)
	if err == nil && matches && strings.HasSuffix(l.phase, "after") {
		pause()
	}
	return event, err
}

func TestApplicationRecoveryCrashHelper(t *testing.T) {
	data := os.Getenv("CODE_AGENT_APPLICATION_CRASH_HELPER")
	if data == "" {
		t.Skip("owned subprocess helper")
	}
	var input applicationCrashInput
	if json.Unmarshal([]byte(data), &input) != nil {
		t.Fatal("invalid helper input")
	}
	ledger, err := OpenSQLiteEventLog(input.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	tasks := NewTaskWorkspaces(NewWorkbench(applicationCrashLedger{ledger, input.Phase, input.Index}, nil), taskWorkspaceOwner, input.Repository, input.Storage).WithBaselineRuntime(applicationRuntimeProfile())
	_, err = tasks.DecideProposal(context.Background(), 7, input.SessionID, input.Seq, "crashed-application", input.ProposalID, "approve")
	t.Fatalf("helper unexpectedly returned: %v", err)
}

func TestReconcileApplicationAfterProducerProcessDies(t *testing.T) {
	testReconcileApplicationAfterProducerDies(t, "item-outcome-before", 0)
}

func TestApplicationCrashMatrixKeepsEveryPlannedItem(t *testing.T) {
	for _, phase := range []string{"parent-intent-before", "parent-intent-after", "item-intent-after", "item-outcome-before", "item-outcome-after", "parent-outcome-before", "parent-outcome-after"} {
		count := 1
		if strings.HasPrefix(phase, "item-") {
			count = 4
		}
		for index := 0; index < count; index++ {
			t.Run(fmt.Sprintf("%s/%d", phase, index), func(t *testing.T) { testReconcileApplicationAfterProducerDies(t, phase, index) })
		}
	}
}

func testReconcileApplicationAfterProducerDies(t *testing.T, phase string, index int) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first lifecycle")
	}
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "sessions.sqlite")
	ledger, err := OpenSQLiteEventLog(database)
	if err != nil {
		t.Fatal(err)
	}
	_, _, repo, storage, view := readyLifecycleProposal(t, ledger)
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(applicationCrashInput{Database: database, Repository: repo, Storage: storage, SessionID: view.Proposal.SessionID, ProposalID: view.Proposal.ID, Seq: int64(view.EventCount), Phase: phase, Index: index})
	command := exec.Command(os.Args[0], "-test.run=^TestApplicationRecoveryCrashHelper$", "-test.timeout=45s")
	command.Env = append(os.Environ(), "CODE_AGENT_APPLICATION_CRASH_HELPER="+string(input))
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "APPLICATION_EFFECT_READY") {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("producer did not reach effect boundary")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("producer effect observation timed out")
	}
	ledger, err = OpenSQLiteEventLog(database)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	tasks := NewTaskWorkspaces(NewWorkbench(ledger, nil), taskWorkspaceOwner, repo, storage).WithBaselineRuntime(applicationRuntimeProfile())
	view, err = tasks.Inspect(ctx, 7, view.Proposal.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	allRecorded := phase == "parent-outcome-before" || phase == "parent-outcome-after" || phase == "item-outcome-after" && index == 3
	if !allRecorded {
		if _, err := tasks.ReconcileApplication(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "while-live", "crashed-application"); !errors.Is(err, ErrSessionStateConflict) {
			t.Fatal("live producer did not fence reconciliation")
		}
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	if phase == "parent-intent-before" {
		view, err = tasks.Inspect(ctx, 7, view.Proposal.SessionID)
		if err != nil || view.Application != nil || len(view.Proposal.Files) != 4 {
			t.Fatal("pre-intent crash lost proposal or invented effects")
		}
		return
	}
	view, err = tasks.ReconcileApplication(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "after-stop", "crashed-application")
	applied := 0
	if strings.HasPrefix(phase, "item-") {
		applied = index
		if phase != "item-intent-after" {
			applied++
		}
	} else if strings.HasPrefix(phase, "parent-outcome") {
		applied = 4
	}
	want := "conflict"
	if applied > 0 {
		want = "partial"
	}
	if applied == 4 {
		want = "applied"
	}
	if err != nil || view.Application == nil || view.Application.State != want || len(view.Application.Items) != 4 {
		t.Fatalf("stopped producer: %+v %v", view, err)
	}
	for itemIndex, item := range view.Application.Items {
		expected := "not_started"
		if itemIndex < applied {
			expected = "applied"
		} else if phase == "item-intent-after" && itemIndex == index {
			expected = "not_applied"
		}
		if item.State != expected {
			t.Fatalf("item %d=%s want=%s", itemIndex, item.State, expected)
		}
	}
	indexBytes, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if hashCodeState(string(indexBytes)) != view.Baseline.IndexSHA256 {
		t.Fatal("crash/reconciliation changed index")
	}
	for _, replay := range []bool{false, true} {
		if replay {
			if _, err := tasks.DecideProposal(ctx, 7, view.Proposal.SessionID, 0, "crashed-application", view.Proposal.ID, "approve"); err != nil {
				t.Fatal(err)
			}
		}
		empty, emptyErr := os.ReadFile(filepath.Join(repo, "empty.txt"))
		if applied == 0 {
			if emptyErr != nil || len(empty) != 0 {
				t.Fatal("unapplied deletion changed original")
			}
		} else if !os.IsNotExist(emptyErr) {
			t.Fatal("confirmed deletion was repeated or lost")
		}
		data, err := os.ReadFile(filepath.Join(repo, "main.go"))
		expected := "dirty"
		if applied >= 2 {
			expected = "reviewed"
		}
		if err != nil || string(data) != expected {
			t.Fatal("edit count differs from planned prefix")
		}
		newFile, newErr := os.ReadFile(filepath.Join(repo, "new.txt"))
		if applied >= 3 {
			if newErr != nil || len(newFile) != 0 {
				t.Fatal("new empty file lost")
			}
		} else if !os.IsNotExist(newErr) {
			t.Fatal("recovery executed a remaining create")
		}
		old, oldErr := os.ReadFile(filepath.Join(repo, "old.txt"))
		moved, movedErr := os.ReadFile(filepath.Join(repo, "moved.txt"))
		if applied == 4 {
			if !os.IsNotExist(oldErr) || movedErr != nil || string(moved) != "rename content" {
				t.Fatal("rename outcome differs")
			}
		} else if oldErr != nil || string(old) != "rename content" || !os.IsNotExist(movedErr) {
			t.Fatal("recovery executed a remaining rename")
		}
	}
}

type refuseApplicationAggregate struct{ EventLog }

type pauseApplicationAggregate struct {
	EventLog
	reached, release chan struct{}
}

func (l pauseApplicationAggregate) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	if kind == proposalApplicationOutcome {
		select {
		case <-l.reached:
		default:
			close(l.reached)
		}
		<-l.release
	}
	return l.EventLog.Append(ctx, id, seq, kind, payload)
}

func TestReconcileApplicationRacingAggregateDoesNotPoisonLedger(t *testing.T) {
	ctx := context.Background()
	tasks, ledger, repo, storage, view := readyLifecycleProposal(t, nil)
	reached, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	producer := NewTaskWorkspaces(NewWorkbench(pauseApplicationAggregate{ledger, reached, release}, nil), taskWorkspaceOwner, repo, storage).WithBaselineRuntime(applicationRuntimeProfile())
	finished := make(chan error, 1)
	go func() {
		_, err := producer.DecideProposal(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "aggregate-race", view.Proposal.ID, "approve")
		finished <- err
	}()
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("aggregate boundary not reached")
	}
	current, err := tasks.Inspect(ctx, 7, view.Proposal.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	current, err = tasks.ReconcileApplication(ctx, 7, view.Proposal.SessionID, int64(current.EventCount), "inspect-aggregate-race", "aggregate-race")
	if err != nil || current.Application.State != "applied" {
		t.Fatalf("reconcile while finalizing: %+v %v", current, err)
	}
	close(release)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("producer did not finish")
	}
	current, err = tasks.Inspect(ctx, 7, view.Proposal.SessionID)
	if err != nil || current.Application == nil || !current.Application.Reconciled {
		t.Fatalf("race poisoned projection: %+v %v", current, err)
	}
}

type refuseFirstApplicationItemOutcome struct{ EventLog }

func (l refuseFirstApplicationItemOutcome) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	if kind == "workspace/application-item-outcome" {
		return Event{}, errors.New("fixture item storage unavailable")
	}
	return l.EventLog.Append(ctx, id, seq, kind, payload)
}

func (l refuseApplicationAggregate) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	if kind == "workspace/application-outcome" {
		return Event{}, errors.New("fixture aggregate storage unavailable")
	}
	return l.EventLog.Append(ctx, id, seq, kind, payload)
}

func TestReconcileApplicationConfirmsRecordedEffectsWithoutReapplying(t *testing.T) {
	ctx := context.Background()
	tasks, ledger, repo, storage, view := readyLifecycleProposal(t, nil)
	failedTasks := NewTaskWorkspaces(NewWorkbench(refuseApplicationAggregate{ledger}, nil), taskWorkspaceOwner, repo, storage).WithBaselineRuntime(applicationRuntimeProfile())
	if _, err := failedTasks.DecideProposal(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "apply-without-aggregate", view.Proposal.ID, "approve"); err == nil {
		t.Fatal("aggregate fault not exercised")
	}
	view, err := tasks.Inspect(ctx, 7, view.Proposal.SessionID)
	if err != nil || view.Application == nil || view.Application.State != "unknown" || len(view.Application.Items) != 4 {
		t.Fatalf("retained intent: %+v %v", view, err)
	}
	view, err = tasks.ReconcileApplication(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "read-application", "apply-without-aggregate")
	if err != nil || view.Application.State != "applied" || !view.Application.Reconciled {
		t.Fatalf("read-only reconciliation: %+v %v", view, err)
	}
	for _, item := range view.Application.Items {
		if item.State != "applied" {
			t.Fatal("confirmed effect was lost")
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "empty.txt"), []byte("user replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.ReconcileApplication(ctx, 7, view.Proposal.SessionID, 0, "read-application", "apply-without-aggregate"); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.DecideProposal(ctx, 7, view.Proposal.SessionID, 0, "apply-without-aggregate", view.Proposal.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "empty.txt"))
	if err != nil || string(data) != "user replacement" {
		t.Fatal("replay repeated the deletion")
	}
}

func TestReconcileApplicationPreservesPartialAndUserReplacement(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "known partial", true: "user replacement"}[replacement], func(t *testing.T) {
			ctx := context.Background()
			tasks, ledger, repo, storage, view := readyLifecycleProposal(t, nil)
			failedTasks := NewTaskWorkspaces(NewWorkbench(refuseFirstApplicationItemOutcome{ledger}, nil), taskWorkspaceOwner, repo, storage).WithBaselineRuntime(applicationRuntimeProfile())
			view, err := failedTasks.DecideProposal(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "partial-application", view.Proposal.ID, "approve")
			if err != nil || view.Application == nil || view.Application.State != "unknown" {
				t.Fatalf("item fault: %+v %v", view, err)
			}
			if replacement {
				if err := os.WriteFile(filepath.Join(repo, "empty.txt"), []byte("user replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			view, err = tasks.ReconcileApplication(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "inspect-partial", "partial-application")
			want := "partial"
			if replacement {
				want = "unknown"
			}
			if err != nil || view.Application.State != want || !view.Application.Reconciled || len(view.Application.Items) != 4 {
				t.Fatalf("reconcile: %+v %v", view, err)
			}
			for index, item := range view.Application.Items {
				expected := "not_started"
				if index == 0 {
					expected = "applied"
					if replacement {
						expected = "unknown"
					}
				}
				if item.State != expected {
					t.Fatalf("item %d state=%s want=%s", index, item.State, expected)
				}
			}
			if _, err := tasks.DecideProposal(ctx, 7, view.Proposal.SessionID, 0, "partial-application", view.Proposal.ID, "approve"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(repo, "new.txt")); !os.IsNotExist(err) {
				t.Fatal("reconciliation ran the next create")
			}
			data, _ := os.ReadFile(filepath.Join(repo, "main.go"))
			if string(data) != "dirty" {
				t.Fatal("reconciliation ran the next edit")
			}
			if replacement {
				data, err := os.ReadFile(filepath.Join(repo, "empty.txt"))
				if err != nil || string(data) != "user replacement" {
					t.Fatal("reconciliation overwrote user replacement")
				}
			}
		})
	}
}

func TestRepeatedReconciliationNeverPromotesAnItemWithoutIntent(t *testing.T) {
	ctx := context.Background()
	tasks, ledger, repo, storage, view := readyLifecycleProposal(t, nil)
	producer := NewTaskWorkspaces(NewWorkbench(refuseFirstApplicationItemOutcome{ledger}, nil), taskWorkspaceOwner, repo, storage).WithBaselineRuntime(applicationRuntimeProfile())
	view, err := producer.DecideProposal(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "no-intent", view.Proposal.ID, "approve")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{"first-observation", "second-observation"} {
		view, err = tasks.ReconcileApplication(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), request, "no-intent")
		if err != nil || view.Application == nil || view.Application.State != "unknown" {
			t.Fatalf("observation: %+v %v", view, err)
		}
		for _, item := range view.Application.Items {
			if item.Path == "new.txt" && item.State != "unknown" {
				t.Fatalf("never-dispatched create became %s", item.State)
			}
		}
	}
}

func TestReconcileDeletionPreservesReplacementDirectory(t *testing.T) {
	ctx := context.Background()
	tasks, ledger, repo, storage, view := readyLifecycleProposal(t, nil)
	producer := NewTaskWorkspaces(NewWorkbench(refuseFirstApplicationItemOutcome{ledger}, nil), taskWorkspaceOwner, repo, storage).WithBaselineRuntime(applicationRuntimeProfile())
	view, err := producer.DecideProposal(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "directory-replacement", view.Proposal.ID, "approve")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, "empty.txt"), 0755); err != nil {
		t.Fatal(err)
	}
	view, err = tasks.ReconcileApplication(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "inspect-directory", "directory-replacement")
	if err != nil || view.Application.State != "unknown" || view.Application.Items[0].State != "unknown" {
		t.Fatalf("replacement directory was treated as absent: %+v %v", view.Application, err)
	}
	info, err := os.Stat(filepath.Join(repo, "empty.txt"))
	if err != nil || !info.IsDir() {
		t.Fatal("replacement directory was not retained")
	}
}

func TestReconciledPartialAllowsFinishButRequiresNewTask(t *testing.T) {
	ctx := context.Background()
	tasks, ledger, repo, storage, view := readyLifecycleProposal(t, nil)
	oldID, oldBaseline, oldProposal := view.Proposal.SessionID, view.Proposal.BaselineChecksum, view.Proposal.ID
	actor, err := identity.Default().BindSession(oldID)
	if err != nil {
		t.Fatal(err)
	}
	producer := NewTaskWorkspaces(NewWorkbench(refuseFirstApplicationItemOutcome{ledger}, nil), taskWorkspaceOwner, repo, storage).WithBaselineRuntime(applicationRuntimeProfile())
	view, err = producer.DecideProposal(ctx, 7, oldID, int64(view.EventCount), "partial-to-finish", oldProposal, "approve")
	if err != nil {
		t.Fatal(err)
	}
	if err := ModelTaskIdle(ctx, ledger, actor); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("unreconciled effects did not fence completion: %v", err)
	}
	view, err = tasks.ReconcileApplication(ctx, 7, oldID, int64(view.EventCount), "inspect-to-finish", "partial-to-finish")
	if err != nil || view.Application.State != "partial" {
		t.Fatalf("reconcile partial: %+v %v", view, err)
	}
	if err := ModelTaskIdle(ctx, ledger, actor); err != nil {
		t.Fatalf("known stopped partial cannot finish: %v", err)
	}
	if _, err := tasks.ModelWorkspace(ctx, 7, oldID); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatal("old task resumed after partial application")
	}
	if _, err := tasks.DecideProposal(ctx, 7, oldID, int64(view.EventCount), "repeat-remaining", oldProposal, "approve"); err == nil {
		t.Fatal("old approval replayed remaining effects")
	}
	w := NewWorkbench(ledger, nil)
	created, err := w.CreateWithWorkingDir(ctx, 7, "repo", "review remaining work", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := tasks.Prepare(ctx, 7, created.ID, 1, "fresh-baseline")
	current, captureErr := worktree.NewManager(repo, "HEAD").CaptureBaseline(ctx)
	if err != nil || captureErr != nil || fresh.Baseline == nil || fresh.Baseline.Checksum == oldBaseline || fresh.Baseline.Checksum != current.Checksum {
		t.Fatalf("new task did not capture current original: %+v %v %v", fresh, err, captureErr)
	}
	if _, err := tasks.ModelWorkspace(ctx, 7, created.ID); err != nil {
		t.Fatalf("new baseline did not admit a new task: %v", err)
	}
	retained, err := tasks.Inspect(ctx, 7, oldID)
	if err != nil || retained.Proposal.ID != oldProposal || retained.Application.State != "partial" {
		t.Fatal("new task lost the old partial proposal")
	}
}

func TestReconcileApplicationRequiresRefreshedCursorForNewFacts(t *testing.T) {
	ctx := context.Background()
	tasks, ledger, repo, storage, view := readyLifecycleProposal(t, nil)
	producer := NewTaskWorkspaces(NewWorkbench(refuseFirstApplicationItemOutcome{ledger}, nil), taskWorkspaceOwner, repo, storage).WithBaselineRuntime(applicationRuntimeProfile())
	view, err := producer.DecideProposal(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "cursor-application", view.Proposal.ID, "approve")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, view.Proposal.SessionID, int64(view.EventCount), "audit/read-only-observation", map[string]any{"source": "fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.ReconcileApplication(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "cursor-recovery", "cursor-application"); !errors.Is(err, ErrSequenceConflict) {
		t.Fatalf("stale recovery cursor was not identified: %v", err)
	}
	fresh, err := tasks.Inspect(ctx, 7, view.Proposal.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.ReconcileApplication(ctx, 7, view.Proposal.SessionID, int64(fresh.EventCount), "cursor-recovery", "cursor-application"); err != nil {
		t.Fatal(err)
	}
}
