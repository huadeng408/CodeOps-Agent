package repository

import (
	"fmt"
	"strings"

	"code-agent/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// KnowledgeDocumentRepository persists immutable document provenance and status.
type KnowledgeDocumentRepository interface {
	CreateOrGetDocument(document *model.KnowledgeDocument) (*model.KnowledgeDocument, error)
	MarkDocumentStatus(documentID string, status model.KnowledgeDocumentStatus, lastError string) error
	ListDocumentsByGeneration(generation string) ([]model.KnowledgeDocument, error)
	// ListDocumentsByGenerationAndStatus returns documents whose corpus_generation
	// matches and status is in the whitelist, ordered by document_id for
	// deterministic enumeration. An empty statuses slice skips the status filter
	// (returns all rows of the generation). Used by the read-only importer polling
	// endpoint.
	ListDocumentsByGenerationAndStatus(generation string, statuses []string) ([]model.KnowledgeDocument, error)
	CountActiveDocuments(generation string) (int64, error)
	// GetDocumentByFileMD5 returns the most relevant document row for a file_md5.
	// When content has changed over time there may be several versions sharing
	// one file_md5; a non-FAILED row is preferred (usability beats recency) and,
	// among rows of equal usability, the most recently updated one wins. Returns
	// gorm.ErrRecordNotFound when no row exists, so callers can treat a missing
	// file_md5 as "not a corpus document" without ambiguity.
	GetDocumentByFileMD5(fileMD5 string) (*model.KnowledgeDocument, error)
}

type knowledgeDocumentRepository struct {
	db *gorm.DB
}

func NewKnowledgeDocumentRepository(db *gorm.DB) KnowledgeDocumentRepository {
	return &knowledgeDocumentRepository{db: db}
}

func (r *knowledgeDocumentRepository) CreateOrGetDocument(document *model.KnowledgeDocument) (*model.KnowledgeDocument, error) {
	if document == nil || strings.TrimSpace(document.DocumentID) == "" {
		return nil, fmt.Errorf("document_id is required")
	}
	var existing model.KnowledgeDocument
	err := r.db.First(&existing, "document_id = ?", document.DocumentID).Error
	if err == nil {
		if existing.ContentSHA256 == document.ContentSHA256 {
			return &existing, nil
		}
		if strings.TrimSpace(document.ContentSHA256) == "" {
			return nil, fmt.Errorf("content_sha256 is required for a new document version")
		}
		document.DocumentID = document.DocumentID + "#" + document.ContentSHA256
	} else if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	if err := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(document).Error; err != nil {
		return nil, err
	}
	var stored model.KnowledgeDocument
	if err := r.db.First(&stored, "document_id = ?", document.DocumentID).Error; err != nil {
		return nil, err
	}
	return &stored, nil
}

func (r *knowledgeDocumentRepository) MarkDocumentStatus(documentID string, status model.KnowledgeDocumentStatus, lastError string) error {
	if strings.TrimSpace(documentID) == "" {
		return fmt.Errorf("document_id is required")
	}
	return r.db.Model(&model.KnowledgeDocument{}).
		Where("document_id = ?", documentID).
		Updates(map[string]any{"status": status, "last_error": lastError}).Error
}

func (r *knowledgeDocumentRepository) ListDocumentsByGeneration(generation string) ([]model.KnowledgeDocument, error) {
	var documents []model.KnowledgeDocument
	err := r.db.Where("corpus_generation = ?", generation).Order("document_id asc").Find(&documents).Error
	return documents, err
}

// ListDocumentsByGenerationAndStatus returns documents whose corpus_generation
// matches and status is in the whitelist, ordered by document_id. An empty
// statuses slice skips the status filter. The IN clause is only appended when
// at least one status is supplied so the zero-value call stays portable across
// MySQL and sqlite.
func (r *knowledgeDocumentRepository) ListDocumentsByGenerationAndStatus(generation string, statuses []string) ([]model.KnowledgeDocument, error) {
	var documents []model.KnowledgeDocument
	query := r.db.Where("corpus_generation = ?", generation)
	if len(statuses) > 0 {
		query = query.Where("status IN ?", statuses)
	}
	err := query.Order("document_id asc").Find(&documents).Error
	return documents, err
}

func (r *knowledgeDocumentRepository) CountActiveDocuments(generation string) (int64, error) {
	var count int64
	err := r.db.Model(&model.KnowledgeDocument{}).
		Where("corpus_generation = ? AND status = ?", generation, model.DocumentActive).
		Count(&count).Error
	return count, err
}

// GetDocumentByFileMD5 returns the most relevant document row for a file_md5.
// When content has changed over time there may be several versions sharing
// one file_md5; a non-FAILED row is preferred (usability beats recency) and,
// among rows of equal usability, the most recently updated one wins. Returns
// gorm.ErrRecordNotFound when no row exists so callers can treat a missing
// file_md5 as "not a corpus document" without ambiguity. Selection happens in
// Go rather than via a DB-specific boolean sort expression so the query stays
// portable across MySQL and the sqlite test harness.
func (r *knowledgeDocumentRepository) GetDocumentByFileMD5(fileMD5 string) (*model.KnowledgeDocument, error) {
	var documents []model.KnowledgeDocument
	if err := r.db.Where("file_md5 = ?", fileMD5).Order("updated_at desc").Find(&documents).Error; err != nil {
		return nil, err
	}
	if len(documents) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	for i := range documents {
		if documents[i].Status != model.DocumentFailed {
			return &documents[i], nil
		}
	}
	return &documents[0], nil
}
