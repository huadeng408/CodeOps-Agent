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
	// Run-scoped operations. These use a run-prefixed idempotency_key
	// (run:<runID>:<fileMD5>:<stage>:<chunkID>) so a controlled replay with a
	// fresh RunID is deduped within the run but is NOT shadowed by a legacy
	// SUCCESS row (which carries the old file_md5:stage:chunk_id key). Legacy
	// methods above keep their original semantics for messages without RunID.
	GetByRunKey(runID, fileMD5, stage string, chunkID int) (*model.PipelineTask, error)
	MarkProcessingRun(runID, fileMD5, stage string, chunkID int) (*model.PipelineTask, error)
	MarkSuccessRun(runID, fileMD5, stage string, chunkID int) error
	MarkRetryRun(runID, fileMD5, stage string, chunkID int, lastError string) (int, error)
	MarkFailedRun(runID, fileMD5, stage string, chunkID int, lastError string) error
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

// buildPipelineKey builds the legacy pipeline key (no run scope).
func buildPipelineKey(fileMD5, stage string, chunkID int) string {
	return fmt.Sprintf("%s:%s:%d", fileMD5, stage, chunkID)
}

// buildRunKey builds the run-scoped idempotency key. The "run:" prefix keeps
// run-keyed rows in a separate namespace from legacy keys (file_md5:stage:chunk_id),
// so both can coexist for the same file_md5 without colliding on the unique index.
func buildRunKey(runID, fileMD5, stage string, chunkID int) string {
	return fmt.Sprintf("run:%s:%s:%s:%d", runID, fileMD5, stage, chunkID)
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

// GetByRunKey returns the row for a run-scoped idempotency key, or
// gorm.ErrRecordNotFound if no such run row exists.
func (r *pipelineTaskRepository) GetByRunKey(runID, fileMD5, stage string, chunkID int) (*model.PipelineTask, error) {
	var task model.PipelineTask
	err := r.db.Where("idempotency_key = ?", buildRunKey(runID, fileMD5, stage, chunkID)).First(&task).Error
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// MarkProcessingRun upserts a PROCESSING row scoped to runID. Re-marking the
// same run key returns the same row without raising a unique conflict, so
// duplicate messages within a controlled replay dedup cleanly.
func (r *pipelineTaskRepository) MarkProcessingRun(runID, fileMD5, stage string, chunkID int) (*model.PipelineTask, error) {
	task, err := r.GetByRunKey(runID, fileMD5, stage, chunkID)
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
			RunID:          runID,
			IdempotencyKey: buildRunKey(runID, fileMD5, stage, chunkID),
		}
		return task, r.db.Create(task).Error
	}
	task.Status = model.PipelineStatusProcessing
	return task, r.db.Save(task).Error
}

// MarkSuccessRun flips the run row to SUCCESS.
func (r *pipelineTaskRepository) MarkSuccessRun(runID, fileMD5, stage string, chunkID int) error {
	task, err := r.MarkProcessingRun(runID, fileMD5, stage, chunkID)
	if err != nil {
		return err
	}
	task.Status = model.PipelineStatusSuccess
	task.LastError = ""
	return r.db.Save(task).Error
}

// MarkRetryRun bumps the run row retry counter and records the sanitized error.
func (r *pipelineTaskRepository) MarkRetryRun(runID, fileMD5, stage string, chunkID int, lastError string) (int, error) {
	task, err := r.MarkProcessingRun(runID, fileMD5, stage, chunkID)
	if err != nil {
		return 0, err
	}
	task.Status = model.PipelineStatusFailed
	task.RetryCount++
	task.LastError = lastError
	return task.RetryCount, r.db.Save(task).Error
}

// MarkFailedRun sets the run row to FAILED with the sanitized error.
func (r *pipelineTaskRepository) MarkFailedRun(runID, fileMD5, stage string, chunkID int, lastError string) error {
	task, err := r.MarkProcessingRun(runID, fileMD5, stage, chunkID)
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
