package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/handler"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/safety"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
	"code-agent/pkg/token"
	"github.com/gin-gonic/gin"
)

type admissionConversation struct{ calls atomic.Int32 }

func (a *admissionConversation) RunConversation(context.Context, orchestrator.ConversationRequest, orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	a.calls.Add(1)
	return orchestrator.ConversationResult{Success: true, Message: "fixture conversation resumed"}, nil
}

func TestBrowserContinuationWaitsForActiveReflectionWithoutUsageReconciliation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first native Git directory pin contract")
	}
	ctx := context.Background()
	repo, storage := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.test"}, {"add", "."}, {"commit", "-qm", "fixture"}} {
		command := exec.Command("git", safety.HardenedGitArgs(repo, args[0], args[1:])...)
		command.Env = safety.ScrubGitEnvironment(os.Environ())
		if err := command.Run(); err != nil {
			t.Fatal(err)
		}
	}
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	w := session.NewWorkbench(ledger, nil)
	created, err := w.CreateWithWorkingDir(ctx, 7, "repo", "reflection occupancy", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	tasks := session.NewTaskWorkspaces(w, func(context.Context) (uint, error) { return 7, nil }, repo, storage).WithBaselineRuntime(session.TaskBaselineRuntime{
		Runner: sandbox.NewOpenSandboxRunner(sandbox.Config{TrustRoot: storage}), Ready: func(context.Context) error { return nil },
		Acquire: func(context.Context, uint, string) error { return nil }, Finish: func(context.Context, uint, string) error { return nil },
	})
	prepared, err := tasks.Prepare(ctx, 7, created.ID, 1, "prepare")
	if err != nil || prepared.State != "prepared" {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	message, err := w.AppendUserMessage(ctx, 7, created.ID, 3, "resume this intent")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := w.CreateCheckpoint(ctx, 7, created.ID, 4, message.ID, "continue")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.UpdateStatus(ctx, 7, created.ID, 5, "paused"); err != nil {
		t.Fatal(err)
	}
	entered, finish := make(chan struct{}), make(chan struct{})
	var providerCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		r.Body.Close()
		providerCalls.Add(1)
		close(entered)
		select {
		case <-finish:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"model":"fixture-model","choices":[{"message":{"content":"reflection"}}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`)
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	budget := admission.NewBudget(ledger)
	executors := newContinuationToolExecutors(storage)
	defer executors.Close()
	executors.taskWorkspaces, executors.taskOwner = tasks, func(context.Context) (uint, error) { return 7, nil }
	gate := localModelWorkspaceAdmission(tasks, budget, executors.PrepareWorkspace)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-authority", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	gateway.SetTaskAdmission(func(ctx context.Context, id string) error { _, err := gate(ctx, 7, id); return err })
	binding, release, err := gateway.Bind(7, created.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	reflectionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan *pb.ModelCallResponse, 1)
	go func() {
		response, _ := gateway.Invoke(reflectionCtx, &pb.ModelCallRequest{Capability: binding.Capability, CallId: "live-reflection", Purpose: "memory_reflection",
			PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"reflect"}]}`)})
		done <- response
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("reflection did not enter the boundary fixture")
	}
	adapter := &admissionConversation{}
	runner := session.NewSessionRunner(w, adapter, nil, session.SessionRunnerOptions{WorkerID: "http-reflection", Workspace: gate})
	defer runner.Close()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("claims", &token.CustomClaims{UserID: 7, TokenType: token.TokenTypeAccess})
		c.Next()
	})
	router.POST("/sessions/:id/continue", handler.NewEventHandler(w, runner).ContinueSession)
	body, _ := json.Marshal(map[string]any{"requestId": "after-reflection", "expectedSeq": 6, "checkpointHash": checkpoint.Hash})
	post := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/sessions/"+created.ID+"/continue", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, req)
		return response
	}
	rejected := post()
	if rejected.Code != http.StatusConflict || !strings.Contains(rejected.Body.String(), "active model") || strings.Contains(rejected.Body.String(), "reconciliation") {
		t.Fatalf("live reflection was misclassified: %d %s", rejected.Code, rejected.Body.String())
	}
	view, err := w.Get(ctx, 7, created.ID)
	active, budgetErr := budget.Inspect(ctx, 7)
	if err != nil || budgetErr != nil || view.EventCount != 6 || view.Run != nil || active.ActiveCalls != 1 || active.ReservedTokens != 103 || active.UnknownUsage || adapter.calls.Load() != 0 {
		t.Fatal("rejected continuation changed the intent, run or reflection reservation")
	}
	close(finish)
	select {
	case response := <-done:
		if response == nil || response.ErrorCode != "" {
			t.Fatalf("reflection did not settle: %+v", response)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reflection did not finish")
	}
	accepted := post()
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("same request did not resume after reflection: %d %s", accepted.Code, accepted.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		view, err = w.Get(ctx, 7, created.ID)
		if err == nil && view.Run != nil && view.Run.Status == session.RunCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("accepted continuation did not complete: %+v %v", view, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if duplicate := post(); duplicate.Code != http.StatusAccepted {
		t.Fatalf("duplicate request was not idempotent: %d", duplicate.Code)
	}
	after, err := budget.Inspect(ctx, 7)
	if err != nil || after.ActiveCalls != 0 || after.UsedTokens != 10 || after.ReservedTokens != 0 || after.UnknownUsage || providerCalls.Load() != 1 || adapter.calls.Load() != 1 {
		t.Fatalf("resume reset usage or repeated work: %+v %v", after, err)
	}
}

func TestWorkspacePreparationFailurePreventsModelAndContinuationDispatch(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first native task workspace pin contract")
	}
	ctx := context.Background()
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
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "preparation failure", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	tasks := session.NewTaskWorkspaces(workbench, func(context.Context) (uint, error) { return 7, nil }, repo, storage).WithBaselineRuntime(session.TaskBaselineRuntime{
		Runner: sandbox.NewOpenSandboxRunner(sandbox.Config{TrustRoot: storage}), Ready: func(context.Context) error { return nil },
		Acquire: func(context.Context, uint, string) error { return nil }, Finish: func(context.Context, uint, string) error { return nil },
	})
	prepared, err := tasks.Prepare(ctx, 7, created.ID, 1, "prepare")
	if err != nil || prepared.State != "prepared" {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	preparationErr := errors.New("workspace resources unavailable")
	budget := admission.NewBudget(ledger)
	gate := localModelWorkspaceAdmission(tasks, budget, func(context.Context, string, string) error { return preparationErr })
	var providerCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		fmt.Fprint(w, `{"model":"fixture-model","usage":{"prompt_tokens":7,"completion_tokens":3}}`)
	}))
	defer upstream.Close()
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-authority", InputLimit: 100, OutputLimit: 3})
	gateway.SetTaskAdmission(func(ctx context.Context, id string) error { _, err := gate(ctx, 7, id); return err })
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.Bind(7, created.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	response, err := gateway.Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability, CallId: "blocked-preparation", PayloadJson: []byte(`{"model":"fixture-model","messages":[]}`)})
	if err != nil || response.GetErrorCode() != "task_prerequisites_missing" || providerCalls.Load() != 0 {
		t.Errorf("preparation failure reached model dispatch: code=%s calls=%d err=%v", response.GetErrorCode(), providerCalls.Load(), err)
	}
	conversation := &admissionConversation{}
	runner := session.NewSessionRunner(workbench, conversation, nil, session.SessionRunnerOptions{Workspace: gate})
	defer runner.Close()
	_, err = runner.SubmitMessage(ctx, session.SubmitMessageCommand{RequestID: "blocked-preparation", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(prepared.EventCount), Content: "continue", Actor: identity.Default()})
	if !errors.Is(err, preparationErr) {
		t.Errorf("continuation did not retain preparation failure: %v", err)
	}
	view, viewErr := workbench.Get(ctx, 7, created.ID)
	batch, budgetErr := budget.Inspect(ctx, 7)
	if viewErr != nil || budgetErr != nil || view.Run != nil || view.EventCount != prepared.EventCount || conversation.calls.Load() != 0 || batch.ID != "" || batch.ActiveCalls != 0 || batch.ReservedTokens != 0 || batch.UsedTokens != 0 {
		t.Fatalf("preparation failure changed the run or accounting: view=%+v batch=%+v errors=%v/%v", view, batch, viewErr, budgetErr)
	}
	for name, unavailable := range map[string]func(context.Context, uint, string) (session.TaskModelWorkspace, error){
		"missing workspace":   localModelWorkspaceAdmission(nil, budget, func(context.Context, string, string) error { return preparationErr }),
		"missing budget":      localModelWorkspaceAdmission(tasks, nil, func(context.Context, string, string) error { return preparationErr }),
		"missing preparation": localModelWorkspaceAdmission(tasks, budget, nil),
	} {
		t.Run(name, func(t *testing.T) {
			workspace, err := unavailable(ctx, 7, created.ID)
			if !errors.Is(err, session.ErrContinuationUnavailable) || workspace != (session.TaskModelWorkspace{}) {
				t.Fatalf("incomplete admission returned a workspace: %+v %v", workspace, err)
			}
		})
	}
}
