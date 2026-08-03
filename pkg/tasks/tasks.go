// Package tasks defines the structure for tasks that are sent to Kafka.
package tasks

import "code-agent/internal/model"

type Stage string

const (
	StageParse Stage = "parse"
	StageChunk Stage = "chunk"
	StageEmbed Stage = "embed"
	StageIndex Stage = "index"
)

// FileProcessingTask represents the data structure for a pipeline processing job.
type FileProcessingTask struct {
	FileMD5      string `json:"file_md5"`
	ObjectURL    string `json:"object_url,omitempty"`
	FileName     string `json:"file_name"`
	UserID       uint   `json:"user_id"`
	OrgTag       string `json:"org_tag"`
	IsPublic     bool   `json:"is_public"`
	Stage        Stage  `json:"stage"`
	TaskChunkID  int    `json:"task_chunk_id,omitempty"`
	ChunkStart   int    `json:"chunk_start,omitempty"`
	TotalChunks  int    `json:"total_chunks,omitempty"`
	ParsedObject string `json:"parsed_object,omitempty"`
	LastError    string `json:"last_error,omitempty"`
	// CorpusGeneration marks documents that belong to a pinned corpus; the
	// pipeline then writes structured chunks to the corpus text index and
	// enforces the native embedding contract.
	CorpusGeneration string `json:"corpus_generation,omitempty"`
	// RunID scopes this message to a controlled run so the consumer does not
	// skip it on a stale prior SUCCESS (empty keeps legacy dedup semantics).
	RunID string `json:"run_id,omitempty"`
	// DocumentID is the knowledge_document identifier the pipeline updates as
	// the document advances toward ACTIVE.
	DocumentID string `json:"document_id,omitempty"`
	// Provenance carries the pinned corpus identity (source commit + hashes +
	// target index) validated at the internal trust boundary. Nil for ordinary
	// uploads, which never enter the corpus pipeline.
	Provenance *model.CorpusProvenance `json:"provenance,omitempty"`
}
