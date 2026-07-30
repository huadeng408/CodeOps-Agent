package repository

import (
	"fmt"
	"strings"

	"code-agent/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// KnowledgeSourceRepository persists immutable source-version metadata.
type KnowledgeSourceRepository interface {
	CreateOrGetSource(source *model.KnowledgeSource) (*model.KnowledgeSource, error)
}

type knowledgeSourceRepository struct {
	db *gorm.DB
}

func NewKnowledgeSourceRepository(db *gorm.DB) KnowledgeSourceRepository {
	return &knowledgeSourceRepository{db: db}
}

func (r *knowledgeSourceRepository) CreateOrGetSource(source *model.KnowledgeSource) (*model.KnowledgeSource, error) {
	if source == nil || strings.TrimSpace(source.SourceVersionID) == "" {
		return nil, fmt.Errorf("source_version_id is required")
	}
	if err := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(source).Error; err != nil {
		return nil, err
	}
	var stored model.KnowledgeSource
	if err := r.db.First(&stored, "source_version_id = ?", source.SourceVersionID).Error; err != nil {
		return nil, err
	}
	return &stored, nil
}
