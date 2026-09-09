package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"code-agent/internal/serverconfig"
	"code-agent/internal/session"

	"github.com/gin-gonic/gin"
)

func TestResolveSearchTraceIndexRequiresSingleExpectedAliasTarget(t *testing.T) {
	resolve := func(_ context.Context, alias string) ([]string, error) {
		if alias != "knowledge_base_current" {
			t.Fatalf("alias = %q", alias)
		}
		return []string{"knowledge_base_v2_bge_m3"}, nil
	}

	got, err := resolveSearchTraceIndex(context.Background(), "knowledge_base_current", "knowledge_base_v2_bge_m3", resolve)
	if err != nil {
		t.Fatalf("resolveSearchTraceIndex: %v", err)
	}
	if got != "knowledge_base_v2_bge_m3" {
		t.Fatalf("physical index = %q", got)
	}
}

func TestResolveSearchTraceIndexRejectsAliasDrift(t *testing.T) {
	resolve := func(_ context.Context, _ string) ([]string, error) {
		return []string{"knowledge_base", "knowledge_base_v2_bge_m3"}, nil
	}

	_, err := resolveSearchTraceIndex(context.Background(), "knowledge_base_current", "knowledge_base_v2_bge_m3", resolve)
	if err == nil || !strings.Contains(err.Error(), "exactly one expected physical index") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveTraceIndexForStartupDegradesOutsideStrictO3(t *testing.T) {
	resolve := func(_ context.Context, _ string) ([]string, error) {
		return nil, fmt.Errorf("elasticsearch unavailable")
	}

	got, err := resolveTraceIndexForStartup(context.Background(), false, "knowledge_base_current", "knowledge_base_v2_bge_m3", resolve)
	if err != nil {
		t.Fatalf("non-strict startup must preserve ES degradation: %v", err)
	}
	if got != "" {
		t.Fatalf("non-strict startup physical pin = %q, want empty", got)
	}

	_, err = resolveTraceIndexForStartup(context.Background(), true, "knowledge_base_current", "knowledge_base_v2_bge_m3", resolve)
	if err == nil || !strings.Contains(err.Error(), "elasticsearch unavailable") {
		t.Fatalf("strict O3 startup error = %v", err)
	}
}

func TestHealthzReportsEmbeddingPreflightOK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/healthz", healthzHandler(func() string { return "ok" }))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var body struct {
		Status             string `json:"status"`
		EmbeddingPreflight string `json:"embedding_preflight"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" || body.EmbeddingPreflight != "ok" {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestHealthzReportsEmbeddingPreflightDegraded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/healthz", healthzHandler(func() string { return "degraded: embedding preflight failed" }))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body struct {
		Status             string `json:"status"`
		EmbeddingPreflight string `json:"embedding_preflight"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.EmbeddingPreflight != "degraded: embedding preflight failed" {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestHealthzReportsContinuationRuntimeProjection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	now := time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)
	router.GET("/healthz", healthzHandlerWithContinuation(func() string { return "ok" }, func() session.ContinuationSupervisorStatus {
		return session.ContinuationSupervisorStatus{Attached: true, Generation: 7, LastRecoveryAt: &now}
	}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var body struct {
		Continuation struct {
			Attached       bool       `json:"attached"\`
			Generation     uint64     `json:"generation"\`
			LastRecoveryAt *time.Time `json:"last_recovery_at"\`
		} `json:"continuation"\`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Continuation.Attached || body.Continuation.Generation != 7 || body.Continuation.LastRecoveryAt == nil {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestHealthzReportsContinuationRecoveryBackoff(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	nextRetry := time.Date(2026, 9, 9, 8, 0, 5, 0, time.UTC)
	router.GET("/healthz", healthzHandlerWithContinuation(func() string { return "ok" }, func() session.ContinuationSupervisorStatus {
		return session.ContinuationSupervisorStatus{
			Attached: false, Generation: 8, ConsecutiveFailures: 2,
			LastHealthError: "orchestrator unavailable", NextRetryAt: &nextRetry,
		}
	}))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	continuation, ok := body["continuation"].(map[string]any)
	if !ok {
		t.Fatalf("continuation projection missing: %s", recorder.Body.String())
	}
	if continuation["attached"] != false || continuation["generation"] != float64(8) || continuation["consecutive_failures"] != float64(2) || continuation["next_retry_at"] == nil {
		t.Fatalf("continuation projection = %#v", continuation)
	}
}

func TestSearchReadIndexUsesConfiguredCorpusReadAlias(t *testing.T) {
	cfg := serverconfig.Config{
		Elasticsearch: serverconfig.ElasticsearchConfig{IndexName: "knowledge_base"},
		Corpus:        serverconfig.CorpusConfig{ReadAlias: "knowledge_base_current"},
	}

	got, err := searchReadIndex(cfg)
	if err != nil {
		t.Fatalf("searchReadIndex: %v", err)
	}
	if got != "knowledge_base_current" {
		t.Fatalf("search read index = %q, want configured corpus read alias", got)
	}
	if got == cfg.Elasticsearch.IndexName {
		t.Fatal("search wiring silently selected legacy Elasticsearch index")
	}
}

func TestSearchReadIndexFailsClosedWhenCorpusReadAliasIsBlank(t *testing.T) {
	cfg := serverconfig.Config{
		Elasticsearch: serverconfig.ElasticsearchConfig{IndexName: "knowledge_base"},
		Corpus:        serverconfig.CorpusConfig{ReadAlias: "  "},
	}

	_, err := searchReadIndex(cfg)
	if err == nil {
		t.Fatal("searchReadIndex returned nil error for blank corpus read alias")
	}
	if !strings.Contains(err.Error(), "corpus.read_alias") {
		t.Fatalf("error = %q, want corpus.read_alias context", err)
	}
}

func TestCORSMiddlewareAllowsConfiguredCredentialedOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(corsMiddleware("http://localhost:3000,http://127.0.0.1:3000"))
	router.GET("/resource", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodGet, "/resource", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow credentials = %q", got)
	}
	if got := response.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("vary = %q", got)
	}
}

func TestCORSMiddlewareRejectsUnconfiguredPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(corsMiddleware("http://localhost:3000"))
	router.OPTIONS("/resource", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodOptions, "/resource", nil)
	request.Header.Set("Origin", "https://untrusted.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("untrusted origin was reflected: %q", got)
	}
}
