package pipeline

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/tasks"
)

// TestIndexStageRejectsInvalidInputsBeforeWriting catches any change that
// lets an incomplete or invalid structured batch reach Elasticsearch.
func TestIndexStageRejectsInvalidInputsBeforeWriting(t *testing.T) {
	tests := []struct {
		name      string
		vectors   []*model.DocumentVector
		cached    map[int][]float32
		corpusCfg serverconfig.CorpusConfig
		wantError string
	}{
		{
			name:      "missing cached vector",
			vectors:   []*model.DocumentVector{structuredVectorItem(0), structuredVectorItem(1)},
			cached:    map[int][]float32{0: native1024(0)},
			corpusCfg: serverconfig.CorpusConfig{TextIndex: "knowledge_base_v2_bge_m3"},
			wantError: "missing vector for chunk=1",
		},
		{
			name:      "wrong structured dimension",
			vectors:   []*model.DocumentVector{structuredVectorItem(0)},
			cached:    map[int][]float32{0: {1, 2}},
			corpusCfg: serverconfig.CorpusConfig{TextIndex: "knowledge_base_v2_bge_m3"},
			wantError: "structured vector validation failed",
		},
		{
			name:      "missing structured index",
			vectors:   []*model.DocumentVector{structuredVectorItem(0)},
			cached:    map[int][]float32{0: native1024(0)},
			corpusCfg: serverconfig.CorpusConfig{},
			wantError: "corpus text index is not configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor, writer, lifecycle, _ := newIndexStageProcessor(t, tt.vectors, tt.cached, tt.corpusCfg, 100, nil, nil)

			err := processor.indexStage().process(context.Background(), fileTask())

			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("process() error = %v, want %q", err, tt.wantError)
			}
			if writer.writes != 0 {
				t.Fatalf("writes = %d, want zero before all validation passes", writer.writes)
			}
			if lifecycle.activeCalls != 0 {
				t.Fatalf("ACTIVE calls = %d, want zero after rejected index", lifecycle.activeCalls)
			}
		})
	}
}

// TestIndexStageActivatesAfterWritesAndIgnoresCleanupFailures catches a
// reordered lifecycle transition or a cleanup failure that falsely retries a
// confirmed Elasticsearch write.
func TestIndexStageActivatesAfterWritesAndIgnoresCleanupFailures(t *testing.T) {
	processor, writer, lifecycle, events := newIndexStageProcessor(
		t,
		[]*model.DocumentVector{structuredVectorItem(0), structuredVectorItem(1)},
		map[int][]float32{0: native1024(0), 1: native1024(1)},
		serverconfig.CorpusConfig{TextIndex: "knowledge_base_v2_bge_m3"},
		1,
		errors.New("cache unavailable"),
		errors.New("object cleanup unavailable"),
	)
	task := fileTask()
	task.DocumentID = "corpus:document:1"
	if err := processor.indexStage().process(context.Background(), task); err != nil {
		t.Fatalf("process() error = %v, want successful confirmed index", err)
	}

	if writer.writes != 2 {
		t.Fatalf("writes = %d, want one write per configured batch", writer.writes)
	}
	if lifecycle.activeCalls != 1 || lifecycle.activeTask.DocumentID != task.DocumentID {
		t.Fatalf("ACTIVE lifecycle call = %+v, want task document %q", lifecycle, task.DocumentID)
	}
	if got, want := strings.Join(*events, ","), "write,write,active,cache-clear,object-delete"; got != want {
		t.Fatalf("side-effect order = %q, want %q", got, want)
	}
}

func newIndexStageProcessor(
	t *testing.T,
	vectors []*model.DocumentVector,
	cached map[int][]float32,
	corpusCfg serverconfig.CorpusConfig,
	bulkSize int,
	clearErr error,
	deleteErr error,
) (*Processor, *recordingIndexWriter, *recordingIndexLifecycle, *[]string) {
	t.Helper()
	events := make([]string, 0, 5)
	writer := &recordingIndexWriter{events: &events}
	lifecycle := &recordingIndexLifecycle{events: &events}
	processor, err := NewProcessorWithDeps(PipelineDeps{
		ObjectStore: &recordingIndexStore{deleteErr: deleteErr, events: &events},
		TaskQueue:   recordingIndexQueue{},
		EmbeddingCache: &recordingIndexCache{
			vectors:  cached,
			clearErr: clearErr,
			events:   &events,
		},
		IndexWriter:       writer,
		DocumentLifecycle: lifecycle,
		Elasticsearch:     serverconfig.ElasticsearchConfig{IndexName: "knowledge_base"},
		Embedding:         serverconfig.EmbeddingConfig{Model: "BAAI/bge-m3", ModelRevision: pinnedRevision, Dimensions: 1024, ExpectedDimensions: 1024},
		Corpus:            corpusCfg,
		Kafka:             serverconfig.KafkaConfig{ESBulkBatchSize: bulkSize},
		DocVectorRepo:     &fakeVectorRepo{vectors: vectors},
	})
	if err != nil {
		t.Fatalf("NewProcessorWithDeps() error = %v", err)
	}
	return processor, writer, lifecycle, &events
}

type recordingIndexWriter struct {
	events *[]string
	writes int
}

func (w *recordingIndexWriter) Write(_ context.Context, _ string, _ []model.EsDocument) error {
	*w.events = append(*w.events, "write")
	w.writes++
	return nil
}

type recordingIndexCache struct {
	vectors  map[int][]float32
	clearErr error
	events   *[]string
}

func (c *recordingIndexCache) Clear(_ context.Context, _ string) error {
	*c.events = append(*c.events, "cache-clear")
	return c.clearErr
}

func (c *recordingIndexCache) Put(context.Context, string, map[int][]float32) error { return nil }
func (c *recordingIndexCache) SetTTL(context.Context, string, time.Duration) error  { return nil }
func (c *recordingIndexCache) Load(context.Context, string) (map[int][]float32, error) {
	return c.vectors, nil
}

type recordingIndexStore struct {
	deleteErr error
	events    *[]string
}

func (recordingIndexStore) Read(context.Context, string, string) (io.ReadCloser, error) {
	return nil, errors.New("not used by index stage")
}
func (recordingIndexStore) Write(context.Context, string, string, io.Reader, int64, string) error {
	return errors.New("not used by index stage")
}
func (recordingIndexStore) Presign(string, string, time.Duration) (string, error) {
	return "", errors.New("not used by index stage")
}
func (s *recordingIndexStore) Delete(context.Context, string, string) error {
	*s.events = append(*s.events, "object-delete")
	return s.deleteErr
}

type recordingIndexQueue struct{}

func (recordingIndexQueue) Publish(tasks.FileProcessingTask) error { return nil }

type recordingIndexLifecycle struct {
	events      *[]string
	activeCalls int
	activeTask  tasks.FileProcessingTask
}

func (l *recordingIndexLifecycle) Active(_ context.Context, task tasks.FileProcessingTask) error {
	*l.events = append(*l.events, "active")
	l.activeCalls++
	l.activeTask = task
	return nil
}
func (recordingIndexLifecycle) Skipped(context.Context, tasks.FileProcessingTask, string) error {
	return nil
}
func (recordingIndexLifecycle) Failed(context.Context, tasks.FileProcessingTask, string, error) error {
	return nil
}
