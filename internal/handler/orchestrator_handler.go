package handler

import (
	"net/http"

	"code-agent/internal/model"
	"code-agent/internal/service"
	"code-agent/internal/telemetry/genai"
	"code-agent/pkg/log"

	"github.com/gin-gonic/gin"
)

// OrchestratorHandler serves internal endpoints consumed by the external LangGraph service.
type OrchestratorHandler struct {
	supportService service.OrchestratorSupportService
	tracer         genai.Tracer
}

// NewOrchestratorHandler creates a new internal orchestrator handler.
func NewOrchestratorHandler(supportService service.OrchestratorSupportService) *OrchestratorHandler {
	return &OrchestratorHandler{supportService: supportService}
}

// SetTracer injects a genai.Tracer for creating retrieve/rerank spans.
func (h *OrchestratorHandler) SetTracer(t genai.Tracer) {
	h.tracer = t
}

// LoadSession returns the current conversation id and history for a user.
func (h *OrchestratorHandler) LoadSession(c *gin.Context) {
	var req model.OrchestratorSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UserID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session request"})
		return
	}

	resp, err := h.supportService.LoadSession(c.Request.Context(), req.UserID)
	if err != nil {
		log.Errorf("[OrchestratorHandler] load session failed user=%d err=%v", req.UserID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": http.StatusOK, "data": resp, "message": "success"})
}

// RetrieveContext returns retrieval artifacts for a planned query.
func (h *OrchestratorHandler) RetrieveContext(c *gin.Context) {
	var req model.OrchestratorRetrieveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid retrieve request"})
		return
	}

	resp, err := h.supportService.RetrieveContext(c.Request.Context(), &req)
	if err != nil {
		log.Errorf("[OrchestratorHandler] retrieve context failed query=%q err=%v", req.Query, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve context"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": http.StatusOK, "data": resp, "message": "success"})
}

// PreparePromptContext returns prompt-related state without executing retrieval.
func (h *OrchestratorHandler) PreparePromptContext(c *gin.Context) {
	var req model.OrchestratorPromptContextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid prompt context request"})
		return
	}

	resp, err := h.supportService.PreparePromptContext(c.Request.Context(), &req)
	if err != nil {
		log.Errorf("[OrchestratorHandler] prepare prompt context failed conversation=%s err=%v", req.ConversationID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare prompt context"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": http.StatusOK, "data": resp, "message": "success"})
}

// SearchKnowledge executes one knowledge retrieval mode for the external orchestrator.
func (h *OrchestratorHandler) SearchKnowledge(c *gin.Context) {
	var req model.OrchestratorKnowledgeSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid knowledge search request"})
		return
	}

	ctx := c.Request.Context()

	// Orchestrator-initiated search — create a rag.retrieve span.
	var retrieveSpan genai.Span
	if h.tracer != nil {
		ctx, retrieveSpan = h.tracer.StartSpan(ctx, "retrieve orchestrator /knowledge-search", genai.OperationRetrieve, genai.SystemGenAI)
		retrieveSpan.SetAttributes(
			genai.QueryHashKV(genai.HashQuery(req.Query)),
			genai.TopNKV(req.TopK),
			genai.RetrievalModeKV(req.Mode),
		)
		defer retrieveSpan.End()
	}

	resp, err := h.supportService.SearchKnowledge(ctx, &req)
	if err != nil {
		if retrieveSpan != nil {
			retrieveSpan.RecordError(err)
		}
		log.Errorf("[OrchestratorHandler] knowledge search failed query=%q mode=%s err=%v", req.Query, req.Mode, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to search knowledge"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": http.StatusOK, "data": resp, "message": "success"})
}

// SearchMemory executes long-term memory retrieval for the external orchestrator.
func (h *OrchestratorHandler) SearchMemory(c *gin.Context) {
	var req model.OrchestratorMemorySearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid memory search request"})
		return
	}

	resp, err := h.supportService.SearchMemory(c.Request.Context(), &req)
	if err != nil {
		log.Errorf("[OrchestratorHandler] memory search failed query=%q err=%v", req.Query, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to search memory"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": http.StatusOK, "data": resp, "message": "success"})
}

// RerankContext reranks fused context snippets for the external orchestrator.
func (h *OrchestratorHandler) RerankContext(c *gin.Context) {
	var req model.OrchestratorRerankRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid rerank request"})
		return
	}

	resp, err := h.supportService.RerankContext(c.Request.Context(), &req)
	if err != nil {
		log.Errorf("[OrchestratorHandler] rerank context failed query=%q err=%v", req.Query, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rerank context"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": http.StatusOK, "data": resp, "message": "success"})
}

// PersistTurn writes the generated answer back into the Go conversation and memory stores.
func (h *OrchestratorHandler) PersistTurn(c *gin.Context) {
	var req model.OrchestratorPersistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid persist request"})
		return
	}

	if err := h.supportService.PersistTurn(c.Request.Context(), &req); err != nil {
		log.Errorf("[OrchestratorHandler] persist turn failed conversation=%s err=%v", req.ConversationID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist turn"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": http.StatusOK, "message": "success"})
}
