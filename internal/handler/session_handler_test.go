package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
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

type testContinuationModule struct{}

type handlerApprovalConversation struct{}

func (handlerApprovalConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	result := handlers.Tool(ctx, orchestrator.ToolCall{ID: "handler-call", Name: "Write", ParametersJSON: `{"content":"ok","path":"handler.txt"}`})
	if result.Error != "" {
		return orchestrator.ConversationResult{}, errors.New(result.Error)
	}
	return orchestrator.ConversationResult{Success: true, Message: "approved through HTTP"}, nil
}

type handlerApprovalTool struct{}

func (handlerApprovalTool) Execute(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult {
	return orchestrator.ToolResult{Output: "written"}
}

func (m *testContinuationModule) RequestContinuation(context.Context, session.ContinueCommand) (session.RunView, error) {
	return session.RunView{RunID: "test-run"}, nil
}
func (m *testContinuationModule) SubmitMessage(context.Context, session.SubmitMessageCommand) (session.RunView, error) {
	return session.RunView{RunID: "test-run"}, nil
}

func (m *testContinuationModule) Recover(context.Context) error { return nil }
func (m *testContinuationModule) Close() error                  { return nil }

func TestSessionErrorStatusMapsContinuationUnavailableTo503(t *testing.T) {
	if got := sessionErrorStatus(session.ErrContinuationUnavailable); got != http.StatusServiceUnavailable {
		t.Fatalf("continuation unavailable status = %d, want %d", got, http.StatusServiceUnavailable)
	}
}

func TestSessionErrorStatusMapsSessionRunnerClosedTo503(t *testing.T) {
	if got := sessionErrorStatus(session.ErrSessionRunnerClosed); got != http.StatusServiceUnavailable {
		t.Fatalf("session runner closed status = %d, want %d", got, http.StatusServiceUnavailable)
	}
}

func TestEventHandlerContinuationSelectionIsSafeDuringReplacement(t *testing.T) {
	h := NewEventHandler(nil)
	first := &testContinuationModule{}
	second := &testContinuationModule{}
	h.SetContinuation(first)
	var wg sync.WaitGroup
	errs := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			h.SetContinuation(first)
			h.SetContinuation(second)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			if got := h.continuationSnapshot(); got != first && got != second {
				select {
				case errs <- fmt.Errorf("unexpected continuation snapshot: %T", got):
				default:
				}
				return
			}
		}
	}()
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}

func TestWriteSessionErrorUsesStableContinuationUnavailableMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/error", func(c *gin.Context) {
		writeSessionError(c, session.ErrContinuationUnavailable, "failed to continue session")
	})
	response := performSessionRequest(router, http.MethodGet, "/error", nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("continuation unavailable response status = %d", response.Code)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"message":"agent continuation is unavailable"`)) {
		t.Fatalf("continuation unavailable response body = %s", response.Body.String())
	}
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
	sessions.GET("/:id/runs/:runId", sessionHandler.RunDetail)
	sessions.GET("/:id/recovery-manifest", sessionHandler.RecoveryManifest)
	sessions.GET("/:id/workspace-manifest", sessionHandler.WorkspaceManifest)
	sessions.POST("/:id/workspace/restore", sessionHandler.RestoreWorkspace)
	sessions.PUT("/:id/title", sessionHandler.UpdateTitle)
	sessions.PUT("/:id/status", sessionHandler.UpdateStatus)
	sessions.DELETE("/:id", sessionHandler.Delete)
	sessions.GET("/:id/events", eventHandler.ListEvents)
	sessions.POST("/:id/events", eventHandler.CreateEvent)
	sessions.GET("/:id/checkpoints", eventHandler.ListCheckpoints)
	sessions.POST("/:id/checkpoints", eventHandler.CreateCheckpoint)
	sessions.POST("/:id/restore/:hash", eventHandler.RestoreCheckpoint)
	sessions.POST("/:id/continue", eventHandler.ContinueSession)
	sessions.POST("/:id/approvals/:runId/:toolCallId", eventHandler.DecideToolApproval)
	return router
}

func messageSubmissionRouter(ownerID uint, workbench *session.Workbench, continuation session.ContinuationModule) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("claims", &token.CustomClaims{UserID: ownerID, TokenType: token.TokenTypeAccess})
		c.Next()
	})
	handler := NewEventHandler(workbench, continuation)
	router.POST("/sessions/:id/messages", handler.SubmitMessage)
	return router
}

func TestSubmitMessageRouteStartsNaturalLanguageTurnIdempotently(t *testing.T) {
	ctx := context.Background()
	workbench, ledger := openHandlerTestWorkbench(t)
	created, err := workbench.Create(ctx, 7, "repo", "natural language", "respond")
	if err != nil {
		t.Fatal(err)
	}
	runner := session.NewSessionRunner(workbench, handlerApprovalConversation{}, handlerApprovalTool{}, session.SessionRunnerOptions{WorkerID: "handler-message"})
	t.Cleanup(func() { _ = runner.Close() })
	router := messageSubmissionRouter(7, workbench, runner)
	body := map[string]any{"requestId": "browser-message-1", "expectedSeq": int64(1), "content": "please answer this natural language request"}
	response := performSessionRequest(router, http.MethodPost, "/sessions/"+created.ID+"/messages", body)
	if response.Code != http.StatusAccepted {
		t.Fatalf("submit message status=%d body=%s", response.Code, response.Body.String())
	}
	var accepted struct {
		Data session.RunView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil || accepted.Data.RunID == "" {
		t.Fatalf("submit message response=%s err=%v", response.Body.String(), err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var completed session.RunView
	for time.Now().Before(deadline) {
		view, runErr := runner.Run(ctx, created.ID, accepted.Data.RunID)
		if runErr != nil {
			t.Fatalf("read submitted run: %v", runErr)
		}
		completed = view
		if completed.Status == session.RunCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.Status != session.RunCompleted {
		t.Fatalf("submitted run status=%q, want %q", completed.Status, session.RunCompleted)
	}
	repeated := performSessionRequest(router, http.MethodPost, "/sessions/"+created.ID+"/messages", body)
	if repeated.Code != http.StatusAccepted || !bytes.Contains(repeated.Body.Bytes(), []byte(accepted.Data.RunID)) {
		t.Fatalf("idempotent message status=%d body=%s", repeated.Code, repeated.Body.String())
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	userMessages := 0
	for _, event := range events {
		if event.Type == "user/message" {
			userMessages++
		}
	}
	if userMessages != 1 {
		t.Fatalf("idempotent route persisted %d user messages", userMessages)
	}

	foreign := performSessionRequest(messageSubmissionRouter(8, workbench, runner), http.MethodPost, "/sessions/"+created.ID+"/messages", map[string]any{
		"requestId": "browser-message-foreign", "expectedSeq": int64(len(events)), "content": "foreign",
	})
	missing := performSessionRequest(messageSubmissionRouter(8, workbench, runner), http.MethodPost, "/sessions/missing/messages", map[string]any{
		"requestId": "browser-message-foreign", "expectedSeq": int64(len(events)), "content": "foreign",
	})
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign=%d %q missing=%d %q", foreign.Code, foreign.Body.String(), missing.Code, missing.Body.String())
	}
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

func TestRestoreWorkspaceRouteAppliesLedgerTransitionAndReceipts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	worktreePath := filepath.Join(root, ".agent", "worktrees", "resume")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(worktreePath, "notes.txt")
	if err := os.WriteFile(filePath, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	workbench, ledger := openHandlerTestWorkbench(t)
	created, err := workbench.Create(ctx, 7, "repo", "restore", "goal")
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "change")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 2, message.ID, "anchor")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 3, "session/continued", map[string]any{"request_id": "restore-request", "run_id": "restore-run", "checkpoint_hash": checkpoint.Hash, "target_event_id": message.ID, "target_seq": message.Seq, "target_checksum": message.Hash, "resume_count": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 4, "session/run-leased", map[string]any{"run_id": "restore-run", "request_id": "restore-request", "lease_id": "restore-lease", "worker_id": "handler", "attempt": 1, "lease_until": time.Now().Add(time.Minute).UTC()}); err != nil {
		t.Fatal(err)
	}
	transitionHash := func(value string) string { sum := sha256.Sum256([]byte(value)); return hex.EncodeToString(sum[:]) }
	comboHash := func(before, after string) string {
		sum := sha256.New()
		_, _ = sum.Write([]byte(before))
		_, _ = sum.Write([]byte{0})
		_, _ = sum.Write([]byte(after))
		return hex.EncodeToString(sum.Sum(nil))
	}
	modification := map[string]any{"run_id": "restore-run", "tool_call_id": "write-1", "tool_name": "Write", "operation": "Write", "summary": "Write modified notes.txt", "path": "notes.txt", "before": "before", "after": "after", "before_sha256": transitionHash("before"), "after_sha256": transitionHash("after"), "diff_sha256": comboHash("before", "after")}
	if _, err := ledger.Append(ctx, created.ID, 5, "code/modified", modification); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 6, "session/run-completed", map[string]any{"run_id": "restore-run", "request_id": "restore-request", "lease_id": "restore-lease", "attempt": 1}); err != nil {
		t.Fatal(err)
	}
	manager := worktree.NewManager(root, "HEAD")
	manager.Restore([]worktree.Worktree{{Name: "resume", Path: worktreePath, ParentSessionID: created.ID, Status: worktree.AgentWorktreeActive, Active: true}})
	router := sessionTestRouter(7, workbench, manager)
	body := map[string]any{"checkpointHash": checkpoint.Hash, "runId": "restore-run", "worktreeName": "resume", "expectedSeq": int64(7)}
	response := performSessionRequest(router, http.MethodPost, "/sessions/"+created.ID+"/workspace/restore", body)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"type":"workspace/restore-completed"`)) {
		t.Fatalf("restore route status=%d body=%s", response.Code, response.Body.String())
	}
	got, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "before" {
		t.Fatalf("restored file = %q, want before", got)
	}
	stale := performSessionRequest(router, http.MethodPost, "/sessions/"+created.ID+"/workspace/restore", body)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale restore status=%d body=%s", stale.Code, stale.Body.String())
	}
	foreign := performSessionRequest(sessionTestRouter(8, workbench, manager), http.MethodPost, "/sessions/"+created.ID+"/workspace/restore", body)
	missing := performSessionRequest(sessionTestRouter(8, workbench, manager), http.MethodPost, "/sessions/missing/workspace/restore", body)
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign=%d %q missing=%d %q", foreign.Code, foreign.Body.String(), missing.Code, missing.Body.String())
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

func TestSessionViewDerivesLastUserInputFromActiveSurface(t *testing.T) {
	workbench, _ := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "input", "goal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workbench.AppendUserMessage(context.Background(), 7, created.ID, 1, "unfinished instruction"); err != nil {
		t.Fatal(err)
	}
	view, err := workbench.Get(context.Background(), 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.LastUserInput != "unfinished instruction" {
		t.Fatalf("last user input = %q", view.LastUserInput)
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
	var envelope struct {
		Data struct {
			CurrentRun *struct {
				RunID  string `json:"runId"`
				Status string `json:"status"`
			} `json:"currentRun"`
			Runs []struct {
				RunID  string `json:"runId"`
				Status string `json:"status"`
			} `json:"runs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.CurrentRun == nil || envelope.Data.CurrentRun.RunID != "run-root" || envelope.Data.CurrentRun.Status != "running" {
		t.Fatalf("current run projection = %+v", envelope.Data.CurrentRun)
	}
	if len(envelope.Data.Runs) != 1 || envelope.Data.Runs[0].RunID != "run-root" || envelope.Data.Runs[0].Status != "running" {
		t.Fatalf("run history projection = %+v", envelope.Data.Runs)
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

func TestRunDetailReturnsSame404ForForeignMissingAndUnknownRun(t *testing.T) {
	workbench, _ := openHandlerTestWorkbench(t)
	created, err := workbench.Create(context.Background(), 7, "repo", "run-detail", "goal")
	if err != nil {
		t.Fatal(err)
	}
	router := sessionTestRouter(8, workbench)
	foreign := performSessionRequest(router, http.MethodGet, "/sessions/"+created.ID+"/runs/run-x", nil)
	missing := performSessionRequest(router, http.MethodGet, "/sessions/missing/runs/run-x", nil)
	unknown := performSessionRequest(sessionTestRouter(7, workbench), http.MethodGet, "/sessions/"+created.ID+"/runs/run-x", nil)
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound || foreign.Body.String() != missing.Body.String() || missing.Body.String() != unknown.Body.String() {
		t.Fatalf("foreign=%d missing=%d unknown=%d; responses must match", foreign.Code, missing.Code, unknown.Code)
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

func TestToolApprovalRouteUsesOwnerRunCallAndLedgerCAS(t *testing.T) {
	ctx := context.Background()
	workbench, _ := openHandlerTestWorkbench(t)
	created, err := workbench.Create(ctx, 7, "repo", "approval", "goal")
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "write a file")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 2, message.ID, "approval")
	if err != nil {
		t.Fatal(err)
	}
	if err := workbench.UpdateStatus(ctx, 7, created.ID, 3, "paused"); err != nil {
		t.Fatal(err)
	}
	permissions := permission.NewController(map[string]permission.Level{"Write": permission.AskSession}, nil)
	runner := session.NewSessionRunner(workbench, handlerApprovalConversation{}, handlerApprovalTool{}, session.SessionRunnerOptions{
		WorkerID: "handler-approval", LeaseDuration: 3 * time.Second,
		HeartbeatInterval: time.Second, Permissions: permissions,
	})
	t.Cleanup(func() { _ = runner.Close() })
	accepted, err := runner.RequestContinuation(ctx, session.ContinueCommand{
		RequestID: "handler-approval-request", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: 4, CheckpointHash: checkpoint.Hash,
		Actor: identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "alice", TenantID: "org:test", Roles: []string{"USER"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var pendingApproval *session.EventView
	for time.Now().Before(deadline) {
		events, readErr := workbench.Events(ctx, 7, created.ID, 0)
		if readErr != nil {
			t.Fatal(readErr)
		}
		found := false
		for _, event := range events {
			if event.Approval != nil && event.Approval.Decision == session.ApprovalPending {
				copy := event
				pendingApproval = &copy
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pendingApproval == nil {
		t.Fatal("pending approval was not persisted")
	}
	path := "/sessions/" + created.ID + "/approvals/" + url.PathEscape(accepted.RunID) + "/handler-call"
	body := map[string]any{"pendingEventId": pendingApproval.ID, "pendingSeq": pendingApproval.Seq, "decision": session.ApprovalApproved}
	approved := performSessionRequest(sessionTestRouter(7, workbench), http.MethodPost, path, body)
	if approved.Code != http.StatusOK || !bytes.Contains(approved.Body.Bytes(), []byte(`"decision":"approved"`)) {
		t.Fatalf("approval status=%d body=%s", approved.Code, approved.Body.String())
	}
	repeated := performSessionRequest(sessionTestRouter(7, workbench), http.MethodPost, path, body)
	if repeated.Code != http.StatusOK || repeated.Body.String() != approved.Body.String() {
		t.Fatalf("idempotent approval status=%d body=%s", repeated.Code, repeated.Body.String())
	}
	conflictingBody := map[string]any{"pendingEventId": pendingApproval.ID, "pendingSeq": pendingApproval.Seq, "decision": session.ApprovalDenied}
	conflicting := performSessionRequest(sessionTestRouter(7, workbench), http.MethodPost, path, conflictingBody)
	if conflicting.Code != http.StatusConflict {
		t.Fatalf("conflicting approval status=%d body=%s", conflicting.Code, conflicting.Body.String())
	}
	foreign := performSessionRequest(sessionTestRouter(8, workbench), http.MethodPost, path, body)
	missingPath := "/sessions/missing/approvals/" + url.PathEscape(accepted.RunID) + "/handler-call"
	missing := performSessionRequest(sessionTestRouter(8, workbench), http.MethodPost, missingPath, body)
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || foreign.Body.String() != missing.Body.String() {
		t.Fatalf("foreign=%d %q missing=%d %q", foreign.Code, foreign.Body.String(), missing.Code, missing.Body.String())
	}
}
