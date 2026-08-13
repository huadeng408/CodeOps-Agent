package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-agent/internal/serverconfig"

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
