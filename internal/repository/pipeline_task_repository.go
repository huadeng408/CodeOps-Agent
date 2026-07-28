// Package repository contains data-access code.
package repository

import (
	"errors"
	"fmt"
	"code-agent/internal/model"

	"gorm.io/gorm"
)

// PipelineTaskRepository defines persistence operations for pipeline task data.
type PipelineTaskRepository interface {
	GetByKey(fileMD5, stage string, chunkID int) (*model.PipelineTask, error)
	MarkProcessing(fileMD5, stage string, chunkID int) (*model.PipelineTask, error)
	MarkSuccess(fileMD5, stage string, chunkID int) error
	MarkRetry(fileMD5, stage string, chunkID int, lastError string) (int, error)
	MarkFailed(fileMD5, stage string, chunkID int, lastError string) error
	ListFailedByFile(fileMD5 string) ([]model.PipelineTask, error)
	DeleteByFileMD5(fileMD5 string) error
}

// pipelineTaskRepository implements persistence operations for pipeline task data.
type pipelineTaskRepository struct {
	db *gorm.DB
}

// NewPipelineTaskRepository creates a pipeline task repository.
func NewPipelineTaskRepository(db *gorm.DB) PipelineTaskRepository {
	return &pipelineTaskRepository{db: db}
}

// buildPipelineKey builds pipeline key.
func buildPipelineKey(fileMD5, stage string, chunkID int) string {
	return fmt.Sprintf("%s:%s:%d", fileMD5, stage, chunkID)
}

// GetByKey returns by key.
func (r *pipelineTaskRepository) GetByKey(fileMD5, stage string, chunkID int) (*model.PipelineTask, error) {
	var task model.PipelineTask
	err := r.db.Where("file_md5 = ? AND stage = ? AND chunk_id = ?", fileMD5, stage, chunkID).First(&task).Error
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// MarkProcessing handles mark processing.
func (r *pipelineTaskRepository) MarkProcessing(fileMD5, stage string, chunkID int) (*model.PipelineTask, error) {
	task, err := r.GetByKey(fileMD5, stage, chunkID)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		task = &model.PipelineTask{
			FileMD5:        fileMD5,
			Stage:          stage,
			ChunkID:        chunkID,
			Status:         model.PipelineStatusProcessing,
			RetryCount:     0,
			IdempotencyKey: buildPipelineKey(fileMD5, stage, chunkID),
		}
		return task, r.db.Create(task).Error
	}
	task.Status = model.PipelineStatusProcessing
	return task, r.db.Save(task).Error
}

// MarkSuccess handles mark success.
func (r *pipelineTaskRepository) MarkSuccess(fileMD5, stage string, chunkID int) error {
	task, err := r.MarkProcessing(fileMD5, stage, chunkID)
	if err != nil {
		return err
	}
	task.Status = model.PipelineStatusSuccess
	task.LastError = ""
	return r.db.Save(task).Error
}

// MarkRetry handles mark retry.
func (r *pipelineTaskRepository) MarkRetry(fileMD5, stage string, chunkID int, lastError string) (int, error) {
	task, err := r.MarkProcessing(fileMD5, stage, chunkID)
	if err != nil {
		return 0, err
	}
	task.Status = model.PipelineStatusFailed
	task.RetryCount++
	task.LastError = lastError
	return task.RetryCount, r.db.Save(task).Error
}

// MarkFailed handles mark failed.
func (r *pipelineTaskRepository) MarkFailed(fileMD5, stage string, chunkID int, lastError string) error {
	task, err := r.MarkProcessing(fileMD5, stage, chunkID)
	if err != nil {
		return err
	}
	task.Status = model.PipelineStatusFailed
	task.LastError = lastError
	return r.db.Save(task).Error
}

// ListFailedByFile lists failed by file.
func (r *pipelineTaskRepository) ListFailedByFile(fileMD5 string) ([]model.PipelineTask, error) {
	var tasks []model.PipelineTask
	err := r.db.Where("file_md5 = ? AND status = ?", fileMD5, model.PipelineStatusFailed).Order("updated_at desc").Find(&tasks).Error
	return tasks, err
}

// DeleteByFileMD5 deletes all pipeline task rows for one file.
func (r *pipelineTaskRepository) DeleteByFileMD5(fileMD5 string) error {
	return r.db.Where("file_md5 = ?", fileMD5).Delete(&model.PipelineTask{}).Error
}
