package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
)

// This isolated restart fixture uses real Go commands and SQLite. Its
// conversation adapter cannot call a provider or execute tools.
type startupYieldGuardBlockedConversation struct{}

func (startupYieldGuardBlockedConversation) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, _ orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	data, _ := json.Marshal(map[string]string{"session_id": request.SessionID, "run_id": request.RunID})
	fmt.Println("STARTUP_YIELD_GUARD_READY " + string(data))
	<-ctx.Done()
	return orchestrator.ConversationResult{}, ctx.Err()
}

func startupYieldGuardWorkspace(w *session.Workbench, budget *admission.Budget) func(context.Context, uint, string) (session.TaskModelWorkspace, error) {
	return func(ctx context.Context, owner uint, id string) (session.TaskModelWorkspace, error) {
		view, err := w.Get(ctx, owner, id)
		if err != nil {
			return session.TaskModelWorkspace{}, err
		}
		if _, err := budget.AcquireTask(ctx, admission.ActorOwner(identity.Default()), id); err != nil {
			return session.TaskModelWorkspace{}, err
		}
		return session.TaskModelWorkspace{WorkingDir: view.WorkingDir}, nil
	}
}

func TestStartupYieldGuardFixtureProcess(t *testing.T) {
	ledgerPath := os.Getenv("CODEOPS_STARTUP_YIELD_GUARD_LEDGER")
	if ledgerPath == "" {
		t.Skip("owned isolated restart fixture helper")
	}
	ledger, err := session.OpenSQLiteEventLog(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	w := session.NewWorkbench(ledger, nil)
	created, err := w.CreateWithWorkingDir(context.Background(), 7, "restart-fixture", "unfinished task", "", os.Getenv("CODEOPS_STARTUP_YIELD_GUARD_WORKSPACE"))
	if err != nil {
		t.Fatal(err)
	}
	workspaceAdmission := startupYieldGuardWorkspace(w, admission.NewBudget(ledger))
	if os.Getenv("CODEOPS_STARTUP_YIELD_GUARD_NO_ADMISSION") == "1" {
		workspaceAdmission = func(context.Context, uint, string) (session.TaskModelWorkspace, error) {
			return session.TaskModelWorkspace{WorkingDir: created.WorkingDir}, nil
		}
	}
	runner := session.NewSessionRunner(w, startupYieldGuardBlockedConversation{}, nil, session.SessionRunnerOptions{
		WorkerID: "owned-helper", LeaseDuration: 40 * time.Millisecond, HeartbeatInterval: 10 * time.Millisecond,
		Workspace: workspaceAdmission,
	})
	defer runner.Close()
	if _, err := runner.SubmitMessage(context.Background(), session.SubmitMessageCommand{OwnerID: 7, Actor: identity.Default(), SessionID: created.ID, RequestID: "unfinished-request", ExpectedSeq: 1, Content: "retain this ordinary task"}); err != nil {
		t.Fatal(err)
	}
	<-context.Background().Done()
}

// SessionIDs has no ordering contract; this database adapter makes the
// preserved workload reach startup first, without changing any stored fact.
type startupYieldGuardOrderedLedger struct {
	session.EventLog
	first string
}

func (l startupYieldGuardOrderedLedger) SessionIDs(ctx context.Context) ([]string, error) {
	ids, err := l.EventLog.SessionIDs(ctx)
	sort.SliceStable(ids, func(i, j int) bool { return ids[i] == l.first && ids[j] != l.first })
	return ids, err
}

func startupYieldGuardStoppedTask(t *testing.T, ctx context.Context, ledgerPath, workspace string) (string, string) {
	t.Helper()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartupYieldGuardFixtureProcess$")
	child.Env = append(os.Environ(), "CODEOPS_STARTUP_YIELD_GUARD_LEDGER="+ledgerPath, "CODEOPS_STARTUP_YIELD_GUARD_WORKSPACE="+workspace)
	stdout, err := child.StdoutPipe()
	if err != nil || child.Start() != nil {
		t.Fatal("owned startup fixture did not start")
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	var ready map[string]string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "STARTUP_YIELD_GUARD_READY ") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "STARTUP_YIELD_GUARD_READY ")), &ready); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if ready["session_id"] == "" || ready["run_id"] == "" {
		t.Fatal("owned task did not reach its blocked conversation")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	return ready["session_id"], ready["run_id"]
}

func TestServerStartupRecoveryPreservesYieldedUnfinishedTask(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ledgerPath, workspace := filepath.Join(t.TempDir(), "isolated.sqlite"), t.TempDir()
	yieldedID, _ := startupYieldGuardStoppedTask(t, ctx, ledgerPath, workspace)
	ledger, err := session.OpenSQLiteEventLog(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	if _, err := budget.YieldTask(ctx, admission.ActorOwner(identity.Default()), yieldedID); err != nil {
		t.Fatal(err)
	}
	eligibleID, eligibleRun := startupYieldGuardStoppedTask(t, ctx, ledgerPath, workspace)
	before, err := session.ReadVerifiedSnapshot(ctx, ledger, yieldedID)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := budget.Inspect(ctx, admission.ActorOwner(identity.Default()))
	if err != nil || batch.TaskID != eligibleID || batch.ActiveCalls != 0 {
		t.Fatalf("second task did not retain eligible admission: %+v %v", batch, err)
	}
	w := session.NewWorkbench(startupYieldGuardOrderedLedger{EventLog: ledger, first: yieldedID}, nil)
	conversation := &admissionConversation{}
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	runner := session.NewSessionRunner(w, conversation, nil, session.SessionRunnerOptions{
		WorkerID: "restarted-server", WorkerCount: 1,
		StartupRecoveryAllowed: gateway.StartupRecoveryAllowed,
		Now:                    func() time.Time { return time.Now().Add(time.Minute) },
		Workspace:              startupYieldGuardWorkspace(w, budget),
	})
	defer runner.Close()
	module := managedContinuation{runner: runner}
	if err := module.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		run, err := runner.Run(ctx, eligibleID, eligibleRun)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == session.RunCompleted {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("eligible task was not recovered: %v", ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := session.ReadVerifiedSnapshot(ctx, ledger, yieldedID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Events, after.Events) {
		t.Fatalf("startup recovery mutated yielded unfinished task: events %d -> %d; previous head %s, new head %s", len(before.Events), len(after.Events), before.Events[len(before.Events)-1].Checksum, after.Events[len(after.Events)-1].Checksum)
	}
	if conversation.calls.Load() != 1 {
		t.Fatalf("startup dispatched unexpected conversations: %d", conversation.calls.Load())
	}
}

func TestServerStartupRecoveryPreservesYieldedBorrowedChildBeforeFirstRun(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "isolated.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	w := session.NewWorkbench(ledger, nil)
	root, err := w.CreateWithWorkingDir(ctx, 7, "restart-fixture", "borrowed child", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	budget := admission.NewBudget(ledger)
	if _, err := budget.AcquireTask(ctx, admission.ActorOwner(actor), root.ID); err != nil {
		t.Fatal(err)
	}
	borrowed := func(context.Context, identity.Actor, string, orchestrator.ToolCall) (session.TaskModelWorkspace, error) {
		return session.TaskModelWorkspace{WorkingDir: root.WorkingDir}, nil
	}
	conversation := &admissionConversation{}
	seed := session.NewSessionRunner(w, conversation, nil, session.SessionRunnerOptions{Workspace: startupYieldGuardWorkspace(w, budget), ReadOnlyAgentWorkspace: borrowed})
	// Go creates the signed parent link and borrowed child. The occupied root
	// slot then rejects child admission, preserving the real no-run window.
	result := seed.ExecuteAgentTool(ctx, actor, root.ID, orchestrator.ToolCall{ID: "created-child", Name: "SpawnAgent", ParametersJSON: `{"kind":"explore","title":"inspect","objective":"read explicit task material"}`})
	if result.Error == "" {
		t.Fatal("fixture did not retain the created-before-admission window")
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	ids, err := ledger.SessionIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	childID := ""
	for _, id := range ids {
		if strings.HasPrefix(id, "agent-") {
			if childID != "" {
				t.Fatal("fixture created more than one child")
			}
			childID = id
		}
	}
	child, err := w.Get(ctx, 7, childID)
	if err != nil || child.Run != nil || child.WorkingDir != root.WorkingDir {
		t.Fatalf("borrowed child did not retain its no-run state: %+v %v", child, err)
	}
	childActor := actor
	childActor.SessionID = childID
	if actualRoot, err := session.ModelTaskRoot(ctx, ledger, childActor); err != nil || actualRoot != root.ID {
		t.Fatalf("Go-created child did not authenticate to the root: %s %v", actualRoot, err)
	}
	if _, err := budget.YieldTask(ctx, admission.ActorOwner(actor), root.ID); err != nil {
		t.Fatal(err)
	}
	before := map[string]session.LedgerSnapshot{}
	for _, id := range []string{root.ID, childID, session.VerificationAdmissionLedgerID} {
		before[id], err = session.ReadVerifiedSnapshot(ctx, ledger, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	recovered := session.NewSessionRunner(w, conversation, nil, session.SessionRunnerOptions{
		StartupRecoveryAllowed: gateway.StartupRecoveryAllowed, ReadOnlyAgentWorkspace: borrowed,
		Workspace: func(ctx context.Context, owner uint, id string) (session.TaskModelWorkspace, error) {
			view, err := w.Get(ctx, owner, id)
			if err != nil {
				return session.TaskModelWorkspace{}, err
			}
			bound, err := identity.Default().BindSession(id)
			if err != nil {
				return session.TaskModelWorkspace{}, err
			}
			taskRoot, err := session.ModelTaskRoot(ctx, ledger, bound)
			if err != nil {
				return session.TaskModelWorkspace{}, err
			}
			_, err = budget.AcquireTask(ctx, admission.ActorOwner(bound), taskRoot)
			return session.TaskModelWorkspace{WorkingDir: view.WorkingDir}, err
		},
	})
	defer recovered.Close()
	module := managedContinuation{runner: recovered}
	if err := module.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	for id, original := range before {
		after, err := session.ReadVerifiedSnapshot(ctx, ledger, id)
		if err != nil || !reflect.DeepEqual(original.Events, after.Events) {
			t.Fatalf("startup repaired a yielded root or child: %s %v", id, err)
		}
	}
	if conversation.calls.Load() != 0 {
		t.Fatal("yielded borrowed child reached a conversation")
	}
}

func TestServerStartupRecoveryRetainsExplicitAndNewTaskAdmission(t *testing.T) {
	for _, mode := range []string{"reacquired_startup", "explicit_session", "never_admitted"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ledgerPath, workspace := filepath.Join(t.TempDir(), "isolated.sqlite"), t.TempDir()
			if mode == "never_admitted" {
				t.Setenv("CODEOPS_STARTUP_YIELD_GUARD_NO_ADMISSION", "1")
			}
			id, runID := startupYieldGuardStoppedTask(t, ctx, ledgerPath, workspace)
			t.Setenv("CODEOPS_STARTUP_YIELD_GUARD_NO_ADMISSION", "")
			ledger, err := session.OpenSQLiteEventLog(ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			budget := admission.NewBudget(ledger)
			actor, err := identity.Default().BindSession(id)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "never_admitted" {
				if _, err := budget.YieldTask(ctx, admission.ActorOwner(actor), id); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "reacquired_startup" {
				if _, err := budget.AcquireTask(ctx, admission.ActorOwner(actor), id); err != nil {
					t.Fatal(err)
				}
			}
			gateway := admission.NewGateway(budget, admission.ProviderConfig{})
			allowed, err := gateway.StartupRecoveryAllowed(ctx, actor)
			if err != nil || allowed != (mode != "explicit_session") {
				t.Fatalf("incorrect pre-recovery scheduling fence: %t %v", allowed, err)
			}
			w := session.NewWorkbench(ledger, nil)
			conversation := &admissionConversation{}
			runner := session.NewSessionRunner(w, conversation, nil, session.SessionRunnerOptions{
				StartupRecoveryAllowed: gateway.StartupRecoveryAllowed,
				Now:                    func() time.Time { return time.Now().Add(time.Minute) },
				Workspace:              startupYieldGuardWorkspace(w, budget),
			})
			defer runner.Close()
			if mode == "explicit_session" {
				err = runner.RecoverSession(ctx, 7, id)
			} else {
				err = (&managedContinuation{runner: runner}).Recover(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			for {
				run, err := runner.Run(ctx, id, runID)
				if err != nil {
					t.Fatal(err)
				}
				if run.Status == session.RunCompleted {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("authorized task was not recovered")
				case <-time.After(5 * time.Millisecond):
				}
			}
			if err := runner.Close(); err != nil {
				t.Fatal(err)
			}
			if allowed, err := gateway.StartupRecoveryAllowed(ctx, actor); err != nil || !allowed || conversation.calls.Load() != 1 {
				t.Fatalf("authorized recovery did not reacquire exactly once: %t %v", allowed, err)
			}
		})
	}
}

func TestServerStartupRecoveryRejectsInvalidPolicyWithoutSessionWrites(t *testing.T) {
	for _, mode := range []string{"malformed_admission", "callback_error", "nil_gateway"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ledgerPath, workspace := filepath.Join(t.TempDir(), "isolated.sqlite"), t.TempDir()
			id, _ := startupYieldGuardStoppedTask(t, ctx, ledgerPath, workspace)
			ledger, err := session.OpenSQLiteEventLog(ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			budget := admission.NewBudget(ledger)
			gateway := admission.NewGateway(budget, admission.ProviderConfig{})
			policy := gateway.StartupRecoveryAllowed
			switch mode {
			case "malformed_admission":
				events, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ledger.Append(ctx, session.VerificationAdmissionLedgerID, int64(len(events)), "admission/unrecognized-fixture", map[string]any{}); err != nil {
					t.Fatal(err)
				}
			case "callback_error":
				policy = func(context.Context, identity.Actor) (bool, error) {
					return false, errors.New("fixture admission unavailable")
				}
			case "nil_gateway":
				var missing *admission.Gateway
				policy = missing.StartupRecoveryAllowed
			}
			before, err := session.ReadVerifiedSnapshot(ctx, ledger, id)
			if err != nil {
				t.Fatal(err)
			}
			w := session.NewWorkbench(ledger, nil)
			conversation := &admissionConversation{}
			runner := session.NewSessionRunner(w, conversation, nil, session.SessionRunnerOptions{
				StartupRecoveryAllowed: policy,
				Now:                    func() time.Time { return time.Now().Add(time.Minute) },
				Workspace:              startupYieldGuardWorkspace(w, budget),
			})
			defer runner.Close()
			var partial *session.PartialRecoveryError
			if err := (&managedContinuation{runner: runner}).Recover(ctx); !errors.As(err, &partial) {
				t.Fatalf("unavailable policy did not fail closed: %v", err)
			}
			if err := runner.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := session.ReadVerifiedSnapshot(ctx, ledger, id)
			if err != nil || !reflect.DeepEqual(before.Events, after.Events) || conversation.calls.Load() != 0 {
				t.Fatalf("failed startup admission changed the Session: %v", err)
			}
		})
	}
}
