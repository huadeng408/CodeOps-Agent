package pipeline

import (
	"context"
	"strings"
	"testing"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/log"
	"code-agent/pkg/tasks"
)

func init() {
	log.Init("error", "console", "")
}

// fakeVectorRepo stores vectors in memory.
type fakeVectorRepo struct {
	vectors  []*model.DocumentVector
	batchErr error
}

func (f *fakeVectorRepo) FindByFileMD5(fileMD5 string) ([]*model.DocumentVector, error) {
	out := make([]*model.DocumentVector, 0)
	for _, v := range f.vectors {
		if v.FileMD5 == fileMD5 {
			out = append(out, v)
		}
	}
	return out, nil
}

func (f *fakeVectorRepo) CountByFileMD5(fileMD5 string) (int64, error) {
	var count int64
	for _, v := range f.vectors {
		if v.FileMD5 == fileMD5 {
			count++
		}
	}
	return count, nil
}

func (f *fakeVectorRepo) BatchCreate(vectors []*model.DocumentVector) error {
	if f.batchErr != nil {
		return f.batchErr
	}
	f.vectors = append(f.vectors, vectors...)
	return nil
}

func (f *fakeVectorRepo) FindByFileMD5Range(fileMD5 string, offset, limit int) ([]*model.DocumentVector, error) {
	return f.FindByFileMD5(fileMD5)
}

func (f *fakeVectorRepo) DeleteByFileMD5(fileMD5 string) error {
	out := make([]*model.DocumentVector, 0)
	for _, v := range f.vectors {
		if v.FileMD5 != fileMD5 {
			out = append(out, v)
		}
	}
	f.vectors = out
	return nil
}

func (f *fakeVectorRepo) FindByParentChunkID(parentChunkID string) ([]*model.DocumentVector, error) {
	var out []*model.DocumentVector
	for _, v := range f.vectors {
		if v.ParentChunkID == parentChunkID {
			out = append(out, v)
		}
	}
	return out, nil
}

// fakeESWriter records writes and failures.
type fakeESWriter struct {
	writes    int
	lastIndex string
	lastDocs  []model.EsDocument
	failOnce  bool
}

func (w *fakeESWriter) bulk(ctx context.Context, index string, docs []model.EsDocument) error {
	w.writes++
	w.lastIndex = index
	w.lastDocs = docs
	if w.failOnce {
		w.failOnce = false
		return &esWriteError{}
	}
	return nil
}

type esWriteError struct{}

func (e *esWriteError) Error() string { return "es write failed" }

func newTestIndexProcessor() (*Processor, *fakeVectorRepo, *fakeESWriter) {
	repo := &fakeVectorRepo{}
	writer := &fakeESWriter{}
	p := &Processor{
		esCfg:        serverconfig.ElasticsearchConfig{IndexName: "knowledge_base"},
		embeddingCfg: serverconfig.EmbeddingConfig{Model: "BAAI/bge-m3", ModelRevision: pinnedRevision, Dimensions: 1024, ExpectedDimensions: 1024},
		corpusCfg:    serverconfig.CorpusConfig{TextIndex: "knowledge_base_v2_bge_m3"},
		docVectorRepo: repo,
		esWriter:      writer.bulk,
	}
	return p, repo, writer
}

const pinnedRevision = "BAAI/bge-m3@8f1b7f9d4c2a6e5b0d9c8f7a6b5c4d3e2f1a0b9c"

func structuredVectorItem(chunkID int) *model.DocumentVector {
	return &model.DocumentVector{
		FileMD5:          "file-1",
		ChunkID:          chunkID,
		TextContent:      "exact source",
		EmbeddingText:    "title\nexact source",
		DocumentID:       "doc-1",
		SourceSHA256:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ParentChunkID:    "parent-1",
		PageID:           "doc-1:p0",
		PageSpan:         []int{0, 0},
		ElementIDs:       []string{"e1"},
		ElementTypes:     []string{"text"},
		BBoxRefs:         []string{"e1:0,0,1,1"},
		TokenCount:       2,
		TokenizerID:      "whitespace-v1",
		ParserName:       "mineru",
		ParserVersion:    "3.4.4",
		CorpusGeneration: "techdocs-2026-07-30-v1",
		UserID:           7,
		OrgTag:           "research",
		IsPublic:         false,
	}
}

func native1024(i int) []float32 {
	v := make([]float32, 1024)
	for j := range v {
		v[j] = float32((i + j) % 7)
	}
	return v
}

func TestProcessIndexZeroWritesOnDimensionMismatch(t *testing.T) {
	p, repo, writer := newTestIndexProcessor()
	repo.vectors = []*model.DocumentVector{structuredVectorItem(0)}
	p.vectorCache = func(_ context.Context, _ string) (map[int][]float32, error) {
		return map[int][]float32{0: {1, 2}}, nil // 512-dim vector (or any non-1024)
	}

	err := p.processIndex(context.Background(), fileTask())
	if err == nil || !strings.Contains(err.Error(), "structured vector validation failed") {
		t.Fatalf("expected validation error, got %v", err)
	}
	if writer.writes != 0 {
		t.Fatalf("expected ZERO ES writes on dimension mismatch, got %d", writer.writes)
	}
}

func TestProcessIndexWritesToCorpusIndexWithProvenance(t *testing.T) {
	p, repo, writer := newTestIndexProcessor()
	repo.vectors = []*model.DocumentVector{structuredVectorItem(0), structuredVectorItem(1)}
	p.vectorCache = func(_ context.Context, _ string) (map[int][]float32, error) {
		return map[int][]float32{0: native1024(0), 1: native1024(1)}, nil
	}

	if err := p.processIndex(context.Background(), fileTask()); err != nil {
		t.Fatal(err)
	}
	if writer.writes != 1 {
		t.Fatalf("expected 1 bulk write, got %d", writer.writes)
	}
	if writer.lastIndex != "knowledge_base_v2_bge_m3" {
		t.Fatalf("index = %q, want corpus text index", writer.lastIndex)
	}
	if len(writer.lastDocs) != 2 {
		t.Fatalf("docs = %d, want 2", len(writer.lastDocs))
	}
	doc := writer.lastDocs[0]
	if doc.ModelVersion != pinnedRevision {
		t.Fatalf("model version = %q, want pinned revision", doc.ModelVersion)
	}
	if doc.SourceSHA256 == "" || doc.DocumentID == "" || doc.PageID == "" || len(doc.ElementIDs) == 0 {
		t.Fatalf("provenance missing in ES doc: %+v", doc)
	}
	if doc.TextContent != "exact source" || doc.EmbeddingText != "title\nexact source" {
		t.Fatalf("text/embedding fields not separated: %+v", doc)
	}
}

func fileTask() tasks.FileProcessingTask {
	return tasks.FileProcessingTask{FileMD5: "file-1"}
}
