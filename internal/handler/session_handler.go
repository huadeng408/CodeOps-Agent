package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"code-agent/internal/session"
	"code-agent/internal/worktree"

	"github.com/gin-gonic/gin"
)

// SessionHandler is a deliberately thin transport adapter over the canonical
// session Workbench. It never talks to a projection table directly.
type SessionHandler struct {
	workbench session.WorkbenchModule
	tickets   *session.WebSocketTickets
	worktree  *worktree.Manager
}

func NewSessionHandler(workbench session.WorkbenchModule) *SessionHandler {
	return &SessionHandler{workbench: workbench}
}

// NewSessionHandlerWithTickets wires the optional one-time WebSocket ticket
// issuer. Keeping this separate preserves the small HTTP session seam.
func NewSessionHandlerWithTickets(workbench session.WorkbenchModule, tickets *session.WebSocketTickets) *SessionHandler {
	return &SessionHandler{workbench: workbench, tickets: tickets}
}

// NewSessionHandlerWithWorktree adds the optional read-only workspace recovery
// seam while preserving the small constructor used by unit tests and legacy
// transports.
func NewSessionHandlerWithWorktree(workbench session.WorkbenchModule, tickets *session.WebSocketTickets, manager *worktree.Manager) *SessionHandler {
	return &SessionHandler{workbench: workbench, tickets: tickets, worktree: manager}
}

type createSessionRequest struct {
	ProjectName string `json:"projectName"`
	Title       string `json:"title"`
	Goal        string `json:"goal"`
}

type expectedSeqRequest struct {
	ExpectedSeq *int64 `json:"expectedSeq"`
}

type updateTitleRequest struct {
	Title       string `json:"title"`
	ExpectedSeq *int64 `json:"expectedSeq"`
}

type updateStatusRequest struct {
	Status      string `json:"status"`
	ExpectedSeq *int64 `json:"expectedSeq"`
}

type recoveryManifest struct {
	Session           session.SessionView    `json:"session"`
	LedgerSeq         int64                  `json:"ledgerSeq"`
	LatestLedgerHash  string                 `json:"latestLedgerHash"`
	EventCount        int                    `json:"eventCount"`
	RewindCount       int                    `json:"rewindCount"`
	ContinuationCount int                    `json:"continuationCount"`
	CheckpointCount   int                    `json:"checkpointCount"`
	LatestRecovery    *recoveryManifestEvent `json:"latestRecovery,omitempty"`
	CurrentRun        *recoveryRunSummary    `json:"currentRun,omitempty"`
	Runs              []recoveryRunSummary   `json:"runs,omitempty"`
}

type recoveryRunSummary struct {
	RunID         string   `json:"runId"`
	Status        string   `json:"status"`
	Attempt       int      `json:"attempt"`
	RetryOfRunID  string   `json:"retryOfRunId,omitempty"`
	RetryOfRunIDs []string `json:"retryOfRunIds,omitempty"`
}

type recoveryManifestEvent struct {
	Type        string `json:"type"`
	Seq         int64  `json:"seq"`
	TargetSeq   *int64 `json:"targetSeq,omitempty"`
	ResumeCount int    `json:"resumeCount,omitempty"`
}

type workspaceManifest struct {
	Available bool                `json:"available"`
	Reason    string              `json:"reason,omitempty"`
	Worktrees []workspaceWorktree `json:"worktrees,omitempty"`
}

type workspaceWorktree struct {
	Name      string   `json:"name"`
	BaseRef   string   `json:"baseRef,omitempty"`
	Status    string   `json:"status,omitempty"`
	Active    bool     `json:"active,omitempty"`
	DiffLines []string `json:"diffLines,omitempty"`
	DiffError string   `json:"diffError,omitempty"`
}

func (h *SessionHandler) Create(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	var req createSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeSessionError(c, errors.Join(session.ErrInvalidSessionInput, err), "invalid session request")
		return
	}
	view, err := h.workbench.Create(c.Request.Context(), owner, req.ProjectName, req.Title, req.Goal)
	if err != nil {
		writeSessionError(c, err, "failed to create session")
		return
	}
	writeSessionData(c, http.StatusOK, view)
}

func (h *SessionHandler) List(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	views, err := h.workbench.List(c.Request.Context(), owner)
	if err != nil {
		writeSessionError(c, err, "failed to list sessions")
		return
	}
	writeSessionData(c, http.StatusOK, views)
}

func (h *SessionHandler) Get(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	view, err := h.workbench.Get(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		writeSessionError(c, err, "session not found")
		return
	}
	writeSessionData(c, http.StatusOK, view)
}

// RecoveryManifest exposes a read-only, ledger-derived resume summary. It is
// intentionally assembled from the same owner-scoped APIs as the UI rather
// than introducing a second persistence model for recovery state.
func (h *SessionHandler) RecoveryManifest(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	view, err := h.workbench.Get(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		writeSessionError(c, err, "session not found")
		return
	}
	events, err := h.workbench.Events(c.Request.Context(), owner, c.Param("id"), 0)
	if err != nil {
		writeSessionError(c, err, "failed to read recovery manifest")
		return
	}
	checkpoints, err := h.workbench.ListCheckpoints(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		writeSessionError(c, err, "failed to read recovery manifest")
		return
	}
	manifest := recoveryManifest{Session: view, EventCount: len(events), CheckpointCount: len(checkpoints), LedgerSeq: -1}
	if view.Run != nil {
		manifest.CurrentRun = &recoveryRunSummary{
			RunID: view.Run.RunID, Status: string(view.Run.Status), Attempt: view.Run.Attempt,
			RetryOfRunID: view.Run.RetryOfRunID, RetryOfRunIDs: append([]string(nil), view.Run.RetryOfRunIDs...),
		}
	}
	for _, run := range view.Runs {
		manifest.Runs = append(manifest.Runs, recoveryRunSummary{
			RunID: run.RunID, Status: string(run.Status), Attempt: run.Attempt,
			RetryOfRunID: run.RetryOfRunID, RetryOfRunIDs: append([]string(nil), run.RetryOfRunIDs...),
		})
	}
	if len(events) > 0 {
		manifest.LedgerSeq = events[len(events)-1].Seq
		manifest.LatestLedgerHash = events[len(events)-1].Hash
	}
	for _, event := range events {
		switch event.Type {
		case "session/rewind":
			manifest.RewindCount++
			manifest.LatestRecovery = &recoveryManifestEvent{Type: event.Type, Seq: event.Seq, TargetSeq: event.RewindTargetSeq}
		case "session/continued":
			manifest.ContinuationCount++
			if event.Continuation != nil {
				manifest.LatestRecovery = &recoveryManifestEvent{Type: event.Type, Seq: event.Seq, TargetSeq: &event.Continuation.TargetSeq, ResumeCount: event.Continuation.ResumeCount}
			}
		}
	}
	writeSessionData(c, http.StatusOK, manifest)
}

// WorkspaceManifest is a read-only summary of managed agent checkouts bound to
// this session. It never falls back to the server's global working directory.
func (h *SessionHandler) WorkspaceManifest(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	if _, err := h.workbench.Get(c.Request.Context(), owner, c.Param("id")); err != nil {
		writeSessionError(c, err, "session not found")
		return
	}
	manifest := workspaceManifest{Available: false, Reason: "no session worktree is currently bound"}
	if h.worktree == nil {
		manifest.Reason = "workspace recovery is unavailable"
		writeSessionData(c, http.StatusOK, manifest)
		return
	}
	for _, tree := range h.worktree.List() {
		if tree.ParentSessionID != c.Param("id") && tree.ChildSessionID != c.Param("id") {
			continue
		}
		item := workspaceWorktree{Name: tree.Name, BaseRef: tree.BaseRef, Status: tree.Status, Active: tree.Active}
		lines, diffErr := h.worktree.DiffLinesAt(c.Request.Context(), tree.Path)
		if diffErr != nil {
			item.DiffError = "workspace diff unavailable"
		} else {
			item.DiffLines = lines
		}
		manifest.Worktrees = append(manifest.Worktrees, item)
	}
	if len(manifest.Worktrees) > 0 {
		manifest.Available = true
		manifest.Reason = ""
	}
	writeSessionData(c, http.StatusOK, manifest)
}

func (h *SessionHandler) UpdateTitle(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	var req updateTitleRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ExpectedSeq == nil {
		writeSessionError(c, errors.Join(session.ErrInvalidSessionInput, errors.New("expectedSeq is required")), "expectedSeq is required")
		return
	}
	if err := h.workbench.UpdateTitle(c.Request.Context(), owner, c.Param("id"), *req.ExpectedSeq, req.Title); err != nil {
		writeSessionError(c, err, "failed to update session title")
		return
	}
	writeSessionData(c, http.StatusOK, nil)
}

func (h *SessionHandler) UpdateStatus(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	var req updateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ExpectedSeq == nil {
		writeSessionError(c, errors.Join(session.ErrInvalidSessionInput, errors.New("expectedSeq is required")), "expectedSeq is required")
		return
	}
	if err := h.workbench.UpdateStatus(c.Request.Context(), owner, c.Param("id"), *req.ExpectedSeq, req.Status); err != nil {
		writeSessionError(c, err, "failed to update session status")
		return
	}
	writeSessionData(c, http.StatusOK, nil)
}

func (h *SessionHandler) Delete(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	expected, ok := parseExpectedSeq(c)
	if !ok {
		writeSessionError(c, errors.Join(session.ErrInvalidSessionInput, errors.New("expectedSeq is required")), "expectedSeq is required")
		return
	}
	if err := h.workbench.Delete(c.Request.Context(), owner, c.Param("id"), expected); err != nil {
		writeSessionError(c, err, "failed to delete session")
		return
	}
	writeSessionData(c, http.StatusOK, nil)
}

// IssueWebSocketTicket is called while the normal JWT middleware is active.
func (h *SessionHandler) IssueWebSocketTicket(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	if h.tickets == nil {
		writeSessionError(c, errors.New("websocket tickets are unavailable"), "websocket tickets are unavailable")
		return
	}
	if _, err := h.workbench.Get(c.Request.Context(), owner, c.Param("id")); err != nil {
		writeSessionError(c, err, "session not found")
		return
	}
	raw, expiresAt, err := h.tickets.Issue(owner, c.Param("id"))
	if err != nil {
		writeSessionError(c, err, "failed to issue websocket ticket")
		return
	}
	writeSessionData(c, http.StatusOK, gin.H{"ticket": raw, "expiresAt": expiresAt})
}

func parseExpectedSeq(c *gin.Context) (int64, bool) {
	raw := strings.TrimSpace(c.Query("expectedSeq"))
	if raw == "" {
		var req expectedSeqRequest
		if c.Request.Body == nil || c.ShouldBindJSON(&req) != nil || req.ExpectedSeq == nil {
			return 0, false
		}
		return *req.ExpectedSeq, *req.ExpectedSeq >= 0
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	return parsed, err == nil && parsed >= 0
}

func writeSessionData(c *gin.Context, status int, data any) {
	c.JSON(status, gin.H{"code": status, "message": "success", "data": data})
}
