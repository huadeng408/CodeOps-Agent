// Package pipeline contains the async document pipeline.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"code-agent/internal/model"
	"code-agent/internal/repository"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/documentparser"
	"code-agent/pkg/embedding"
	orchestratorclient "code-agent/pkg/orchestrator"
	"code-agent/pkg/tasks"
)

const (
	embeddingCacheTTLSeconds   = 7200
	minimumEmbedWindowChunks   = 256
	embedWindowBatchMultiplier = 64
)

// Processor represents a processor.
type Processor struct {
	documentParser  *documentparser.Client
	embeddingClient embedding.Client
	esCfg           serverconfig.ElasticsearchConfig
	minioCfg        serverconfig.MinIOConfig
	embeddingCfg    serverconfig.EmbeddingConfig
	corpusCfg       serverconfig.CorpusConfig
	kafkaCfg        serverconfig.KafkaConfig
	uploadRepo      repository.UploadRepository
	docVectorRepo   repository.DocumentVectorRepository
	ingestionClient orchestratorclient.IngestionClient
	// documentRepo advances the knowledge_document lifecycle (STAGED→ACTIVE
	// on index success, →FAILED on persist failure). Nil-tolerant so legacy
	// uploads and tests that never carry a DocumentID are left untouched.
	documentRepo repository.KnowledgeDocumentRepository

	objectStore    ObjectStore
	taskQueue      TaskQueue
	embeddingCache EmbeddingCache
	indexWriter    IndexWriter
	lifecycle      DocumentLifecycle

	// esWriter is the legacy function seam retained for existing tests.
	// Injectable so tests can assert zero writes on validation failure.
	esWriter func(ctx context.Context, index string, docs []model.EsDocument) error
	// vectorCache loads cached embeddings; defaulted to loadCachedEmbeddingMap.
	// Injectable so tests avoid the Redis global.
	vectorCache func(ctx context.Context, cacheKey string) (map[int][]float32, error)
}

// NewProcessor creates a processor using the legacy global clients through
// production adapters. It remains source-compatible with existing callers.
func NewProcessor(
	documentParser *documentparser.Client,
	embeddingClient embedding.Client,
	esCfg serverconfig.ElasticsearchConfig,
	minioCfg serverconfig.MinIOConfig,
	embeddingCfg serverconfig.EmbeddingConfig,
	corpusCfg serverconfig.CorpusConfig,
	kafkaCfg serverconfig.KafkaConfig,
	uploadRepo repository.UploadRepository,
	docVectorRepo repository.DocumentVectorRepository,
	ingestionClient orchestratorclient.IngestionClient,
	documentRepo repository.KnowledgeDocumentRepository,
) *Processor {
	processor, err := NewProcessorWithDeps(PipelineDeps{
		ObjectStore:       legacyObjectStore{cfg: minioCfg},
		TaskQueue:         legacyTaskQueue{},
		EmbeddingCache:    legacyEmbeddingCache{},
		IndexWriter:       legacyIndexWriter{},
		DocumentLifecycle: legacyDocumentLifecycle{repo: documentRepo},
		DocumentParser:    documentParser,
		EmbeddingClient:   embeddingClient,
		Elasticsearch:     esCfg,
		MinIO:             minioCfg,
		Embedding:         embeddingCfg,
		Corpus:            corpusCfg,
		Kafka:             kafkaCfg,
		UploadRepo:        uploadRepo,
		DocVectorRepo:     docVectorRepo,
		IngestionClient:   ingestionClient,
		DocumentRepo:      documentRepo,
	})
	if err != nil {
		panic(err)
	}
	return processor
}

// Process handles process.
func (p *Processor) Process(ctx context.Context, task tasks.FileProcessingTask) error {
	switch task.Stage {
	case tasks.StageParse:
		return p.processParse(ctx, task)
	case tasks.StageChunk:
		return p.processChunk(ctx, task)
	case tasks.StageEmbed:
		return p.processEmbed(ctx, task)
	case tasks.StageIndex:
		return p.processIndex(ctx, task)
	default:
		return fmt.Errorf("unknown pipeline stage: %s", task.Stage)
	}
}

// processIndex delegates index policy to the stage module while Processor
// retains the stable Process routing interface.
func (p *Processor) processIndex(ctx context.Context, task tasks.FileProcessingTask) error {
	return p.indexStage().process(ctx, task)
}

func structuredArtifact(task tasks.FileProcessingTask, artifact orchestratorclient.ParsedArtifact) bool {
	return strings.EqualFold(filepath.Ext(task.FileName), ".pdf") ||
		len(artifact.Elements) > 0 ||
		strings.TrimSpace(artifact.DocumentID) != "" ||
		strings.TrimSpace(artifact.ParserName) != "" ||
		strings.TrimSpace(artifact.ParserVersion) != ""
}

func documentVectorFromStructuredChunk(task tasks.FileProcessingTask, index int, chunk model.StructuredChunk, modelVersion string) *model.DocumentVector {
	sourceText := chunk.Text
	if sourceText == "" {
		sourceText = chunk.EmbeddingText
	}
	embeddingText := chunk.EmbeddingText
	if embeddingText == "" {
		embeddingText = sourceText
	}
	// The task's knowledge_document identifier (git@commit:path) is the
	// corpus document_id eval qrels match on; the worker-supplied chunk
	// identifier is only a fallback for legacy uploads without a task
	// DocumentID.
	documentID := chunk.DocumentID
	if strings.TrimSpace(task.DocumentID) != "" {
		documentID = task.DocumentID
	}
	// Source identity comes from the provenance validated at the internal
	// trust boundary; SourceURL keeps the chunk's value when the worker
	// already supplied one.
	var sourceID, sourcePath, sourceCommit string
	sourceURL := chunk.SourceURL
	if task.Provenance != nil {
		sourceID = task.Provenance.SourceID
		sourcePath = task.Provenance.SourcePath
		sourceCommit = task.Provenance.SourceCommit
		if strings.TrimSpace(sourceURL) == "" {
			sourceURL = task.Provenance.SourceURL
		}
	}
	return &model.DocumentVector{
		FileMD5:          task.FileMD5,
		ChunkID:          index,
		TextContent:      sourceText,
		EmbeddingText:    embeddingText,
		ModelVersion:     modelVersion,
		DocumentID:       documentID,
		SourceSHA256:     chunk.SourceSHA256,
		SourceURL:        sourceURL,
		SourcePath:       sourcePath,
		SourceCommit:     sourceCommit,
		SourceID:         sourceID,
		PageID:           chunk.PageID,
		ParentChunkID:    chunk.ParentChunkID,
		SheetName:        chunk.SheetName,
		CellRange:        chunk.CellRange,
		SectionPath:      chunk.SectionPath,
		PageSpan:         chunk.PageSpan,
		ElementIDs:       chunk.ElementIDs,
		ElementTypes:     chunk.ElementTypes,
		BBoxRefs:         chunk.BBoxRefs,
		AssetRefs:        chunk.AssetRefs,
		TokenCount:       chunk.TokenCount,
		TokenizerID:      chunk.TokenizerID,
		ParserName:       chunk.ParserName,
		ParserVersion:    chunk.ParserVersion,
		CorpusGeneration: chunk.CorpusGeneration,
		UserID:           task.UserID,
		OrgTag:           task.OrgTag,
		IsPublic:         task.IsPublic,
	}
}

func embeddingInput(item *model.DocumentVector) string {
	if strings.TrimSpace(item.EmbeddingText) != "" {
		return item.EmbeddingText
	}
	return item.TextContent
}

func esDocumentFromVector(item model.DocumentVector, vector []float32, modelVersion string) model.EsDocument {
	return model.EsDocument{
		VectorID:         item.FileMD5 + "_" + strconv.Itoa(item.ChunkID),
		FileMD5:          item.FileMD5,
		ChunkID:          item.ChunkID,
		TextContent:      item.TextContent,
		EmbeddingText:    item.EmbeddingText,
		Vector:           vector,
		ModelVersion:     modelVersion,
		DocumentID:       item.DocumentID,
		SourceSHA256:     item.SourceSHA256,
		SourceURL:        item.SourceURL,
		SourcePath:       item.SourcePath,
		SourceCommit:     item.SourceCommit,
		SourceID:         item.SourceID,
		ParentChunkID:    item.ParentChunkID,
		SectionPath:      item.SectionPath,
		PageID:           item.PageID,
		PageSpan:         item.PageSpan,
		SheetName:        item.SheetName,
		CellRange:        item.CellRange,
		ElementIDs:       item.ElementIDs,
		ElementTypes:     item.ElementTypes,
		BBoxRefs:         item.BBoxRefs,
		AssetRefs:        item.AssetRefs,
		TokenCount:       item.TokenCount,
		TokenizerID:      item.TokenizerID,
		ParserName:       item.ParserName,
		ParserVersion:    item.ParserVersion,
		CorpusGeneration: item.CorpusGeneration,
		TargetIndex:      item.TargetIndex,
		UserID:           item.UserID,
		OrgTag:           item.OrgTag,
		IsPublic:         item.IsPublic,
	}
}

// hashSHA256 returns the hex sha256 of data.
func hashSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// sourceSHA256ForChunk resolves the source hash for a structured chunk. A
// corpus task carrying Provenance.SourceSHA256 (the raw file hash validated at
// the internal trust boundary) always wins — the worker/client artifact hash
// is only a fallback when no provenance is present (legacy uploads). This
// keeps document_vectors.source_sha256 aligned with the raw content hash
// stored on knowledge_document.content_sha256 rather than the parsed JSON.
func sourceSHA256ForChunk(task tasks.FileProcessingTask, current string, artifactBytes []byte) string {
	if task.Provenance != nil && strings.TrimSpace(task.Provenance.SourceSHA256) != "" {
		return task.Provenance.SourceSHA256
	}
	if strings.TrimSpace(current) == "" {
		return hashSHA256(artifactBytes)
	}
	return current
}

// embeddingCacheKey handles embedding cache key.
func (p *Processor) embeddingCacheKey(fileMD5 string) string {
	return "pipeline:embeddings:" + fileMD5
}

// parsedObjectName handles parsed object name.
func (p *Processor) parsedObjectName(fileMD5 string) string {
	return "parsed/" + fileMD5 + ".txt"
}

func (p *Processor) parsedArtifactObjectName(fileMD5 string) string {
	return "parsed/" + fileMD5 + ".json"
}

// embedWindowChunks handles embed window chunks.
func (p *Processor) embedWindowChunks() int {
	batchSize := p.kafkaCfg.EmbeddingBatchSize
	if batchSize <= 0 {
		batchSize = 8
	}
	window := batchSize * embedWindowBatchMultiplier
	if window < minimumEmbedWindowChunks {
		window = minimumEmbedWindowChunks
	}
	return window
}

// splitText splits text.
func (p *Processor) splitText(text string, chunkSize int, chunkOverlap int) []string {
	if chunkSize <= chunkOverlap {
		return p.simpleSplit(text, chunkSize)
	}

	var chunks []string
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}

	step := chunkSize - chunkOverlap
	for i := 0; i < len(runes); i += step {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
		if end == len(runes) {
			break
		}
	}
	return chunks
}

// simpleSplit handles simple split.
func (p *Processor) simpleSplit(text string, chunkSize int) []string {
	var chunks []string
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}
