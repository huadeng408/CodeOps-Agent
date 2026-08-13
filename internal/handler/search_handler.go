// Package handler contains HTTP and WebSocket endpoint handlers.
package handler

import (
	"net/http"
	"strconv"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	"code-agent/internal/service"
	"code-agent/internal/telemetry/genai"
	"code-agent/pkg/log"

	"github.com/gin-gonic/gin"
)

// SearchHandler handles search requests.
type SearchHandler struct {
	searchService service.SearchService
	tracer        genai.Tracer
}

// NewSearchHandler creates a search handler.
func NewSearchHandler(searchService service.SearchService) *SearchHandler {
	return &SearchHandler{
		searchService: searchService,
	}
}

// SetTracer injects a genai.Tracer for creating retrieve/rerank spans.
func (h *SearchHandler) SetTracer(t genai.Tracer) {
	h.tracer = t
}

// HybridSearch handles hybrid search.
func (h *SearchHandler) HybridSearch(c *gin.Context) {
	query := c.Query("query")
	queryHash := genai.HashQuery(query)
	log.Infof("[SearchHandler] receive hybrid search request, query_hash=%s", queryHash)

	if query == "" {
		log.Warnf("[SearchHandler] hybrid search rejected: empty query")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid query"})
		return
	}

	defaultTopK := serverconfig.Conf.Retrieval.FinalTopK
	if defaultTopK <= 0 {
		defaultTopK = 5
	}

	topKStr := c.DefaultQuery("topK", strconv.Itoa(defaultTopK))
	topK, err := strconv.Atoi(topKStr)
	if err != nil || topK <= 0 {
		topK = defaultTopK
	}
	disableRerank, _ := strconv.ParseBool(c.DefaultQuery("disableRerank", "false"))

	user, exists := c.Get("user")
	if !exists {
		log.Errorf("[SearchHandler] failed to load user from gin context")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load user"})
		return
	}

	searchCtx := c.Request.Context()
	if disableRerank {
		searchCtx = service.WithRerankDisabled(searchCtx)
	}

	// Create rag.retrieve span (HTTP handler layer)
	var retrieveSpan genai.Span
	if h.tracer != nil {
		searchCtx, retrieveSpan = h.tracer.StartSpan(searchCtx, "retrieve HTTP /api/v1/search/hybrid", genai.OperationRetrieve, genai.SystemGenAI)
		retrieveSpan.SetAttributes(
			genai.QueryHashKV(genai.HashQuery(query)),
			genai.TopNKV(topK),
			genai.RetrievalModeKV("hybrid"),
		)
		defer retrieveSpan.End()
	}

	results, err := h.searchService.HybridSearch(searchCtx, query, topK, user.(*model.User))
	if err != nil {
		if retrieveSpan != nil {
			retrieveSpan.RecordError(err)
		}
		log.Errorf("[SearchHandler] hybrid search failed, query_hash=%s topK=%d disableRerank=%t err=%v", queryHash, topK, disableRerank, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "search failed"})
		return
	}

	log.Infof("[SearchHandler] hybrid search completed, query_hash=%s topK=%d disableRerank=%t resultCount=%d", queryHash, topK, disableRerank, len(results))
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": results, "message": "success"})
}
