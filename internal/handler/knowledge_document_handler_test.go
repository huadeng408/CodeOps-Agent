package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"code-agent/internal/middleware"
	"code-agent/internal/model"
	"code-agent/internal/serverconfig"

	"github.com/gin-gonic/gin"
)

// recordingDocumentLister is the read-only query seam used by the status
// endpoint tests. It records the last ListDocumentsByGenerationAndStatus call
// so tests can assert zero-call validation (generation/status gates) and that
// the forwarded generation/status match the query. A non-nil err surfaces as a
// 500 with a sanitized body.
type recordingDocumentLister struct {
	listCalls []recordedListCall
	documents []model.KnowledgeDocument
	listErr   error
}

type recordedListCall struct {
	generation string
	statuses   []string
}

func (r *recordingDocumentLister) ListDocumentsByGenerationAndStatus(generation string, statuses []string) ([]model.KnowledgeDocument, error) {
	r.listCalls = append(r.listCalls, recordedListCall{
		generation: generation,
		statuses:   append([]string(nil), statuses...),
	})
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.documents, nil
}

func testDocumentCorpusConfig() serverconfig.CorpusConfig {
	return serverconfig.CorpusConfig{
		Generation: testCorpusGeneration,
		TextIndex:  testTargetIndex,
		LoaderUser: testLoaderUser,
	}
}

// sampleStatusDocuments returns two corpus documents whose lastError already
// carries a sanitized summary (the model persists summaries, never raw errors).
func sampleStatusDocuments() []model.KnowledgeDocument {
	return []model.KnowledgeDocument{
		{
			DocumentID:       defaultTestDocumentID,
			SourceID:         "go",
			SourcePath:       "doc/asm.html",
			SourceCommit:     testCommitHex,
			ContentSHA256:    "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
			Status:           model.DocumentActive,
			LastError:        "",
			TargetIndex:      testTargetIndex,
			CorpusGeneration: testCorpusGeneration,
		},
		{
			DocumentID:       "go@" + testCommitHex + ":doc/runtime.md",
			SourceID:         "go",
			SourcePath:       "doc/runtime.md",
			SourceCommit:     testCommitHex,
			ContentSHA256:    "1111111111111111111111111111111111111111111111111111111111111111",
			Status:           model.DocumentActive,
			LastError:        "chunk stage failed: source_id=go source_path=doc/runtime.md",
			TargetIndex:      testTargetIndex,
			CorpusGeneration: testCorpusGeneration,
		},
	}
}

// TestKnowledgeDocumentStatus_ReturnsDocuments is the happy path: a valid
// internal GET with the configured generation and an ACTIVE status filter
// returns 200, body.documents is an array, every item carries the projected
// provenance/status fields, and lastError is echoed verbatim (the model
// already stores a sanitized summary).
func TestKnowledgeDocumentStatus_ReturnsDocuments(t *testing.T) {
	lister := &recordingDocumentLister{documents: sampleStatusDocuments()}
	resp := serveKnowledgeDocuments(t, lister, testDocumentCorpusConfig(), "correct-secret", testCorpusGeneration, "ACTIVE")

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if len(lister.listCalls) != 1 {
		t.Fatalf("repo list calls = %d, want 1", len(lister.listCalls))
	}
	call := lister.listCalls[0]
	if call.generation != testCorpusGeneration {
		t.Errorf("forwarded generation = %q, want %q", call.generation, testCorpusGeneration)
	}
	if len(call.statuses) != 1 || call.statuses[0] != string(model.DocumentActive) {
		t.Errorf("forwarded statuses = %v, want [ACTIVE]", call.statuses)
	}

	var body struct {
		Documents []struct {
			DocumentID       string `json:"documentId"`
			SourceID         string `json:"sourceId"`
			SourcePath       string `json:"sourcePath"`
			SourceCommit     string `json:"sourceCommit"`
			ContentSHA256    string `json:"contentSha256"`
			Status           string `json:"status"`
			LastError        string `json:"lastError"`
			TargetIndex      string `json:"targetIndex"`
			CorpusGeneration string `json:"corpusGeneration"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, resp.Body.String())
	}
	if len(body.Documents) != 2 {
		t.Fatalf("documents length = %d, want 2", len(body.Documents))
	}
	first := body.Documents[0]
	if first.DocumentID != defaultTestDocumentID ||
		first.SourceID != "go" ||
		first.SourcePath != "doc/asm.html" ||
		first.SourceCommit != testCommitHex ||
		first.Status != string(model.DocumentActive) ||
		first.TargetIndex != testTargetIndex ||
		first.CorpusGeneration != testCorpusGeneration {
		t.Errorf("unexpected first document projection: %+v", first)
	}
	// lastError must be echoed verbatim (sanitized summary already stored).
	wantErr := "chunk stage failed: source_id=go source_path=doc/runtime.md"
	if body.Documents[1].LastError != wantErr {
		t.Errorf("lastError = %q, want echoed sanitized summary %q", body.Documents[1].LastError, wantErr)
	}
}

// TestKnowledgeDocumentStatus_RejectsGenerationMismatch asserts a generation
// that differs from cfg.Corpus.Generation is rejected with 400 and zero repo
// calls — a caller cannot enumerate documents of another generation.
func TestKnowledgeDocumentStatus_RejectsGenerationMismatch(t *testing.T) {
	lister := &recordingDocumentLister{documents: sampleStatusDocuments()}
	resp := serveKnowledgeDocuments(t, lister, testDocumentCorpusConfig(), "correct-secret", "techdocs-DIFFERENT-v9", "ACTIVE")

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if len(lister.listCalls) != 0 {
		t.Fatalf("repo list calls = %d, want 0 (generation gate must reject before repo)", len(lister.listCalls))
	}
}

// TestKnowledgeDocumentStatus_RejectsMissingGeneration covers an absent
// generation query parameter: 400, zero repo calls.
func TestKnowledgeDocumentStatus_RejectsMissingGeneration(t *testing.T) {
	lister := &recordingDocumentLister{documents: sampleStatusDocuments()}
	resp := serveKnowledgeDocuments(t, lister, testDocumentCorpusConfig(), "correct-secret", "", "ACTIVE")

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if len(lister.listCalls) != 0 {
		t.Fatalf("repo list calls = %d, want 0", len(lister.listCalls))
	}
}

// TestKnowledgeDocumentStatus_RejectsInvalidStatus asserts the status filter
// must be one of STAGED/ACTIVE/FAILED; an unknown value or a missing status
// yields 400 with zero repo calls.
func TestKnowledgeDocumentStatus_RejectsInvalidStatus(t *testing.T) {
	cases := []struct {
		name   string
		status string
	}{
		{name: "unknown status", status: "QUEUED"},
		{name: "lowercase active", status: "active"},
		{name: "empty status", status: ""},
		{name: "garbage", status: ";;;"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			lister := &recordingDocumentLister{documents: sampleStatusDocuments()}
			resp := serveKnowledgeDocuments(t, lister, testDocumentCorpusConfig(), "correct-secret", testCorpusGeneration, tt.status)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if len(lister.listCalls) != 0 {
				t.Fatalf("repo list calls = %d, want 0 (status whitelist must reject %q before repo)", len(lister.listCalls), tt.status)
			}
		})
	}
}

// TestKnowledgeDocumentStatus_AcceptsWhitelistStatuses is a regression that the
// three canonical statuses all reach the repo and return 200.
func TestKnowledgeDocumentStatus_AcceptsWhitelistStatuses(t *testing.T) {
	for _, status := range []string{string(model.DocumentStaged), string(model.DocumentActive), string(model.DocumentFailed)} {
		t.Run(status, func(t *testing.T) {
			lister := &recordingDocumentLister{documents: sampleStatusDocuments()}
			resp := serveKnowledgeDocuments(t, lister, testDocumentCorpusConfig(), "correct-secret", testCorpusGeneration, status)
			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
			}
			if len(lister.listCalls) != 1 || len(lister.listCalls[0].statuses) != 1 || lister.listCalls[0].statuses[0] != status {
				t.Fatalf("unexpected repo call: %+v", lister.listCalls)
			}
		})
	}
}

// TestKnowledgeDocumentStatus_AcceptsSkippedStatus asserts the SKIPPED status
// (a corpus document whose content was empty after parse and was gracefully
// skipped) is a valid query filter: 200, one repo call forwarding ["SKIPPED"],
// and the returned document carries status=SKIPPED with its sanitized summary.
func TestKnowledgeDocumentStatus_AcceptsSkippedStatus(t *testing.T) {
	lister := &recordingDocumentLister{documents: []model.KnowledgeDocument{
		{
			DocumentID:       defaultTestDocumentID,
			SourceID:         "go",
			SourcePath:       "doc/_index.md",
			SourceCommit:     testCommitHex,
			ContentSHA256:    "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
			Status:           model.DocumentSkipped,
			LastError:        "parse: empty content after parse",
			TargetIndex:      testTargetIndex,
			CorpusGeneration: testCorpusGeneration,
		},
	}}
	resp := serveKnowledgeDocuments(t, lister, testDocumentCorpusConfig(), "correct-secret", testCorpusGeneration, "SKIPPED")

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if len(lister.listCalls) != 1 || len(lister.listCalls[0].statuses) != 1 || lister.listCalls[0].statuses[0] != string(model.DocumentSkipped) {
		t.Fatalf("unexpected repo call: %+v", lister.listCalls)
	}

	var body struct {
		Documents []struct {
			DocumentID string `json:"documentId"`
			Status     string `json:"status"`
			LastError  string `json:"lastError"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, resp.Body.String())
	}
	if len(body.Documents) != 1 || body.Documents[0].Status != string(model.DocumentSkipped) {
		t.Fatalf("expected one SKIPPED document, got %+v", body.Documents)
	}
	if body.Documents[0].LastError != "parse: empty content after parse" {
		t.Fatalf("lastError = %q, want echoed sanitized summary", body.Documents[0].LastError)
	}
}

// TestKnowledgeDocumentStatus_RequiresInternalToken mirrors the ingest handler
// auth contract: the internal group is guarded by InternalAuthMiddleware, so a
// missing or wrong X-Internal-Token yields 401 with zero repo calls.
func TestKnowledgeDocumentStatus_RequiresInternalToken(t *testing.T) {
	for _, tt := range []struct {
		name       string
		token      string
		wantStatus int
		wantCalls  int
	}{
		{name: "missing token", wantStatus: http.StatusUnauthorized, wantCalls: 0},
		{name: "wrong token", token: "wrong-secret", wantStatus: http.StatusUnauthorized, wantCalls: 0},
		{name: "correct token", token: "correct-secret", wantStatus: http.StatusOK, wantCalls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lister := &recordingDocumentLister{documents: sampleStatusDocuments()}
			resp := serveKnowledgeDocuments(t, lister, testDocumentCorpusConfig(), tt.token, testCorpusGeneration, "ACTIVE")
			if resp.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, tt.wantStatus, resp.Body.String())
			}
			if len(lister.listCalls) != tt.wantCalls {
				t.Fatalf("repo list calls = %d, want %d", len(lister.listCalls), tt.wantCalls)
			}
		})
	}
}

// TestKnowledgeDocumentStatus_SanitizesRepoError asserts a repository failure
// surfaces as 500 with a fixed sanitized message and never echoes the raw
// err.Error() (GC6: no internal detail leakage).
func TestKnowledgeDocumentStatus_SanitizesRepoError(t *testing.T) {
	lister := &recordingDocumentLister{listErr: errors.New("mysql: user=root password=hunter2 query failed")}
	resp := serveKnowledgeDocuments(t, lister, testDocumentCorpusConfig(), "correct-secret", testCorpusGeneration, "ACTIVE")

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusInternalServerError, resp.Body.String())
	}
	if len(lister.listCalls) != 1 {
		t.Fatalf("repo list calls = %d, want 1 (repo must be invoked on valid request)", len(lister.listCalls))
	}
	for _, leak := range []string{"mysql", "password", "hunter2", "user=root", "query failed"} {
		if bytes.Contains(resp.Body.Bytes(), []byte(leak)) {
			t.Fatalf("response leaks internal detail %q: %s", leak, resp.Body.String())
		}
	}
}

// serveKnowledgeDocuments mounts the handler under the same InternalAuthMiddleware
// that guards the production internal group and issues a GET with the given
// query parameters. An empty generation/status omits the parameter. The shared
// secret is configured here so every case exercises the full authed route; the
// RequiresInternalToken case still varies the token header to drive 401/200.
func serveKnowledgeDocuments(t *testing.T, lister KnowledgeDocumentLister, corpus serverconfig.CorpusConfig, token, generation, status string) *httptest.ResponseRecorder {
	t.Helper()
	previousSecret := serverconfig.Conf.AI.Orchestrator.SharedSecret
	serverconfig.Conf.AI.Orchestrator.SharedSecret = "correct-secret"
	t.Cleanup(func() { serverconfig.Conf.AI.Orchestrator.SharedSecret = previousSecret })

	gin.SetMode(gin.TestMode)
	target := "/internal/orchestrator/knowledge-documents"
	q := url.Values{}
	if generation != "" {
		q.Set("generation", generation)
	}
	if status != "" {
		q.Set("status", status)
	}
	if encoded := q.Encode(); encoded != "" {
		target = target + "?" + encoded
	}
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if token != "" {
		request.Header.Set("X-Internal-Token", token)
	}
	recorder := httptest.NewRecorder()
	router := gin.New()
	group := router.Group("/internal")
	group.Use(middleware.InternalAuthMiddleware())
	h := NewKnowledgeDocumentHandler(lister, corpus)
	group.GET("/orchestrator/knowledge-documents", h.List)
	router.ServeHTTP(recorder, request)
	return recorder
}
