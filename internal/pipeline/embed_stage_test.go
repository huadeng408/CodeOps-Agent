package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/tasks"
)

type embedCacheRecorder struct {
	clears int
	puts   []map[int][]float32
	ttls   []time.Duration
	loaded map[int][]float32
}

func (c *embedCacheRecorder) Clear(context.Context, string) error { c.clears++; return nil }
func (c *embedCacheRecorder) Put(_ context.Context, _ string, vectors map[int][]float32) error {
	c.puts = append(c.puts, vectors)
	return nil
}
func (c *embedCacheRecorder) SetTTL(_ context.Context, _ string, ttl time.Duration) error {
	c.ttls = append(c.ttls, ttl)
	return nil
}
func (c *embedCacheRecorder) Load(context.Context, string) (map[int][]float32, error) {
	return c.loaded, nil
}

type embedClientFake struct {
	calls       int
	vectorCount int
	err         error
	cancelAware bool
}

func (f *embedClientFake) CreateEmbedding(ctx context.Context, text string) ([]float32, error) {
	vectors, err := f.CreateEmbeddings(ctx, []string{text})
	if err != nil || len(vectors) == 0 {
		return nil, err
	}
	return vectors[0], nil
}
func (f *embedClientFake) CreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	f.calls++
	if f.cancelAware && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	count := f.vectorCount
	if count < 0 {
		count = len(texts)
	}
	vectors := make([][]float32, count)
	for i := range vectors {
		vectors[i] = []float32{float32(i + 1), 2}
	}
	return vectors, nil
}

type embedRangeRepo struct{ fakeVectorRepo }

func (r *embedRangeRepo) FindByFileMD5Range(fileMD5 string, offset, limit int) ([]*model.DocumentVector, error) {
	all, err := r.FindByFileMD5(fileMD5)
	if err != nil {
		return nil, err
	}
	if offset >= len(all) {
		return nil, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end], nil
}

func embedVector(id int) *model.DocumentVector {
	return &model.DocumentVector{FileMD5: "embed-file", ChunkID: id, TextContent: "chunk", ModelVersion: "model"}
}

func TestEmbedStageWritesCacheTTLAndPublishesIndex(t *testing.T) {
	repo := &embedRangeRepo{fakeVectorRepo: fakeVectorRepo{vectors: []*model.DocumentVector{embedVector(0)}}}
	cache := &embedCacheRecorder{}
	queue := &stageTaskQueue{}
	client := &embedClientFake{vectorCount: -1}
	processor := &Processor{docVectorRepo: repo, embeddingClient: client, embeddingCache: cache, taskQueue: queue, kafkaCfg: serverconfig.KafkaConfig{EmbeddingBatchSize: 8}}
	task := tasks.FileProcessingTask{FileMD5: "embed-file", Stage: tasks.StageEmbed, TotalChunks: 1, TaskChunkID: 1}

	if err := processor.processEmbed(context.Background(), task); err != nil {
		t.Fatalf("processEmbed() error = %v", err)
	}
	if client.calls != 1 || len(cache.puts) != 1 || len(cache.puts[0]) != 1 || len(cache.ttls) != 1 || cache.ttls[0] != 7200*time.Second {
		t.Fatalf("embedding/cache calls = calls:%d puts:%v ttls:%v", client.calls, cache.puts, cache.ttls)
	}
	if len(queue.tasks) != 1 || queue.tasks[0].Stage != tasks.StageIndex || queue.tasks[0].TotalChunks != 1 || queue.tasks[0].TaskChunkID != 0 {
		t.Fatalf("index continuation = %+v", queue.tasks)
	}
}

func TestEmbedStagePublishesPartialContinuation(t *testing.T) {
	vectors := make([]*model.DocumentVector, 257)
	for i := range vectors {
		vectors[i] = embedVector(i)
	}
	repo := &embedRangeRepo{fakeVectorRepo: fakeVectorRepo{vectors: vectors}}
	cache := &embedCacheRecorder{}
	queue := &stageTaskQueue{}
	processor := &Processor{docVectorRepo: repo, embeddingClient: &embedClientFake{vectorCount: -1}, embeddingCache: cache, taskQueue: queue, kafkaCfg: serverconfig.KafkaConfig{EmbeddingBatchSize: 4}}
	task := tasks.FileProcessingTask{FileMD5: "embed-file", Stage: tasks.StageEmbed, TotalChunks: 257, TaskChunkID: 1}

	if err := processor.processEmbed(context.Background(), task); err != nil {
		t.Fatalf("processEmbed() error = %v", err)
	}
	if len(queue.tasks) != 1 || queue.tasks[0].Stage != tasks.StageEmbed || queue.tasks[0].ChunkStart != 256 || queue.tasks[0].TaskChunkID != 2 || queue.tasks[0].TotalChunks != 257 {
		t.Fatalf("partial continuation = %+v", queue.tasks)
	}
	if len(cache.puts) != 64 || len(cache.ttls) != 1 {
		t.Fatalf("window batching = puts:%d ttls:%d", len(cache.puts), len(cache.ttls))
	}
}

func TestEmbedStageRejectsVectorCountMismatchWithoutPublishing(t *testing.T) {
	repo := &embedRangeRepo{fakeVectorRepo: fakeVectorRepo{vectors: []*model.DocumentVector{embedVector(0), embedVector(1)}}}
	cache := &embedCacheRecorder{}
	queue := &stageTaskQueue{}
	processor := &Processor{docVectorRepo: repo, embeddingClient: &embedClientFake{vectorCount: 1}, embeddingCache: cache, taskQueue: queue}
	task := tasks.FileProcessingTask{FileMD5: "embed-file", Stage: tasks.StageEmbed, TotalChunks: 2}

	err := processor.processEmbed(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "vector count mismatch") {
		t.Fatalf("error = %v, want vector count mismatch", err)
	}
	if len(queue.tasks) != 0 || len(cache.puts) != 0 || len(cache.ttls) != 0 {
		t.Fatalf("side effects after mismatch: queue=%v puts=%v ttls=%v", queue.tasks, cache.puts, cache.ttls)
	}
}

func TestEmbedStagePropagatesContextCancellation(t *testing.T) {
	repo := &embedRangeRepo{fakeVectorRepo: fakeVectorRepo{vectors: []*model.DocumentVector{embedVector(0)}}}
	queue := &stageTaskQueue{}
	processor := &Processor{docVectorRepo: repo, embeddingClient: &embedClientFake{vectorCount: -1, cancelAware: true}, embeddingCache: &embedCacheRecorder{}, taskQueue: queue}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := processor.processEmbed(ctx, tasks.FileProcessingTask{FileMD5: "embed-file", Stage: tasks.StageEmbed, TotalChunks: 1})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if len(queue.tasks) != 0 {
		t.Fatalf("canceled embedding published task: %+v", queue.tasks)
	}
}
