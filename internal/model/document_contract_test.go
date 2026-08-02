package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenFixture loads the shared Go/Python document contract fixture.
func goldenFixture(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "tests", "fixtures", "document_contract.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatalf("parse golden fixture: %v", err)
	}
	return payload
}

func TestDocumentContractMatchesPythonGoldenFixture(t *testing.T) {
	payload := goldenFixture(t)
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var doc DocumentContract
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("go cannot parse python golden fixture: %v", err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("golden fixture fails validation: %v", err)
	}
	out, _ := json.Marshal(doc)
	var back map[string]any
	json.Unmarshal(out, &back)
	// Round trip must preserve every field exactly as the Python side emits it.
	if len(back) != len(payload) {
		t.Fatalf("field count mismatch: go=%d python=%d", len(back), len(payload))
	}
	for key, want := range payload {
		if got, ok := back[key]; !ok || fmtString(got) != fmtString(want) {
			t.Fatalf("field %q mismatch: go=%v python=%v", key, got, want)
		}
	}
}

func fmtString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestDocumentContractRejectsMissingSourceSHA256(t *testing.T) {
	doc := DocumentContract{
		DocumentID: "doc-1", SourceID: "python", SourceURI: "https://example.com/doc.rst",
		ParserName: "mineru", ParserVersion: "3.4.4", CorpusGeneration: "techdocs-2026-07-30-v1",
		// SourceSHA256 intentionally blank.
	}
	err := doc.Validate()
	if err == nil || !strings.Contains(err.Error(), "source_sha256") {
		t.Fatalf("expected source_sha256 validation error, got %v", err)
	}
}

func TestDocumentContractRejectsMissingParserVersion(t *testing.T) {
	doc := DocumentContract{
		DocumentID: "doc-1", SourceID: "python", SourceURI: "https://example.com/doc.rst",
		SourceSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ParserName: "mineru", ParserVersion: "", CorpusGeneration: "techdocs-2026-07-30-v1",
	}
	err := doc.Validate()
	if err == nil || !strings.Contains(err.Error(), "parser_version") {
		t.Fatalf("expected parser_version validation error, got %v", err)
	}
}

func TestDocumentContractValidatesAndRoundTrips(t *testing.T) {
	doc := DocumentContract{
		DocumentID: "python@0123456789abcdef0123456789abcdef01234567:Doc/library.rst",
		SourceID: "python",
		SourceURI: "https://docs.python.org/3/library/index.html",
		SourceSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Mime: "text/x-rst",
		ParserName: "mineru", ParserVersion: "3.4.4", ParserBackend: "pipeline",
		LicenseID: "PSF-2.0", CorpusGeneration: "techdocs-2026-07-30-v1",
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	payload, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DocumentContract
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SourceSHA256 != doc.SourceSHA256 || decoded.CorpusGeneration != doc.CorpusGeneration {
		t.Fatalf("round trip lost fields: %+v", decoded)
	}
}

func TestStructuredChunkRejectsMissingSourceSHA256(t *testing.T) {
	chunk := StructuredChunk{
		DocumentID: "doc-1", ChunkID: "c1", Text: "body",
		PageID: "doc-1:p1", ElementIDs: []string{"e1"}, ElementTypes: []string{"text"},
		TokenCount: 10, ParserName: "mineru", ParserVersion: "3.4.4",
		CorpusGeneration: "techdocs-2026-07-30-v1",
		// SourceSHA256 intentionally blank.
	}
	err := chunk.Validate()
	if err == nil || !strings.Contains(err.Error(), "source_sha256") {
		t.Fatalf("expected source_sha256 validation error, got %v", err)
	}
}
