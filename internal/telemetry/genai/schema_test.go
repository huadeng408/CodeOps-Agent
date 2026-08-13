package genai

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

// RAGAttributeNames is the frozen list of attribute keys the Go and Python
// sides must agree on (design spec §6.2). The Python test
// tests/test_trace_schema.py asserts the identical names.
var RAGAttributeNames = []string{
	AttrCorpusGeneration,
	AttrIndexAlias,
	AttrIndexPhysical,
	AttrMappingVersion,
	AttrQueryHash,
	AttrTopN,
	AttrRetrievalMode,
	AttrRerankerApplied,
	AttrVisualPath,
	AttrDocumentHash,
	AttrDocumentLength,
}

func TestRAGAttributeNamesFrozen(t *testing.T) {
	// Guard against accidental renames: Phoenix join tests and Python
	// golden tests depend on these exact keys.
	want := []string{
		"rag.corpus_generation",
		"rag.index_alias",
		"rag.index_physical",
		"rag.mapping_version",
		"rag.query_hash",
		"rag.top_n",
		"rag.retrieval_mode",
		"rag.reranker_applied",
		"rag.visual_path",
		"rag.document_hash",
		"rag.document_length",
	}
	if len(RAGAttributeNames) != len(want) {
		t.Fatalf("RAG attribute count = %d, want %d", len(RAGAttributeNames), len(want))
	}
	for i := range want {
		if RAGAttributeNames[i] != want[i] {
			t.Fatalf("RAG attribute[%d] = %q, want %q", i, RAGAttributeNames[i], want[i])
		}
	}
}

func TestQueryHashIsStableAndPrivacySafe(t *testing.T) {
	if HashQuery("") != "" {
		t.Fatal("empty query hash should be empty")
	}
	a := HashQuery("hello world")
	b := HashQuery("hello world")
	if a == "" || a != b {
		t.Fatalf("hash not stable: %q vs %q", a, b)
	}
	// The hash must NOT contain the query text.
	if len(a) > 20 {
		t.Fatalf("hash too long: %q", a)
	}
	if want := "b94d27b9934d3e08"; a != want {
		t.Fatalf("query hash = %q, want lowercase sha256 prefix %q", a, want)
	}
}

func TestDocumentAttributesAreHashAndLengthOnly(t *testing.T) {
	hash := DocumentHashKV("abc123")
	if hash.Key != AttrDocumentHash || hash.Value.AsString() != "abc123" {
		t.Fatalf("document hash attr = %+v", hash)
	}
	length := DocumentLengthKV(42)
	if length.Key != AttrDocumentLength || length.Value.AsInt64() != 42 {
		t.Fatalf("document length attr = %+v", length)
	}
}

func TestRAGConstructorKeys(t *testing.T) {
	checks := []struct {
		kv  attribute.KeyValue
		key string
	}{
		{CorpusGenerationKV("g"), AttrCorpusGeneration},
		{IndexAliasKV("a"), AttrIndexAlias},
		{IndexPhysicalKV("i"), AttrIndexPhysical},
		{MappingVersionKV("v"), AttrMappingVersion},
		{TopNKV(10), AttrTopN},
		{RetrievalModeKV("hybrid"), AttrRetrievalMode},
		{RerankerAppliedKV(true), AttrRerankerApplied},
		{VisualPathKV("disabled"), AttrVisualPath},
	}
	for _, check := range checks {
		if string(check.kv.Key) != check.key {
			t.Fatalf("constructor key = %q, want %q", check.kv.Key, check.key)
		}
	}
}
