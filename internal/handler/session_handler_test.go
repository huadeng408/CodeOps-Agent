package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"code-agent/internal/session"
	"code-agent/pkg/token"

	"github.com/gin-gonic/gin"
)

func openHandlerTestWorkbench(t *testing.T) (*session.Workbench, *session.SQLiteEventLog) {
	t.Helper()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatalf("open handler test ledger: %v", err)
	}
	t.Cleanup(func() {
		if err := ledger.Close(); err != nil {
			t.Errorf("close handler test ledger: %v", err)
		}
	})
	return session.NewWorkbench(ledger, nil), ledger
}

func sessionTestRouter(ownerID uint, workbench *session.Workbench) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("claims", &token.CustomClaims{UserID: ownerID, TokenType: token.TokenTypeAccess})
		c.Next()
	})
	sessions := router.Group("/sessions")
	sessionHandler := NewSessionHandler(workbench)
	eventHandler := NewEventHandler(workbench)
	sessions.POST("", sessionHandler.Create)
	sessions.GET("", sessionHandler.List)
	sessions.GET("/:id", sessionHandler.Get)
	sessions.PUT("/:id/title", sessionHandler.UpdateTitle)
	sessions.PUT("/:id/status", sessionHandler.UpdateStatus)
	sessions.DELETE("/:id", sessionHandler.Delete)
	sessions.GET("/:id/events", eventHandler.ListEvents)
	sessions.POST("/:id/events", eventHandler.CreateEvent)
	sessions.GET("/:id/checkpoints", eventHandler.ListCheckpoints)
	sessions.POST("/:id/checkpoints", eventHandler.CreateCheckpoint)
	sessions.POST("/:id/restore/:hash", eventHandler.RestoreCheckpoint)
	return router
}

func performSessionRequest(router http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			panic(err)
		}
	}
	request := httptest.NewRequest(method, path, &payload)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestSessionRoutesNeverAcceptOwnerZero(t *testing.T) {
	workbench, _ := openHandlerTestWorkbench(t)
	response := performSessionRequest(
		sessionTestRouter(0, workbench), http.MethodGet, "/sessions/missing", nil,
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("zero-owner status = %d, want 401; body=%s", response.Code, response.Body.String())
	}
}

func TestSessionRoutesReturnSame404ForForeignAndMissing(t *testing.T) {
	workbench, _ := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "title", "goal")
	if err != nil {
		t.Fatalf("create owner session: %v", err)
	}
	router := sessionTestRouter(8, workbench)
	foreign := performSessionRequest(router, http.MethodGet, "/sessions/"+created.ID, nil)
	missing := performSessionRequest(router, http.MethodGet, "/sessions/missing", nil)
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf("foreign=%d missing=%d; want 404", foreign.Code, missing.Code)
	}
	if foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign response %q enumerates differently from missing %q", foreign.Body.String(), missing.Body.String())
	}
}

func TestAppendEventRejectsBrowserSelectedAuthorAndType(t *testing.T) {
	workbench, ledger := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "title", "goal")
	if err != nil {
		t.Fatal(err)
	}
	response := performSessionRequest(sessionTestRouter(7, workbench), http.MethodPost, "/sessions/"+created.ID+"/events", map[string]any{
		"expectedSeq": int64(1),
		"content":     "forged",
		"author":      "assistant",
		"type":        "assistant/message",
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("forged event status = %d, want 400; body=%s", response.Code, response.Body.String())
	}
	events, err := ledger.Events(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("forged event changed canonical ledger: %+v", events)
	}
}

func TestAppendEventReturns409ForStaleExpectedSequence(t *testing.T) {
	workbench, ledger := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "title", "goal")
	if err != nil {
		t.Fatal(err)
	}
	router := sessionTestRouter(7, workbench)
	body := map[string]any{"expectedSeq": int64(1), "content": "one"}
	first := performSessionRequest(router, http.MethodPost, "/sessions/"+created.ID+"/events", body)
	if first.Code != http.StatusOK {
		t.Fatalf("first append status = %d; body=%s", first.Code, first.Body.String())
	}
	stale := performSessionRequest(router, http.MethodPost, "/sessions/"+created.ID+"/events", body)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale append status = %d, want 409; body=%s", stale.Code, stale.Body.String())
	}
	events, err := ledger.Events(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("stale append changed canonical ledger: %+v", events)
	}
}
