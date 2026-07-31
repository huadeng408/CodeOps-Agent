// Package model contains persistent models and DTOs.
package model

// DocumentVector 对应于数据库中的 document_vectors 表。
// 它的结构与 Java 项目中的 DocumentVector 实体完全一致。
type DocumentVector struct {
	VectorID         uint     `gorm:"primaryKey;autoIncrement;column:vector_id"`
	FileMD5          string   `gorm:"type:varchar(32);not null;index;column:file_md5"`
	ChunkID          int      `gorm:"not null;column:chunk_id"`
	TextContent      string   `gorm:"type:text;column:text_content"`
	EmbeddingText    string   `gorm:"type:text;column:embedding_text"`
	ModelVersion     string   `gorm:"type:varchar(128);column:model_version"`
	DocumentID       string   `gorm:"type:varchar(512);column:document_id"`
	PageID           string   `gorm:"type:varchar(255);column:page_id"`
	ParentChunkID    string   `gorm:"type:varchar(255);column:parent_chunk_id"`
	SectionPath      []string `gorm:"type:json;serializer:json;column:section_path"`
	PageSpan         []int    `gorm:"type:json;serializer:json;column:page_span"`
	ElementIDs       []string `gorm:"type:json;serializer:json;column:element_ids"`
	ElementTypes     []string `gorm:"type:json;serializer:json;column:element_types"`
	BBoxRefs         []string `gorm:"type:json;serializer:json;column:bbox_refs"`
	AssetRefs        []string `gorm:"type:json;serializer:json;column:asset_refs"`
	TokenCount       int      `gorm:"column:token_count"`
	TokenizerID      string   `gorm:"type:varchar(255);column:tokenizer_id"`
	ParserName       string   `gorm:"type:varchar(128);column:parser_name"`
	ParserVersion    string   `gorm:"type:varchar(128);column:parser_version"`
	CorpusGeneration string   `gorm:"type:varchar(128);column:corpus_generation"`
	TargetIndex      string   `gorm:"type:varchar(255);column:target_index"`
	UserID           uint     `gorm:"not null;column:user_id"`
	OrgTag           string   `gorm:"type:varchar(50);column:org_tag"`
	IsPublic         bool     `gorm:"not null;default:false;column:is_public"`
}

// TableName handles table name.
func (DocumentVector) TableName() string {
	return "document_vectors"
}
