package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
)

type handlerResumeGatewayConversation struct {
	gateway *admission.Gateway
	results chan error
}

func (conversation handlerResumeGatewayConversation) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, _ orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	binding, release, err := conversation.gateway.BindActor(request.Actor, request.SessionID)
	if err == nil {
		defer release()
		var response *pb.ModelCallResponse
		response, err = conversation.gateway.Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability, CallId: request.RunID + ":model",
			PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"continue the task"}]}`)})
		if err == nil && response.GetErrorCode() != "" {
			err = errors.New(response.GetErrorCode())
		}
	}
	conversation.results <- err
	return orchestrator.ConversationResult{Success: err == nil, Message: "fixture response"}, err
}

func TestSubmitMessageLateFinishCannotDispatchOrDuplicateRun(t *testing.T) {
	ctx := context.Background()
	workbench, ledger := openHandlerTestWorkbench(t)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "fixture", "late close before continuation", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "user:7", TenantID: "tenant:user:7", Roles: []string{"USER"}, SessionID: created.ID}
	var upstreamPosts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamPosts.Add(1)
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":8,"completion_tokens":3}}`))
	}))
	defer upstream.Close()
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
		Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	if err := gateway.FinishTask(ctx, actor); err != nil {
		t.Fatal(err)
	}
	before, err := budget.Inspect(ctx, admission.ActorOwner(actor))
	if err != nil {
		t.Fatal(err)
	}
	conversation := handlerResumeGatewayConversation{gateway: gateway, results: make(chan error, 1)}
	runner := session.NewSessionRunner(workbench, conversation, nil, session.SessionRunnerOptions{
		ResumeTaskForMessage: func(ctx context.Context, actor identity.Actor) error {
			if err := gateway.ResumeTask(ctx, actor); err != nil {
				return err
			}
			// Reproduce the late close in each staging pass, including immediately before continuation.
			return gateway.FinishTask(ctx, actor)
		},
	})
	defer runner.Close()
	router := messageSubmissionRouter(7, workbench, runner)
	path := "/sessions/" + created.ID + "/messages"
	body := map[string]any{"requestId": "resume-with-late-close", "expectedSeq": 1, "content": "continue the same workload"}
	response := performSessionRequest(router, http.MethodPost, path, body)
	var accepted struct{ Data session.RunView }
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &accepted) != nil || accepted.Data.RunID == "" {
		t.Fatalf("message was not retained as one Run: %d %s", response.Code, response.Body.String())
	}
	select {
	case err := <-conversation.results:
		if !errors.Is(err, admission.ErrTaskClosed) && (err == nil || err.Error() != "code_task_closed") {
			t.Fatalf("late close did not fence the actual Gateway call: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the retained Run never attempted its Gateway call")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		run, err := runner.Run(ctx, created.ID, accepted.Data.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == session.RunFailed {
			break
		}
		if run.Status == session.RunCompleted || time.Now().After(deadline) {
			t.Fatalf("late close did not retain a known terminal failure: %+v", run)
		}
		time.Sleep(5 * time.Millisecond)
	}
	repeated := performSessionRequest(router, http.MethodPost, path, body)
	view, err := workbench.Get(ctx, 7, created.ID)
	if repeated.Code != http.StatusAccepted || !bytes.Contains(repeated.Body.Bytes(), []byte(accepted.Data.RunID)) || err != nil || len(view.Runs) != 1 || view.Runs[0].RunID != accepted.Data.RunID {
		t.Fatalf("same request duplicated the retained failed Run: status=%d view=%+v err=%v", repeated.Code, view, err)
	}
	after, err := budget.Inspect(ctx, admission.ActorOwner(actor))
	if err != nil || after.ID != before.ID || after.TaskID != "" || after.UsedTokens != 0 || after.ReservedTokens != 0 || after.ActiveCalls != 0 || upstreamPosts.Load() != 0 {
		t.Fatalf("closed admission reached model transport or reservation: %+v posts=%d err=%v", after, upstreamPosts.Load(), err)
	}
	facts, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range facts {
		if fact.Type == "admission/call_reserved" {
			t.Fatal("late close created an unadmitted model-call reservation")
		}
	}
}

func TestSubmitMessageResumesFinishedTaskAcrossStagedInterruption(t *testing.T) {
	for _, stage := range []string{"before resume", "after resume"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			workbench, ledger := openHandlerTestWorkbench(t)
			created, err := workbench.CreateWithWorkingDir(ctx, 7, "fixture", "resume retained intent", "", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "user:7", TenantID: "tenant:user:7", Roles: []string{"USER"}, SessionID: created.ID}
			budget := admission.NewBudget(ledger)
			gateway := admission.NewGateway(budget, admission.ProviderConfig{})
			if err := gateway.FinishTask(ctx, actor); err != nil {
				t.Fatal(err)
			}
			protected, err := workbench.CreateWithWorkingDir(ctx, 7, "fixture", "keep startup fenced", "", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			protectedActor := actor
			protectedActor.SessionID = protected.ID
			if _, err := budget.AcquireTask(ctx, admission.ActorOwner(actor), protected.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := budget.YieldTask(ctx, admission.ActorOwner(actor), protected.ID); err != nil {
				t.Fatal(err)
			}
			before, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
			if err != nil {
				t.Fatal(err)
			}
			runner := session.NewSessionRunner(workbench, handlerApprovalConversation{}, handlerApprovalTool{}, session.SessionRunnerOptions{
				ResumeTaskForMessage: func(ctx context.Context, actor identity.Actor) error {
					if stage == "before resume" {
						return session.ErrContinuationUnavailable
					}
					if err := gateway.ResumeTask(ctx, actor); err != nil {
						return err
					}
					return session.ErrContinuationUnavailable
				},
			})
			defer runner.Close()
			router := messageSubmissionRouter(7, workbench, runner)
			path := "/sessions/" + created.ID + "/messages"
			stale := performSessionRequest(router, http.MethodPost, path, map[string]any{"requestId": "stale", "expectedSeq": 0, "content": "do not reopen"})
			unchanged, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
			if stale.Code != http.StatusConflict || err != nil || len(unchanged) != len(before) {
				t.Fatalf("stale message reopened admission: status=%d facts=%d err=%v", stale.Code, len(unchanged), err)
			}
			body := map[string]any{"requestId": "next-planned-attempt", "expectedSeq": 1, "content": "continue the same workload"}
			interrupted := performSessionRequest(router, http.MethodPost, path, body)
			view, err := workbench.Get(ctx, 7, created.ID)
			if interrupted.Code != http.StatusServiceUnavailable || err != nil || view.EventCount != 2 || view.Run != nil {
				t.Fatalf("interrupted admission lost accepted intent or dispatched: status=%d view=%+v err=%v", interrupted.Code, view, err)
			}
			if err := runner.Close(); err != nil {
				t.Fatal(err)
			}
			restarted := session.NewSessionRunner(workbench, handlerApprovalConversation{}, handlerApprovalTool{}, session.SessionRunnerOptions{
				ResumeTaskForMessage: gateway.ResumeTask, StartupRecoveryAllowed: gateway.StartupRecoveryAllowed,
			})
			defer restarted.Close()
			if err := restarted.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			if allowed, err := gateway.StartupRecoveryAllowed(ctx, protectedActor); err != nil || allowed {
				t.Fatalf("restart removed the protected task's yielded fence: %t %v", allowed, err)
			}
			router = messageSubmissionRouter(7, workbench, restarted)
			response := performSessionRequest(router, http.MethodPost, path, body)
			var accepted struct{ Data session.RunView }
			if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &accepted) != nil || accepted.Data.RunID == "" {
				t.Fatalf("same request could not resume its retained intent: %d %s", response.Code, response.Body.String())
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				run, err := restarted.Run(ctx, created.ID, accepted.Data.RunID)
				if err != nil {
					t.Fatal(err)
				}
				if run.Status == session.RunCompleted {
					break
				}
				if run.Status == session.RunFailed || time.Now().After(deadline) {
					t.Fatalf("resumed request did not complete: %+v", run)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err := gateway.FinishTask(ctx, actor); err != nil {
				t.Fatal(err)
			}
			repeated := performSessionRequest(router, http.MethodPost, path, body)
			if repeated.Code != http.StatusAccepted || !bytes.Contains(repeated.Body.Bytes(), []byte(accepted.Data.RunID)) {
				t.Fatalf("completed request changed Run identity: %d %s", repeated.Code, repeated.Body.String())
			}
			batch, err := budget.Inspect(ctx, admission.ActorOwner(actor))
			if err != nil || batch.TaskID != "" {
				t.Fatalf("retry of completed request reopened the task: %+v %v", batch, err)
			}
			facts, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
			if err != nil {
				t.Fatal(err)
			}
			resumes := 0
			for _, fact := range facts {
				if fact.Type == "admission/task_resumed" {
					resumes++
				}
			}
			if resumes != 1 {
				t.Fatalf("one interrupted request appended %d resumes", resumes)
			}
			if allowed, err := gateway.StartupRecoveryAllowed(ctx, protectedActor); err != nil || allowed {
				t.Fatalf("explicit resume removed an unrelated yielded fence: %t %v", allowed, err)
			}
		})
	}
}
