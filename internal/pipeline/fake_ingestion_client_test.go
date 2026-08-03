package pipeline

import (
	"context"

	"code-agent/internal/model"
	orchestratorclient "code-agent/pkg/orchestrator"
	"code-agent/pkg/tasks"
)

// fakeIngestionClient is a stub external ingestion worker for pipeline tests.
// It returns canned chunk/index results so the external chunk and index paths
// can be exercised without a live worker or Kafka/MinIO backend.
type fakeIngestionClient struct {
	indexResult int
	indexErr    error
	chunkResult orchestratorclient.ChunkResult
	chunkErr    error
	parseResult orchestratorclient.ParsedArtifact
	parseErr    error
}

func (f *fakeIngestionClient) Enabled() bool { return true }

func (f *fakeIngestionClient) Parse(context.Context, tasks.FileProcessingTask, string) (orchestratorclient.ParsedArtifact, error) {
	return f.parseResult, f.parseErr
}

func (f *fakeIngestionClient) Chunk(context.Context, tasks.FileProcessingTask, orchestratorclient.ParsedArtifact, int, int) (orchestratorclient.ChunkResult, error) {
	return f.chunkResult, f.chunkErr
}

func (f *fakeIngestionClient) Embed(context.Context, tasks.FileProcessingTask, []string) ([][]float32, error) {
	return nil, nil
}

func (f *fakeIngestionClient) Index(context.Context, tasks.FileProcessingTask, string, []model.EsDocument) (int, error) {
	return f.indexResult, f.indexErr
}
