package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
)

type cancellationConversation struct{ started chan struct{} }

func (conversation cancellationConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, _ orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	close(conversation.started)
	<-ctx.Done()
	return orchestrator.ConversationResult{}, ctx.Err()
}

func TestCancelRunHTTPUsesAuthenticatedOwnerAndRetainsHistory(t *testing.T) {
	ctx := context.Background()
	workbench, _ := openHandlerTestWorkbench(t)
	created, err := workbench.Create(ctx, 7, "repo", "cancel", "respond")
	if err != nil {
		t.Fatal(err)
	}
	conversation := cancellationConversation{started: make(chan struct{})}
	runner := session.NewSessionRunner(workbench, conversation, nil, session.SessionRunnerOptions{})
	defer runner.Close()
	slot := session.NewContinuationSlot()
	slot.Attach(runner)
	router := messageSubmissionRouter(7, workbench, slot)
	router.POST("/sessions/:id/runs/:runId/cancel", NewEventHandlerWithContinuationSlot(workbench, slot).CancelRun)
	submitted := performSessionRequest(router, http.MethodPost, "/sessions/"+created.ID+"/messages", map[string]any{"requestId": "turn", "expectedSeq": 1, "content": "work"})
	var accepted struct {
		Data session.RunView `json:"data"`
	}
	if submitted.Code != http.StatusAccepted || json.Unmarshal(submitted.Body.Bytes(), &accepted) != nil {
		t.Fatalf("submit failed: %s", submitted.Body.String())
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("conversation did not start")
	}
	other := messageSubmissionRouter(8, workbench, slot)
	other.POST("/sessions/:id/runs/:runId/cancel", NewEventHandlerWithContinuationSlot(workbench, slot).CancelRun)
	path := "/sessions/" + created.ID + "/runs/" + accepted.Data.RunID + "/cancel"
	refused := performSessionRequest(other, http.MethodPost, path, map[string]any{"requestId": "foreign-cancel"})
	if refused.Code != http.StatusNotFound {
		t.Fatalf("another owner could cancel: %d", refused.Code)
	}
	canceled := performSessionRequest(router, http.MethodPost, path, map[string]any{"requestId": "owner-cancel"})
	if canceled.Code != http.StatusAccepted {
		t.Fatalf("cancel failed: %d %s", canceled.Code, canceled.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, err := workbench.Get(ctx, 7, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Run.Status == session.RunCanceled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("HTTP cancellation was not durable")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
