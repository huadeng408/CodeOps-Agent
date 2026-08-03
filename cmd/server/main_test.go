package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

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
