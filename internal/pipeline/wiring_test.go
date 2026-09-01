package pipeline

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"code-agent/internal/serverconfig"
)

func TestPipelineWiringProductionConstructorInstallsPorts(t *testing.T) {
	processor := NewProcessor(
		nil,
		nil,
		serverconfig.ElasticsearchConfig{},
		serverconfig.MinIOConfig{},
		serverconfig.EmbeddingConfig{},
		serverconfig.CorpusConfig{},
		serverconfig.KafkaConfig{},
		nil,
		nil,
		nil,
		nil,
	)
	if processor.objectStore == nil || processor.taskQueue == nil || processor.embeddingCache == nil || processor.indexWriter == nil || processor.lifecycle == nil {
		t.Fatalf("production constructor left a required port nil: object=%T queue=%T cache=%T index=%T lifecycle=%T", processor.objectStore, processor.taskQueue, processor.embeddingCache, processor.indexWriter, processor.lifecycle)
	}
}

func TestPipelineStageDoesNotUseGlobals(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	dir := filepath.Dir(currentFile)
	stageFiles := []string{"parse_stage.go", "chunk_stage.go", "embed_stage.go", "index_stage.go", "processor.go"}
	for _, name := range stageFiles {
		contents, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, forbidden := range []string{
			"storage.MinioClient",
			"database.RDB",
			"kafka.Produce",
			"es.BulkIndexDocuments",
		} {
			if strings.Contains(string(contents), forbidden) {
				t.Errorf("%s directly references infrastructure global %q", name, forbidden)
			}
		}
	}
}
