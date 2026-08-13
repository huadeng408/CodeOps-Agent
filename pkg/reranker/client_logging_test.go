package reranker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-agent/internal/serverconfig"
)

func TestRerankDoesNotExposeNonOKResponseBody(t *testing.T) {
	const privateBody = "private reranker failure detail"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(privateBody))
	}))
	defer server.Close()

	client := NewClient(serverconfig.RerankerConfig{Enabled: true, BaseURL: server.URL, Model: "test-model"})
	_, err := client.Rerank(context.Background(), "private query", []Document{{ID: "1", Text: "private retrieved document"}}, 1)
	if err == nil {
		t.Fatal("Rerank() error = nil, want non-200 error")
	}
	if strings.Contains(err.Error(), privateBody) {
		t.Fatalf("Rerank() error exposes upstream body: %v", err)
	}
	if !strings.Contains(err.Error(), "status=502 Bad Gateway") || !strings.Contains(err.Error(), "body_bytes=") {
		t.Fatalf("Rerank() error = %q, want status and body byte diagnostics", err)
	}
}
