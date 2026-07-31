package pipeline

import (
	"encoding/json"
	"testing"

	"code-agent/internal/model"
	orchestratorclient "code-agent/pkg/orchestrator"
	"code-agent/pkg/tasks"
)

func TestDocumentVectorFromStructuredChunkPreservesProvenance(t *testing.T) {
	task := tasks.FileProcessingTask{FileMD5: "file-md5", UserID: 7, OrgTag: "research", IsPublic: true}
	chunk := model.StructuredChunk{
		DocumentID:       "doc-1",
		ChunkID:          "doc-1:chunk:1",
		ParentChunkID:    "doc-1:parent:1",
		Text:             "exact source",
		EmbeddingText:    "title\nexact source",
		SectionPath:      []string{"Guide", "Intro"},
		PageID:           "doc-1:p1",
		PageSpan:         []int{1, 1},
		ElementIDs:       []string{"e1"},
		ElementTypes:     []string{"text"},
		BBoxRefs:         []string{"e1:0,0,1,1"},
		AssetRefs:        []string{"asset-1"},
		TokenCount:       2,
		TokenizerID:      "whitespace-v1",
		ParserName:       "mineru",
		ParserVersion:    "3.4.4",
		CorpusGeneration: "techdocs-2026-07-30-v1",
	}

	vector := documentVectorFromStructuredChunk(task, 3, chunk, "text-embedding-3-small")
	if vector.FileMD5 != task.FileMD5 || vector.ChunkID != 3 || vector.TextContent != chunk.EmbeddingText {
		t.Fatalf("vector identity/text = %+v", vector)
	}
	if vector.DocumentID != chunk.DocumentID || vector.PageID != chunk.PageID || vector.ParentChunkID != chunk.ParentChunkID {
		t.Fatalf("vector document provenance = %+v", vector)
	}
	if vector.TokenizerID != chunk.TokenizerID || vector.ParserName != chunk.ParserName || vector.ParserVersion != chunk.ParserVersion || vector.CorpusGeneration != chunk.CorpusGeneration {
		t.Fatalf("vector parser provenance = %+v", vector)
	}
	if vector.OrgTag != task.OrgTag || vector.UserID != task.UserID || vector.IsPublic != task.IsPublic {
		t.Fatalf("vector task provenance = %+v", vector)
	}
	if len(vector.SectionPath) != 2 || len(vector.PageSpan) != 2 || len(vector.ElementIDs) != 1 || len(vector.ElementTypes) != 1 || len(vector.BBoxRefs) != 1 || len(vector.AssetRefs) != 1 {
		t.Fatalf("vector arrays = %+v", vector)
	}
}

func TestDocumentVectorFromStructuredChunkFallsBackToSourceText(t *testing.T) {
	vector := documentVectorFromStructuredChunk(tasks.FileProcessingTask{}, 0, model.StructuredChunk{Text: "source"}, "model")
	if vector.TextContent != "source" {
		t.Fatalf("text content = %q, want source", vector.TextContent)
	}
}

func TestDocumentVectorFromStructuredChunkDoesNotTrustWorkerACLFields(t *testing.T) {
	task := tasks.FileProcessingTask{FileMD5: "task-md5", UserID: 7, OrgTag: "trusted-org", IsPublic: false}
	chunk := model.StructuredChunk{
		DocumentID: "doc-1", ChunkID: "chunk-1", Text: "body",
		FileMD5: "worker-md5", UserID: 999, OrgTag: "attacker-org", IsPublic: true,
	}
	vector := documentVectorFromStructuredChunk(task, 0, chunk, "model")
	if vector.FileMD5 != task.FileMD5 || vector.UserID != task.UserID || vector.OrgTag != task.OrgTag || vector.IsPublic != task.IsPublic {
		t.Fatalf("worker fields crossed authority boundary: %+v", vector)
	}
}

func TestStructuredArtifactUsesMineruProvenanceBeyondFileExtension(t *testing.T) {
	task := tasks.FileProcessingTask{FileName: "renamed.docx"}
	artifact := orchestratorclient.ParsedArtifact{ParserName: "mineru", Elements: []json.RawMessage{json.RawMessage(`{"type":"text"}`)}}
	if !structuredArtifact(task, artifact) {
		t.Fatal("MinerU artifact with a renamed PDF extension must remain structured")
	}
}
