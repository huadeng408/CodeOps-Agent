package session_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/safety"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
	"code-agent/internal/tools"
)

type pythonProposalRuntime func(context.Context, sandbox.Request) (sandbox.Result, error)

func (run pythonProposalRuntime) Run(ctx context.Context, request sandbox.Request) (sandbox.Result, error) {
	return run(ctx, request)
}

type pythonProposalConversation struct{ result chan orchestrator.ToolResult }

func (conversation pythonProposalConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	result := handlers.Tool(ctx, orchestrator.ToolCall{ID: "python-edit", Name: "Edit", ParametersJSON: `{"path":"interval.py","old_string":"INTERVAL = 2","new_string":"INTERVAL = 3"}`})
	conversation.result <- result
	return orchestrator.ConversationResult{Success: result.ExitCode == 0 && result.Error == "", Message: "Python change is ready for review"}, nil
}

func TestPythonProposalTestsFrozenChangesWithApprovedDependency(t *testing.T) {
	ctx := context.Background()
	tasks, workbench, ledger, repo, storage, sid, dependency := preparePythonProposalTask(t)
	index, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	// The external runtime returns a known result; preparation, editing and the Ledger are real.
	runs := 0
	tasks.WithBaselineRuntime(pythonProposalProfile(t, ledger, pythonProposalRuntime(func(_ context.Context, request sandbox.Request) (sandbox.Result, error) {
		runs++
		if request.Program != "python" || request.DependencyOperationID != dependency || request.FrozenInput == nil || request.WorkingDir != "" || request.Command != "" || len(request.Args) != 2 || request.Args[0] != "-c" || !strings.Contains(request.Args[1], "'--no-index','--no-deps'") || !strings.Contains(request.Args[1], "'/deps/pytz-2026.5-py2.py3-none-any.whl'") || !strings.HasSuffix(request.Args[1], "[sys.executable,'-m','unittest','discover','-v'])") {
			t.Fatalf("proposal left the approved Python execution contract: %+v", request)
		}
		return sandbox.Result{ExitCode: 0, Output: "Ran 1 test\nOK", Stopped: true, RuntimeID: "fixture-python", InputSHA256: request.FrozenInput.SourceSHA256}, nil
	})))
	editPythonProposalTask(t, tasks, workbench, storage, sid)
	if _, err := admission.NewBudget(ledger).AcquireTask(ctx, 7, sid); err != nil {
		t.Fatal(err)
	}
	current, err := tasks.Inspect(ctx, 7, sid)
	if err != nil {
		t.Fatal(err)
	}
	view, err := tasks.GenerateProposal(ctx, 7, sid, int64(current.EventCount), "python-proposal")
	if err != nil || view.Proposal == nil || view.Proposal.State != "ready" || !slices.Equal(view.Proposal.Test.Command, []string{"python", "-m", "unittest", "discover", "-v"}) || view.Proposal.Test.InputSHA256 != view.Proposal.FinalStateSHA256 || view.Proposal.Test.ArchiveSHA256 == "" || runs != 1 {
		t.Fatalf("Python proposal did not test the frozen change: %+v runs=%d err=%v", view, runs, err)
	}
	content, _ := os.ReadFile(filepath.Join(repo, "interval.py"))
	afterIndex, _ := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if string(content) != "INTERVAL = 2\n" || !bytes.Equal(index, afterIndex) {
		t.Fatal("proposal changed the dirty original or index")
	}
	reopened := session.NewTaskWorkspaces(session.NewWorkbench(ledger, nil), func(context.Context) (uint, error) { return 7, nil }, repo, storage)
	again, err := reopened.GenerateProposal(ctx, 7, sid, int64(current.EventCount), "python-proposal")
	if err != nil || again.Proposal == nil || again.Proposal.ID != view.Proposal.ID || again.EventCount != view.EventCount || runs != 1 {
		t.Fatalf("reopened Python proposal lost its retained identity or ran again: %+v %v", again, err)
	}
	if _, err := reopened.DecideProposal(ctx, 8, sid, int64(again.EventCount), "apply", view.Proposal.ID, "approve"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatal("foreign owner approved a Python proposal")
	}
	applied, err := reopened.DecideProposal(ctx, 7, sid, int64(again.EventCount), "apply", view.Proposal.ID, "approve")
	if err != nil || applied.Application == nil || applied.Application.State != "applied" {
		t.Fatalf("Python proposal did not use the existing application boundary: %+v %v", applied, err)
	}
	content, _ = os.ReadFile(filepath.Join(repo, "interval.py"))
	afterIndex, _ = os.ReadFile(filepath.Join(repo, ".git", "index"))
	if string(content) != "INTERVAL = 3\n" || !bytes.Equal(index, afterIndex) {
		t.Fatal("approved Python content or index was not preserved")
	}
}

func TestPythonProposalRefusesChangedApprovedDependency(t *testing.T) {
	ctx := context.Background()
	tasks, workbench, ledger, _, storage, sid, dependency := preparePythonProposalTask(t)
	runs := 0
	tasks.WithBaselineRuntime(pythonProposalProfile(t, ledger, pythonProposalRuntime(func(context.Context, sandbox.Request) (sandbox.Result, error) {
		runs++
		return sandbox.Result{}, nil
	})))
	editPythonProposalTask(t, tasks, workbench, storage, sid)
	wheel := filepath.Join(storage, "dependency-"+dependency, sandbox.PytzName)
	if err := os.Chmod(wheel, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wheel, []byte("unapproved replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	view, err := tasks.Inspect(ctx, 7, sid)
	if err != nil || view.Dependency.State != "blocked" || view.ProposalAvailable {
		t.Fatalf("changed dependency was admitted: %+v %v", view, err)
	}
	refused, err := tasks.GenerateProposal(ctx, 7, sid, int64(view.EventCount), "invalid-dependency")
	if !errors.Is(err, session.ErrSessionStateConflict) || refused.Proposal != nil || refused.EventCount != view.EventCount || runs != 0 {
		t.Fatalf("changed dependency produced a test or proposal: %+v runs=%d err=%v", refused, runs, err)
	}
}

func preparePythonProposalTask(t *testing.T) (*session.TaskWorkspaces, *session.Workbench, *session.SQLiteEventLog, string, string, string, string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("native Windows task preparation contract")
	}
	wheelPath := os.Getenv("CODE_AGENT_TEST_PYTZ_WHEEL")
	if wheelPath == "" {
		t.Skip("set CODE_AGENT_TEST_PYTZ_WHEEL to the approved cached wheel; tests never download dependencies")
	}
	wheel, err := os.ReadFile(wheelPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(wheel)
	if len(wheel) != sandbox.PytzSize || hex.EncodeToString(digest[:]) != sandbox.PytzSHA256 {
		t.Fatal("test wheel does not match the approved artifact")
	}
	repo, storage := t.TempDir(), t.TempDir()
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", safety.HardenedGitArgs(repo, args[0], args[1:])...)
		command.Env = safety.ScrubGitEnvironment(os.Environ())
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("Git fixture: %s %v", output, err)
		}
	}
	git("init")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.test")
	for name, content := range map[string]string{"setup.py": "# Python project fixture\n", "interval.py": "INTERVAL = 1\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	git("commit", "-m", "fixture")
	if err := os.WriteFile(filepath.Join(repo, "interval.py"), []byte("INTERVAL = 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.CreateWithWorkingDir(context.Background(), 7, "repo", "Python proposal", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	tasks := session.NewTaskWorkspaces(workbench, func(context.Context) (uint, error) { return 7, nil }, repo, storage)
	profile := pythonProposalProfile(t, ledger, nil)
	profile.DependencyClient = &http.Client{Transport: dependencyTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != sandbox.PytzSource {
			return nil, errors.New("unapproved dependency request")
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(wheel)), Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(wheel))}, nil
	})}
	tasks.WithBaselineRuntime(profile)
	view, err := tasks.Prepare(context.Background(), 7, created.ID, 1, "prepare")
	if err != nil || view.State != "prepared" {
		t.Fatalf("prepare: %+v %v", view, err)
	}
	view, err = tasks.PrepareDependency(context.Background(), 7, created.ID, int64(view.EventCount), "dependency")
	if err != nil || view.Dependency == nil || view.Dependency.State != "ready" {
		t.Fatalf("dependency: %+v %v", view, err)
	}
	return tasks, workbench, ledger, repo, storage, created.ID, view.Dependency.OperationID
}

func pythonProposalProfile(t *testing.T, ledger session.EventLog, runner sandbox.Runner) session.TaskBaselineRuntime {
	t.Helper()
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	return session.TaskBaselineRuntime{Toolchain: "python", Runner: runner, Ready: func(context.Context) error { return nil },
		Acquire: func(ctx context.Context, owner uint, id string) error {
			_, err := budget.AcquireTask(ctx, owner, id)
			return err
		},
		Finish: func(ctx context.Context, owner uint, id string) error {
			_, err := budget.FinishTask(ctx, owner, id)
			return err
		},
		BeginProposal: gateway.BindExistingTask}
}

func editPythonProposalTask(t *testing.T, tasks *session.TaskWorkspaces, workbench *session.Workbench, storage, sid string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	view, err := tasks.Inspect(ctx, 7, sid)
	if err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(filepath.Join(storage, view.WorkspaceID))
	t.Cleanup(func() { _ = executor.Close() })
	conversation := pythonProposalConversation{result: make(chan orchestrator.ToolResult, 1)}
	runner := session.NewSessionRunner(workbench, conversation, session.ToolExecutionFunc(func(ctx context.Context, _ identity.Actor, _ string, call orchestrator.ToolCall) orchestrator.ToolResult {
		var arguments map[string]any
		if err := json.Unmarshal([]byte(call.ParametersJSON), &arguments); err != nil {
			return orchestrator.ToolResult{Error: err.Error(), ExitCode: 1}
		}
		result, err := executor.Execute(ctx, tools.ToolRequest{Name: call.Name, Arguments: arguments, OperationID: call.OperationID, OwnerSessionID: sid})
		out := orchestrator.ToolResult{ExitCode: int32(result.ExitCode), Output: result.Output, Error: result.Error}
		if err != nil {
			out.Error = err.Error()
		}
		for _, change := range result.Changes {
			out.Changes = append(out.Changes, orchestrator.CodeChange{Path: change.Path, Before: change.Before, After: change.After, BeforeExists: change.BeforeExists, AfterExists: change.AfterExists})
		}
		return out
	}), session.SessionRunnerOptions{WorkerID: "python-proposal", LeaseDuration: time.Minute, Workspace: tasks.ModelWorkspace})
	t.Cleanup(func() { _ = runner.Close() })
	current, err := workbench.Get(ctx, 7, sid)
	if err != nil {
		t.Fatal(err)
	}
	run, err := runner.SubmitMessage(ctx, session.SubmitMessageCommand{RequestID: "edit", SessionID: sid, OwnerID: 7, ExpectedSeq: int64(current.EventCount), Content: "Update interval.py for the fixture", Actor: identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "alice", TenantID: "org:one", Roles: []string{"USER"}}})
	if err != nil {
		t.Fatalf("submit public Edit: %v", err)
	}
	select {
	case result := <-conversation.result:
		if result.ExitCode != 0 || result.Error != "" {
			t.Fatalf("Go Edit failed: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for {
		state, err := runner.Run(ctx, sid, run.RunID)
		if err != nil || state.Status == session.RunFailed || ctx.Err() != nil {
			t.Fatalf("Edit run did not complete: %+v %v", state, err)
		}
		if state.Status == session.RunCompleted {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
