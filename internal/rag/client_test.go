package rag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientSearchSendsContractAndDecodesEnvelope(t *testing.T) {
	t.Parallel()

	var received struct {
		User struct {
			ID         uint   `json:"id"`
			OrgTags    string `json:"orgTags"`
			PrimaryOrg string `json:"primaryOrg"`
		} `json:"user"`
		Query         string `json:"query"`
		TopK          int    `json:"topK"`
		Mode          string `json:"mode"`
		DisableRerank bool   `json:"disableRerank"`
		RunID         string `json:"runId"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/internal/orchestrator/knowledge-search" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Internal-Token"); got != "test-secret" {
			t.Errorf("X-Internal-Token = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":200,"data":{"results":[{"fileMd5":"abc123","fileName":"guide.pdf","chunkId":7,"textContent":"matched text","score":0.91,"userId":"42","orgTag":"engineering","isPublic":true}]},"message":"success"}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Enabled:          true,
		BaseURL:          server.URL + "/",
		InternalToken:    "test-secret",
		UserID:           42,
		OrgTag:           "engineering",
		IngestProvenance: IngestProvenanceConfig{RunID: "rag-run-20260813"},
	})
	results, err := client.Search(context.Background(), SearchOptions{
		Query:         "mineru ocr",
		TopK:          8,
		Mode:          "hybrid",
		DisableRerank: true,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if received.User.ID != 42 || received.User.OrgTags != "engineering" || received.User.PrimaryOrg != "engineering" || received.Query != "mineru ocr" || received.TopK != 8 || received.Mode != "hybrid" || !received.DisableRerank || received.RunID != "rag-run-20260813" {
		t.Fatalf("unexpected request: %+v", received)
	}
	want := []SearchResult{{
		FileMD5:     "abc123",
		FileName:    "guide.pdf",
		ChunkID:     7,
		TextContent: "matched text",
		Score:       0.91,
		UserID:      "42",
		OrgTag:      "engineering",
		IsPublic:    true,
	}}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("results = %#v, want %#v", results, want)
	}
}

func TestClientUnavailableConfigurationDoesNotDial(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected dial")
	})
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "disabled", cfg: Config{BaseURL: "http://rag.invalid", InternalToken: "secret", UserID: 9}},
		{name: "missing base URL", cfg: Config{Enabled: true, InternalToken: "secret", UserID: 9}},
		{name: "missing internal token", cfg: Config{Enabled: true, BaseURL: "http://rag.invalid", UserID: 9}},
		{name: "missing user ID", cfg: Config{Enabled: true, BaseURL: "http://rag.invalid", InternalToken: "secret"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.HTTPClient = &http.Client{Transport: transport}
			client := NewClient(tt.cfg)
			_, err := client.Search(context.Background(), SearchOptions{Query: "query", Mode: "hybrid"})
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Search error = %v, want ErrUnavailable", err)
			}
		})
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("HTTP calls = %d, want 0", got)
	}
}

func TestClientRejectsMultipleOrganizationTagsWithoutDialing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("knowledge content"), 0o600); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected dial")
	})
	client := NewClient(Config{
		Enabled:       true,
		BaseURL:       "http://rag.invalid",
		InternalToken: "secret",
		UserID:        9,
		OrgTag:        "engineering,platform",
		HTTPClient:    &http.Client{Transport: transport},
	})

	operations := []struct {
		name string
		run  func() error
	}{
		{
			name: "search",
			run: func() error {
				_, err := client.Search(context.Background(), SearchOptions{Query: "query", Mode: "hybrid"})
				return err
			},
		},
		{
			name: "ingest",
			run: func() error {
				_, err := client.Ingest(context.Background(), path)
				return err
			},
		},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.run()
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("error = %v, want ErrUnavailable", err)
			}
			if !strings.Contains(err.Error(), "rag_org_tag must contain a single organization tag") {
				t.Fatalf("error = %q", err)
			}
		})
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("HTTP calls = %d, want 0", got)
	}
}

func TestClientSearchValidatesQueryAndMode(t *testing.T) {
	t.Parallel()

	client := NewClient(Config{Enabled: true, BaseURL: "http://rag.invalid", InternalToken: "secret", UserID: 9})
	for _, tt := range []struct {
		name string
		opts SearchOptions
	}{
		{name: "blank query", opts: SearchOptions{Query: "  ", Mode: "hybrid"}},
		{name: "invalid mode", opts: SearchOptions{Query: "query", Mode: "semantic"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := client.Search(context.Background(), tt.opts); err == nil {
				t.Fatal("Search returned nil error")
			}
		})
	}
}

func TestClientSearchAcceptsSupportedModes(t *testing.T) {
	t.Parallel()

	var modes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Mode string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		modes = append(modes, body.Mode)
		_, _ = io.WriteString(w, `{"code":200,"data":{"results":[]},"message":"success"}`)
	}))
	defer server.Close()
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, InternalToken: "secret", UserID: 9})

	for _, mode := range []string{"hybrid", "bm25", "vector"} {
		if _, err := client.Search(context.Background(), SearchOptions{Query: "query", Mode: mode}); err != nil {
			t.Fatalf("Search mode %q: %v", mode, err)
		}
	}
	if !reflect.DeepEqual(modes, []string{"hybrid", "bm25", "vector"}) {
		t.Fatalf("modes = %#v", modes)
	}
}

func TestClientSearchRejectsHTTPAndEnvelopeFailures(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "non success HTTP", status: http.StatusBadGateway, body: `upstream failed`},
		{name: "malformed JSON", status: http.StatusOK, body: `{`},
		{name: "missing data", status: http.StatusOK, body: `{"code":200,"message":"success"}`},
		{name: "failure code", status: http.StatusOK, body: `{"code":500,"data":{},"message":"failed"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			client := NewClient(Config{Enabled: true, BaseURL: server.URL, InternalToken: "secret", UserID: 9})
			if _, err := client.Search(context.Background(), SearchOptions{Query: "query", Mode: "hybrid"}); err == nil {
				t.Fatal("Search returned nil error")
			}
		})
	}
}

func TestClientDoesNotFollowRedirectsOrMutateProvidedHTTPClient(t *testing.T) {
	for _, useProvidedClient := range []bool{false, true} {
		name := "default client"
		if useProvidedClient {
			name = "provided client"
		}
		t.Run(name, func(t *testing.T) {
			var targetCalls atomic.Int32
			var targetToken atomic.Value
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetCalls.Add(1)
				targetToken.Store(r.Header.Get("X-Internal-Token"))
				_, _ = io.WriteString(w, `{"code":200,"data":{"results":[]},"message":"success"}`)
			}))
			defer target.Close()

			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+searchPath, http.StatusFound)
			}))
			defer source.Close()

			cfg := Config{Enabled: true, BaseURL: source.URL, InternalToken: "redirect-secret", UserID: 9}
			var provided *http.Client
			if useProvidedClient {
				provided = &http.Client{Timeout: time.Second}
				cfg.HTTPClient = provided
			}

			client := NewClient(cfg)
			if provided != nil && provided.CheckRedirect != nil {
				t.Fatal("NewClient mutated the provided HTTP client's CheckRedirect")
			}
			_, err := client.Search(context.Background(), SearchOptions{Query: "query", Mode: "hybrid"})
			if err == nil {
				t.Error("Search followed redirect, want error")
			}
			if got := targetCalls.Load(); got != 0 {
				t.Errorf("redirect target received %d requests with X-Internal-Token %q", got, targetToken.Load())
			}
			if provided != nil && provided.CheckRedirect != nil {
				t.Fatal("Search mutated the provided HTTP client's CheckRedirect")
			}
		})
	}
}

func TestClientIngestSendsMultipartAndDecodesEnvelope(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("knowledge content"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/orchestrator/knowledge-ingest" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Internal-Token"); got != "test-secret" {
			t.Errorf("X-Internal-Token = %q", got)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		if got := r.FormValue("userId"); got != "42" {
			t.Errorf("userId = %q", got)
		}
		if got := r.FormValue("orgTag"); got != "engineering" {
			t.Errorf("orgTag = %q", got)
		}
		if got := r.FormValue("isPublic"); got != "true" {
			t.Errorf("isPublic = %q", got)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer file.Close()
		content, err := io.ReadAll(file)
		if err != nil {
			t.Fatal(err)
		}
		if header.Filename != "notes.txt" || string(content) != "knowledge content" {
			t.Errorf("file = %q %q", header.Filename, content)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"code":202,"data":{"fileMd5":"def456","fileName":"notes.txt","objectUrl":"minio://knowledge/notes.txt"},"message":"ingestion queued"}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Enabled:          true,
		BaseURL:          server.URL,
		InternalToken:    "test-secret",
		UserID:           42,
		OrgTag:           "engineering",
		IngestPublic:     true,
		IngestProvenance: testIngestProvenance(),
	})
	result, err := client.Ingest(context.Background(), path)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	want := &IngestResult{
		FileMD5:   "def456",
		FileName:  "notes.txt",
		ObjectURL: "minio://knowledge/notes.txt",
		Message:   "ingestion queued",
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
}

func TestClientIngestWithOptionsSendsAuditableProvenanceAndRawBytesSHA256(t *testing.T) {
	t.Parallel()

	content := []byte("original knowledge bytes\n")
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	wantFields := map[string]string{
		"sourceId":         "pilot-corpus",
		"sourcePath":       "fixtures/reports/report.txt",
		"sourceUrl":        "https://example.invalid/corpus/report.txt",
		"sourceCommit":     "0123456789abcdef0123456789abcdef01234567",
		"sourceSha256":     "03c7c6b5dac2c0e98da804b77290d755ae00b1ad943a5550dda0fcd4815155cd",
		"targetIndex":      "knowledge_pilot_v1",
		"corpusGeneration": "pilot-20260813",
		"runId":            "pilot-run-20260813-001",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		for field, want := range wantFields {
			if got := r.FormValue(field); got != want {
				t.Errorf("%s = %q, want %q", field, got, want)
			}
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer file.Close()
		got, err := io.ReadAll(file)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, content) {
			t.Fatalf("uploaded bytes = %q, want %q", got, content)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"code":202,"data":{"fileMd5":"abc","fileName":"report.txt"},"message":"queued"}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Enabled:       true,
		BaseURL:       server.URL,
		InternalToken: "secret",
		UserID:        42,
		IngestProvenance: IngestProvenanceConfig{
			SourceID:         "pilot-corpus",
			SourceURL:        "https://example.invalid/corpus",
			SourceCommit:     "0123456789abcdef0123456789abcdef01234567",
			TargetIndex:      "knowledge_pilot_v1",
			CorpusGeneration: "pilot-20260813",
			RunID:            "configured-run-must-be-overridden",
		},
	})
	_, err := client.IngestWithOptions(context.Background(), path, IngestOptions{
		SourcePath: "fixtures/reports/report.txt",
		SourceURL:  "https://example.invalid/corpus/report.txt",
		RunID:      "pilot-run-20260813-001",
	})
	if err != nil {
		t.Fatalf("IngestWithOptions: %v", err)
	}
}

func TestClientIngestDoesNotLeakAbsoluteLocalPath(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "private", "report.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("knowledge"), 0o600); err != nil {
		t.Fatal(err)
	}
	var sourcePath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		sourcePath = r.FormValue("sourcePath")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"code":202,"data":{"fileMd5":"abc","fileName":"report.txt"}}`)
	}))
	defer server.Close()
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, InternalToken: "secret", UserID: 1, IngestProvenance: testIngestProvenance()})
	if _, err := client.Ingest(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if sourcePath != "report.txt" {
		t.Fatalf("sourcePath = %q, want basename without local path disclosure", sourcePath)
	}
}

func TestClientPipelineStatusUsesExactRunAndFileScope(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/internal/orchestrator/pipeline-status" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("runId") != "run-20260813" || r.URL.Query().Get("fileMd5") != "0123456789abcdef0123456789abcdef" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		if r.Header.Get("X-Internal-Token") != "secret" {
			t.Fatal("missing internal token")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"runId":"run-20260813","fileMd5":"0123456789abcdef0123456789abcdef","complete":false,"stages":[{"stage":"parse","status":"SUCCESS"},{"stage":"chunk","status":"MISSING"},{"stage":"embed","status":"MISSING"},{"stage":"index","status":"MISSING"}]}`)
	}))
	defer server.Close()
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, InternalToken: "secret", UserID: 1})
	status, err := client.PipelineStatus(context.Background(), "run-20260813", "0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if status.Complete || len(status.Stages) != 4 || status.Stages[1].Status != "MISSING" {
		t.Fatalf("status = %+v", status)
	}
}

func TestWaitForPipelineCompletionPollsUntilAllStagesSucceed(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		complete := requests == 2
		status := "PROCESSING"
		if complete {
			status = "SUCCESS"
		}
		_, _ = fmt.Fprintf(w, `{"runId":"run-1","fileMd5":"0123456789abcdef0123456789abcdef","complete":%t,"stages":[{"stage":"parse","status":"SUCCESS"},{"stage":"chunk","status":"SUCCESS"},{"stage":"embed","status":"SUCCESS"},{"stage":"index","status":%q}]}`, complete, status)
	}))
	defer server.Close()
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, InternalToken: "secret", UserID: 1})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, err := client.WaitForPipelineCompletion(ctx, "run-1", "0123456789abcdef0123456789abcdef", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Complete || requests != 2 {
		t.Fatalf("status=%+v requests=%d", status, requests)
	}
}

func TestWaitForPipelineCompletionFailsImmediatelyOnFailedStage(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"runId":"run-2","fileMd5":"abcdef0123456789abcdef0123456789","complete":false,"stages":[{"stage":"parse","status":"SUCCESS"},{"stage":"chunk","status":"SUCCESS"},{"stage":"embed","status":"FAILED","lastError":"pipeline stage failed"},{"stage":"index","status":"MISSING"}]}`)
	}))
	defer server.Close()
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, InternalToken: "secret", UserID: 1})
	_, err := client.WaitForPipelineCompletion(context.Background(), "run-2", "abcdef0123456789abcdef0123456789", time.Hour)
	if err == nil || !strings.Contains(err.Error(), "embed") || !strings.Contains(err.Error(), "pipeline stage failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestClientPipelineStatusRejectsWrongScopeOrIncompleteCompletion(t *testing.T) {
	t.Parallel()
	tests := []string{
		`{"runId":"wrong","fileMd5":"0123456789abcdef0123456789abcdef","complete":true,"stages":[{"stage":"parse","status":"SUCCESS"},{"stage":"chunk","status":"SUCCESS"},{"stage":"embed","status":"SUCCESS"},{"stage":"index","status":"SUCCESS"}]}`,
		`{"runId":"run-1","fileMd5":"wrong","complete":true,"stages":[{"stage":"parse","status":"SUCCESS"},{"stage":"chunk","status":"SUCCESS"},{"stage":"embed","status":"SUCCESS"},{"stage":"index","status":"SUCCESS"}]}`,
		`{"runId":"run-1","fileMd5":"0123456789abcdef0123456789abcdef","complete":true,"stages":[{"stage":"index","status":"SUCCESS"}]}`,
		`{"runId":"run-1","fileMd5":"0123456789abcdef0123456789abcdef","complete":true,"stages":[{"stage":"parse","status":"SUCCESS"},{"stage":"chunk","status":"SUCCESS"},{"stage":"embed","status":"PROCESSING"},{"stage":"index","status":"SUCCESS"}]}`,
	}
	for _, payload := range tests {
		payload := payload
		t.Run(payload, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, payload) }))
			defer server.Close()
			client := NewClient(Config{Enabled: true, BaseURL: server.URL, InternalToken: "secret", UserID: 1})
			if _, err := client.PipelineStatus(context.Background(), "run-1", "0123456789abcdef0123456789abcdef"); err == nil {
				t.Fatal("PipelineStatus accepted invalid completion evidence")
			}
		})
	}
}

func TestConfiguredSourceURLIsAlreadyACompleteDocumentURL(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("knowledge"), 0o600); err != nil {
		t.Fatal(err)
	}
	var sourceURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		sourceURL = r.FormValue("sourceUrl")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"code":202,"data":{"fileMd5":"abc","fileName":"report.txt"}}`)
	}))
	defer server.Close()
	config := testIngestProvenance()
	config.SourceURL = "https://example.invalid/repo/blob/0123456789abcdef0123456789abcdef01234567/report.txt"
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, InternalToken: "secret", UserID: 1, IngestProvenance: config})
	if _, err := client.Ingest(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if sourceURL != config.SourceURL {
		t.Fatalf("sourceUrl = %q, want exact configured document URL %q", sourceURL, config.SourceURL)
	}
}

func TestClientIngestRejectsMalformedPinnedProvenanceWithoutDialing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("knowledge"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected dial")
	})
	config := Config{
		Enabled:          true,
		BaseURL:          "http://rag.invalid",
		InternalToken:    "secret",
		UserID:           42,
		HTTPClient:       &http.Client{Transport: transport},
		IngestProvenance: testIngestProvenance(),
	}
	config.IngestProvenance.SourceCommit = "main"

	_, err := NewClient(config).IngestWithOptions(context.Background(), path, IngestOptions{SourcePath: "reports/report.txt"})
	if err == nil || !strings.Contains(err.Error(), "sourceCommit") {
		t.Fatalf("error = %v, want sourceCommit validation failure", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("HTTP calls = %d, want zero", calls.Load())
	}
}

func TestClientIngestFileRewindsAndKeepsCallerHandleOpen(t *testing.T) {
	content := "0123456789 knowledge content"
	path := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Seek(9, io.SeekStart); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		part, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer part.Close()
		got, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		if header.Filename != "display name.txt" {
			t.Errorf("filename = %q", header.Filename)
		}
		if string(got) != content {
			t.Errorf("content = %q", got)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"code":202,"data":{"fileMd5":"abc","fileName":"display name.txt"},"message":"queued"}`)
	}))
	defer server.Close()

	client := NewClient(Config{Enabled: true, BaseURL: server.URL, InternalToken: "secret", UserID: 9, IngestProvenance: testIngestProvenance()})
	if _, err := client.IngestFile(context.Background(), file, "display name.txt"); err != nil {
		t.Fatalf("IngestFile: %v", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("caller-owned file was closed: %v", err)
	}
	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read caller-owned file: %v", err)
	}
	if string(got) != content {
		t.Fatalf("caller-owned file content = %q", got)
	}
}

func TestClientIngestFileRejectsNilAndNonRegularHandles(t *testing.T) {
	client := NewClient(Config{Enabled: true, BaseURL: "http://rag.invalid", InternalToken: "secret", UserID: 9})
	if _, err := client.IngestFile(context.Background(), nil, "file.txt"); err == nil {
		t.Fatal("IngestFile(nil) returned nil error")
	}
	directory, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if _, err := client.IngestFile(context.Background(), directory, "folder"); err == nil {
		t.Fatal("IngestFile(directory) returned nil error")
	}
}

func TestClientIngestRejectsMissingAndNonRegularFiles(t *testing.T) {
	t.Parallel()

	client := NewClient(Config{Enabled: true, BaseURL: "http://rag.invalid", InternalToken: "secret", UserID: 9})
	for _, path := range []string{filepath.Join(t.TempDir(), "missing.txt"), t.TempDir()} {
		if _, err := client.Ingest(context.Background(), path); err == nil {
			t.Fatalf("Ingest(%q) returned nil error", path)
		}
	}
}

func TestClientIngestRejectsHTTPAndEnvelopeFailures(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("knowledge content"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "non success HTTP", status: http.StatusInternalServerError, body: `failed`},
		{name: "missing data", status: http.StatusAccepted, body: `{"code":202,"message":"queued"}`},
		{name: "malformed JSON", status: http.StatusAccepted, body: `{`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			client := NewClient(Config{Enabled: true, BaseURL: server.URL, InternalToken: "secret", UserID: 9, IngestProvenance: testIngestProvenance()})
			if _, err := client.Ingest(context.Background(), path); err == nil {
				t.Fatal("Ingest returned nil error")
			}
		})
	}
}

func TestClientIngestReturnsWhenServerRespondsWithoutReadingBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.txt")
	if err := os.WriteFile(path, make([]byte, 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}

	bodySeen := make(chan io.ReadCloser, 1)
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		bodySeen <- req.Body
		return &http.Response{
			StatusCode: http.StatusRequestEntityTooLarge,
			Status:     "413 Request Entity Too Large",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("upload rejected")),
			Request:    req,
		}, nil
	})
	client := NewClient(Config{
		Enabled:          true,
		BaseURL:          "http://rag.invalid",
		InternalToken:    "secret",
		UserID:           9,
		IngestProvenance: testIngestProvenance(),
		HTTPClient:       &http.Client{Transport: transport},
	})

	done := make(chan error, 1)
	go func() {
		_, err := client.Ingest(context.Background(), path)
		done <- err
	}()
	requestBody := <-bodySeen

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Ingest returned nil error")
		}
	case <-time.After(500 * time.Millisecond):
		_ = requestBody.Close()
		err := <-done
		t.Fatalf("Ingest blocked after the server returned an early response; cleanup result: %v", err)
	}
}

func testIngestProvenance() IngestProvenanceConfig {
	return IngestProvenanceConfig{
		SourceID:         "unit-test-corpus",
		SourceCommit:     "0123456789abcdef0123456789abcdef01234567",
		TargetIndex:      "knowledge_unit_test",
		CorpusGeneration: "unit-test-generation",
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestUnavailableErrorIsRecognizable(t *testing.T) {
	t.Parallel()

	err := NewUnavailableError("rag_enabled is false")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("errors.Is(%v, ErrUnavailable) = false", err)
	}
	if !strings.Contains(err.Error(), "rag_enabled is false") {
		t.Fatalf("error = %q", err)
	}
}
