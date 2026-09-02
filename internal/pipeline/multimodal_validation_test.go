package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"code-agent/internal/model"
	orchestratorclient "code-agent/pkg/orchestrator"
	"code-agent/pkg/tasks"
)

func validPDFStructuredChunk() model.StructuredChunk {
	return model.StructuredChunk{
		DocumentID:       "doc-1",
		ChunkID:          "doc-1:chunk:1",
		Text:             "page evidence",
		PageID:           "doc-1:p2",
		PageSpan:         []int{2, 2},
		ElementIDs:       []string{"doc-1:p2:e1"},
		ElementTypes:     []string{"text"},
		BBoxRefs:         []string{"doc-1:p2:e1:0,0,100,100"},
		TokenCount:       2,
		ParserName:       "mineru",
		ParserVersion:    "3.4.4",
		SourceSHA256:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		CorpusGeneration: "techdocs-2026-07-30-v1",
	}
}

func TestValidatePDFStructuredChunkAcceptsSinglePageMineruEvidence(t *testing.T) {
	if err := validatePDFStructuredChunk(validPDFStructuredChunk()); err != nil {
		t.Fatalf("valid PDF structured chunk rejected: %v", err)
	}
}

func TestValidatePDFStructuredChunkRejectsCrossPageEvidence(t *testing.T) {
	chunk := validPDFStructuredChunk()
	chunk.PageSpan = []int{2, 3}

	err := validatePDFStructuredChunk(chunk)
	if err == nil || !strings.Contains(err.Error(), "must not span multiple pages") {
		t.Fatalf("expected cross-page rejection, got %v", err)
	}
}

func TestValidatePDFStructuredChunkRejectsNonMineruParser(t *testing.T) {
	chunk := validPDFStructuredChunk()
	chunk.ParserName = "tika"

	err := validatePDFStructuredChunk(chunk)
	if err == nil || !strings.Contains(err.Error(), "parser_name must be mineru") {
		t.Fatalf("expected parser rejection, got %v", err)
	}
}

func TestProcessChunkExternalArtifactRejectsCrossPagePDFBeforePersistence(t *testing.T) {
	chunk := validPDFStructuredChunk()
	chunk.PageSpan = []int{2, 3}
	chunk.DocumentID = "pdf-md5"
	chunk.ChunkID = "pdf-md5:chunk:1"
	chunk.PageID = "pdf-md5:p2"
	chunk.ElementIDs = []string{"pdf-md5:p2:e1"}
	chunk.BBoxRefs = []string{"pdf-md5:p2:e1:0,0,100,100"}
	chunk.SourceSHA256 = testArtifactSHA
	client := &fakeIngestionClient{
		chunkResult: orchestratorclient.ChunkResult{
			StructuredChunks: []model.StructuredChunk{chunk},
		},
	}
	repo := &fakeVectorRepo{}
	processor := newExternalChunkProcessor(repo, client, &fakeDocumentRepo{})
	artifactBytes, err := json.Marshal(validMineruArtifactWithElementID("pdf-md5", "pdf-md5:p2:e1"))
	if err != nil {
		t.Fatal(err)
	}

	task := tasks.FileProcessingTask{FileMD5: "pdf-md5", FileName: "scan.pdf"}
	err = processor.processChunkExternalArtifact(context.Background(), task, "parsed/pdf.json", artifactBytes)
	if err == nil || !strings.Contains(err.Error(), "must not span multiple pages") {
		t.Fatalf("expected cross-page PDF rejection, got %v", err)
	}
	if len(repo.vectors) != 0 {
		t.Fatalf("cross-page PDF chunk reached persistence: %d vectors", len(repo.vectors))
	}
}
