package handler

import (
	"code-agent/internal/session"
	"code-agent/pkg/token"
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
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

func TestProposalDecisionHTTPRejectsUnapprovedBodiesAndForeignOwner(t *testing.T) {
	w, ledger := openHandlerTestWorkbench(t)
	ctx := context.Background()
	repo, storage := t.TempDir(), t.TempDir()
	created, err := w.CreateWithWorkingDir(ctx, 7, "repo", "review", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	tasks := session.NewTaskWorkspaces(w, func(context.Context) (uint, error) { return 7, nil }, repo, storage)
	for _, test := range []struct {
		name  string
		owner uint
		body  map[string]any
		want  int
	}{
		{"missing approval", 7, map[string]any{"expectedSeq": 1, "requestId": "decision", "proposalId": strings.Repeat("a", 64)}, 400},
		{"injected root", 7, map[string]any{"expectedSeq": 1, "requestId": "decision", "proposalId": strings.Repeat("a", 64), "decision": "approve", "root": repo}, 400},
		{"injected content", 7, map[string]any{"expectedSeq": 1, "requestId": "decision", "proposalId": strings.Repeat("a", 64), "decision": "approve", "content": "overwrite"}, 400},
		{"foreign owner", 8, map[string]any{"expectedSeq": 1, "requestId": "decision", "proposalId": strings.Repeat("a", 64), "decision": "approve"}, 404},
		{"unprepared proposal", 7, map[string]any{"expectedSeq": 1, "requestId": "decision", "proposalId": strings.Repeat("a", 64), "decision": "approve"}, 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set("claims", &token.CustomClaims{UserID: test.owner}); c.Next() })
			router.POST("/sessions/:id/task-workspace/proposal/decision", NewTaskWorkspaceHandler(w, tasks).DecideProposal)
			response := performSessionRequest(router, http.MethodPost, "/sessions/"+created.ID+"/task-workspace/proposal/decision", test.body)
			if response.Code != test.want {
				t.Fatalf("HTTP=%d want=%d", response.Code, test.want)
			}
		})
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil || len(events) != 1 {
		t.Fatal("refused HTTP decision added effect facts")
	}
}
