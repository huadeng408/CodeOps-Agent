package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"code-agent/internal/session"
	"code-agent/internal/worktree"
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

func sessionTestRouter(ownerID uint, workbench *session.Workbench, managers ...*worktree.Manager) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("claims", &token.CustomClaims{UserID: ownerID, TokenType: token.TokenTypeAccess})
		c.Next()
	})
	sessions := router.Group("/sessions")
	var manager *worktree.Manager
	if len(managers) > 0 {
		manager = managers[0]
	}
	sessionHandler := NewSessionHandlerWithWorktree(workbench, nil, manager)
	eventHandler := NewEventHandler(workbench)
	sessions.POST("", sessionHandler.Create)
	sessions.GET("", sessionHandler.List)
	sessions.GET("/:id", sessionHandler.Get)
	sessions.GET("/:id/runs", sessionHandler.RunHistory)
	sessions.GET("/:id/recovery-manifest", sessionHandler.RecoveryManifest)
	sessions.GET("/:id/workspace-manifest", sessionHandler.WorkspaceManifest)
	sessions.PUT("/:id/title", sessionHandler.UpdateTitle)
	sessions.PUT("/:id/status", sessionHandler.UpdateStatus)
	sessions.DELETE("/:id", sessionHandler.Delete)
	sessions.GET("/:id/events", eventHandler.ListEvents)
	sessions.POST("/:id/events", eventHandler.CreateEvent)
	sessions.GET("/:id/checkpoints", eventHandler.ListCheckpoints)
	sessions.POST("/:id/checkpoints", eventHandler.CreateCheckpoint)
	sessions.POST("/:id/restore/:hash", eventHandler.RestoreCheckpoint)
	sessions.POST("/:id/continue", eventHandler.ContinueSession)
	return router
}

func TestWorkspaceManifestIsSessionScopedAndReportsUnavailableCleanly(t *testing.T) {
	workbench, _ := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "workspace", "goal")
	if err != nil {
		t.Fatal(err)
	}
	manager := worktree.NewManager(t.TempDir(), "HEAD")
	router := sessionTestRouter(7, workbench, manager)
	response := performSessionRequest(router, http.MethodGet, "/sessions/"+created.ID+"/workspace-manifest", nil)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"available":false`)) {
		t.Fatalf("workspace manifest status=%d body=%s", response.Code, response.Body.String())
	}
	foreign := performSessionRequest(sessionTestRouter(8, workbench, manager), http.MethodGet, "/sessions/"+created.ID+"/workspace-manifest", nil)
	missing := performSessionRequest(router, http.MethodGet, "/sessions/missing/workspace-manifest", nil)
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign=%d missing=%d; bodies must match", foreign.Code, missing.Code)
	}
}

func TestWorkspaceManifestIncludesBoundWorktreeDiff(t *testing.T) {
	root := t.TempDir()
	if _, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	path := filepath.Join(root, ".agent", "worktrees", "resume")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Command("git", "-C", path, "init").CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "changed.txt"), []byte("pending\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := worktree.NewManager(root, "HEAD")
	manager.Restore([]worktree.Worktree{{Name: "resume", Path: path, ParentSessionID: "placeholder"}})
	workbench, _ := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "workspace", "goal")
	if err != nil {
		t.Fatal(err)
	}
	manager.Restore([]worktree.Worktree{{Name: "resume", Path: path, ParentSessionID: created.ID}})
	response := performSessionRequest(sessionTestRouter(7, workbench, manager), http.MethodGet, "/sessions/"+created.ID+"/workspace-manifest", nil)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"available":true`)) || !bytes.Contains(response.Body.Bytes(), []byte("changed.txt")) {
		t.Fatalf("workspace diff missing: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRecoveryManifestReturnsLedgerBoundResumeSummary(t *testing.T) {
	workbench, _ := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "recovery", "goal")
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(context.Background(), 7, created.ID, 1, "resume me")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(context.Background(), 7, created.ID, 2, message.ID, "anchor")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workbench.RestoreCheckpoint(context.Background(), 7, created.ID, checkpoint.Hash, 3); err != nil {
		t.Fatal(err)
	}
	router := sessionTestRouter(7, workbench)
	response := performSessionRequest(router, http.MethodGet, "/sessions/"+created.ID+"/recovery-manifest", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("manifest status = %d; body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			LedgerSeq         int64  `json:"ledgerSeq"`
			LatestLedgerHash  string `json:"latestLedgerHash"`
			EventCount        int    `json:"eventCount"`
			RewindCount       int    `json:"rewindCount"`
			ContinuationCount int    `json:"continuationCount"`
			CheckpointCount   int    `json:"checkpointCount"`
			LatestRecovery    *struct {
				Type string `json:"type"`
				Seq  int64  `json:"seq"`
			} `json:"latestRecovery"`
			CurrentRun *struct {
				RunID        string `json:"runId"`
				RetryOfRunID string `json:"retryOfRunId"`
			} `json:"currentRun"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.LedgerSeq != 3 || envelope.Data.EventCount != 4 || envelope.Data.RewindCount != 1 || envelope.Data.ContinuationCount != 0 || envelope.Data.CheckpointCount != 1 {
		t.Fatalf("manifest summary = %+v", envelope.Data)
	}
	if envelope.Data.LatestRecovery == nil || envelope.Data.LatestRecovery.Type != "session/rewind" || envelope.Data.LatestRecovery.Seq != 3 {
		t.Fatalf("latest recovery = %+v", envelope.Data.LatestRecovery)
	}
	if envelope.Data.LatestLedgerHash == "" {
		t.Fatal("latest ledger hash is empty")
	}
	foreign := performSessionRequest(sessionTestRouter(8, workbench), http.MethodGet, "/sessions/"+created.ID+"/recovery-manifest", nil)
	missing := performSessionRequest(router, http.MethodGet, "/sessions/missing/recovery-manifest", nil)
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign=%d missing=%d; bodies must match", foreign.Code, missing.Code)
	}
}

func TestRecoveryManifestIncludesCurrentRunLineage(t *testing.T) {
	workbench, ledger := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "lineage", "goal")
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(context.Background(), 7, created.ID, 1, "resume")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(context.Background(), 7, created.ID, 2, message.ID, "anchor")
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"request_id": "request-root", "run_id": "run-root", "checkpoint_hash": checkpoint.Hash, "target_event_id": message.ID, "target_seq": 1, "target_checksum": checkpoint.TargetHash, "surface_sha256": "surface", "input_event_id": message.ID, "resume_count": 1}
	if _, err := ledger.Append(context.Background(), created.ID, 3, "session/continued", payload); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(context.Background(), created.ID, 4, "session/run-leased", map[string]any{"run_id": "run-root", "request_id": "request-root", "lease_id": "lease-root", "worker_id": "worker-root", "attempt": 1, "lease_until": time.Now().Add(time.Minute).UTC()}); err != nil {
		t.Fatal(err)
	}
	router := sessionTestRouter(7, workbench)
	response := performSessionRequest(router, http.MethodGet, "/sessions/"+created.ID+"/recovery-manifest", nil)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"runId":"run-root"`)) {
		t.Fatalf("manifest current run missing: status=%d body=%s", response.Code, response.Body.String())
	}
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

func TestRunHistoryReturnsSame404ForForeignAndMissing(t *testing.T) {
	workbench, _ := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "history", "goal")
	if err != nil {
		t.Fatal(err)
	}
	router := sessionTestRouter(8, workbench)
	foreign := performSessionRequest(router, http.MethodGet, "/sessions/"+created.ID+"/runs", nil)
	missing := performSessionRequest(router, http.MethodGet, "/sessions/missing/runs", nil)
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign=%d missing=%d; bodies must match", foreign.Code, missing.Code)
	}
}

func TestRunHistoryReturnsCanonicalEmptyArrayForSessionWithoutRuns(t *testing.T) {
	workbench, _ := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "history-empty", "goal")
	if err != nil {
		t.Fatal(err)
	}
	response := performSessionRequest(sessionTestRouter(7, workbench), http.MethodGet, "/sessions/"+created.ID+"/runs", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("run history status = %d; body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data []session.RunView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data == nil {
		t.Fatalf("run history data must be an array, body=%s", response.Body.String())
	}
	if len(envelope.Data) != 0 {
		t.Fatalf("run history = %+v, want empty array", envelope.Data)
	}
}

func TestDeleteSessionAcceptsExpectedSequenceInQuery(t *testing.T) {
	workbench, ledger := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "delete-query", "goal")
	if err != nil {
		t.Fatal(err)
	}
	router := sessionTestRouter(7, workbench)
	response := performSessionRequest(router, http.MethodDelete, "/sessions/"+created.ID+"?expectedSeq=1", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("query delete status = %d; body=%s", response.Code, response.Body.String())
	}
	if _, err := workbench.Get(context.Background(), 7, created.ID); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("deleted session lookup error = %v, want not found", err)
	}
	events, err := ledger.Events(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Type != "session/deleted" {
		t.Fatalf("delete did not append tombstone: %+v", events)
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

func TestContinueSessionRouteReturnsDurableResumeReceipt(t *testing.T) {
	workbench, ledger := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "title", "goal")
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(context.Background(), 7, created.ID, 1, "checkpoint input")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workbench.CreateCheckpoint(context.Background(), 7, created.ID, 2, message.ID, "continue here"); err != nil {
		t.Fatal(err)
	}
	if err := workbench.UpdateStatus(context.Background(), 7, created.ID, 3, "paused"); err != nil {
		t.Fatal(err)
	}
	router := sessionTestRouter(7, workbench)
	response := performSessionRequest(router, http.MethodPost, "/sessions/"+created.ID+"/continue", map[string]any{
		"expectedSeq": int64(4),
	})
	if response.Code != http.StatusOK {
		t.Fatalf("continue status = %d; body=%s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"type":"session/continued"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`resume #1`)) {
		t.Fatalf("continue response lacks resume receipt: %s", response.Body.String())
	}
	stale := performSessionRequest(router, http.MethodPost, "/sessions/"+created.ID+"/continue", map[string]any{
		"expectedSeq": int64(4),
	})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale continue status = %d, want 409; body=%s", stale.Code, stale.Body.String())
	}
	events, err := ledger.Events(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 || events[4].Type != "session/continued" {
		t.Fatalf("continue changed canonical history unexpectedly: %+v", events)
	}
	manifest := performSessionRequest(router, http.MethodGet, "/sessions/"+created.ID+"/recovery-manifest", nil)
	if manifest.Code != http.StatusOK || !bytes.Contains(manifest.Body.Bytes(), []byte(`"continuationCount":1`)) || !bytes.Contains(manifest.Body.Bytes(), []byte(`"type":"session/continued"`)) || !bytes.Contains(manifest.Body.Bytes(), []byte(`"targetSeq":1`)) {
		t.Fatalf("manifest does not reflect continuation: status=%d body=%s", manifest.Code, manifest.Body.String())
	}
}

func TestContinueSessionRouteHidesForeignSessionLikeMissing(t *testing.T) {
	workbench, _ := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "title", "goal")
	if err != nil {
		t.Fatal(err)
	}
	router := sessionTestRouter(8, workbench)
	body := map[string]any{"expectedSeq": int64(1)}
	foreign := performSessionRequest(router, http.MethodPost, "/sessions/"+created.ID+"/continue", body)
	missing := performSessionRequest(router, http.MethodPost, "/sessions/missing/continue", body)
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign=%d %q missing=%d %q; continue must not enumerate sessions", foreign.Code, foreign.Body.String(), missing.Code, missing.Body.String())
	}
}
