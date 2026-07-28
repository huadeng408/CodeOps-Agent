// Package tasks defines the structure for tasks that are sent to Kafka.
package tasks

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
}
