package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/safety"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
)

func TestReopenedTaskCanContinueAfterItsModelHolderProcessDied(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first task workspace contract")
	}
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	repo, storage := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.test"}, {"add", "."}, {"commit", "-qm", "fixture"}} {
		command := exec.CommandContext(ctx, "git", safety.HardenedGitArgs(repo, args[0], args[1:])...)
		command.Env = safety.ScrubGitEnvironment(os.Environ())
		if err := command.Run(); err != nil {
			t.Fatal(err)
		}
	}
	ledgerPath := filepath.Join(t.TempDir(), "ledger.sqlite")
	ledger, err := session.OpenSQLiteEventLog(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ledger.Close() }()
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "restart", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	newTasks := func() *session.TaskWorkspaces {
		return session.NewTaskWorkspaces(workbench, func(context.Context) (uint, error) { return 7, nil }, repo, storage).WithBaselineRuntime(session.TaskBaselineRuntime{
			Runner: sandbox.NewOpenSandboxRunner(sandbox.Config{TrustRoot: storage}), Ready: func(context.Context) error { return nil },
			Acquire: func(context.Context, uint, string) error { return nil }, Finish: func(context.Context, uint, string) error { return nil },
		})
	}
	tasks := newTasks()
	prepared, err := tasks.Prepare(ctx, 7, created.ID, 1, "prepare")
	if err != nil || prepared.State != "prepared" {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	holder := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecoveryModelHolderHelper$", "-test.v")
	holder.Env = append(os.Environ(), "CODE_AGENT_RECOVERY_MODEL_DB="+ledgerPath, "CODE_AGENT_RECOVERY_MODEL_SESSION="+created.ID)
	output, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if holder.ProcessState == nil {
			_ = holder.Process.Kill()
			_ = holder.Wait()
		}
	}()
	scanner := bufio.NewScanner(output)
	ready := false
	for scanner.Scan() {
		if scanner.Text() == "MODEL_HOLDER_READY" {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("model holder did not reserve its call")
	}
	budget := admission.NewBudget(ledger)
	before, err := budget.Inspect(ctx, 7)
	if err != nil || before.ActiveCalls != 1 || before.ReservedTokens != 103 {
		t.Fatalf("active fixture: %+v %v", before, err)
	}
	actor := identity.Default()
	command := session.SubmitMessageCommand{RequestID: "after-restart", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(prepared.EventCount), Content: "continue", Actor: actor}
	executors := newContinuationToolExecutors(storage)
	defer executors.Close()
	executors.taskWorkspaces, executors.taskOwner = tasks, func(context.Context) (uint, error) { return 7, nil }
	runner := session.NewSessionRunner(workbench, &admissionConversation{}, nil, session.SessionRunnerOptions{Workspace: localModelWorkspaceAdmission(tasks, budget, executors.PrepareWorkspace)})
	if _, err := runner.SubmitMessage(ctx, command); !errors.Is(err, admission.ErrRelayBusy) {
		t.Fatalf("live model holder was not fenced: %v", err)
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()
	if _, err := localModelWorkspaceAdmission(tasks, budget, executors.PrepareWorkspace)(ctx, 8, created.ID); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("foreign owner reached recovery: %v", err)
	}
	foreign, err := budget.Inspect(ctx, 7)
	if err != nil || foreign.ActiveCalls != 1 || foreign.UnknownUsage {
		t.Fatalf("foreign owner changed recovery facts: %+v %v", foreign, err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	ledger, err = session.OpenSQLiteEventLog(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	workbench = session.NewWorkbench(ledger, nil)
	tasks = newTasks()
	executors.taskWorkspaces = tasks
	budget = admission.NewBudget(ledger)
	adapter := &admissionConversation{}
	runner = session.NewSessionRunner(workbench, adapter, nil, session.SessionRunnerOptions{Workspace: localModelWorkspaceAdmission(tasks, budget, executors.PrepareWorkspace)})
	defer runner.Close()
	if _, err := runner.SubmitMessage(ctx, command); err != nil {
		t.Fatalf("confirmed dead model holder prevented the same task from continuing: %v", err)
	}
	after, err := budget.Inspect(ctx, 7)
	if err != nil || after.ID != before.ID || after.TaskID != created.ID || after.TaskOwnerID != before.TaskOwnerID || after.UsedTokens != before.UsedTokens || after.ReservedTokens != before.ReservedTokens || after.ActiveCalls != 0 || !after.UnknownUsage {
		t.Fatalf("recovery reset task or accounting: %+v %v", after, err)
	}
	if err := ledger.Verify(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryModelHolderHelper(t *testing.T) {
	ledgerPath := os.Getenv("CODE_AGENT_RECOVERY_MODEL_DB")
	if ledgerPath == "" {
		t.Skip("owned model process helper")
	}
	ledger, err := session.OpenSQLiteEventLog(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		fmt.Println("MODEL_HOLDER_READY")
		<-request.Context().Done()
	}))
	defer upstream.Close()
	gateway := admission.NewGateway(admission.NewBudget(ledger), admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	actor, err := identity.Default().BindSession(os.Getenv("CODE_AGENT_RECOVERY_MODEL_SESSION"))
	if err != nil {
		t.Fatal(err)
	}
	binding, release, err := gateway.BindActor(actor, actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, _ = gateway.Invoke(context.Background(), &pb.ModelCallRequest{Capability: binding.Capability, CallId: "interrupted-model-call", PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"fixture"}]}`)})
}
