package tasks

import (
	"encoding/json"
	"testing"

	"code-agent/internal/model"
)

func validTaskProvenance() *model.CorpusProvenance {
	return &model.CorpusProvenance{
		SourceID:         "go",
		SourcePath:       "doc/asm.html",
		SourceCommit:     "0123456789abcdef0123456789abcdef01234567",
		SourceSHA256:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		TargetIndex:      "knowledge_base_v2_bge_m3",
		CorpusGeneration: "techdocs-2026-07-30-v1",
	}
}

func TestFileProcessingTaskRunDocumentProvenanceRoundTrip(t *testing.T) {
	task := FileProcessingTask{
		FileMD5:    "796c9a98",
		FileName:   "asm.html",
		Stage:      StageChunk,
		RunID:      "import-1754000000000000000",
		DocumentID: "go@0123456789abcdef0123456789abcdef01234567:doc/asm.html",
		Provenance: validTaskProvenance(),
	}
	if task.RunID == "" || task.DocumentID == "" || task.Provenance == nil {
		t.Fatal("FileProcessingTask must expose RunID, DocumentID and Provenance")
	}
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["run_id"] != task.RunID {
		t.Fatalf("RunID must serialize under json key \"run_id\", got %v", decoded["run_id"])
	}
	if decoded["document_id"] != task.DocumentID {
		t.Fatalf("DocumentID must serialize under json key \"document_id\", got %v", decoded["document_id"])
	}
	prov, ok := decoded["provenance"].(map[string]any)
	if !ok {
		t.Fatalf("Provenance must serialize under json key \"provenance\" as object, got %T", decoded["provenance"])
	}
	if prov["source_id"] != "go" || prov["corpus_generation"] != "techdocs-2026-07-30-v1" {
		t.Fatalf("provenance payload lost fields: %+v", prov)
	}
}

func TestFileProcessingTaskOmitsEmptyRunDocumentProvenance(t *testing.T) {
	task := FileProcessingTask{FileMD5: "796c9a98", FileName: "asm.html", Stage: StageParse}
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"run_id", "document_id", "provenance"} {
		if _, present := decoded[key]; present {
			t.Fatalf("empty %s must be omitted from json payload", key)
		}
	}
}

func TestFileProcessingTaskProvenanceDeserializesIntoTypedField(t *testing.T) {
	task := FileProcessingTask{Provenance: validTaskProvenance()}
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded FileProcessingTask
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Provenance == nil || decoded.Provenance.SourceID != "go" {
		t.Fatalf("Provenance must deserialize into *model.CorpusProvenance, got %+v", decoded.Provenance)
	}
}
