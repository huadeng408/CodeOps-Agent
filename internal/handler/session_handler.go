package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"code-agent/internal/session"

	"github.com/gin-gonic/gin"
)

// SessionHandler is a deliberately thin transport adapter over the canonical
// session Workbench. It never talks to a projection table directly.
type SessionHandler struct {
	workbench session.WorkbenchModule
	tickets   *session.WebSocketTickets
}

func NewSessionHandler(workbench session.WorkbenchModule) *SessionHandler {
	return &SessionHandler{workbench: workbench}
}

// NewSessionHandlerWithTickets wires the optional one-time WebSocket ticket
// issuer. Keeping this separate preserves the small HTTP session seam.
func NewSessionHandlerWithTickets(workbench session.WorkbenchModule, tickets *session.WebSocketTickets) *SessionHandler {
	return &SessionHandler{workbench: workbench, tickets: tickets}
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
	Session           session.SessionView `json:"session"`
	LedgerSeq         int64               `json:"ledgerSeq"`
	EventCount        int                 `json:"eventCount"`
	RewindCount       int                 `json:"rewindCount"`
	ContinuationCount int                 `json:"continuationCount"`
	CheckpointCount   int                 `json:"checkpointCount"`
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
	if len(events) > 0 {
		manifest.LedgerSeq = events[len(events)-1].Seq
	}
	for _, event := range events {
		switch event.Type {
		case "session/rewind":
			manifest.RewindCount++
		case "session/continued":
			manifest.ContinuationCount++
		}
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
