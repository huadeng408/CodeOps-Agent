package pipeline

import (
	"context"
	"errors"
	"io"
	"time"

	"code-agent/internal/model"
	"code-agent/internal/repository"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/documentparser"
	"code-agent/pkg/embedding"
	orchestratorclient "code-agent/pkg/orchestrator"
	"code-agent/pkg/tasks"
)

// ObjectStore is the object-storage seam used by pipeline stages.
type ObjectStore interface {
	Read(context.Context, string, string) (io.ReadCloser, error)
	Write(context.Context, string, string, io.Reader, int64, string) error
	Presign(string, string, time.Duration) (string, error)
	Delete(context.Context, string, string) error
}

// TaskQueue is the queue seam used to advance a file-processing task.
type TaskQueue interface {
	Publish(tasks.FileProcessingTask) error
}

// EmbeddingCache is the cache seam for embedding windows.
type EmbeddingCache interface {
	Clear(context.Context, string) error
	Put(context.Context, string, map[int][]float32) error
	SetTTL(context.Context, string, time.Duration) error
	Load(context.Context, string) (map[int][]float32, error)
}

// IndexWriter is the Elasticsearch write seam.
type IndexWriter interface {
	Write(context.Context, string, []model.EsDocument) error
}

// DocumentLifecycle records the pipeline-owned document state transitions.
type DocumentLifecycle interface {
	Active(context.Context, tasks.FileProcessingTask) error
	Skipped(context.Context, tasks.FileProcessingTask, string) error
	Failed(context.Context, tasks.FileProcessingTask, string, error) error
}

// PipelineDeps is the composition input for Processor. Infrastructure clients
// are intentionally represented only by the five pipeline ports above.
type PipelineDeps struct {
	ObjectStore       ObjectStore
	TaskQueue         TaskQueue
	EmbeddingCache    EmbeddingCache
	IndexWriter       IndexWriter
	DocumentLifecycle DocumentLifecycle

	DocumentParser  *documentparser.Client
	EmbeddingClient embedding.Client
	Elasticsearch   serverconfig.ElasticsearchConfig
	MinIO           serverconfig.MinIOConfig
	Embedding       serverconfig.EmbeddingConfig
	Corpus          serverconfig.CorpusConfig
	Kafka           serverconfig.KafkaConfig
	UploadRepo      repository.UploadRepository
	DocVectorRepo   repository.DocumentVectorRepository
	IngestionClient orchestratorclient.IngestionClient
	DocumentRepo    repository.KnowledgeDocumentRepository
}

// NewProcessorWithDeps constructs a Processor from explicit ports and
// application dependencies. The required ports are validated in a stable
// order so composition errors are deterministic.
func NewProcessorWithDeps(deps PipelineDeps) (*Processor, error) {
	if deps.ObjectStore == nil {
		return nil, errors.New("pipeline: object store is required")
	}
	if deps.TaskQueue == nil {
		return nil, errors.New("pipeline: task queue is required")
	}
	if deps.EmbeddingCache == nil {
		return nil, errors.New("pipeline: embedding cache is required")
	}
	if deps.IndexWriter == nil {
		return nil, errors.New("pipeline: index writer is required")
	}
	if deps.DocumentLifecycle == nil {
		return nil, errors.New("pipeline: document lifecycle is required")
	}

	return &Processor{
		documentParser:  deps.DocumentParser,
		embeddingClient: deps.EmbeddingClient,
		esCfg:           deps.Elasticsearch,
		minioCfg:        deps.MinIO,
		embeddingCfg:    deps.Embedding,
		corpusCfg:       deps.Corpus,
		kafkaCfg:        deps.Kafka,
		uploadRepo:      deps.UploadRepo,
		docVectorRepo:   deps.DocVectorRepo,
		ingestionClient: deps.IngestionClient,
		documentRepo:    deps.DocumentRepo,
		objectStore:     deps.ObjectStore,
		taskQueue:       deps.TaskQueue,
		embeddingCache:  deps.EmbeddingCache,
		indexWriter:     deps.IndexWriter,
		lifecycle:       deps.DocumentLifecycle,
	}, nil
}
