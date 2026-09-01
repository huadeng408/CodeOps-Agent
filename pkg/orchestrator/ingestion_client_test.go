package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-agent/internal/serverconfig"
	"code-agent/pkg/log"
	"code-agent/pkg/tasks"
)

func TestChunkSendsArtifactElementsAndDecodesStructuredChunks(t *testing.T) {
	log.Init("error", "console", "")

	var request chunkRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ingestion/chunk" {
			t.Fatalf("request path = %s, want /v1/ingestion/chunk", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"chunks":["legacy"],"structuredChunks":[{"document_id":"doc-1","source_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","chunk_id":"chunk-1","text":"body","embedding_text":"body context","page_id":"doc-1:p1","sheet_name":"Metrics","cell_range":"Metrics!A1:C40","element_ids":["e1"],"element_types":["table"],"token_count":1,"parser_name":"openpyxl","parser_version":"3.1.5","corpus_generation":"techdocs-2026-07-30-v1"}]},"message":"success"}`))
	}))
	defer server.Close()

	client := NewIngestionClient(serverconfig.AIOrchestratorConfig{
		IngestionEnabled: true,
		BaseURL:          server.URL,
	})
	artifact := ParsedArtifact{
		ParsedText: "body",
		Elements:   []json.RawMessage{json.RawMessage(`{"document_id":"doc-1","element_id":"e1","type":"text"}`)},
	}
	result, err := client.Chunk(context.Background(), tasks.FileProcessingTask{FileName: "guide.pdf"}, artifact, 1000, 100)
	if err != nil {
		t.Fatalf("chunk: %v", err)
	}
	if request.Text != "body" {
		t.Fatalf("request text = %q, want body", request.Text)
	}
	if len(request.Elements) != 1 || !strings.Contains(string(request.Elements[0]), `"element_id":"e1"`) {
		t.Fatalf("request elements = %s, want e1 payload", request.Elements)
	}
	if len(result.StructuredChunks) != 1 || result.StructuredChunks[0].DocumentID != "doc-1" {
		t.Fatalf("structured chunks = %+v", result.StructuredChunks)
	}
	if result.StructuredChunks[0].SheetName != "Metrics" || result.StructuredChunks[0].CellRange != "Metrics!A1:C40" {
		t.Fatalf("spreadsheet coordinates = %+v", result.StructuredChunks[0])
	}
}

func TestChunkRejectsStructuredChunkMissingRequiredProvenance(t *testing.T) {
	log.Init("error", "console", "")

	for _, field := range []string{"document_id", "page_id", "element_ids"} {
		field := field
		t.Run(field, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				chunk := map[string]any{
					"document_id":       "doc-1",
					"source_sha256":     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
					"chunk_id":          "chunk-1",
					"text":              "body",
					"page_id":           "doc-1:p1",
					"element_ids":       []string{"e1"},
					"element_types":     []string{"text"},
					"token_count":       1,
					"parser_name":       "mineru",
					"parser_version":    "3.4.4",
					"corpus_generation": "techdocs-2026-07-30-v1",
				}
				delete(chunk, field)
				response := map[string]any{"code": 200, "data": map[string]any{"structuredChunks": []any{chunk}}}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Fatalf("encode response: %v", err)
				}
			}))
			defer server.Close()

			client := NewIngestionClient(serverconfig.AIOrchestratorConfig{IngestionEnabled: true, BaseURL: server.URL})
			artifact := ParsedArtifact{ParsedText: "body", Elements: []json.RawMessage{json.RawMessage(`{"type":"text"}`)}}
			_, err := client.Chunk(context.Background(), tasks.FileProcessingTask{FileName: "guide.pdf"}, artifact, 1000, 100)
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("chunk error = %v, want missing %s provenance error", err, field)
			}
		})
	}
}
