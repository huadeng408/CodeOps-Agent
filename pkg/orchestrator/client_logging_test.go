package orchestrator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/tasks"
)

func TestStreamResponseDoesNotExposeNonOKResponseBody(t *testing.T) {
	const privateBody = "private upstream failure detail"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(privateBody))
	}))
	defer server.Close()

	client := NewClient(serverconfig.AIOrchestratorConfig{Enabled: true, BaseURL: server.URL})
	err := client.StreamResponse(context.Background(), "private user query", &model.User{ID: 7}, nil, nil)
	if err == nil {
		t.Fatal("StreamResponse() error = nil, want non-200 error")
	}
	if strings.Contains(err.Error(), privateBody) {
		t.Fatalf("StreamResponse() error exposes upstream body: %v", err)
	}
	if !strings.Contains(err.Error(), "status=502 Bad Gateway") || !strings.Contains(err.Error(), "body_bytes=") {
		t.Fatalf("StreamResponse() error = %q, want status and body byte diagnostics", err)
	}
}

func TestMemoryClientDoesNotExposeNonOKResponseBody(t *testing.T) {
	const privateBody = "private memory worker failure detail"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(privateBody))
	}))
	defer server.Close()

	client := NewMemoryClient(serverconfig.AIOrchestratorConfig{Enabled: true, BaseURL: server.URL})
	_, err := client.ExtractLongTermMemory(context.Background(), "private question", "private answer", "private summary")
	if err == nil {
		t.Fatal("ExtractLongTermMemory() error = nil, want non-200 error")
	}
	if strings.Contains(err.Error(), privateBody) {
		t.Fatalf("ExtractLongTermMemory() error exposes upstream body: %v", err)
	}
	if !strings.Contains(err.Error(), "status=502 Bad Gateway") || !strings.Contains(err.Error(), "body_bytes=") {
		t.Fatalf("ExtractLongTermMemory() error = %q, want status and body byte diagnostics", err)
	}
}

func TestIngestionClientDoesNotExposeNonOKResponseBody(t *testing.T) {
	const privateBody = "private ingestion worker failure detail"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(privateBody))
	}))
	defer server.Close()

	client := NewIngestionClient(serverconfig.AIOrchestratorConfig{IngestionEnabled: true, BaseURL: server.URL})
	_, err := client.Embed(context.Background(), tasks.FileProcessingTask{FileName: "private.pdf"}, []string{"private document text"})
	if err == nil {
		t.Fatal("Embed() error = nil, want non-200 error")
	}
	if strings.Contains(err.Error(), privateBody) {
		t.Fatalf("Embed() error exposes upstream body: %v", err)
	}
	if !strings.Contains(err.Error(), "status=502 Bad Gateway") || !strings.Contains(err.Error(), "body_bytes=") {
		t.Fatalf("Embed() error = %q, want status and body byte diagnostics", err)
	}
}
