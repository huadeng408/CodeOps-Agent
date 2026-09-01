package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"code-agent/internal/serverconfig"
	orchestratorclient "code-agent/pkg/orchestrator"
	"code-agent/pkg/tasks"
)

type stageObjectStore struct {
	readData   map[string]string
	operations []string
	writes     []stageObjectWrite
}

type stageObjectWrite struct {
	bucket      string
	object      string
	contentType string
	body        string
}

func (s *stageObjectStore) Read(_ context.Context, _ string, object string) (io.ReadCloser, error) {
	s.operations = append(s.operations, "read:"+object)
	return io.NopCloser(bytes.NewBufferString(s.readData[object])), nil
}

func (s *stageObjectStore) Write(_ context.Context, bucket, object string, reader io.Reader, _ int64, contentType string) error {
	body, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	s.operations = append(s.operations, "write:"+object)
	s.writes = append(s.writes, stageObjectWrite{bucket: bucket, object: object, contentType: contentType, body: string(body)})
	return nil
}

func (s *stageObjectStore) Presign(_, _ string, _ time.Duration) (string, error) { return "", nil }
func (s *stageObjectStore) Delete(context.Context, string, string) error         { return nil }

type stageTaskQueue struct {
	operations []string
	tasks      []tasks.FileProcessingTask
}

func (q *stageTaskQueue) Publish(task tasks.FileProcessingTask) error {
	q.operations = append(q.operations, "publish:"+string(task.Stage))
	q.tasks = append(q.tasks, task)
	return nil
}

type stageCache struct{ clears []string }

func (c *stageCache) Clear(_ context.Context, key string) error {
	c.clears = append(c.clears, key)
	return nil
}
func (*stageCache) Put(context.Context, string, map[int][]float32) error { return nil }
func (*stageCache) SetTTL(context.Context, string, time.Duration) error  { return nil }
func (*stageCache) Load(context.Context, string) (map[int][]float32, error) {
	return nil, nil
}

func TestParseStageExternalWritesArtifactAndPublishesChunk(t *testing.T) {
	store := &stageObjectStore{}
	queue := &stageTaskQueue{}
	processor := &Processor{
		minioCfg:        serverconfig.MinIOConfig{BucketName: "documents"},
		ingestionClient: &fakeIngestionClient{parseResult: orchestratorclient.ParsedArtifact{ParsedText: "external parsed text"}},
		objectStore:     store,
		taskQueue:       queue,
	}
	task := tasks.FileProcessingTask{FileMD5: "file-parse", FileName: "doc.md", ObjectURL: "https://source.example/doc.md", Stage: tasks.StageParse}

	if err := processor.processParse(context.Background(), task); err != nil {
		t.Fatalf("processParse() error = %v", err)
	}
	if len(store.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(store.writes))
	}
	write := store.writes[0]
	if write.bucket != "documents" || write.object != "parsed/file-parse.json" || write.contentType != "application/json" {
		t.Fatalf("unexpected artifact write: %+v", write)
	}
	if len(queue.tasks) != 1 || queue.tasks[0].Stage != tasks.StageChunk || queue.tasks[0].ParsedObject != write.object {
		t.Fatalf("unexpected next task: %+v", queue.tasks)
	}
	if len(store.operations) != 1 || store.operations[0] != "write:parsed/file-parse.json" || len(queue.operations) != 1 || queue.operations[0] != "publish:chunk" {
		t.Fatalf("stage order mismatch: storage=%v queue=%v", store.operations, queue.operations)
	}
}

func TestChunkStageLocalReadsPersistsAndPublishesEmbed(t *testing.T) {
	store := &stageObjectStore{readData: map[string]string{"parsed/file-chunk.txt": "one two three"}}
	queue := &stageTaskQueue{}
	cache := &stageCache{}
	repo := &fakeVectorRepo{}
	processor := &Processor{
		minioCfg:       serverconfig.MinIOConfig{BucketName: "documents"},
		embeddingCfg:   serverconfig.EmbeddingConfig{Model: "model-v1"},
		docVectorRepo:  repo,
		objectStore:    store,
		taskQueue:      queue,
		embeddingCache: cache,
	}
	task := tasks.FileProcessingTask{FileMD5: "file-chunk", FileName: "doc.txt", ParsedObject: "parsed/file-chunk.txt", Stage: tasks.StageChunk, UserID: 4, OrgTag: "org"}

	if err := processor.processChunk(context.Background(), task); err != nil {
		t.Fatalf("processChunk() error = %v", err)
	}
	if len(repo.vectors) != 1 || repo.vectors[0].TextContent != "one two three" || repo.vectors[0].ModelVersion != "model-v1" {
		t.Fatalf("persisted vectors = %+v", repo.vectors)
	}
	if len(cache.clears) != 1 || cache.clears[0] != "pipeline:embeddings:file-chunk" {
		t.Fatalf("cache clears = %v", cache.clears)
	}
	if len(queue.tasks) != 1 || queue.tasks[0].Stage != tasks.StageEmbed || queue.tasks[0].TotalChunks != 1 || queue.tasks[0].ParsedObject != task.ParsedObject {
		t.Fatalf("unexpected next task: %+v", queue.tasks)
	}
	if len(store.operations) != 1 || store.operations[0] != "read:parsed/file-chunk.txt" || len(queue.operations) != 1 || queue.operations[0] != "publish:embed" {
		t.Fatalf("stage order mismatch: storage=%v queue=%v", store.operations, queue.operations)
	}
}

func TestChunkStageExternalReadsArtifactPersistsAndPublishesEmbed(t *testing.T) {
	artifact := orchestratorclient.ParsedArtifact{ParsedText: "external text"}
	artifactBytes, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	store := &stageObjectStore{readData: map[string]string{"parsed/file-external.json": string(artifactBytes)}}
	queue := &stageTaskQueue{}
	cache := &stageCache{}
	repo := &fakeVectorRepo{}
	processor := &Processor{
		minioCfg:        serverconfig.MinIOConfig{BucketName: "documents"},
		embeddingCfg:    serverconfig.EmbeddingConfig{Model: "external-model"},
		docVectorRepo:   repo,
		ingestionClient: &fakeIngestionClient{chunkResult: orchestratorclient.ChunkResult{Chunks: []string{"external chunk"}}},
		objectStore:     store,
		taskQueue:       queue,
		embeddingCache:  cache,
	}
	task := tasks.FileProcessingTask{FileMD5: "file-external", FileName: "doc.md", Stage: tasks.StageChunk}

	if err := processor.processChunk(context.Background(), task); err != nil {
		t.Fatalf("processChunk() error = %v", err)
	}
	if len(repo.vectors) != 1 || repo.vectors[0].TextContent != "external chunk" {
		t.Fatalf("persisted vectors = %+v", repo.vectors)
	}
	if len(store.operations) != 1 || store.operations[0] != "read:parsed/file-external.json" {
		t.Fatalf("storage operations = %v", store.operations)
	}
	if len(queue.tasks) != 1 || queue.tasks[0].Stage != tasks.StageEmbed || queue.tasks[0].ParsedObject != "parsed/file-external.json" {
		t.Fatalf("unexpected next task: %+v", queue.tasks)
	}
}
