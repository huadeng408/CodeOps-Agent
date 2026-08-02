package model

import (
	"fmt"
	"strings"
)

// StructuredChunk is the durable, traceable unit produced by structured ingestion.
type StructuredChunk struct {
	DocumentID       string   `json:"document_id"`
	SourceSHA256     string   `json:"source_sha256,omitempty"`
	SourceURL        string   `json:"source_url,omitempty"`
	ChunkID          string   `json:"chunk_id"`
	ParentChunkID    string   `json:"parent_chunk_id,omitempty"`
	Text             string   `json:"text"`
	EmbeddingText    string   `json:"embedding_text,omitempty"`
	SectionPath      []string `json:"section_path,omitempty"`
	PageID           string   `json:"page_id"`
	PageSpan         []int    `json:"page_span,omitempty"`
	ElementIDs       []string `json:"element_ids"`
	ElementTypes     []string `json:"element_types"`
	BBoxRefs         []string `json:"bbox_refs,omitempty"`
	AssetRefs        []string `json:"asset_refs,omitempty"`
	TokenCount       int      `json:"token_count"`
	TokenizerID      string   `json:"tokenizer_id"`
	ParserName       string   `json:"parser_name"`
	ParserVersion    string   `json:"parser_version"`
	CorpusGeneration string   `json:"corpus_generation"`
	TargetIndex      string   `json:"target_index,omitempty"`
	FileMD5          string   `json:"file_md5,omitempty"`
	UserID           uint     `json:"user_id"`
	OrgTag           string   `json:"org_tag,omitempty"`
	IsPublic         bool     `json:"is_public"`
}

// Validate checks the minimum provenance required for durable retrieval.
func (c StructuredChunk) Validate() error {
	if strings.TrimSpace(c.DocumentID) == "" {
		return fmt.Errorf("document_id is required")
	}
	if strings.TrimSpace(c.SourceSHA256) == "" {
		return fmt.Errorf("source_sha256 is required")
	}
	if strings.TrimSpace(c.ChunkID) == "" {
		return fmt.Errorf("chunk_id is required")
	}
	if c.TokenCount <= 0 {
		return fmt.Errorf("token_count must be positive")
	}
	if strings.TrimSpace(c.ParserName) == "" {
		return fmt.Errorf("parser_name is required")
	}
	if strings.TrimSpace(c.ParserVersion) == "" {
		return fmt.Errorf("parser_version is required")
	}
	if strings.TrimSpace(c.CorpusGeneration) == "" {
		return fmt.Errorf("corpus_generation is required")
	}
	if strings.TrimSpace(c.PageID) == "" {
		return fmt.Errorf("page_id provenance is required")
	}
	if len(c.ElementIDs) == 0 {
		return fmt.Errorf("element_ids provenance is required")
	}
	if len(c.ElementTypes) == 0 {
		return fmt.Errorf("element_types provenance is required")
	}
	return nil
}
