// Package model 定义了与数据库表对应的 Go 结构体。
package model

// SearchResponseDTO 定义了返回给前端的搜索结果结构。
type SearchResponseDTO struct {
	FileMD5       string   `json:"fileMd5"`
	FileName      string   `json:"fileName"` // 新增：原始文件名
	ChunkID       int      `json:"chunkId"`
	TextContent   string   `json:"textContent"`
	Score         float64  `json:"score"` // 新增：搜索得分
	UserID        string   `json:"userId"`
	OrgTag        string   `json:"orgTag"`
	IsPublic      bool     `json:"isPublic"`
	DocumentID    string   `json:"documentId,omitempty"`
	ParentChunkID string   `json:"parentChunkId,omitempty"`
	SectionPath   []string `json:"sectionPath,omitempty"`
	PageID        string   `json:"pageId,omitempty"`
	PageSpan      []int    `json:"pageSpan,omitempty"`
	ElementIDs    []string `json:"elementIds,omitempty"`
	ElementTypes  []string `json:"elementTypes,omitempty"`
	BBoxRefs      []string `json:"bboxRefs,omitempty"`
	AssetRefs     []string `json:"assetRefs,omitempty"`
	SourceURL     string   `json:"sourceUrl,omitempty"`
	TokenCount    int      `json:"tokenCount,omitempty"`
	// CitationKey is the stable citation for this chunk
	// (source/page/element, falling back to file_md5:chunk_id), filled by
	// evidence expansion.
	CitationKey string `json:"citationKey,omitempty"`
	// ExpansionStatus is "full" when same-parent neighbor evidence was
	// loaded for this chunk, "partial" when the parent is missing.
	ExpansionStatus string `json:"expansionStatus,omitempty"`
}

// EsDocument 代表存储在 Elasticsearch 中的文档结构。
// EsDocument 定义了存储在 Elasticsearch 中的文档结构。
type EsDocument struct {
	VectorID         string    `json:"vector_id"` // 唯一标识，例如 fileMd5 + chunkId
	FileMD5          string    `json:"file_md5"`
	ChunkID          int       `json:"chunk_id"`
	TextContent      string    `json:"text_content"`
	SourceURL        string    `json:"source_url,omitempty"`
	SourceSHA256     string    `json:"source_sha256,omitempty"`
	SourcePath       string    `json:"source_path,omitempty"`
	SourceCommit     string    `json:"source_commit,omitempty"`
	SourceID         string    `json:"source_id,omitempty"`
	Vector           []float32 `json:"vector"` // 文本内容的向量表示
	ModelVersion     string    `json:"model_version"`
	DocumentID       string    `json:"document_id,omitempty"`
	ParentChunkID    string    `json:"parent_chunk_id,omitempty"`
	EmbeddingText    string    `json:"embedding_text,omitempty"`
	SectionPath      []string  `json:"section_path,omitempty"`
	PageID           string    `json:"page_id,omitempty"`
	PageSpan         []int     `json:"page_span,omitempty"`
	ElementIDs       []string  `json:"element_ids,omitempty"`
	ElementTypes     []string  `json:"element_types,omitempty"`
	BBoxRefs         []string  `json:"bbox_refs,omitempty"`
	AssetRefs        []string  `json:"asset_refs,omitempty"`
	TokenCount       int       `json:"token_count,omitempty"`
	TokenizerID      string    `json:"tokenizer_id,omitempty"`
	ParserName       string    `json:"parser_name,omitempty"`
	ParserVersion    string    `json:"parser_version,omitempty"`
	CorpusGeneration string    `json:"corpus_generation,omitempty"`
	TargetIndex      string    `json:"target_index,omitempty"`
	UserID           uint      `json:"user_id"`
	OrgTag           string    `json:"org_tag"`
	IsPublic         bool      `json:"is_public"`
}
