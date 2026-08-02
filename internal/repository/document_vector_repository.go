// Package repository contains data-access code.
package repository

import (
	"code-agent/internal/model"

	"gorm.io/gorm"
)

// DocumentVectorRepository defines persistence operations for document vector data.
type DocumentVectorRepository interface {
	BatchCreate(vectors []*model.DocumentVector) error
	FindByFileMD5(fileMD5 string) ([]*model.DocumentVector, error)
	FindByFileMD5Range(fileMD5 string, offset, limit int) ([]*model.DocumentVector, error)
	CountByFileMD5(fileMD5 string) (int64, error)
	DeleteByFileMD5(fileMD5 string) error
	// FindByParentChunkID returns all children of a parent chunk (evidence
	// expansion support). ACL enforcement happens in the caller.
	FindByParentChunkID(parentChunkID string) ([]*model.DocumentVector, error)
}

// documentVectorRepository implements persistence operations for document vector data.
type documentVectorRepository struct {
	db *gorm.DB
}

// NewDocumentVectorRepository creates a document vector repository.
func NewDocumentVectorRepository(db *gorm.DB) DocumentVectorRepository {
	return &documentVectorRepository{db: db}
}

// FindByParentChunkID finds all children of a parent chunk.
func (r *documentVectorRepository) FindByParentChunkID(parentChunkID string) ([]*model.DocumentVector, error) {
	if parentChunkID == "" {
		return []*model.DocumentVector{}, nil
	}
	var vectors []*model.DocumentVector
	err := r.db.Where("parent_chunk_id = ?", parentChunkID).Order("chunk_id asc").Find(&vectors).Error
	return vectors, err
}

// BatchCreate handles batch create.
func (r *documentVectorRepository) BatchCreate(vectors []*model.DocumentVector) error {
	if len(vectors) == 0 {
		return nil
	}
	return r.db.CreateInBatches(vectors, 100).Error
}

// FindByFileMD5 finds by file 5.
func (r *documentVectorRepository) FindByFileMD5(fileMD5 string) ([]*model.DocumentVector, error) {
	var vectors []*model.DocumentVector
	err := r.db.Where("file_md5 = ?", fileMD5).Order("chunk_id asc").Find(&vectors).Error
	return vectors, err
}

// FindByFileMD5Range finds by file 5 range.
func (r *documentVectorRepository) FindByFileMD5Range(fileMD5 string, offset, limit int) ([]*model.DocumentVector, error) {
	if limit <= 0 {
		return []*model.DocumentVector{}, nil
	}
	var vectors []*model.DocumentVector
	err := r.db.
		Where("file_md5 = ?", fileMD5).
		Order("chunk_id asc").
		Offset(offset).
		Limit(limit).
		Find(&vectors).Error
	return vectors, err
}

// CountByFileMD5 handles count by file 5.
func (r *documentVectorRepository) CountByFileMD5(fileMD5 string) (int64, error) {
	var count int64
	err := r.db.Model(&model.DocumentVector{}).Where("file_md5 = ?", fileMD5).Count(&count).Error
	return count, err
}

// DeleteByFileMD5 deletes by file 5.
func (r *documentVectorRepository) DeleteByFileMD5(fileMD5 string) error {
	return r.db.Where("file_md5 = ?", fileMD5).Delete(&model.DocumentVector{}).Error
}
