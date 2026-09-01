package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"code-agent/internal/model"
	"code-agent/pkg/embedding"
	"code-agent/pkg/log"
	"code-agent/pkg/tasks"
)

// indexStage owns the pipeline's index, lifecycle and successful-cleanup
// policy. Its single process interface keeps Processor as a stage router while
// allowing its infrastructure ports to be replaced in focused tests.
type indexStage struct {
	processor *Processor
}

func (p *Processor) indexStage() indexStage {
	return indexStage{processor: p}
}

func (stage indexStage) process(ctx context.Context, task tasks.FileProcessingTask) error {
	log.Infof("[Processor][index] start file=%s", task.FileMD5)
	if stage.processor.ingestionClient != nil && stage.processor.ingestionClient.Enabled() {
		return stage.processExternal(ctx, task)
	}
	return stage.processLocal(ctx, task)
}

func (stage indexStage) processLocal(ctx context.Context, task tasks.FileProcessingTask) error {
	cacheKey, indexName, docs, err := stage.documents(ctx, task)
	if err != nil {
		return err
	}

	bulkSize := stage.processor.kafkaCfg.ESBulkBatchSize
	if bulkSize <= 0 {
		bulkSize = 100
	}
	writer := stage.processor.indexWriterPort()
	for i := 0; i < len(docs); i += bulkSize {
		end := i + bulkSize
		if end > len(docs) {
			end = len(docs)
		}
		if err := writer.Write(ctx, indexName, docs[i:end]); err != nil {
			return fmt.Errorf("index: bulk index failed batch_start=%d: %w", i, err)
		}
	}

	stage.markDocumentActive(ctx, task)
	stage.clearLocalArtifacts(ctx, cacheKey, task.FileMD5)
	log.Infof("[Processor][index] done file=%s docs=%d", task.FileMD5, len(docs))
	return nil
}

func (stage indexStage) processExternal(ctx context.Context, task tasks.FileProcessingTask) error {
	log.Infof("[Processor][index] start file=%s worker=external", task.FileMD5)
	cacheKey, indexName, docs, err := stage.documents(ctx, task)
	if err != nil {
		return err
	}
	indexed, err := stage.processor.ingestionClient.Index(ctx, task, indexName, docs)
	if err != nil {
		return fmt.Errorf("index: external worker failed: %w", err)
	}
	if indexed != len(docs) {
		return fmt.Errorf("index: external worker indexed %d/%d documents", indexed, len(docs))
	}

	stage.markDocumentActive(ctx, task)
	stage.clearExternalArtifacts(ctx, cacheKey, task.FileMD5)
	log.Infof("[Processor][index] done file=%s docs=%d worker=external", task.FileMD5, len(docs))
	return nil
}

func (stage indexStage) documents(ctx context.Context, task tasks.FileProcessingTask) (string, string, []model.EsDocument, error) {
	savedVectors, err := stage.processor.docVectorRepo.FindByFileMD5(task.FileMD5)
	if err != nil {
		return "", "", nil, fmt.Errorf("index: load chunks failed: %w", err)
	}
	if len(savedVectors) == 0 {
		return "", "", nil, errors.New("index: chunks are empty")
	}

	cacheKey := stage.processor.embeddingCacheKey(task.FileMD5)
	loadCache := stage.processor.vectorCache
	if loadCache == nil {
		loadCache = stage.processor.loadCachedEmbeddingMap
	}
	vectorMap, err := loadCache(ctx, cacheKey)
	if err != nil {
		return "", "", nil, fmt.Errorf("index: read cached vectors failed: %w", err)
	}
	if len(vectorMap) == 0 {
		return "", "", nil, errors.New("index: cached vectors are empty")
	}

	docs := make([]model.EsDocument, 0, len(savedVectors))
	for _, item := range savedVectors {
		vector, ok := vectorMap[item.ChunkID]
		if !ok || len(vector) == 0 {
			return "", "", nil, fmt.Errorf("index: missing vector for chunk=%d", item.ChunkID)
		}
		if err := stage.validateStructuredVector(*item, vector); err != nil {
			return "", "", nil, fmt.Errorf("index: structured vector validation failed for chunk=%d: %w", item.ChunkID, err)
		}
		modelVersion := stage.processor.embeddingCfg.Model
		if stage.processor.embeddingCfg.ModelRevision != "" {
			modelVersion = stage.processor.embeddingCfg.ModelRevision
		}
		docs = append(docs, esDocumentFromVector(*item, vector, modelVersion))
	}

	indexName := stage.indexNameFor(*savedVectors[0])
	if strings.TrimSpace(indexName) == "" {
		return "", "", nil, errors.New("index: corpus text index is not configured for structured chunks")
	}
	return cacheKey, indexName, docs, nil
}

func (stage indexStage) clearLocalArtifacts(ctx context.Context, cacheKey, fileMD5 string) {
	if err := stage.processor.embeddingCachePort().Clear(ctx, cacheKey); err != nil {
		log.Warnf("[Processor][index] clear cached vectors failed file=%s err=%v", fileMD5, err)
	}
	if err := stage.processor.objectStorePort().Delete(ctx, stage.processor.minioCfg.BucketName, stage.processor.parsedObjectName(fileMD5)); err != nil {
		log.Warnf("[Processor][index] delete parsed object failed file=%s err=%v", fileMD5, err)
	}
}

func (stage indexStage) clearExternalArtifacts(ctx context.Context, cacheKey, fileMD5 string) {
	if err := stage.processor.embeddingCachePort().Clear(ctx, cacheKey); err != nil {
		log.Warnf("[Processor][index] clear cached vectors failed file=%s err=%v", fileMD5, err)
	}
	if err := stage.processor.objectStorePort().Delete(ctx, stage.processor.minioCfg.BucketName, stage.processor.parsedArtifactObjectName(fileMD5)); err != nil {
		log.Warnf("[Processor][index] delete parsed artifact failed file=%s err=%v", fileMD5, err)
	}
}

// markDocumentActive flips a corpus document to ACTIVE after the index stage
// confirms the ES write. Nil-tolerant and a no-op for tasks without a
// DocumentID so legacy uploads never trigger a lifecycle update.
func (stage indexStage) markDocumentActive(ctx context.Context, task tasks.FileProcessingTask) {
	if strings.TrimSpace(task.DocumentID) == "" {
		return
	}
	if err := stage.processor.lifecyclePort().Active(ctx, task); err != nil {
		log.Warnf("[Processor] mark document active failed doc=%s err=%v", task.DocumentID, err)
	}
}

// markDocumentSkipped records a graceful skip (not indexed, not a failure)
// against a corpus document. It is used when parse extracts no text and must
// not turn a valid empty corpus document into a retried pipeline failure.
func (stage indexStage) markDocumentSkipped(ctx context.Context, task tasks.FileProcessingTask, reason string) {
	if strings.TrimSpace(task.DocumentID) == "" {
		return
	}
	if err := stage.processor.lifecyclePort().Skipped(ctx, task, reason); err != nil {
		log.Warnf("[Processor] mark document skipped failed doc=%s err=%v", task.DocumentID, err)
	}
}

// markDocumentFailed records a sanitized failure summary against a corpus
// document. The raw error remains available to the caller for retry/logging.
func (stage indexStage) markDocumentFailed(ctx context.Context, task tasks.FileProcessingTask, stageName string, cause error) {
	if strings.TrimSpace(task.DocumentID) == "" {
		return
	}
	if err := stage.processor.lifecyclePort().Failed(ctx, task, stageName, cause); err != nil {
		log.Warnf("[Processor] mark document failed failed doc=%s err=%v", task.DocumentID, err)
	}
}

// maxErrorSummaryLen bounds the last_error excerpt persisted to the document
// row; the remainder of the wrapped error chain stays in logs only.
const maxErrorSummaryLen = 160

// sanitizeErrorSummary reduces an error to a single short, stage-prefixed line
// safe to persist as knowledge_document.last_error. It strips newlines and
// bounds the output so tokens and document payloads do not enter the row.
func sanitizeErrorSummary(stageName string, cause error) string {
	stageName = strings.TrimSpace(stageName)
	if cause == nil {
		if stageName == "" {
			return "failed"
		}
		return stageName + " failed"
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
		if stageName == "" {
			return "failed"
		}
		return stageName + " failed"
	}
	if stageName == "" {
		return msg
	}
	return stageName + ": " + msg
}

// indexNameFor resolves the ES index for a document vector. Structured chunks
// must land in the corpus text index; legacy vectors retain the legacy index.
func (stage indexStage) indexNameFor(item model.DocumentVector) string {
	if item.CorpusGeneration != "" {
		return stage.processor.corpusCfg.TextIndex
	}
	return stage.processor.esCfg.IndexName
}

// validateStructuredVector enforces the native embedding contract for every
// structured chunk on both local and external index paths.
func (stage indexStage) validateStructuredVector(item model.DocumentVector, vector []float32) error {
	if item.CorpusGeneration == "" {
		return nil
	}
	expected := stage.processor.embeddingCfg.ExpectedDimensions
	if expected <= 0 {
		expected = stage.processor.embeddingCfg.Dimensions
	}
	return embedding.ValidateEmbeddingContract(stage.processor.embeddingCfg.ModelRevision, expected, vector)
}

// processIndexExternal remains a compatibility entry point for existing
// callers and tests; the policy implementation belongs to indexStage.
func (p *Processor) processIndexExternal(ctx context.Context, task tasks.FileProcessingTask) error {
	return p.indexStage().processExternal(ctx, task)
}
