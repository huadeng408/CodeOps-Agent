package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"code-agent/internal/session"

	"github.com/gin-gonic/gin"
)

// EventHandler is the browser transport for user messages and immutable
// checkpoint facts. Tool/assistant events are produced by the orchestrator,
// never selected by an untrusted browser request.
type EventHandler struct{ workbench session.WorkbenchModule }

func NewEventHandler(workbench session.WorkbenchModule) *EventHandler {
	return &EventHandler{workbench: workbench}
}

type createEventRequest struct {
	Type        string `json:"type"`
	EventType   string `json:"eventType"`
	Author      string `json:"author"`
	Content     string `json:"content"`
	ExpectedSeq *int64 `json:"expectedSeq"`
}

type createCheckpointRequest struct {
	EventID     string `json:"eventId"`
	Label       string `json:"label"`
	ExpectedSeq *int64 `json:"expectedSeq"`
}

type restoreCheckpointRequest struct {
	ExpectedSeq *int64 `json:"expectedSeq"`
}

func (h *EventHandler) ListEvents(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	limit := 0
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 0 {
			writeSessionError(c, errors.Join(session.ErrInvalidSessionInput, errors.New("invalid event limit")), "invalid event limit")
			return
		}
	}
	var events []session.EventView
	if raw := strings.TrimSpace(c.Query("after")); raw != "" {
		after, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || after < -1 {
			writeSessionError(c, errors.Join(session.ErrInvalidSessionInput, errors.New("invalid event cursor")), "invalid event cursor")
			return
		}
		events, err = h.workbench.EventsAfter(c.Request.Context(), owner, c.Param("id"), after, limit)
	} else {
		events, err = h.workbench.Events(c.Request.Context(), owner, c.Param("id"), limit)
	}
	if err != nil {
		writeSessionError(c, err, "failed to list events")
		return
	}
	writeSessionData(c, http.StatusOK, events)
}

func (h *EventHandler) CreateEvent(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	var req createEventRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ExpectedSeq == nil {
		writeSessionError(c, errors.Join(session.ErrInvalidSessionInput, errors.New("expectedSeq is required")), "expectedSeq is required")
		return
	}
	requestedType := strings.TrimSpace(req.Type)
	if requestedType == "" {
		requestedType = strings.TrimSpace(req.EventType)
	}
	// Accept only the harmless aliases used by older clients, and normalize
	// every accepted request to the canonical user/message event.
	if requestedType != "" && requestedType != "user/message" && requestedType != "user_input" && requestedType != "message" {
		writeSessionError(c, errors.Join(session.ErrInvalidSessionInput, errors.New("browser may only append user messages")), "browser may only append user messages")
		return
	}
	if strings.TrimSpace(req.Author) != "" && strings.TrimSpace(req.Author) != "user" {
		writeSessionError(c, errors.Join(session.ErrInvalidSessionInput, errors.New("browser may not choose event author")), "browser may not choose event author")
		return
	}
	event, err := h.workbench.AppendUserMessage(c.Request.Context(), owner, c.Param("id"), *req.ExpectedSeq, req.Content)
	if err != nil {
		writeSessionError(c, err, "failed to append event")
		return
	}
	writeSessionData(c, http.StatusOK, event)
}

func (h *EventHandler) ListCheckpoints(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	checkpoints, err := h.workbench.ListCheckpoints(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		writeSessionError(c, err, "failed to list checkpoints")
		return
	}
	writeSessionData(c, http.StatusOK, checkpoints)
}

func (h *EventHandler) CreateCheckpoint(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	var req createCheckpointRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ExpectedSeq == nil {
		writeSessionError(c, errors.Join(session.ErrInvalidSessionInput, errors.New("expectedSeq is required")), "expectedSeq is required")
		return
	}
	checkpoint, err := h.workbench.CreateCheckpoint(c.Request.Context(), owner, c.Param("id"), *req.ExpectedSeq, req.EventID, req.Label)
	if err != nil {
		writeSessionError(c, err, "failed to create checkpoint")
		return
	}
	writeSessionData(c, http.StatusOK, checkpoint)
}

func (h *EventHandler) RestoreCheckpoint(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	var req restoreCheckpointRequest
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if bindErr := c.ShouldBindJSON(&req); bindErr != nil {
			writeSessionError(c, errors.Join(session.ErrInvalidSessionInput, bindErr), "invalid restore request")
			return
		}
	}
	if req.ExpectedSeq == nil {
		if raw := strings.TrimSpace(c.Query("expectedSeq")); raw != "" {
			parsed, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr == nil && parsed >= 0 {
				req.ExpectedSeq = &parsed
			}
		}
	}
	if req.ExpectedSeq == nil {
		writeSessionError(c, errors.Join(session.ErrInvalidSessionInput, errors.New("expectedSeq is required")), "expectedSeq is required")
		return
	}
	rewind, err := h.workbench.RestoreCheckpoint(c.Request.Context(), owner, c.Param("id"), c.Param("hash"), *req.ExpectedSeq)
	if err != nil {
		writeSessionError(c, err, "failed to restore checkpoint")
		return
	}
	writeSessionData(c, http.StatusOK, rewind)
}
