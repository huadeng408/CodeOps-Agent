package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"code-agent/internal/serverconfig"
	"code-agent/pkg/log"
	"code-agent/pkg/tasks"
)

func init() {
	log.Init("error", "console", "")
}

// TestChunkClientFillsMissingProvenance proves the client-side fill logic:
// a worker response WITHOUT source_sha256/parser_version must be filled
// before validation so the v2 contract passes.
func TestChunkClientFillsMissingProvenance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"code": 200,
			"data": map[string]any{
				"chunks": []string{"body"},
				"structuredChunks": []map[string]any{
					{
						"document_id":       "doc-1",
						"chunk_id":          "c1",
						"text":              "body",
						"page_id":           "doc-1:p0",
						"element_ids":       []string{"e1"},
						"element_types":     []string{"text"},
						"token_count":       1,
						"parser_name":       "native",
						"parser_version":    "", // MISSING
						"corpus_generation": "techdocs-2026-07-30-v1",
						// source_sha256 MISSING
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewIngestionClient(serverconfig.AIOrchestratorConfig{
		IngestionEnabled: true,
		BaseURL:          server.URL,
	})
	artifact := ParsedArtifact{ParsedText: "body", Elements: []json.RawMessage{json.RawMessage(`{"type":"text"}`)}}
	result, err := client.Chunk(context.Background(), tasks.FileProcessingTask{FileName: "doc.md"}, artifact, 1000, 100)
	if err != nil {
		t.Fatalf("Chunk() should fill provenance and pass validation: %v", err)
	}
	if len(result.StructuredChunks) != 1 {
		t.Fatalf("chunks = %d", len(result.StructuredChunks))
	}
	c := result.StructuredChunks[0]
	if c.ParserVersion == "" {
		t.Fatal("ParserVersion was not filled")
	}
	if c.SourceSHA256 == "" {
		t.Fatal("SourceSHA256 was not filled")
	}
	t.Logf("filled: parser=%s sha=%s...", c.ParserVersion, c.SourceSHA256[:8])
}
