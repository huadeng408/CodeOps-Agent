package tools

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"code-agent/internal/rag"
	"code-agent/internal/telemetry/genai"

	"go.opentelemetry.io/otel/attribute"
)

// ---------------------------------------------------------------------------
// recording RAG searcher and tracer for tests
// ---------------------------------------------------------------------------

type recordingRAGSearcher struct {
	options []rag.SearchOptions
	results []rag.SearchResult
	err     error
}

func (s *recordingRAGSearcher) Search(_ context.Context, options rag.SearchOptions) ([]rag.SearchResult, error) {
	s.options = append(s.options, options)
	return s.results, s.err
}

type recordingTracer struct {
	spans []*recordingSpan
}

type recordingSpan struct {
	name       string
	operation  string
	provider   string
	attrs      []attribute.KeyValue
	errors     []error
	events     []string
	ended      bool
}

func (s *recordingSpan) End() { s.ended = true }

func (s *recordingSpan) SetAttributes(kvs ...attribute.KeyValue) {
	s.attrs = append(s.attrs, kvs...)
}

func (s *recordingSpan) RecordError(err error) { s.errors = append(s.errors, err) }

func (s *recordingSpan) AddEvent(name string) { s.events = append(s.events, name) }

func (s *recordingSpan) attrKeys() []string {
	keys := make([]string, 0, len(s.attrs))
	for _, kv := range s.attrs {
		keys = append(keys, string(kv.Key))
	}
	return keys
}

func (t *recordingTracer) StartSpan(ctx context.Context, name string, operation string, provider string) (context.Context, genai.Span) {
	span := &recordingSpan{name: name, operation: operation, provider: provider}
	t.spans = append(t.spans, span)
	return ctx, span
}

func (t *recordingTracer) Shutdown(ctx context.Context) error { return nil }

// ---------------------------------------------------------------------------
// Existing tests
// ---------------------------------------------------------------------------

func TestRAGSearchKnowledgeUsesDefaultsAndFormatsResults(t *testing.T) {
	searcher := &recordingRAGSearcher{results: []rag.SearchResult{{
		FileMD5:     "abc123",
		FileName:    "guide.pdf",
		ChunkID:     7,
		TextContent: "matched text",
		Score:       0.91,
		OrgTag:      "engineering",
	}}}
	executor := NewExecutor(t.TempDir())
	executor.SetRAGSearcher(searcher)

	result, err := executor.Execute(context.Background(), ToolRequest{
		Name:      "SearchKnowledge",
		Arguments: map[string]any{"query": "  mineru ocr  "},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 || result.Error != "" {
		t.Fatalf("result = %+v", result)
	}
	if len(searcher.options) != 1 {
		t.Fatalf("search calls = %d, want 1", len(searcher.options))
	}
	want := rag.SearchOptions{Query: "mineru ocr", TopK: 5, Mode: "hybrid", DisableRerank: false}
	if got := searcher.options[0]; got != want {
		t.Fatalf("options = %+v, want %+v", got, want)
	}
	for _, text := range []string{"guide.pdf", "7", "0.91", "matched text", "abc123", "engineering"} {
		if !strings.Contains(result.Output, text) {
			t.Errorf("output %q does not contain %q", result.Output, text)
		}
	}
}

func TestRAGSearchKnowledgePassesExplicitArguments(t *testing.T) {
	for _, topK := range []any{8, float64(9)} {
		searcher := &recordingRAGSearcher{}
		executor := NewExecutor(t.TempDir())
		executor.SetRAGSearcher(searcher)
		result, err := executor.Execute(context.Background(), ToolRequest{
			Name: "SearchKnowledge",
			Arguments: map[string]any{
				"query":          "vector databases",
				"top_k":          topK,
				"mode":           "vector",
				"disable_rerank": true,
			},
		})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("top_k=%v: result=%+v err=%v", topK, result, err)
		}
		if len(searcher.options) != 1 {
			t.Fatalf("top_k=%v: search calls = %d", topK, len(searcher.options))
		}
		wantTopK := 8
		if _, ok := topK.(float64); ok {
			wantTopK = 9
		}
		want := rag.SearchOptions{Query: "vector databases", TopK: wantTopK, Mode: "vector", DisableRerank: true}
		if got := searcher.options[0]; got != want {
			t.Fatalf("top_k=%v: options = %+v, want %+v", topK, got, want)
		}
	}
}

func TestRAGSearchKnowledgeRejectsInvalidArgumentsWithoutSearching(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
	}{
		{name: "missing query", args: map[string]any{}},
		{name: "blank query", args: map[string]any{"query": "  "}},
		{name: "zero top k", args: map[string]any{"query": "q", "top_k": 0}},
		{name: "fractional top k", args: map[string]any{"query": "q", "top_k": 1.5}},
		{name: "oversized top k", args: map[string]any{"query": "q", "top_k": 51}},
		{name: "string top k", args: map[string]any{"query": "q", "top_k": "5"}},
		{name: "unsupported mode", args: map[string]any{"query": "q", "mode": "semantic"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			searcher := &recordingRAGSearcher{}
			executor := NewExecutor(t.TempDir())
			executor.SetRAGSearcher(searcher)
			result, err := executor.Execute(context.Background(), ToolRequest{Name: "SearchKnowledge", Arguments: tt.args})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.ExitCode != 1 || result.Error == "" {
				t.Fatalf("result = %+v, want validation failure", result)
			}
			if len(searcher.options) != 0 {
				t.Fatalf("search calls = %d, want 0", len(searcher.options))
			}
		})
	}
}

func TestRAGSearchKnowledgeReportsEmptyAndTruncatesOutput(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		searcher := &recordingRAGSearcher{}
		executor := NewExecutor(t.TempDir())
		executor.SetRAGSearcher(searcher)
		result, err := executor.Execute(context.Background(), ToolRequest{Name: "SearchKnowledge", Arguments: map[string]any{"query": "none"}})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if !strings.Contains(strings.ToLower(result.Output), "no knowledge") {
			t.Fatalf("empty output = %q", result.Output)
		}
	})

	t.Run("truncated", func(t *testing.T) {
		searcher := &recordingRAGSearcher{results: []rag.SearchResult{{FileName: "large.txt", ChunkID: 1, Score: 1, TextContent: strings.Repeat("content ", 30)}}}
		executor := NewExecutor(t.TempDir())
		executor.MaxOutputBytes = 60
		executor.SetRAGSearcher(searcher)
		result, err := executor.Execute(context.Background(), ToolRequest{Name: "SearchKnowledge", Arguments: map[string]any{"query": "large"}})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if !result.Truncated || !strings.Contains(result.Output, "Output truncated") {
			t.Fatalf("result = %+v, want truncated output", result)
		}
	})
}

type countingRAGTransport struct{ calls atomic.Int32 }

func (t *countingRAGTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return nil, errors.New("unexpected dial")
}

func TestRAGSearchKnowledgeUnavailableReturnsToolFailureWithoutDial(t *testing.T) {
	t.Run("nil searcher", func(t *testing.T) {
		result, err := NewExecutor(t.TempDir()).Execute(context.Background(), ToolRequest{Name: "SearchKnowledge", Arguments: map[string]any{"query": "q"}})
		if err != nil || result.ExitCode != 1 || result.Error == "" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("disabled client", func(t *testing.T) {
		transport := &countingRAGTransport{}
		client := rag.NewClient(rag.Config{
			BaseURL:       "http://rag.invalid",
			InternalToken: "secret",
			UserID:        9,
			HTTPClient:    &http.Client{Transport: transport},
		})
		executor := NewExecutor(t.TempDir())
		executor.SetRAGSearcher(client)
		result, err := executor.Execute(context.Background(), ToolRequest{Name: "SearchKnowledge", Arguments: map[string]any{"query": "q"}})
		if err != nil || result.ExitCode != 1 || result.Error == "" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if !errors.Is(errors.New(result.Error), rag.ErrUnavailable) && !strings.Contains(result.Error, rag.ErrUnavailable.Error()) {
			t.Fatalf("error = %q, want RAG unavailable", result.Error)
		}
		if got := transport.calls.Load(); got != 0 {
			t.Fatalf("HTTP calls = %d, want 0", got)
		}
	})
}

// ---------------------------------------------------------------------------
// NEW tests — span tracing for SearchKnowledge
// ---------------------------------------------------------------------------

func TestRAGSearchKnowledgeCreatesRetrieveSpan(t *testing.T) {
	searcher := &recordingRAGSearcher{results: []rag.SearchResult{{
		FileMD5:     "abc123",
		FileName:    "test.pdf",
		ChunkID:     1,
		TextContent: "content",
		Score:       0.95,
	}}}
	tracer := &recordingTracer{}
	executor := NewExecutor(t.TempDir())
	executor.SetRAGSearcher(searcher)
	executor.SetTracer(tracer)

	result, err := executor.Execute(context.Background(), ToolRequest{
		Name:      "SearchKnowledge",
		Arguments: map[string]any{"query": "test query", "top_k": 10, "mode": "vector"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 || result.Error != "" {
		t.Fatalf("result = %+v, want success", result)
	}

	// A retrieve span was created
	if len(tracer.spans) != 1 {
		t.Fatalf("spans = %d, want exactly 1 retrieve span", len(tracer.spans))
	}
	span := tracer.spans[0]
	if span.operation != genai.OperationRetrieve {
		t.Errorf("span operation = %q, want %q", span.operation, genai.OperationRetrieve)
	}
	if span.provider != genai.SystemGenAI {
		t.Errorf("span provider = %q, want %q", span.provider, genai.SystemGenAI)
	}
	if !span.ended {
		t.Error("retrieve span was not ended")
	}
}

func TestRAGSearchKnowledgeRecordsErrorOnSearchFailure(t *testing.T) {
	searcher := &recordingRAGSearcher{err: errors.New("search down")}
	tracer := &recordingTracer{}
	executor := NewExecutor(t.TempDir())
	executor.SetRAGSearcher(searcher)
	executor.SetTracer(tracer)

	result, err := executor.Execute(context.Background(), ToolRequest{
		Name:      "SearchKnowledge",
		Arguments: map[string]any{"query": "fail"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatal("expected non-zero exit code for search failure")
	}

	if len(tracer.spans) != 1 {
		t.Fatalf("spans = %d, want exactly 1 span even on error", len(tracer.spans))
	}
	span := tracer.spans[0]
	if len(span.errors) != 1 {
		t.Errorf("span errors = %d, want 1 recorded error", len(span.errors))
	}
}

func TestRAGSearchKnowledgeNoSpanWhenNoTracer(t *testing.T) {
	searcher := &recordingRAGSearcher{results: []rag.SearchResult{{
		FileMD5: "ok", FileName: "f", ChunkID: 0, TextContent: "c", Score: 1,
	}}}
	executor := NewExecutor(t.TempDir())
	executor.SetRAGSearcher(searcher)
	// No tracer set — verify no nil dereference

	result, err := executor.Execute(context.Background(), ToolRequest{
		Name:      "SearchKnowledge",
		Arguments: map[string]any{"query": "ok"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 || result.Error != "" {
		t.Fatalf("result = %+v, want success", result)
	}
	// Should not panic — just runs without spans
}

func TestRAGSearchKnowledgeNoSpanForValidationFailure(t *testing.T) {
	searcher := &recordingRAGSearcher{}
	tracer := &recordingTracer{}
	executor := NewExecutor(t.TempDir())
	executor.SetRAGSearcher(searcher)
	executor.SetTracer(tracer)

	// Missing query → validation failure before any span is created
	result, err := executor.Execute(context.Background(), ToolRequest{
		Name:      "SearchKnowledge",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatal("expected validation failure")
	}

	// No span should be created for validation failures (fail-fast)
	if len(tracer.spans) != 0 {
		t.Errorf("spans = %d, want 0 for validation failures", len(tracer.spans))
	}
}
