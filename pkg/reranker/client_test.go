package reranker

import (
	"testing"

	"code-agent/internal/serverconfig"
)

func TestNewClientLeavesRequestTimeoutToCallerContext(t *testing.T) {
	client, ok := NewClient(serverconfig.RerankerConfig{
		Enabled: true,
		BaseURL: "http://127.0.0.1:8008",
		Model:   "test-model",
	}).(*httpClient)
	if !ok {
		t.Fatal("expected enabled reranker to use httpClient")
	}
	if client.client.Timeout != 0 {
		t.Fatalf("http client timeout = %s, want zero so the caller context controls the deadline", client.client.Timeout)
	}
}
