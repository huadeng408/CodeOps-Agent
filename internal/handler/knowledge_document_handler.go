package handler

import (
	"net/http"
	"strings"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"

	"github.com/gin-gonic/gin"
)

// KnowledgeDocumentLister is the read-only query seam the status endpoint
// depends on. The concrete repository.KnowledgeDocumentRepository satisfies it
// structurally; declaring a narrow interface here keeps the handler (and its
// tests) decoupled from the full persistence contract.
type KnowledgeDocumentLister interface {
	ListDocumentsByGenerationAndStatus(generation string, statuses []string) ([]model.KnowledgeDocument, error)
}

// knowledgeDocumentStatusResponse is the projected, importer-facing view of a
// knowledge_document row. Only the fields the polling client needs are exposed
// (GC6: minimize surface); lastError is echoed verbatim because the model
// already stores a sanitized summary rather than raw internal errors.
type knowledgeDocumentStatusResponse struct {
	DocumentID       string `json:"documentId"`
	SourceID         string `json:"sourceId"`
	SourcePath       string `json:"sourcePath"`
	SourceCommit     string `json:"sourceCommit"`
	ContentSHA256    string `json:"contentSha256"`
	Status           string `json:"status"`
	LastError        string `json:"lastError"`
	TargetIndex      string `json:"targetIndex"`
	CorpusGeneration string `json:"corpusGeneration"`
}

// KnowledgeDocumentHandler exposes the read-only corpus document status query
// used by the importer to poll ingestion progress. It validates that the
// requested generation matches the configured corpus generation and that the
// status filter is within the documented whitelist before touching the repo,
// so a caller cannot enumerate documents of another generation or probe
// arbitrary status values. Errors are mapped to fixed sanitized messages and
// never echo the underlying error text.
type KnowledgeDocumentHandler struct {
	repo   KnowledgeDocumentLister
	corpus serverconfig.CorpusConfig
}

// NewKnowledgeDocumentHandler creates a status-query handler bound to the
// configured corpus generation. The corpus config is the authority for the
// generation equality check; the repo is the read-only document source.
func NewKnowledgeDocumentHandler(repo KnowledgeDocumentLister, corpus serverconfig.CorpusConfig) *KnowledgeDocumentHandler {
	return &KnowledgeDocumentHandler{repo: repo, corpus: corpus}
}

// List handles GET /internal/orchestrator/knowledge-documents?generation=...&status=ACTIVE.
// Status codes: 200 = documents projected; 400 = generation/status validation;
// 401 = handled by InternalAuthMiddleware; 500 = repository failure.
func (h *KnowledgeDocumentHandler) List(c *gin.Context) {
	generation := strings.TrimSpace(c.Query("generation"))
	if generation == "" || generation != h.corpus.Generation {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    http.StatusBadRequest,
			"message": "invalid or missing generation",
			"data":    nil,
		})
		return
	}

	status, ok := validDocumentStatus(c.Query("status"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    http.StatusBadRequest,
			"message": "invalid or missing status",
			"data":    nil,
		})
		return
	}

	documents, err := h.repo.ListDocumentsByGenerationAndStatus(generation, []string{string(status)})
	if err != nil {
		// Sanitized: never surface the underlying DB/driver error text.
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    http.StatusInternalServerError,
			"message": "failed to list knowledge documents",
			"data":    nil,
		})
		return
	}

	out := make([]knowledgeDocumentStatusResponse, 0, len(documents))
	for i := range documents {
		d := documents[i]
		out = append(out, knowledgeDocumentStatusResponse{
			DocumentID:       d.DocumentID,
			SourceID:         d.SourceID,
			SourcePath:       d.SourcePath,
			SourceCommit:     d.SourceCommit,
			ContentSHA256:    d.ContentSHA256,
			Status:           string(d.Status),
			LastError:        d.LastError,
			TargetIndex:      d.TargetIndex,
			CorpusGeneration: d.CorpusGeneration,
		})
	}

	c.JSON(http.StatusOK, gin.H{"documents": out})
}

// validDocumentStatus trims and validates the status query parameter against the
// canonical lifecycle whitelist. The match is exact (case-sensitive) so callers
// must supply the uppercase constant form the model uses; lowercase or unknown
// values are rejected as 400 rather than silently coerced.
func validDocumentStatus(raw string) (model.KnowledgeDocumentStatus, bool) {
	switch strings.TrimSpace(raw) {
	case string(model.DocumentStaged):
		return model.DocumentStaged, true
	case string(model.DocumentActive):
		return model.DocumentActive, true
	case string(model.DocumentFailed):
		return model.DocumentFailed, true
	case string(model.DocumentSkipped):
		return model.DocumentSkipped, true
	default:
		return "", false
	}
}
