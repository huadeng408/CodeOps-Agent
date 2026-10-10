package handler

import (
	"code-agent/pkg/token"
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"testing"
)

func TestTaskWorkspaceHTTPKeepsOwnerAndCursorBoundaries(t *testing.T) {
	w, _ := openHandlerTestWorkbench(t)
	created, err := w.Create(context.Background(), 7, "repo", "title", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []uint{0, 8, 7} {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			if actor != 0 {
				c.Set("claims", &token.CustomClaims{UserID: actor})
			}
			c.Next()
		})
		h := NewTaskWorkspaceHandler(w, nil)
		router.GET("/sessions/:id/task-workspace", h.Inspect)
		router.POST("/sessions/:id/task-workspace/prepare", h.Prepare)
		response := performSessionRequest(router, http.MethodGet, "/sessions/"+created.ID+"/task-workspace", nil)
		want := map[uint]int{0: 401, 8: 404, 7: 200}[actor]
		if response.Code != want {
			t.Fatalf("owner %d GET = %d", actor, response.Code)
		}
		response = performSessionRequest(router, http.MethodPost, "/sessions/"+created.ID+"/task-workspace/prepare", map[string]any{"expectedSeq": 1, "requestId": "prepare"})
		if actor == 7 {
			want = 503
		}
		if response.Code != want {
			t.Fatalf("owner %d POST = %d", actor, response.Code)
		}
	}
}
