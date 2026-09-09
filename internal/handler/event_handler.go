package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"code-agent/internal/identity"
	"code-agent/internal/model"
	"code-agent/internal/session"
	"code-agent/pkg/token"

	"github.com/gin-gonic/gin"
)

// EventHandler is the browser transport for user messages and immutable
// checkpoint facts. Tool/assistant events are produced by the orchestrator,
// never selected by an untrusted browser request.
type EventHandler struct {
	workbench      session.WorkbenchModule
	continuationMu sync.RWMutex
	continuation   session.ContinuationModule
	slot           *session.ContinuationSlot
}

func NewEventHandler(workbench session.WorkbenchModule, continuation ...session.ContinuationModule) *EventHandler {
	handler := &EventHandler{workbench: workbench}
	if len(continuation) > 0 {
		handler.continuation = continuation[0]
	}
	return handler
}

// NewEventHandlerWithContinuationSlot keeps the HTTP handler stable while the
// orchestrator supervisor swaps the live runner in and out.
func NewEventHandlerWithContinuationSlot(workbench session.WorkbenchModule, slot *session.ContinuationSlot) *EventHandler {
	return &EventHandler{workbench: workbench, slot: slot}
}

// SetContinuation swaps the live runner behind the stable handler seam. This
// is used by server startup/reconnect supervision; requests already in flight
// retain the runner they selected while new requests observe the replacement.
func (h *EventHandler) SetContinuation(continuation session.ContinuationModule) {
	if h == nil {
		return
	}
	h.continuationMu.Lock()
	defer h.continuationMu.Unlock()
	h.continuation = continuation
}

func (h *EventHandler) continuationSnapshot() session.ContinuationModule {
	if h == nil {
		return nil
	}
	h.continuationMu.RLock()
	defer h.continuationMu.RUnlock()
	return h.continuation
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

type continueSessionRequest struct {
	RequestID      string `json:"requestId"`
	CheckpointHash string `json:"checkpointHash"`
	ExpectedSeq    *int64 `json:"expectedSeq"`
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

// ContinueSession accepts a durable continuation request. The runner returns
// queued before any model or tool work starts; lifecycle facts stream through
// the canonical Session Ledger afterwards.
func (h *EventHandler) ContinueSession(c *gin.Context) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	var req continueSessionRequest
	if c.Request.Body == nil || c.ShouldBindJSON(&req) != nil || req.ExpectedSeq == nil {
		writeSessionError(c, errors.Join(session.ErrInvalidSessionInput, errors.New("requestId and expectedSeq are required")), "requestId and expectedSeq are required")
		return
	}
	continuation := h.continuationSnapshot()
	if h.slot != nil {
		continuation = h.slot
	}
	if continuation == nil {
		if strings.TrimSpace(req.RequestID) == "" {
			continued, legacyErr := h.workbench.ContinueFromCheckpoint(c.Request.Context(), owner, c.Param("id"), req.CheckpointHash, *req.ExpectedSeq)
			if legacyErr != nil {
				writeSessionError(c, legacyErr, "failed to continue session")
				return
			}
			writeSessionData(c, http.StatusOK, continued)
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": http.StatusServiceUnavailable, "message": "agent continuation is unavailable", "data": nil})
		return
	}
	actor, err := authenticatedActor(c, c.Param("id"))
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	continued, err := continuation.RequestContinuation(c.Request.Context(), session.ContinueCommand{
		RequestID: strings.TrimSpace(req.RequestID), SessionID: c.Param("id"),
		CheckpointHash: req.CheckpointHash, OwnerID: owner, ExpectedSeq: *req.ExpectedSeq,
		Actor: actor,
	})
	if err != nil {
		writeSessionError(c, err, "failed to continue session")
		return
	}
	writeSessionData(c, http.StatusAccepted, continued)
}

func authenticatedActor(c *gin.Context, sessionID string) (identity.Actor, error) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		return identity.Actor{}, err
	}
	claimsValue, ok := c.Get("claims")
	if !ok {
		return identity.Actor{}, errAuthenticatedOwner
	}
	claims, ok := claimsValue.(*token.CustomClaims)
	if !ok || claims == nil {
		return identity.Actor{}, errAuthenticatedOwner
	}
	subject := strings.TrimSpace(claims.Username)
	role := strings.TrimSpace(claims.Role)
	tenant := ""
	if value, exists := c.Get("user"); exists {
		if user, valid := value.(*model.User); valid && user != nil && user.ID == owner {
			subject = strings.TrimSpace(user.Username)
			role = strings.TrimSpace(user.Role)
			tenant = strings.TrimSpace(user.PrimaryOrg)
		}
	}
	if subject == "" {
		subject = fmt.Sprintf("user:%d", owner)
	}
	if role == "" {
		role = "USER"
	}
	if tenant == "" {
		tenant = fmt.Sprintf("tenant:user:%d", owner)
	}
	return identity.Actor{
		SchemaVersion: identity.SchemaVersion, ActorID: fmt.Sprintf("user:%d", owner),
		Subject: subject, TenantID: tenant, Roles: []string{role}, SessionID: strings.TrimSpace(sessionID),
	}, nil
}
