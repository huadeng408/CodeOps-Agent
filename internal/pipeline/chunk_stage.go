package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"code-agent/internal/model"
	"code-agent/pkg/log"
	orchestratorclient "code-agent/pkg/orchestrator"
	"code-agent/pkg/tasks"
)

// processChunk processes chunk.
func (p *Processor) processChunk(ctx context.Context, task tasks.FileProcessingTask) error {
	log.Infof("[Processor][chunk] start file=%s", task.FileMD5)

	if p.ingestionClient != nil && p.ingestionClient.Enabled() {
		return p.processChunkExternal(ctx, task)
	}

	parsedObject := task.ParsedObject
	if parsedObject == "" {
		parsedObject = p.parsedObjectName(task.FileMD5)
	}

	object, err := p.objectStorePort().Read(ctx, p.minioCfg.BucketName, parsedObject)
	if err != nil {
		return fmt.Errorf("chunk: read parsed object failed: %w", err)
	}
	defer object.Close()

	textBytes, err := io.ReadAll(object)
	if err != nil {
		return fmt.Errorf("chunk: read parsed stream failed: %w", err)
	}
	textContent := string(textBytes)
	if textContent == "" {
		return errors.New("chunk: parsed text is empty")
	}

	chunks := p.splitText(textContent, 1000, 100)
	if len(chunks) == 0 {
		return errors.New("chunk: no chunks generated")
	}

	if err := p.docVectorRepo.DeleteByFileMD5(task.FileMD5); err != nil {
		log.Warnf("[Processor][chunk] clear old chunks failed file=%s err=%v", task.FileMD5, err)
	}
	_ = p.embeddingCachePort().Clear(ctx, p.embeddingCacheKey(task.FileMD5))

	dbVectors := make([]*model.DocumentVector, 0, len(chunks))
	for i, chunk := range chunks {
		dbVectors = append(dbVectors, &model.DocumentVector{
			FileMD5:      task.FileMD5,
			ChunkID:      i,
			TextContent:  chunk,
			ModelVersion: p.embeddingCfg.Model,
			UserID:       task.UserID,
			OrgTag:       task.OrgTag,
			IsPublic:     task.IsPublic,
		})
	}
	if err := p.docVectorRepo.BatchCreate(dbVectors); err != nil {
		return fmt.Errorf("chunk: persist chunks failed: %w", err)
	}

	next := task
	next.Stage = tasks.StageEmbed
	next.ParsedObject = parsedObject
	next.TaskChunkID = 1
	next.ChunkStart = 0
	next.TotalChunks = len(chunks)
	if err := p.taskQueuePort().Publish(next); err != nil {
		return fmt.Errorf("chunk: enqueue embed task failed: %w", err)
	}
	log.Infof("[Processor][chunk] done file=%s chunks=%d", task.FileMD5, len(chunks))
	return nil
}

// processChunkExternal delegates chunk-stage execution to the external ingestion worker.
func (p *Processor) processChunkExternal(ctx context.Context, task tasks.FileProcessingTask) error {
	log.Infof("[Processor][chunk] external worker file=%s", task.FileMD5)

	parsedObject := task.ParsedObject
	if parsedObject == "" {
		parsedObject = p.parsedArtifactObjectName(task.FileMD5)
	}

	object, err := p.objectStorePort().Read(ctx, p.minioCfg.BucketName, parsedObject)
	if err != nil {
		return fmt.Errorf("chunk: read parsed object failed: %w", err)
	}
	defer object.Close()

	textBytes, err := io.ReadAll(object)
	if err != nil {
		return fmt.Errorf("chunk: read parsed stream failed: %w", err)
	}
	return p.processChunkExternalArtifact(ctx, task, parsedObject, textBytes)
}

// processChunkExternalArtifact runs the chunk-stage pipeline (decode → chunk →
// provenance fill → persist → enqueue) against the parsed artifact bytes
// already loaded from object storage. Splitting the MinIO read out keeps the
// fill/persist/lifecycle logic unit-testable without the storage or Kafka
// globals.
func (p *Processor) processChunkExternalArtifact(ctx context.Context, task tasks.FileProcessingTask, parsedObject string, textBytes []byte) error {
	var artifact orchestratorclient.ParsedArtifact
	if err := json.Unmarshal(textBytes, &artifact); err != nil {
		return fmt.Errorf("chunk: decode structured artifact failed: %w", err)
	}
	textContent := artifact.ParsedText
	if strings.TrimSpace(textContent) == "" {
		return errors.New("chunk: structured artifact text is empty")
	}
	if strings.EqualFold(filepath.Ext(task.FileName), ".pdf") && len(artifact.Elements) == 0 {
		return errors.New("chunk: structured PDF artifact has no elements")
	}

	chunkResult, err := p.ingestionClient.Chunk(ctx, task, artifact, 1000, 100)
	if err != nil {
		return fmt.Errorf("chunk: external worker failed: %w", err)
	}
	structuredPath := structuredArtifact(task, artifact)
	if structuredPath && len(chunkResult.StructuredChunks) == 0 {
		return errors.New("chunk: structured artifact returned no structured chunks")
	}
	// Go is the authority for source provenance: prefer the raw file hash
	// carried on the task (validated at the internal trust boundary) over the
	// worker/client artifact hash, falling back to the artifact hash only for
	// legacy uploads that carry no provenance.
	for index := range chunkResult.StructuredChunks {
		chunk := &chunkResult.StructuredChunks[index]
		chunk.SourceSHA256 = sourceSHA256ForChunk(task, chunk.SourceSHA256, textBytes)
		if err := chunk.Validate(); err != nil {
			return fmt.Errorf("chunk: structured chunk %d is invalid: %w", index, err)
		}
		if strings.EqualFold(filepath.Ext(task.FileName), ".pdf") || strings.EqualFold(artifact.ParserName, "mineru") {
			if err := validatePDFStructuredChunk(*chunk); err != nil {
				return fmt.Errorf("chunk: structured PDF chunk %d is invalid: %w", index, err)
			}
		}
	}
	if !structuredPath && len(chunkResult.StructuredChunks) == 0 && len(chunkResult.Chunks) == 0 {
		return errors.New("chunk: no chunks generated")
	}

	if err := p.docVectorRepo.DeleteByFileMD5(task.FileMD5); err != nil {
		log.Warnf("[Processor][chunk] clear old chunks failed file=%s err=%v", task.FileMD5, err)
	}
	_ = p.embeddingCachePort().Clear(ctx, p.embeddingCacheKey(task.FileMD5))

	var dbVectors []*model.DocumentVector
	if len(chunkResult.StructuredChunks) > 0 {
		dbVectors = make([]*model.DocumentVector, 0, len(chunkResult.StructuredChunks))
		for i, chunk := range chunkResult.StructuredChunks {
			if task.CorpusGeneration != "" {
				chunk.CorpusGeneration = task.CorpusGeneration
				chunk.TargetIndex = p.corpusCfg.TextIndex
			}
			dbVectors = append(dbVectors, documentVectorFromStructuredChunk(task, i, chunk, p.embeddingCfg.Model))
		}
	} else {
		dbVectors = make([]*model.DocumentVector, 0, len(chunkResult.Chunks))
		for i, chunk := range chunkResult.Chunks {
			dbVectors = append(dbVectors, &model.DocumentVector{
				FileMD5:      task.FileMD5,
				ChunkID:      i,
				TextContent:  chunk,
				ModelVersion: p.embeddingCfg.Model,
				UserID:       task.UserID,
				OrgTag:       task.OrgTag,
				IsPublic:     task.IsPublic,
			})
		}
	}
	if err := p.docVectorRepo.BatchCreate(dbVectors); err != nil {
		p.indexStage().markDocumentFailed(ctx, task, "chunk", err)
		return fmt.Errorf("chunk: persist chunks failed: %w", err)
	}

	next := task
	next.Stage = tasks.StageEmbed
	next.ParsedObject = parsedObject
	next.TaskChunkID = 1
	next.ChunkStart = 0
	next.TotalChunks = len(dbVectors)
	if err := p.taskQueuePort().Publish(next); err != nil {
		return fmt.Errorf("chunk: enqueue embed task failed: %w", err)
	}
	log.Infof("[Processor][chunk] done file=%s chunks=%d worker=external", task.FileMD5, len(dbVectors))
	return nil
}
