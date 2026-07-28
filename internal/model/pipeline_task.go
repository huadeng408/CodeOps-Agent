// Package model contains persistent models and DTOs.
package model

import "time"

const (
	PipelineStatusPending    = "PENDING"
	PipelineStatusProcessing = "PROCESSING"
	PipelineStatusSuccess    = "SUCCESS"
	PipelineStatusFailed     = "FAILED"
)

// PipelineTask tracks per-stage processing status for replay and observability.
type PipelineTask struct {
	ID             uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	FileMD5        string    `gorm:"type:varchar(32);not null;index:idx_file_stage_chunk,priority:1" json:"fileMd5"`
	Stage          string    `gorm:"type:varchar(20);not null;index:idx_file_stage_chunk,priority:2" json:"stage"`
	ChunkID        int       `gorm:"not null;default:-1;index:idx_file_stage_chunk,priority:3" json:"chunkId"`
	Status         string    `gorm:"type:varchar(20);not null;index" json:"status"`
	RetryCount     int       `gorm:"not null;default:0" json:"retryCount"`
	LastError      string    `gorm:"type:text" json:"lastError"`
	IdempotencyKey string    `gorm:"type:varchar(96);not null;uniqueIndex" json:"idempotencyKey"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"createdAt"`
}

// TableName handles table name.
func (PipelineTask) TableName() string {
	return "pipeline_task"
}
