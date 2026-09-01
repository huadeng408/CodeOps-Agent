package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"code-agent/pkg/log"
	"code-agent/pkg/objectpath"
	"code-agent/pkg/tasks"
)

// processParse processes parse.
func (p *Processor) processParse(ctx context.Context, task tasks.FileProcessingTask) error {
	log.Infof("[Processor][parse] start file=%s name=%s", task.FileMD5, task.FileName)

	if p.ingestionClient != nil && p.ingestionClient.Enabled() {
		return p.processParseExternal(ctx, task)
	}

	objectName := objectpath.MergedObjectName(task.FileMD5, task.FileName)
	object, err := p.objectStorePort().Read(ctx, p.minioCfg.BucketName, objectName)
	if err != nil {
		return fmt.Errorf("parse: download object failed: %w", err)
	}
	defer object.Close()

	buf := new(bytes.Buffer)
	size, err := buf.ReadFrom(object)
	if err != nil {
		return fmt.Errorf("parse: read object stream failed: %w", err)
	}
	if size == 0 {
		return errors.New("parse: empty file content")
	}

	textContent, err := p.documentParser.ExtractText(ctx, bytes.NewReader(buf.Bytes()), task.FileName)
	if err != nil {
		return fmt.Errorf("parse: document extraction failed: %w", err)
	}
	if textContent == "" {
		return errors.New("parse: extracted text is empty")
	}

	parsedObject := p.parsedObjectName(task.FileMD5)
	reader := bytes.NewReader([]byte(textContent))
	if err := p.objectStorePort().Write(
		ctx,
		p.minioCfg.BucketName,
		parsedObject,
		reader,
		reader.Size(),
		"text/plain; charset=utf-8",
	); err != nil {
		return fmt.Errorf("parse: persist parsed text failed: %w", err)
	}

	next := task
	next.Stage = tasks.StageChunk
	next.ParsedObject = parsedObject
	if err := p.taskQueuePort().Publish(next); err != nil {
		return fmt.Errorf("parse: enqueue chunk task failed: %w", err)
	}
	log.Infof("[Processor][parse] done file=%s text_len=%d", task.FileMD5, utf8.RuneCountInString(textContent))
	return nil
}

// processParseExternal delegates parse-stage execution to the external ingestion worker.
func (p *Processor) processParseExternal(ctx context.Context, task tasks.FileProcessingTask) error {
	objectURL := task.ObjectURL
	if strings.TrimSpace(objectURL) == "" {
		objectName := objectpath.MergedObjectName(task.FileMD5, task.FileName)
		url, err := p.objectStorePort().Presign(p.minioCfg.BucketName, objectName, time.Hour)
		if err != nil {
			return fmt.Errorf("parse: generate presigned url failed: %w", err)
		}
		objectURL = url
	}

	artifact, err := p.ingestionClient.Parse(ctx, task, objectURL)
	if err != nil {
		return fmt.Errorf("parse: external worker failed: %w", err)
	}
	if strings.TrimSpace(artifact.ParsedText) == "" {
		// A corpus document (carries a DocumentID) whose parsed text comes back
		// empty — e.g. a k8s _index.md that is pure Hugo front matter with no
		// body — is a data-quality reality, not a format bug. Mark it SKIPPED
		// (not indexed, not a failure) and end the parse stage cleanly so the
		// task is not retried as FAILED and the importer poll resolves. No chunk
		// task is produced. Legacy uploads (no DocumentID) keep the original
		// error so a normal empty-file upload still surfaces a parse failure.
		if strings.TrimSpace(task.DocumentID) != "" {
			p.markDocumentSkipped(ctx, task, "parse: empty content after parse")
			log.Infof("[Processor][parse] skip empty corpus document file=%s doc=%s", task.FileMD5, task.DocumentID)
			return nil
		}
		return errors.New("parse: extracted text is empty")
	}
	if structuredArtifact(task, artifact) {
		if strings.TrimSpace(artifact.DocumentID) == "" || strings.TrimSpace(artifact.ParserName) == "" || strings.TrimSpace(artifact.ParserVersion) == "" || len(artifact.Elements) == 0 {
			return errors.New("parse: structured MinerU PDF provenance is incomplete")
		}
	}

	artifactBytes, err := json.Marshal(artifact)
	if err != nil {
		return fmt.Errorf("parse: encode structured artifact failed: %w", err)
	}
	parsedObject := p.parsedArtifactObjectName(task.FileMD5)
	reader := bytes.NewReader(artifactBytes)
	if err := p.objectStorePort().Write(
		ctx,
		p.minioCfg.BucketName,
		parsedObject,
		reader,
		reader.Size(),
		"application/json",
	); err != nil {
		return fmt.Errorf("parse: persist parsed text failed: %w", err)
	}

	next := task
	next.Stage = tasks.StageChunk
	next.ParsedObject = parsedObject
	if err := p.taskQueuePort().Publish(next); err != nil {
		return fmt.Errorf("parse: enqueue chunk task failed: %w", err)
	}
	log.Infof("[Processor][parse] done file=%s text_len=%d worker=external", task.FileMD5, utf8.RuneCountInString(artifact.ParsedText))
	return nil
}
