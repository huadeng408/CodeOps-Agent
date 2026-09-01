// Package pipeline contains the async document pipeline.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"code-agent/internal/model"
	"code-agent/internal/repository"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/documentparser"
	"code-agent/pkg/embedding"
	"code-agent/pkg/log"
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

// processEmbed processes embed.
func (p *Processor) processEmbed(ctx context.Context, task tasks.FileProcessingTask) error {
	if p.ingestionClient != nil && p.ingestionClient.Enabled() {
		return p.processEmbedExternal(ctx, task)
	}

	cacheKey := p.embeddingCacheKey(task.FileMD5)
	if err := p.ensureEmbeddingHashCache(ctx, cacheKey); err != nil {
		return fmt.Errorf("embed: prepare cache failed: %w", err)
	}

	totalChunks := task.TotalChunks
	if totalChunks <= 0 {
		count, err := p.docVectorRepo.CountByFileMD5(task.FileMD5)
		if err != nil {
			return fmt.Errorf("embed: count chunks failed: %w", err)
		}
		totalChunks = int(count)
	}
	if totalChunks == 0 {
		return errors.New("embed: chunks are empty")
	}

	windowSize := p.embedWindowChunks()
	chunkStart := task.ChunkStart
	if chunkStart < 0 {
		chunkStart = 0
	}
	if chunkStart >= totalChunks {
		return p.enqueueIndexTask(task, totalChunks)
	}

	limit := windowSize
	remaining := totalChunks - chunkStart
	if remaining < limit {
		limit = remaining
	}
	savedVectors, err := p.docVectorRepo.FindByFileMD5Range(task.FileMD5, chunkStart, limit)
	if err != nil {
		return fmt.Errorf("embed: load chunk range failed start=%d limit=%d: %w", chunkStart, limit, err)
	}
	if len(savedVectors) == 0 {
		return fmt.Errorf("embed: no chunks found in range start=%d limit=%d", chunkStart, limit)
	}

	batchSize := p.kafkaCfg.EmbeddingBatchSize
	if batchSize <= 0 {
		batchSize = 8
	}

	log.Infof(
		"[Processor][embed] start file=%s task_chunk=%d start=%d window=%d total=%d",
		task.FileMD5,
		task.TaskChunkID,
		chunkStart,
		len(savedVectors),
		totalChunks,
	)

	for i := 0; i < len(savedVectors); i += batchSize {
		end := i + batchSize
		if end > len(savedVectors) {
			end = len(savedVectors)
		}

		texts := make([]string, 0, end-i)
		for _, item := range savedVectors[i:end] {
			texts = append(texts, embeddingInput(item))
		}
		vectors, err := p.embeddingClient.CreateEmbeddings(ctx, texts)
		if err != nil {
			return fmt.Errorf("embed: embedding batch failed batch_start=%d: %w", i, err)
		}
		if len(vectors) != len(texts) {
			return fmt.Errorf("embed: vector count mismatch expected=%d actual=%d", len(texts), len(vectors))
		}

		cacheVectors := make(map[int][]float32, len(vectors))
		for j := range vectors {
			cacheVectors[savedVectors[i+j].ChunkID] = vectors[j]
		}
		if len(cacheVectors) > 0 {
			if err := p.embeddingCachePort().Put(ctx, cacheKey, cacheVectors); err != nil {
				return fmt.Errorf("embed: write vector cache failed: %w", err)
			}
		}
	}
	if err := p.embeddingCachePort().SetTTL(ctx, cacheKey, embeddingCacheTTLSeconds*time.Second); err != nil {
		return fmt.Errorf("embed: refresh cache ttl failed: %w", err)
	}

	nextStart := chunkStart + len(savedVectors)
	if nextStart < totalChunks {
		taskChunkID := task.TaskChunkID
		if taskChunkID <= 0 {
			taskChunkID = chunkStart/windowSize + 1
		}
		next := task
		next.Stage = tasks.StageEmbed
		next.TaskChunkID = taskChunkID + 1
		next.ChunkStart = nextStart
		next.TotalChunks = totalChunks
		if err := p.taskQueuePort().Publish(next); err != nil {
			return fmt.Errorf("embed: enqueue next embed task failed: %w", err)
		}
		log.Infof(
			"[Processor][embed] partial file=%s done=%d/%d next_start=%d next_task_chunk=%d",
			task.FileMD5,
			nextStart,
			totalChunks,
			next.ChunkStart,
			next.TaskChunkID,
		)
		return nil
	}

	if err := p.enqueueIndexTask(task, totalChunks); err != nil {
		return err
	}
	log.Infof("[Processor][embed] done file=%s total=%d", task.FileMD5, totalChunks)
	return nil
}

// processIndex processes index.
func (p *Processor) processIndex(ctx context.Context, task tasks.FileProcessingTask) error {
	log.Infof("[Processor][index] start file=%s", task.FileMD5)

	if p.ingestionClient != nil && p.ingestionClient.Enabled() {
		return p.processIndexExternal(ctx, task)
	}

	savedVectors, err := p.docVectorRepo.FindByFileMD5(task.FileMD5)
	if err != nil {
		return fmt.Errorf("index: load chunks failed: %w", err)
	}
	if len(savedVectors) == 0 {
		return errors.New("index: chunks are empty")
	}

	cacheKey := p.embeddingCacheKey(task.FileMD5)
	loadCache := p.vectorCache
	if loadCache == nil {
		loadCache = p.loadCachedEmbeddingMap
	}
	vectorMap, err := loadCache(ctx, cacheKey)
	if err != nil {
		return fmt.Errorf("index: read cached vectors failed: %w", err)
	}
	if len(vectorMap) == 0 {
		return errors.New("index: cached vectors are empty")
	}

	docs := make([]model.EsDocument, 0, len(savedVectors))
	for _, item := range savedVectors {
		vector, ok := vectorMap[item.ChunkID]
		if !ok || len(vector) == 0 {
			return fmt.Errorf("index: missing vector for chunk=%d", item.ChunkID)
		}
		if err := p.validateStructuredVector(*item, vector); err != nil {
			return fmt.Errorf("index: structured vector validation failed for chunk=%d: %w", item.ChunkID, err)
		}
		modelVersion := p.embeddingCfg.Model
		if p.embeddingCfg.ModelRevision != "" {
			modelVersion = p.embeddingCfg.ModelRevision
		}
		docs = append(docs, esDocumentFromVector(*item, vector, modelVersion))
	}

	indexName := p.indexNameFor(*savedVectors[0])
	if strings.TrimSpace(indexName) == "" {
		return errors.New("index: corpus text index is not configured for structured chunks")
	}
	writer := p.indexWriterPort()
	bulkSize := p.kafkaCfg.ESBulkBatchSize
	if bulkSize <= 0 {
		bulkSize = 100
	}
	for i := 0; i < len(docs); i += bulkSize {
		end := i + bulkSize
		if end > len(docs) {
			end = len(docs)
		}
		if err := writer.Write(ctx, indexName, docs[i:end]); err != nil {
			return fmt.Errorf("index: bulk index failed batch_start=%d: %w", i, err)
		}
	}

	// ES v2 write confirmed -> advance the corpus document to ACTIVE before
	// the cache/parsed-object cleanup runs.
	p.markDocumentActive(ctx, task)

	_ = p.embeddingCachePort().Clear(ctx, cacheKey)
	_ = p.objectStorePort().Delete(ctx, p.minioCfg.BucketName, p.parsedObjectName(task.FileMD5))
	log.Infof("[Processor][index] done file=%s docs=%d", task.FileMD5, len(docs))
	return nil
}

func structuredArtifact(task tasks.FileProcessingTask, artifact orchestratorclient.ParsedArtifact) bool {
	return strings.EqualFold(filepath.Ext(task.FileName), ".pdf") || len(artifact.Elements) > 0 || strings.EqualFold(artifact.ParserName, "mineru")
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

// markDocumentActive flips a corpus document to ACTIVE after the index stage
// confirms the ES write. Nil-tolerant and a no-op for tasks without a
// DocumentID so legacy uploads never trigger a lifecycle update.
func (p *Processor) markDocumentActive(ctx context.Context, task tasks.FileProcessingTask) {
	if strings.TrimSpace(task.DocumentID) == "" {
		return
	}
	if err := p.lifecyclePort().Active(ctx, task); err != nil {
		log.Warnf("[Processor] mark document active failed doc=%s err=%v", task.DocumentID, err)
	}
}

// markDocumentSkipped records a graceful skip (not indexed, not a failure)
// against a corpus document — used when parse extracts no text (empty content
// after parse, e.g. a front-matter-only _index.md). The task ends SUCCESS so it
// is not retried; no chunk task is produced. Nil-tolerant and a no-op for tasks
// without a DocumentID so legacy uploads never trigger a lifecycle update.
func (p *Processor) markDocumentSkipped(ctx context.Context, task tasks.FileProcessingTask, reason string) {
	if strings.TrimSpace(task.DocumentID) == "" {
		return
	}
	if err := p.lifecyclePort().Skipped(ctx, task, reason); err != nil {
		log.Warnf("[Processor] mark document skipped failed doc=%s err=%v", task.DocumentID, err)
	}
}

// markDocumentFailed records a sanitized failure summary against a corpus
// document. The stored last_error never carries tokens, headers or full file
// content (see sanitizeErrorSummary); the raw error is still returned to the
// caller for logging/retry.
func (p *Processor) markDocumentFailed(ctx context.Context, task tasks.FileProcessingTask, stage string, cause error) {
	if strings.TrimSpace(task.DocumentID) == "" {
		return
	}
	if err := p.lifecyclePort().Failed(ctx, task, stage, cause); err != nil {
		log.Warnf("[Processor] mark document failed failed doc=%s err=%v", task.DocumentID, err)
	}
}

// maxErrorSummaryLen bounds the last_error excerpt persisted to the document
// row; the remainder of the wrapped error chain stays in logs only.
const maxErrorSummaryLen = 160

// sanitizeErrorSummary reduces an error to a single short, stage-prefixed line
// safe to persist as knowledge_document.last_error. It strips newlines (so
// later lines of a multi-line message, where secrets or payloads typically
// land, are never echoed) and truncates to a small bound.
func sanitizeErrorSummary(stage string, cause error) string {
	stage = strings.TrimSpace(stage)
	if cause == nil {
		if stage == "" {
			return "failed"
		}
		return stage + " failed"
	}
	msg := strings.TrimSpace(cause.Error())
	if i := strings.IndexAny(msg, "\r\n"); i >= 0 {
		msg = msg[:i]
	}
	msg = strings.TrimSpace(msg)
	if len(msg) > maxErrorSummaryLen {
		msg = msg[:maxErrorSummaryLen]
	}
	if msg == "" {
		if stage == "" {
			return "failed"
		}
		return stage + " failed"
	}
	if stage == "" {
		return msg
	}
	return stage + ": " + msg
}

// indexNameFor resolves the ES index for a document vector. Structured
// chunks with a corpus generation must land in the corpus text index — never
// a hardcoded name; legacy vectors keep the legacy index. An empty result is
// a configuration error and must fail the write.
func (p *Processor) indexNameFor(item model.DocumentVector) string {
	if item.CorpusGeneration != "" {
		return p.corpusCfg.TextIndex
	}
	return p.esCfg.IndexName
}

// validateStructuredVector enforces the native embedding contract for any
// structured chunk on BOTH the local and external processing paths.
func (p *Processor) validateStructuredVector(item model.DocumentVector, vector []float32) error {
	if item.CorpusGeneration == "" {
		return nil // legacy path is not subject to the native-dimension contract
	}
	expected := p.embeddingCfg.ExpectedDimensions
	if expected <= 0 {
		expected = p.embeddingCfg.Dimensions
	}
	return embedding.ValidateEmbeddingContract(p.embeddingCfg.ModelRevision, expected, vector)
}

// processEmbedExternal delegates embedding-stage execution to the external ingestion worker.
func (p *Processor) processEmbedExternal(ctx context.Context, task tasks.FileProcessingTask) error {
	cacheKey := p.embeddingCacheKey(task.FileMD5)
	if err := p.ensureEmbeddingHashCache(ctx, cacheKey); err != nil {
		return fmt.Errorf("embed: prepare cache failed: %w", err)
	}

	totalChunks := task.TotalChunks
	if totalChunks <= 0 {
		count, err := p.docVectorRepo.CountByFileMD5(task.FileMD5)
		if err != nil {
			return fmt.Errorf("embed: count chunks failed: %w", err)
		}
		totalChunks = int(count)
	}
	if totalChunks == 0 {
		return errors.New("embed: chunks are empty")
	}

	windowSize := p.embedWindowChunks()
	chunkStart := task.ChunkStart
	if chunkStart < 0 {
		chunkStart = 0
	}
	if chunkStart >= totalChunks {
		return p.enqueueIndexTask(task, totalChunks)
	}

	limit := windowSize
	remaining := totalChunks - chunkStart
	if remaining < limit {
		limit = remaining
	}
	savedVectors, err := p.docVectorRepo.FindByFileMD5Range(task.FileMD5, chunkStart, limit)
	if err != nil {
		return fmt.Errorf("embed: load chunk range failed start=%d limit=%d: %w", chunkStart, limit, err)
	}
	if len(savedVectors) == 0 {
		return fmt.Errorf("embed: no chunks found in range start=%d limit=%d", chunkStart, limit)
	}

	batchSize := p.kafkaCfg.EmbeddingBatchSize
	if batchSize <= 0 {
		batchSize = 8
	}

	log.Infof(
		"[Processor][embed] start file=%s task_chunk=%d start=%d window=%d total=%d worker=external",
		task.FileMD5,
		task.TaskChunkID,
		chunkStart,
		len(savedVectors),
		totalChunks,
	)

	for i := 0; i < len(savedVectors); i += batchSize {
		end := i + batchSize
		if end > len(savedVectors) {
			end = len(savedVectors)
		}

		texts := make([]string, 0, end-i)
		for _, item := range savedVectors[i:end] {
			texts = append(texts, embeddingInput(item))
		}
		vectors, err := p.ingestionClient.Embed(ctx, task, texts)
		if err != nil {
			return fmt.Errorf("embed: external worker failed batch_start=%d: %w", i, err)
		}
		if len(vectors) != len(texts) {
			return fmt.Errorf("embed: vector count mismatch expected=%d actual=%d", len(texts), len(vectors))
		}

		cacheVectors := make(map[int][]float32, len(vectors))
		for j := range vectors {
			cacheVectors[savedVectors[i+j].ChunkID] = vectors[j]
		}
		if len(cacheVectors) > 0 {
			if err := p.embeddingCachePort().Put(ctx, cacheKey, cacheVectors); err != nil {
				return fmt.Errorf("embed: write vector cache failed: %w", err)
			}
		}
	}
	if err := p.embeddingCachePort().SetTTL(ctx, cacheKey, embeddingCacheTTLSeconds*time.Second); err != nil {
		return fmt.Errorf("embed: refresh cache ttl failed: %w", err)
	}

	nextStart := chunkStart + len(savedVectors)
	if nextStart < totalChunks {
		taskChunkID := task.TaskChunkID
		if taskChunkID <= 0 {
			taskChunkID = chunkStart/windowSize + 1
		}
		next := task
		next.Stage = tasks.StageEmbed
		next.TaskChunkID = taskChunkID + 1
		next.ChunkStart = nextStart
		next.TotalChunks = totalChunks
		if err := p.taskQueuePort().Publish(next); err != nil {
			return fmt.Errorf("embed: enqueue next embed task failed: %w", err)
		}
		log.Infof(
			"[Processor][embed] partial file=%s done=%d/%d next_start=%d next_task_chunk=%d worker=external",
			task.FileMD5,
			nextStart,
			totalChunks,
			next.ChunkStart,
			next.TaskChunkID,
		)
		return nil
	}

	if err := p.enqueueIndexTask(task, totalChunks); err != nil {
		return err
	}
	log.Infof("[Processor][embed] done file=%s total=%d worker=external", task.FileMD5, totalChunks)
	return nil
}

// processIndexExternal delegates index-stage execution to the external ingestion worker.
func (p *Processor) processIndexExternal(ctx context.Context, task tasks.FileProcessingTask) error {
	log.Infof("[Processor][index] start file=%s worker=external", task.FileMD5)

	savedVectors, err := p.docVectorRepo.FindByFileMD5(task.FileMD5)
	if err != nil {
		return fmt.Errorf("index: load chunks failed: %w", err)
	}
	if len(savedVectors) == 0 {
		return errors.New("index: chunks are empty")
	}

	cacheKey := p.embeddingCacheKey(task.FileMD5)
	loadCache := p.vectorCache
	if loadCache == nil {
		loadCache = p.loadCachedEmbeddingMap
	}
	vectorMap, err := loadCache(ctx, cacheKey)
	if err != nil {
		return fmt.Errorf("index: read cached vectors failed: %w", err)
	}
	if len(vectorMap) == 0 {
		return errors.New("index: cached vectors are empty")
	}

	docs := make([]model.EsDocument, 0, len(savedVectors))
	for _, item := range savedVectors {
		vector, ok := vectorMap[item.ChunkID]
		if !ok || len(vector) == 0 {
			return fmt.Errorf("index: missing vector for chunk=%d", item.ChunkID)
		}
		if err := p.validateStructuredVector(*item, vector); err != nil {
			return fmt.Errorf("index: structured vector validation failed for chunk=%d: %w", item.ChunkID, err)
		}
		modelVersion := p.embeddingCfg.Model
		if p.embeddingCfg.ModelRevision != "" {
			modelVersion = p.embeddingCfg.ModelRevision
		}
		docs = append(docs, esDocumentFromVector(*item, vector, modelVersion))
	}

	indexName := p.indexNameFor(*savedVectors[0])
	if strings.TrimSpace(indexName) == "" {
		return errors.New("index: corpus text index is not configured for structured chunks")
	}
	if _, err := p.ingestionClient.Index(ctx, task, indexName, docs); err != nil {
		return fmt.Errorf("index: external worker failed: %w", err)
	}

	// ES v2 write confirmed -> advance the corpus document to ACTIVE before
	// the cache/parsed-object cleanup runs.
	p.markDocumentActive(ctx, task)

	_ = p.embeddingCachePort().Clear(ctx, cacheKey)
	_ = p.objectStorePort().Delete(ctx, p.minioCfg.BucketName, p.parsedArtifactObjectName(task.FileMD5))
	log.Infof("[Processor][index] done file=%s docs=%d worker=external", task.FileMD5, len(docs))
	return nil
}

// enqueueIndexTask handles enqueue index task.
func (p *Processor) enqueueIndexTask(task tasks.FileProcessingTask, totalChunks int) error {
	next := task
	next.Stage = tasks.StageIndex
	next.TaskChunkID = 0
	next.ChunkStart = 0
	next.TotalChunks = totalChunks
	if err := p.taskQueuePort().Publish(next); err != nil {
		return fmt.Errorf("embed: enqueue index task failed: %w", err)
	}
	return nil
}

// hashCachePreparer is an internal compatibility seam for Redis hash caches.
type hashCachePreparer interface {
	EnsureHash(context.Context, string) error
}

// ensureEmbeddingHashCache preserves the legacy string-to-hash migration rule
// without exposing Redis operations to the pipeline stages.
func (p *Processor) ensureEmbeddingHashCache(ctx context.Context, cacheKey string) error {
	cache := p.embeddingCachePort()
	preparer, ok := cache.(hashCachePreparer)
	if !ok {
		return nil
	}
	return preparer.EnsureHash(ctx, cacheKey)
}

// loadCachedEmbeddingMap loads cached embeddings through the cache port.
func (p *Processor) loadCachedEmbeddingMap(ctx context.Context, cacheKey string) (map[int][]float32, error) {
	return p.embeddingCachePort().Load(ctx, cacheKey)
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
