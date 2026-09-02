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
		ParsedText:    "body",
		DocumentID:    "doc-1",
		ParserName:    "openpyxl",
		ParserVersion: "3.1.5",
		SourceSHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Elements:      []json.RawMessage{json.RawMessage(`{"document_id":"doc-1","element_id":"e1","type":"table","parser_name":"openpyxl","parser_version":"3.1.5","source_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)},
	}
	result, err := client.Chunk(context.Background(), tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "guide.xlsx"}, artifact, 1000, 100)
	if err != nil {
		t.Fatalf("chunk: %v", err)
	}
	if request.Text != "body" {
		t.Fatalf("request text = %q, want body", request.Text)
	}
	if request.DocumentID != artifact.DocumentID || request.ParserName != artifact.ParserName || request.ParserVersion != artifact.ParserVersion || request.SourceSHA256 != artifact.SourceSHA256 {
		t.Fatalf("request top-level provenance = %+v, want artifact %+v", request, artifact)
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
			artifact := ParsedArtifact{
				ParsedText:    "body",
				DocumentID:    "doc-1",
				ParserName:    "mineru",
				ParserVersion: "3.4.4",
				SourceSHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Elements:      []json.RawMessage{json.RawMessage(`{"document_id":"doc-1","element_id":"e1","type":"text","parser_name":"mineru","parser_version":"3.4.4","source_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)},
			}
			_, err := client.Chunk(context.Background(), tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "guide.pdf"}, artifact, 1000, 100)
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("chunk error = %v, want missing %s provenance error", err, field)
			}
		})
	}
}

func TestChunkRejectsStructuredResponseNotBoundToArtifact(t *testing.T) {
	log.Init("error", "console", "")

	tests := []struct {
		name  string
		field string
		value string
	}{
		{name: "document", field: "document_id", value: "other-doc"},
		{name: "parser", field: "parser_name", value: "mineru-evil"},
		{name: "version", field: "parser_version", value: "3.4.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk := map[string]any{
				"document_id":       "doc-1",
				"source_sha256":     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				"chunk_id":          "doc-1:chunk:0",
				"text":              "body",
				"page_id":           "doc-1:p0",
				"element_ids":       []string{"doc-1:p0:e0"},
				"element_types":     []string{"text"},
				"token_count":       1,
				"parser_name":       "mineru",
				"parser_version":    "3.4.4",
				"corpus_generation": "techdocs-2026-07-30-v1",
			}
			chunk[tt.field] = tt.value
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				response := map[string]any{"code": 200, "data": map[string]any{"structuredChunks": []any{chunk}}}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Fatalf("encode response: %v", err)
				}
			}))
			defer server.Close()

			client := NewIngestionClient(serverconfig.AIOrchestratorConfig{IngestionEnabled: true, BaseURL: server.URL})
			artifact := ParsedArtifact{
				ParsedText:    "body",
				DocumentID:    "doc-1",
				ParserName:    "mineru",
				ParserVersion: "3.4.4",
				SourceSHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Elements:      []json.RawMessage{json.RawMessage(`{"document_id":"doc-1","element_id":"doc-1:p0:e0","type":"text","parser_name":"mineru","parser_version":"3.4.4","source_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)},
			}

			_, err := client.Chunk(context.Background(), tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "scan.pdf"}, artifact, 1000, 100)
			if err == nil || !strings.Contains(err.Error(), tt.name) {
				t.Fatalf("chunk error = %v, want %s binding error", err, tt.name)
			}
		})
	}
}

func TestChunkDoesNotSynthesizeMissingMineruProvenance(t *testing.T) {
	log.Init("error", "console", "")

	for _, field := range []string{"source_sha256", "parser_version"} {
		field := field
		t.Run(field, func(t *testing.T) {
			chunk := map[string]any{
				"document_id":       "doc-1",
				"source_sha256":     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				"chunk_id":          "doc-1:chunk:0",
				"text":              "body",
				"page_id":           "doc-1:p0",
				"element_ids":       []string{"doc-1:p0:e0"},
				"element_types":     []string{"text"},
				"token_count":       1,
				"parser_name":       "mineru",
				"parser_version":    "3.4.4",
				"corpus_generation": "techdocs-2026-07-30-v1",
			}
			delete(chunk, field)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				response := map[string]any{"code": 200, "data": map[string]any{"structuredChunks": []any{chunk}}}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Fatalf("encode response: %v", err)
				}
			}))
			defer server.Close()

			client := NewIngestionClient(serverconfig.AIOrchestratorConfig{IngestionEnabled: true, BaseURL: server.URL})
			artifact := ParsedArtifact{
				ParsedText:    "body",
				DocumentID:    "doc-1",
				ParserName:    "mineru",
				ParserVersion: "3.4.4",
				SourceSHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Elements:      []json.RawMessage{json.RawMessage(`{"document_id":"doc-1","element_id":"doc-1:p0:e0","type":"text","parser_name":"mineru","parser_version":"3.4.4","source_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)},
			}

			_, err := client.Chunk(context.Background(), tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "scan.pdf"}, artifact, 1000, 100)
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("chunk error = %v, want missing %s error", err, field)
			}
		})
	}
}

func TestChunkRejectsMineruElementClaimForNonMineruArtifact(t *testing.T) {
	log.Init("error", "console", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := map[string]any{"code": 200, "data": map[string]any{
			"structuredChunks": []any{map[string]any{
				"document_id":       "doc-1",
				"source_sha256":     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				"chunk_id":          "doc-1:chunk:0",
				"text":              "body",
				"page_id":           "doc-1:p0",
				"element_ids":       []string{"doc-1:p0:e0"},
				"element_types":     []string{"text"},
				"token_count":       1,
				"parser_name":       "openpyxl",
				"parser_version":    "3.1.5",
				"corpus_generation": "techdocs-2026-07-30-v1",
			}},
		}}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client := NewIngestionClient(serverconfig.AIOrchestratorConfig{IngestionEnabled: true, BaseURL: server.URL})
	artifact := ParsedArtifact{
		ParsedText:    "body",
		DocumentID:    "doc-1",
		ParserName:    "openpyxl",
		ParserVersion: "3.1.5",
		SourceSHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Elements:      []json.RawMessage{json.RawMessage(`{"document_id":"doc-1","element_id":"doc-1:p0:e0","type":"text","parser_name":"mineru","parser_version":"3.1.5","source_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)},
	}

	_, err := client.Chunk(context.Background(), tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "guide.xlsx"}, artifact, 1000, 100)
	if err == nil {
		t.Fatal("MinerU element claim was accepted for a non-MinerU artifact")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "mineru") {
		t.Fatalf("error = %v, want MinerU provenance error", err)
	}
}

func TestChunkRejectsPDFArtifactWithoutElementsBeforeWorker(t *testing.T) {
	log.Init("error", "console", "")
	workerCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		workerCalls++
		_, _ = w.Write([]byte(`{"code":200,"data":{"structuredChunks":[]},"message":"success"}`))
	}))
	defer server.Close()

	client := NewIngestionClient(serverconfig.AIOrchestratorConfig{IngestionEnabled: true, BaseURL: server.URL})
	artifact := ParsedArtifact{
		ParsedText:    "unbound PDF text",
		DocumentID:    "doc-1",
		ParserName:    "mineru",
		ParserVersion: "3.4.4",
		SourceSHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	_, err := client.Chunk(context.Background(), tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "scan.pdf"}, artifact, 1000, 100)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "element") {
		t.Fatalf("chunk error = %v, want missing element provenance error", err)
	}
	if workerCalls != 0 {
		t.Fatalf("worker calls = %d, want 0 for unbound PDF", workerCalls)
	}
}

func TestChunkRejectsStructuredMetadataWithoutElementsBeforeWorker(t *testing.T) {
	log.Init("error", "console", "")
	workerCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		workerCalls++
		_, _ = w.Write([]byte(`{"code":200,"data":{"chunks":["legacy"]},"message":"success"}`))
	}))
	defer server.Close()

	client := NewIngestionClient(serverconfig.AIOrchestratorConfig{IngestionEnabled: true, BaseURL: server.URL})
	artifact := ParsedArtifact{
		ParsedText:    "office text",
		DocumentID:    "doc-1",
		ParserName:    "openpyxl",
		ParserVersion: "3.1.5",
		SourceSHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	_, err := client.Chunk(context.Background(), tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "guide.xlsx"}, artifact, 1000, 100)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "element") {
		t.Fatalf("chunk error = %v, want missing element provenance error", err)
	}
	if workerCalls != 0 {
		t.Fatalf("worker calls = %d, want 0 for incomplete structured artifact", workerCalls)
	}
}

func TestChunkRejectsNativeArtifactElementHashMismatchBeforeWorker(t *testing.T) {
	log.Init("error", "console", "")
	workerCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		workerCalls++
		_, _ = w.Write([]byte(`{"code":200,"data":{"structuredChunks":[]},"message":"success"}`))
	}))
	defer server.Close()

	client := NewIngestionClient(serverconfig.AIOrchestratorConfig{IngestionEnabled: true, BaseURL: server.URL})
	artifact := ParsedArtifact{
		ParsedText:    "office text",
		DocumentID:    "doc-1",
		ParserName:    "openpyxl",
		ParserVersion: "3.1.5",
		SourceSHA256:  "a" + strings.Repeat("a", 63),
		Elements:      []json.RawMessage{json.RawMessage(`{"document_id":"doc-1","element_id":"doc-1:e0","type":"table","parser_name":"openpyxl","parser_version":"3.1.5","source_sha256":"` + strings.Repeat("b", 64) + `"}`)},
	}

	_, err := client.Chunk(context.Background(), tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "guide.xlsx"}, artifact, 1000, 100)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "hash") {
		t.Fatalf("chunk error = %v, want native hash provenance error", err)
	}
	if workerCalls != 0 {
		t.Fatalf("worker calls = %d, want 0 for mismatched native artifact", workerCalls)
	}
}

func TestValidateStructuredArtifactProvenanceUsesStableCorpusDocumentID(t *testing.T) {
	const documentID = "go@0123456789abcdef0123456789abcdef01234567:docs/guide.xlsx"
	artifact := ParsedArtifact{
		ParsedText:    "office text",
		DocumentID:    documentID,
		ParserName:    "openpyxl",
		ParserVersion: "3.1.5",
		SourceSHA256:  strings.Repeat("a", 64),
		Elements:      []json.RawMessage{json.RawMessage(`{"document_id":"` + documentID + `","element_id":"` + documentID + `:e0","type":"table","parser_name":"openpyxl","parser_version":"3.1.5","source_sha256":"` + strings.Repeat("a", 64) + `"}`)},
	}
	task := tasks.FileProcessingTask{
		FileMD5:    "file-md5-not-document-id",
		DocumentID: documentID,
		FileName:   "guide.xlsx",
	}

	if err := validateStructuredArtifactProvenance(task, artifact); err != nil {
		t.Fatalf("stable corpus document identity rejected: %v", err)
	}
}
