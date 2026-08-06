package pipeline

import (
	"testing"

	"code-agent/internal/model"
	"code-agent/pkg/tasks"
)

// corpusProvenance returns the pinned provenance a corpus import attaches to
// a task; the values mirror what the internal knowledge-ingest entry validates.
func corpusProvenance() *model.CorpusProvenance {
	return &model.CorpusProvenance{
		SourceID:         "go",
		SourcePath:       "doc/asm.md",
		SourceURL:        "https://github.com/golang/go/blob/0123456789abcdef0123456789abcdef01234567/doc/asm.md",
		SourceCommit:     "0123456789abcdef0123456789abcdef01234567",
		SourceSHA256:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		TargetIndex:      "knowledge_base_v2_bge_m3",
		CorpusGeneration: "techdocs-2026-07-30-v1",
	}
}

// TestDocumentVectorFromStructuredChunkPrefersTaskDocumentID guards the corpus
// contract: the worker-supplied chunk.DocumentID is an artifact identifier and
// must be overridden by the task's knowledge_document identifier
// (git@commit:path) whenever the task carries one — eval qrels match on that
// corpus form.
func TestDocumentVectorFromStructuredChunkPrefersTaskDocumentID(t *testing.T) {
	task := tasks.FileProcessingTask{
		FileMD5:    "file-md5",
		DocumentID: "go@0123456789abcdef0123456789abcdef01234567:doc/asm.md",
		Provenance: corpusProvenance(),
	}
	chunk := model.StructuredChunk{DocumentID: "worker-artifact-id", ChunkID: "c1", Text: "body"}

	vector := documentVectorFromStructuredChunk(task, 0, chunk, "BAAI/bge-m3@5617a9f61b028005a4858fdac845db406aefb181")
	if vector.DocumentID != task.DocumentID {
		t.Fatalf("DocumentID = %q, want task corpus identifier %q", vector.DocumentID, task.DocumentID)
	}
}

// TestDocumentVectorFromStructuredChunkFillsCorpusProvenance checks that the
// task's validated provenance lands on the vector row: source identity
// (id/path/commit) always comes from the task, while SourceURL prefers the
// chunk's value when the worker already supplied one.
func TestDocumentVectorFromStructuredChunkFillsCorpusProvenance(t *testing.T) {
	task := tasks.FileProcessingTask{
		FileMD5:    "file-md5",
		DocumentID: "go@0123456789abcdef0123456789abcdef01234567:doc/asm.md",
		Provenance: corpusProvenance(),
	}
	chunk := model.StructuredChunk{DocumentID: "worker-artifact-id", ChunkID: "c1", Text: "body"}

	vector := documentVectorFromStructuredChunk(task, 0, chunk, "model")
	prov := task.Provenance
	if vector.SourceID != prov.SourceID || vector.SourcePath != prov.SourcePath || vector.SourceCommit != prov.SourceCommit {
		t.Fatalf("source identity = id %q path %q commit %q, want %+v", vector.SourceID, vector.SourcePath, vector.SourceCommit, prov)
	}
	if vector.SourceURL != prov.SourceURL {
		t.Fatalf("SourceURL = %q, want provenance URL %q", vector.SourceURL, prov.SourceURL)
	}
}

// TestDocumentVectorFromStructuredChunkPrefersChunkSourceURL keeps the worker
// chunk's SourceURL when present — provenance only fills the gap.
func TestDocumentVectorFromStructuredChunkPrefersChunkSourceURL(t *testing.T) {
	task := tasks.FileProcessingTask{FileMD5: "file-md5", Provenance: corpusProvenance()}
	chunk := model.StructuredChunk{
		DocumentID: "worker-artifact-id", ChunkID: "c1", Text: "body",
		SourceURL: "https://worker.example/page",
	}

	vector := documentVectorFromStructuredChunk(task, 0, chunk, "model")
	if vector.SourceURL != chunk.SourceURL {
		t.Fatalf("SourceURL = %q, want chunk URL %q", vector.SourceURL, chunk.SourceURL)
	}
}

// TestDocumentVectorFromStructuredChunkLegacyFallback guarantees ordinary
// uploads (no task DocumentID, nil provenance) keep today's behavior: the
// chunk's own DocumentID is used and no corpus fields are invented.
func TestDocumentVectorFromStructuredChunkLegacyFallback(t *testing.T) {
	task := tasks.FileProcessingTask{FileMD5: "file-md5"}
	chunk := model.StructuredChunk{DocumentID: "legacy-doc", ChunkID: "c1", Text: "body"}

	vector := documentVectorFromStructuredChunk(task, 0, chunk, "model")
	if vector.DocumentID != "legacy-doc" {
		t.Fatalf("DocumentID = %q, want legacy chunk identifier", vector.DocumentID)
	}
	if vector.SourceID != "" || vector.SourcePath != "" || vector.SourceCommit != "" || vector.SourceURL != "" {
		t.Fatalf("legacy vector gained corpus fields: id %q path %q commit %q url %q",
			vector.SourceID, vector.SourcePath, vector.SourceCommit, vector.SourceURL)
	}
}

// TestEsDocumentFromVectorMapsCorpusProvenanceFields checks the ES document
// carries the corpus identity fields so qrels keyed on git@commit:path match
// and retrieval results stay traceable to the pinned source commit.
func TestEsDocumentFromVectorMapsCorpusProvenanceFields(t *testing.T) {
	item := model.DocumentVector{
		FileMD5: "file-md5", ChunkID: 2, TextContent: "body",
		DocumentID:   "go@0123456789abcdef0123456789abcdef01234567:doc/asm.md",
		SourceID:     "go",
		SourcePath:   "doc/asm.md",
		SourceCommit: "0123456789abcdef0123456789abcdef01234567",
		SourceURL:    "https://github.com/golang/go/blob/0123456789abcdef0123456789abcdef01234567/doc/asm.md",
		UserID:       7,
	}

	doc := esDocumentFromVector(item, []float32{1, 2}, "BAAI/bge-m3@5617a9f61b028005a4858fdac845db406aefb181")
	if doc.DocumentID != item.DocumentID {
		t.Fatalf("DocumentID = %q, want %q", doc.DocumentID, item.DocumentID)
	}
	if doc.SourceID != item.SourceID || doc.SourcePath != item.SourcePath || doc.SourceCommit != item.SourceCommit {
		t.Fatalf("source identity = id %q path %q commit %q, want %+v", doc.SourceID, doc.SourcePath, doc.SourceCommit, item)
	}
	if doc.SourceURL != item.SourceURL {
		t.Fatalf("SourceURL = %q, want %q", doc.SourceURL, item.SourceURL)
	}
}
