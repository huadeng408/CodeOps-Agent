// Package model contains persistent models and DTOs.
package model

import (
	"fmt"
	"strings"
	"time"
)

// KnowledgeSourceStatus describes the lifecycle of a source version.
type KnowledgeSourceStatus string

const (
	SourceStaged  KnowledgeSourceStatus = "STAGED"
	SourcePiloted KnowledgeSourceStatus = "PILOTED"
	SourceActive  KnowledgeSourceStatus = "ACTIVE"
	SourceFailed  KnowledgeSourceStatus = "FAILED"
)

// SourceVersionID returns the stable identifier for one source checkout and corpus generation.
// Empty components return an empty identifier rather than creating an ambiguous key.
func SourceVersionID(sourceID, sourceCommit, corpusGeneration string) string {
	sourceID = strings.TrimSpace(sourceID)
	sourceCommit = strings.TrimSpace(sourceCommit)
	corpusGeneration = strings.TrimSpace(corpusGeneration)
	if sourceID == "" || sourceCommit == "" || corpusGeneration == "" {
		return ""
	}
	return fmt.Sprintf("%s@%s#%s", sourceID, sourceCommit, corpusGeneration)
}

// KnowledgeSource records the provenance and lifecycle of a source version.
type KnowledgeSource struct {
	SourceVersionID  string                `gorm:"type:varchar(255);primaryKey" json:"sourceVersionId"`
	SourceID         string                `gorm:"type:varchar(128);not null;index" json:"sourceId"`
	RepositoryURL    string                `gorm:"type:varchar(512)" json:"repositoryUrl"`
	SourceCommit     string                `gorm:"type:varchar(64);not null" json:"sourceCommit"`
	LicenseSPDX      string                `gorm:"type:varchar(64)" json:"licenseSpdx"`
	LicensePath      string                `gorm:"type:varchar(255)" json:"licensePath"`
	LicenseSHA256    string                `gorm:"type:varchar(64)" json:"licenseSha256"`
	CorpusGeneration string                `gorm:"type:varchar(128);not null;index" json:"corpusGeneration"`
	Status           KnowledgeSourceStatus `gorm:"type:varchar(20);not null;index" json:"status"`
	LastError        string                `gorm:"type:text" json:"lastError"`
	FetchedAt        *time.Time            `gorm:"default:null" json:"fetchedAt"`
	CreatedAt        time.Time             `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt        time.Time             `gorm:"autoUpdateTime" json:"updatedAt"`
}

// TableName handles table name.
func (KnowledgeSource) TableName() string {
	return "knowledge_source"
}
