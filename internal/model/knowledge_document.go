// Package model contains persistent models and DTOs.
package model

import (
	"fmt"
	"strings"
	"time"
)

// DocumentID returns the stable identifier for a source document path.
// Empty components return an empty identifier rather than creating an ambiguous key.
func DocumentID(sourceID, sourceCommit, sourcePath string) string {
	sourceID = strings.TrimSpace(sourceID)
	sourceCommit = strings.TrimSpace(sourceCommit)
	sourcePath = strings.TrimSpace(sourcePath)
	if sourceID == "" || sourceCommit == "" || sourcePath == "" {
		return ""
	}
	return fmt.Sprintf("%s@%s:%s", sourceID, sourceCommit, sourcePath)
}

// KnowledgeDocument records one source document and its ingestion provenance.
type KnowledgeDocument struct {
	DocumentID       string    `gorm:"type:varchar(512);primaryKey" json:"documentId"`
	SourceID         string    `gorm:"type:varchar(128);not null;index" json:"sourceId"`
	SourcePath       string    `gorm:"type:varchar(1024);not null" json:"sourcePath"`
	SourceURL        string    `gorm:"type:varchar(1024)" json:"sourceUrl"`
	SourceCommit     string    `gorm:"type:varchar(64);not null" json:"sourceCommit"`
	Title            string    `gorm:"type:varchar(512)" json:"title"`
	DocumentLanguage string    `gorm:"type:varchar(32);not null" json:"documentLanguage"`
	ContentSHA256    string    `gorm:"type:varchar(64);not null" json:"contentSha256"`
	FileMD5          string    `gorm:"type:varchar(32);index" json:"fileMd5"`
	SourceVersionID  string    `gorm:"type:varchar(255);not null;index" json:"sourceVersionId"`
	CorpusGeneration string    `gorm:"type:varchar(128);not null;index" json:"corpusGeneration"`
	TargetIndex      string    `gorm:"type:varchar(255);not null" json:"targetIndex"`
	Status           string    `gorm:"type:varchar(20);not null;index" json:"status"`
	RetryCount       int       `gorm:"not null;default:0" json:"retryCount"`
	LastError        string    `gorm:"type:text" json:"lastError"`
	CreatedAt        time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt        time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

// TableName handles table name.
func (KnowledgeDocument) TableName() string {
	return "knowledge_document"
}
