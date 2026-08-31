package pipeline

import (
	"context"
	"io"
	"testing"
	"time"

	"code-agent/internal/model"
	"code-agent/pkg/tasks"
)

type contractObjectStore struct{}

func (contractObjectStore) Read(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(nil), nil
}
func (contractObjectStore) Write(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}
func (contractObjectStore) Presign(string, string, time.Duration) (string, error) { return "", nil }
func (contractObjectStore) Delete(context.Context, string, string) error { return nil }

type contractTaskQueue struct{}

func (contractTaskQueue) Publish(tasks.FileProcessingTask) error { return nil }

type contractEmbeddingCache struct{}

func (contractEmbeddingCache) Clear(context.Context, string) error { return nil }
func (contractEmbeddingCache) Put(context.Context, string, map[int][]float32) error { return nil }
func (contractEmbeddingCache) SetTTL(context.Context, string, time.Duration) error { return nil }
func (contractEmbeddingCache) Load(context.Context, string) (map[int][]float32, error) {
	return nil, nil
}

type contractIndexWriter struct{}

func (contractIndexWriter) Write(context.Context, string, []model.EsDocument) error { return nil }

type contractLifecycle struct{}

func (contractLifecycle) Active(context.Context, tasks.FileProcessingTask) error { return nil }
func (contractLifecycle) Skipped(context.Context, tasks.FileProcessingTask, string) error { return nil }
func (contractLifecycle) Failed(context.Context, tasks.FileProcessingTask, string, error) error {
	return nil
}

func validPipelineDeps() PipelineDeps {
	return PipelineDeps{
		ObjectStore:      contractObjectStore{},
		TaskQueue:        contractTaskQueue{},
		EmbeddingCache:   contractEmbeddingCache{},
		IndexWriter:      contractIndexWriter{},
		DocumentLifecycle: contractLifecycle{},
	}
}

func TestPipelinePortsAndProcessorWithDeps(t *testing.T) {
	processor, err := NewProcessorWithDeps(validPipelineDeps())
	if err != nil {
		t.Fatalf("NewProcessorWithDeps() error = %v", err)
	}
	if processor == nil {
		t.Fatal("NewProcessorWithDeps() returned nil processor")
	}
}

func TestPipelineProcessorWithDepsRejectsMissingPort(t *testing.T) {
	_, err := NewProcessorWithDeps(PipelineDeps{})
	if err == nil || err.Error() != "pipeline: object store is required" {
		t.Fatalf("NewProcessorWithDeps() error = %v, want deterministic object-store error", err)
	}
}
