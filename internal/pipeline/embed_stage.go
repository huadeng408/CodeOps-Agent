package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"code-agent/pkg/log"
	"code-agent/pkg/tasks"
)

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
