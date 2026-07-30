package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStructuredChunkRejectsMissingProvenance(t *testing.T) {
	err := (StructuredChunk{ChunkID: "c1", Text: "body"}).Validate()
	if err == nil || !strings.Contains(err.Error(), "document_id") {
		t.Fatalf("expected document_id validation error, got %v", err)
	}
}

func TestStructuredChunkValidatesProvenanceAndRoundTripsArrays(t *testing.T) {
	chunk := StructuredChunk{
		DocumentID: "doc-1", ChunkID: "c1", Text: "body", EmbeddingText: "title body",
		PageID: "doc-1:p1", ElementIDs: []string{"e1"}, ElementTypes: []string{"text"},
		PageSpan: []int{1, 1}, BBoxRefs: []string{"p1:e1"}, AssetRefs: []string{"asset://e1"},
		TokenCount: 10, TokenizerID: "bge-m3", ParserName: "mineru", ParserVersion: "3.4.4",
		CorpusGeneration: "techdocs-2026-07-30-v1",
	}
	if err := chunk.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	payload, err := json.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}
	var decoded StructuredChunk
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.ElementIDs) != 1 || decoded.ElementIDs[0] != "e1" || len(decoded.PageSpan) != 2 {
		t.Fatalf("array fields did not round trip: %+v", decoded)
	}
}
