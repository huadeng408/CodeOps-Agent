package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
	"code-agent/pkg/token"
	"github.com/gin-gonic/gin"
)

type handlerCanceledAdmissionConversation struct{ started chan struct{} }

func (conversation handlerCanceledAdmissionConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, _ orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	close(conversation.started)
	<-ctx.Done()
	return orchestrator.ConversationResult{}, ctx.Err()
}

func TestModelTaskAbandonRoutePreservesSessionAndOwnerBoundary(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		owner   uint
		gateway bool
		status  int
	}{
		{"owner", 7, true, http.StatusOK},
		{"anonymous", 0, true, http.StatusUnauthorized},
		{"foreign owner", 8, true, http.StatusNotFound},
		{"unavailable", 7, false, http.StatusServiceUnavailable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			workbench, ledger := openHandlerTestWorkbench(t)
			created, err := workbench.Create(ctx, 7, "fixture", "canceled admission", "")
			if err != nil {
				t.Fatal(err)
			}
			actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "user:7", TenantID: "tenant:user:7", Roles: []string{"USER"}, SessionID: created.ID}
			conversation := handlerCanceledAdmissionConversation{started: make(chan struct{})}
			runner := session.NewSessionRunner(workbench, conversation, nil, session.SessionRunnerOptions{})
			defer runner.Close()
			run, err := runner.SubmitMessage(ctx, session.SubmitMessageCommand{RequestID: "hold", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "hold", Actor: actor})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-conversation.started:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not start")
			}
			if _, err := runner.CancelRun(ctx, session.CancelRunCommand{RequestID: "cancel", SessionID: created.ID, RunID: run.RunID, OwnerID: 7, Actor: actor}); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				current, err := runner.Run(ctx, created.ID, run.RunID)
				if err != nil {
					t.Fatal(err)
				}
				if current.Status == session.RunCanceled {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("cancellation did not complete")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err := runner.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			budget := admission.NewBudget(ledger)
			owner := admission.ActorOwner(actor)
			if _, err := budget.Reserve(ctx, owner, created.ID, "retained-call", 103); err != nil {
				t.Fatal(err)
			}
			if _, err := budget.MarkUnknown(ctx, owner, "retained-call"); err != nil {
				t.Fatal(err)
			}
			var gateway *admission.Gateway
			if scenario.gateway {
				gateway = admission.NewGateway(budget, admission.ProviderConfig{})
			}
			gin.SetMode(gin.TestMode)
			router := gin.New()
			if scenario.owner != 0 {
				router.Use(func(c *gin.Context) {
					c.Set("claims", &token.CustomClaims{UserID: scenario.owner, TokenType: token.TokenTypeAccess})
					c.Next()
				})
			}
			router.POST("/sessions/:id/task/abandon", NewModelTaskHandler(workbench, gateway).Abandon)
			response := performSessionRequest(router, http.MethodPost, "/sessions/"+created.ID+"/task/abandon", nil)
			if response.Code != scenario.status {
				t.Fatalf("status=%d, want %d: %s", response.Code, scenario.status, response.Body.String())
			}
			after, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
			if err != nil || len(after.Events) != len(before.Events) {
				t.Fatal("operator route changed Session facts")
			}
			for i := range before.Events {
				if after.Events[i].Checksum != before.Events[i].Checksum {
					t.Fatal("operator route rewrote Session history")
				}
			}
			batch, err := budget.Inspect(ctx, owner)
			if err != nil || batch.UsedTokens != 0 || batch.ReservedTokens != 103 || !batch.UnknownUsage {
				t.Fatal("operator route changed accounting")
			}
			if scenario.status == http.StatusOK {
				var payload struct {
					Data struct {
						SessionID string `json:"sessionId"`
						State     string `json:"admissionState"`
					} `json:"data"`
				}
				if json.Unmarshal(response.Body.Bytes(), &payload) != nil || payload.Data.SessionID != created.ID || payload.Data.State != "abandoned" || batch.TaskID != "" {
					t.Fatal("operator route did not distinguish admission retirement")
				}
			} else if batch.TaskID != created.ID {
				t.Fatal("rejected operator route changed the task slot")
			}
		})
	}
}
