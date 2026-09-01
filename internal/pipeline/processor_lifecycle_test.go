package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	orchestratorclient "code-agent/pkg/orchestrator"
	"code-agent/pkg/tasks"
)

const lifecycleRawSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const lifecycleArtifactSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func lifecycleDocumentID() string { return "go@abc123:def/doc/asm.html" }

func lifecycleProvenance() *model.CorpusProvenance {
	return &model.CorpusProvenance{
		SourceID:         "go",
		SourcePath:       "doc/asm.html",
		SourceCommit:     "abc123def4567890abc123def4567890abc123de",
		SourceSHA256:     lifecycleRawSHA,
		TargetIndex:      "knowledge_base_v2_bge_m3",
		CorpusGeneration: "techdocs-2026-07-30-v1",
	}
}

// validStructuredChunkForLifecycle returns a chunk that satisfies
// model.StructuredChunk.Validate so the chunk-stage fill/persist path runs to
// completion under test.
func validStructuredChunkForLifecycle() model.StructuredChunk {
	return model.StructuredChunk{
		DocumentID:       "doc-1",
		ChunkID:          "doc-1:chunk:0",
		Text:             "exact source",
		PageID:           "doc-1:p0",
		ElementIDs:       []string{"e1"},
		ElementTypes:     []string{"text"},
		TokenCount:       1,
		ParserName:       "native",
		ParserVersion:    "native-v1",
		CorpusGeneration: "techdocs-2026-07-30-v1",
		SourceSHA256:     lifecycleArtifactSHA,
	}
}

func newExternalIndexProcessor(repo *fakeVectorRepo, ingestion *fakeIngestionClient, docRepo *fakeDocumentRepo) *Processor {
	return &Processor{
		esCfg:          serverconfig.ElasticsearchConfig{IndexName: "knowledge_base"},
		embeddingCfg:   serverconfig.EmbeddingConfig{Model: "BAAI/bge-m3", ModelRevision: pinnedRevision, Dimensions: 1024, ExpectedDimensions: 1024},
		corpusCfg:      serverconfig.CorpusConfig{TextIndex: "knowledge_base_v2_bge_m3"},
		docVectorRepo:  repo,
		ingestionClient: ingestion,
		documentRepo:   docRepo,
	}
}

func newExternalChunkProcessor(repo *fakeVectorRepo, ingestion *fakeIngestionClient, docRepo *fakeDocumentRepo) *Processor {
	return &Processor{
		embeddingCfg:   serverconfig.EmbeddingConfig{Model: "BAAI/bge-m3"},
		corpusCfg:      serverconfig.CorpusConfig{TextIndex: "knowledge_base_v2_bge_m3"},
		docVectorRepo:  repo,
		ingestionClient: ingestion,
		documentRepo:   docRepo,
	}
}

func native1024Cache(chunkID int) func(context.Context, string) (map[int][]float32, error) {
	return func(_ context.Context, _ string) (map[int][]float32, error) {
		return map[int][]float32{chunkID: native1024(chunkID)}, nil
	}
}

func encodeArtifact(a orchestratorclient.ParsedArtifact) ([]byte, error) {
	return json.Marshal(a)
}

// (a) processIndexExternal success marks the document ACTIVE with task.DocumentID.
func TestProcessIndexExternalMarksDocumentActiveOnSuccess(t *testing.T) {
	repo := &fakeVectorRepo{vectors: []*model.DocumentVector{structuredVectorItem(0)}}
	ingestion := &fakeIngestionClient{indexResult: 1}
	docRepo := &fakeDocumentRepo{}
	p := newExternalIndexProcessor(repo, ingestion, docRepo)
	p.vectorCache = native1024Cache(0)

	task := fileTask()
	task.DocumentID = lifecycleDocumentID()

	if err := p.processIndexExternal(context.Background(), task); err != nil {
		t.Fatalf("processIndexExternal failed: %v", err)
	}
	if len(docRepo.markCalls) != 1 {
		t.Fatalf("expected 1 ACTIVE mark, got %d", len(docRepo.markCalls))
	}
	call := docRepo.markCalls[0]
	if call.documentID != task.DocumentID || call.status != model.DocumentActive || call.lastError != "" {
		t.Fatalf("unexpected ACTIVE mark: %+v", call)
	}
}

// (b) ES bulk write failure must not mark the document ACTIVE.
func TestProcessIndexExternalNoActiveMarkOnESFailure(t *testing.T) {
	repo := &fakeVectorRepo{vectors: []*model.DocumentVector{structuredVectorItem(0)}}
	ingestion := &fakeIngestionClient{indexErr: errors.New("es bulk write failed")}
	docRepo := &fakeDocumentRepo{}
	p := newExternalIndexProcessor(repo, ingestion, docRepo)
	p.vectorCache = native1024Cache(0)

	task := fileTask()
	task.DocumentID = lifecycleDocumentID()

	err := p.processIndexExternal(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "external worker failed") {
		t.Fatalf("expected external worker failure, got %v", err)
	}
	if len(docRepo.markCalls) != 0 {
		t.Fatalf("expected ZERO marks on ES failure, got %d (%+v)", len(docRepo.markCalls), docRepo.markCalls)
	}
}

func TestProcessIndexExternalRejectsPartialIndexBeforeActivatingDocument(t *testing.T) {
	repo := &fakeVectorRepo{vectors: []*model.DocumentVector{structuredVectorItem(0), structuredVectorItem(1)}}
	// The worker acknowledged only one of the two documents without returning
	// an HTTP error. This must remain retryable rather than becoming ACTIVE.
	ingestion := &fakeIngestionClient{indexResult: 1}
	docRepo := &fakeDocumentRepo{}
	p := newExternalIndexProcessor(repo, ingestion, docRepo)
	p.vectorCache = func(_ context.Context, _ string) (map[int][]float32, error) {
		return map[int][]float32{0: native1024(0), 1: native1024(1)}, nil
	}

	err := p.processIndexExternal(context.Background(), fileTask())
	if err == nil || !strings.Contains(err.Error(), "indexed 1/2 documents") {
		t.Fatalf("expected partial index rejection, got %v", err)
	}
	if len(docRepo.markCalls) != 0 {
		t.Fatalf("expected ZERO ACTIVE marks after partial index, got %d", len(docRepo.markCalls))
	}
}

func TestProcessIndexExternalRejectsZeroIndexBeforeActivatingDocument(t *testing.T) {
	repo := &fakeVectorRepo{vectors: []*model.DocumentVector{structuredVectorItem(0)}}
	ingestion := &fakeIngestionClient{indexResult: 0}
	docRepo := &fakeDocumentRepo{}
	p := newExternalIndexProcessor(repo, ingestion, docRepo)
	p.vectorCache = native1024Cache(0)

	err := p.processIndexExternal(context.Background(), fileTask())
	if err == nil || !strings.Contains(err.Error(), "indexed 0/1 documents") {
		t.Fatalf("expected zero index rejection, got %v", err)
	}
	if len(docRepo.markCalls) != 0 {
		t.Fatalf("expected ZERO ACTIVE marks after zero index, got %d", len(docRepo.markCalls))
	}
}

// (f) An empty DocumentID (legacy task) must never touch the document repo.
func TestProcessIndexExternalSkipsMarkWhenDocumentIDEmpty(t *testing.T) {
	repo := &fakeVectorRepo{vectors: []*model.DocumentVector{structuredVectorItem(0)}}
	ingestion := &fakeIngestionClient{indexResult: 1}
	docRepo := &fakeDocumentRepo{}
	p := newExternalIndexProcessor(repo, ingestion, docRepo)
	p.vectorCache = native1024Cache(0)

	// task.DocumentID intentionally empty — legacy upload, no lifecycle row.
	if err := p.processIndexExternal(context.Background(), fileTask()); err != nil {
		t.Fatalf("processIndexExternal failed: %v", err)
	}
	if len(docRepo.markCalls) != 0 {
		t.Fatalf("expected ZERO marks when DocumentID empty, got %d (%+v)", len(docRepo.markCalls), docRepo.markCalls)
	}
}

// (c) BatchCreate failure on the external chunk path marks the document FAILED
// with a sanitized summary (no tokens / full payload echoed).
func TestProcessChunkExternalArtifactMarksFailedOnBatchCreateError(t *testing.T) {
	artifactBytes, err := encodeArtifact(orchestratorclient.ParsedArtifact{ParsedText: "exact source"})
	if err != nil {
		t.Fatal(err)
	}
	chunk := validStructuredChunkForLifecycle()
	ingestion := &fakeIngestionClient{chunkResult: orchestratorclient.ChunkResult{StructuredChunks: []model.StructuredChunk{chunk}}}
	repo := &fakeVectorRepo{batchErr: errors.New("persist failed: duplicate key\nSECRET-TOKEN-leak\nfull document body must not be echoed")}
	docRepo := &fakeDocumentRepo{}
	p := newExternalChunkProcessor(repo, ingestion, docRepo)

	task := fileTask()
	task.DocumentID = lifecycleDocumentID()

	err = p.processChunkExternalArtifact(context.Background(), task, "parsed/"+task.FileMD5+".json", artifactBytes)
	if err == nil || !strings.Contains(err.Error(), "persist chunks failed") {
		t.Fatalf("expected persist failure, got %v", err)
	}
	if len(docRepo.markCalls) != 1 {
		t.Fatalf("expected 1 FAILED mark, got %d", len(docRepo.markCalls))
	}
	call := docRepo.markCalls[0]
	if call.documentID != task.DocumentID || call.status != model.DocumentFailed {
		t.Fatalf("unexpected FAILED mark: %+v", call)
	}
	if strings.Contains(call.lastError, "SECRET-TOKEN") {
		t.Fatalf("last_error leaked secret material: %q", call.lastError)
	}
	if strings.Contains(call.lastError, "full document body") {
		t.Fatalf("last_error leaked payload content: %q", call.lastError)
	}
	if strings.ContainsAny(call.lastError, "\r\n") {
		t.Fatalf("last_error must be single-line: %q", call.lastError)
	}
	if !strings.Contains(call.lastError, "chunk") {
		t.Fatalf("last_error should carry the stage: %q", call.lastError)
	}
}

// (d) Provenance with a raw source_sha256 overrides any worker-produced hash.
func TestSourceSHA256ForChunkPrefersProvenanceHash(t *testing.T) {
	task := tasks.FileProcessingTask{Provenance: lifecycleProvenance()}
	got := sourceSHA256ForChunk(task, lifecycleArtifactSHA, []byte("artifact bytes"))
	if got != lifecycleRawSHA {
		t.Fatalf("sourceSHA256ForChunk = %q, want provenance raw hash", got)
	}
}

// (d, end-to-end) The chunk persisted to the vector repo carries the raw hash.
func TestProcessChunkExternalArtifactUsesProvenanceSourceSHA256(t *testing.T) {
	artifactBytes, err := encodeArtifact(orchestratorclient.ParsedArtifact{ParsedText: "exact source"})
	if err != nil {
		t.Fatal(err)
	}
	chunk := validStructuredChunkForLifecycle() // worker reported the artifact hash
	ingestion := &fakeIngestionClient{chunkResult: orchestratorclient.ChunkResult{StructuredChunks: []model.StructuredChunk{chunk}}}
	repo := &fakeVectorRepo{}
	p := newExternalChunkProcessor(repo, ingestion, &fakeDocumentRepo{})

	task := fileTask()
	task.CorpusGeneration = "techdocs-2026-07-30-v1"
	task.Provenance = lifecycleProvenance()

	// The trailing Kafka enqueue is not wired in the unit-test harness, so the
	// call returns an enqueue error after BatchCreate succeeded. The fill under
	// test happens before that step.
	if err := p.processChunkExternalArtifact(context.Background(), task, "parsed/"+task.FileMD5+".json", artifactBytes); err != nil &&
		!strings.Contains(err.Error(), "enqueue embed task") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.vectors) != 1 {
		t.Fatalf("expected 1 vector stored, got %d", len(repo.vectors))
	}
	if repo.vectors[0].SourceSHA256 != lifecycleRawSHA {
		t.Fatalf("SourceSHA256 = %q, want provenance raw hash %q", repo.vectors[0].SourceSHA256, lifecycleRawSHA)
	}
}

// (e) Without provenance the existing artifact-hash fallback is preserved.
func TestSourceSHA256ForChunkFallsBackToArtifactHash(t *testing.T) {
	artifactBytes := []byte("native artifact bytes")
	task := tasks.FileProcessingTask{} // legacy task: no provenance
	got := sourceSHA256ForChunk(task, "", artifactBytes)
	if got != hashSHA256(artifactBytes) {
		t.Fatalf("sourceSHA256ForChunk = %q, want artifact hash %q", got, hashSHA256(artifactBytes))
	}
	// A worker-provided hash is trusted when there is no provenance override.
	if got := sourceSHA256ForChunk(task, lifecycleArtifactSHA, artifactBytes); got != lifecycleArtifactSHA {
		t.Fatalf("sourceSHA256ForChunk = %q, want worker hash preserved", got)
	}
}
