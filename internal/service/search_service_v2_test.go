package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	"code-agent/internal/telemetry/genai"
	"code-agent/pkg/reranker"

	"go.opentelemetry.io/otel/attribute"
)

type recordedSearchSpan struct {
	name       string
	operation  string
	attributes []attribute.KeyValue
	ended      bool
	errors     []error
}

func (s *recordedSearchSpan) End() { s.ended = true }
func (s *recordedSearchSpan) SetAttributes(attributes ...attribute.KeyValue) {
	s.attributes = append(s.attributes, attributes...)
}
func (s *recordedSearchSpan) RecordError(err error) { s.errors = append(s.errors, err) }
func (s *recordedSearchSpan) AddEvent(string)       {}

type recordedSearchTracer struct{ spans []*recordedSearchSpan }

func (t *recordedSearchTracer) StartSpan(ctx context.Context, name, operation, _ string) (context.Context, genai.Span) {
	span := &recordedSearchSpan{name: name, operation: operation}
	t.spans = append(t.spans, span)
	return ctx, span
}
func (t *recordedSearchTracer) Shutdown(context.Context) error { return nil }

type successfulReranker struct{}

func (successfulReranker) Enabled() bool { return true }
func (successfulReranker) Rerank(_ context.Context, _ string, _ []reranker.Document, _ int) ([]reranker.Result, error) {
	return []reranker.Result{{Index: 0, Score: 0.9}}, nil
}

func TestRerankHitsCreatesAndEndsRealRerankSpan(t *testing.T) {
	tracer := &recordedSearchTracer{}
	service := &searchService{
		rerankerClient: successfulReranker{},
		retrievalCfg: normalizeRetrievalConfig(serverconfig.RetrievalConfig{
			RerankTopN: 2, RerankTimeoutMs: 1000,
		}),
		tracer: tracer,
	}

	_, applied, timedOut := service.rerankHits(context.Background(), "safe query", []retrievalHit{
		{ID: "one", Source: model.EsDocument{TextContent: "first document"}},
	}, 1)

	if !applied || timedOut {
		t.Fatalf("rerank state = applied:%t timedOut:%t, want true:false", applied, timedOut)
	}
	if len(tracer.spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(tracer.spans))
	}
	span := tracer.spans[0]
	if !strings.Contains(span.name, "rerank") || span.operation != genai.OperationRerank {
		t.Fatalf("span = name:%q operation:%q, want rerank operation", span.name, span.operation)
	}
	if !span.ended {
		t.Fatal("rerank span was not ended")
	}
}

func TestBuildPermissionFilterACLInQuery(t *testing.T) {
	filter := buildPermissionFilter(7, []string{"research", "engineering"})
	// The ACL must be a bool/should with user_id OR is_public OR org_tag.
	boolClause, ok := filter["bool"].(map[string]any)
	if !ok {
		t.Fatalf("permission filter is not a bool query: %#v", filter)
	}
	should, ok := boolClause["should"].([]any)
	if !ok || len(should) != 3 {
		t.Fatalf("expected 3 should clauses, got %#v", should)
	}
	if min, ok := boolClause["minimum_should_match"]; !ok || min != 1 {
		t.Fatalf("minimum_should_match = %v, want 1", min)
	}
	// Every clause must reference ACL fields only.
	blob, _ := json.Marshal(filter)
	for _, forbidden := range []string{"text_content", "vector", "query_vector"} {
		if strings.Contains(string(blob), forbidden) {
			t.Fatalf("permission filter must not contain %q: %s", forbidden, blob)
		}
	}
}

func TestFuseAndExpandKeepsPageDiversityWithinTopK(t *testing.T) {
	hits := []retrievalHit{
		{Source: model.EsDocument{PageID: "p1", ParentChunkID: "parent-1", ChunkID: 1}},
		{Source: model.EsDocument{PageID: "p1", ParentChunkID: "parent-1", ChunkID: 2}},
		{Source: model.EsDocument{PageID: "p2", ParentChunkID: "parent-2", ChunkID: 3}},
		{Source: model.EsDocument{PageID: "p2", ParentChunkID: "parent-2", ChunkID: 4}},
		{Source: model.EsDocument{PageID: "p3", ParentChunkID: "parent-3", ChunkID: 5}},
	}
	result := fuseAndExpand(hits, 3)
	if len(result) != 3 {
		t.Fatalf("result = %d, want 3 (topK cap)", len(result))
	}
	if result[0].Source.PageID != "p1" || result[1].Source.PageID != "p2" || result[2].Source.PageID != "p3" {
		t.Fatalf("expected one hit per page: %#v", result)
	}
}

func TestFuseAndExpandKeepsParentDiversityWhenPageMissing(t *testing.T) {
	hits := []retrievalHit{
		{Source: model.EsDocument{ParentChunkID: "parent-1", ChunkID: 1}},
		{Source: model.EsDocument{ParentChunkID: "parent-1", ChunkID: 2}},
		{Source: model.EsDocument{ParentChunkID: "parent-2", ChunkID: 3}},
	}
	result := fuseAndExpand(hits, 5)
	if len(result) != 2 || result[0].Source.ParentChunkID != "parent-1" || result[1].Source.ParentChunkID != "parent-2" {
		t.Fatalf("expected parent diversity: %#v", result)
	}
}

func TestFuseAndExpandDoesNotDropDifferentPages(t *testing.T) {
	hits := []retrievalHit{
		{Source: model.EsDocument{PageID: "p1", ParentChunkID: "parent-1", ChunkID: 1}},
		{Source: model.EsDocument{PageID: "p1", ParentChunkID: "parent-1", ChunkID: 2}},
		{Source: model.EsDocument{PageID: "p2", ParentChunkID: "parent-1", ChunkID: 3}},
	}
	// Same parent but different pages must both survive.
	result := fuseAndExpand(hits, 5)
	if len(result) != 2 || result[1].Source.PageID != "p2" {
		t.Fatalf("different pages must be kept: %#v", result)
	}
}

func TestFuseAndExpandKeepsSamePageAcrossDifferentDocuments(t *testing.T) {
	// PageIDs are document-local: two documents both on page 3 must BOTH
	// survive dedup.
	hits := []retrievalHit{
		{Source: model.EsDocument{DocumentID: "doc-a", PageID: "3", ChunkID: 1}},
		{Source: model.EsDocument{DocumentID: "doc-a", PageID: "3", ChunkID: 2}},
		{Source: model.EsDocument{DocumentID: "doc-b", PageID: "3", ChunkID: 3}},
	}
	result := fuseAndExpand(hits, 5)
	if len(result) != 2 {
		t.Fatalf("expected doc-a and doc-b both kept, got %d: %#v", len(result), result)
	}
	if result[0].Source.DocumentID != "doc-a" || result[1].Source.DocumentID != "doc-b" {
		t.Fatalf("expected one hit per document, got %#v", result)
	}
}

func TestFuseAndExpandKeepsSameParentAcrossDifferentDocuments(t *testing.T) {
	hits := []retrievalHit{
		{Source: model.EsDocument{DocumentID: "doc-a", ParentChunkID: "parent-1", ChunkID: 1}},
		{Source: model.EsDocument{DocumentID: "doc-b", ParentChunkID: "parent-1", ChunkID: 2}},
	}
	result := fuseAndExpand(hits, 5)
	if len(result) != 2 {
		t.Fatalf("same parent in different documents must both survive, got %d: %#v", len(result), result)
	}
}

func TestIsVectorDimensionMismatchError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{errors.New("all shards failed: [illegal_argument_exception] vector dimension mismatch"), true},
		{errors.New("query vector dims 1024 does not match index dims 512"), true},
		{errors.New("network timeout"), false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := isVectorDimensionMismatchError(tc.err); got != tc.want {
			t.Fatalf("isVectorDimensionMismatchError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// TestSearchFailsOnDimensionMismatchInAcceptanceMode proves the vector
// dimension mismatch is NOT silently swallowed into a BM25 fallback in
// acceptance mode: the caller sees an explicit error.
func TestSearchFailsOnDimensionMismatchInAcceptanceMode(t *testing.T) {
	esServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if strings.Contains(r.URL.Path, "_search") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"illegal_argument_exception","reason":"vector dimension mismatch"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer esServer.Close()

	svc := newTestSearchService(esServer.URL)
	results, err := svc.Search(context.Background(), SearchOptions{
		Query: "hello",
		TopK:  10,
		Mode:  model.RetrievalModeVector,
	}, &model.User{ID: 7, Username: "tester"})
	if err == nil {
		t.Fatal("expected error for vector dimension mismatch, got nil")
	}
	if len(results) != 0 {
		t.Fatalf("no results expected on dimension mismatch, got %d", len(results))
	}
}
