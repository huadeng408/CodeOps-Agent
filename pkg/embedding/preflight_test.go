package embedding

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-agent/internal/serverconfig"
	"code-agent/pkg/log"
)

const pinnedRevision = "BAAI/bge-m3@8f1b7f9d4c2a6e5b0d9c8f7a6b5c4d3e2f1a0b9c"

func init() {
	log.Init("error", "console", "")
}

func nativeVector(dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = float32(i%7) / 10
	}
	return v
}

// embeddingServer returns an httptest server that mirrors a local
// OpenAI-compatible embedding service.
func embeddingServer(t *testing.T, model, revision string, dim int, count int, requestRecorder *embeddingRequest) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":         "ok",
			"model":          model,
			"model_revision": revision,
			"ready":          true,
		})
	})
	mux.HandleFunc("/embeddings", func(w http.ResponseWriter, r *http.Request) {
		var req embeddingRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		*requestRecorder = req
		w.Header().Set("Content-Type", "application/json")
		items := make([]map[string]any, 0, count)
		for i := 0; i < count; i++ {
			items = append(items, map[string]any{"object": "embedding", "index": i, "embedding": nativeVector(dim)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "model": model, "data": items})
	})
	return httptest.NewServer(mux)
}

func preflightCfg(baseURL, model, revision string, expectedDim int) serverconfig.EmbeddingConfig {
	return serverconfig.EmbeddingConfig{
		Model:                   model,
		ModelRevision:           revision,
		BaseURL:                 baseURL,
		Dimensions:              expectedDim,
		ExpectedDimensions:      expectedDim,
		HealthPath:              "/health",
		RequireNativeDimensions: true,
	}
}

func TestPreflightSucceedsWithNative1024Vectors(t *testing.T) {
	var recorded embeddingRequest
	srv := embeddingServer(t, "BAAI/bge-m3", pinnedRevision, 1024, 2, &recorded)
	defer srv.Close()

	err := Preflight(context.Background(), preflightCfg(srv.URL, "BAAI/bge-m3", pinnedRevision, 1024))
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if recorded.Dimensions != 1024 {
		t.Fatalf("preflight request dimensions = %d, want 1024", recorded.Dimensions)
	}
	if len(recorded.Input) != 2 {
		t.Fatalf("preflight request inputs = %d, want 2 (bilingual sample)", len(recorded.Input))
	}
}

func TestPreflightRejectsModelMismatch(t *testing.T) {
	srv := embeddingServer(t, "BAAI/bge-small-zh-v1.5", pinnedRevision, 1024, 2, &embeddingRequest{})
	defer srv.Close()

	err := Preflight(context.Background(), preflightCfg(srv.URL, "BAAI/bge-m3", pinnedRevision, 1024))
	if err == nil || !strings.Contains(err.Error(), "model mismatch") {
		t.Fatalf("expected model mismatch error, got %v", err)
	}
}

func TestPreflightRejectsRevisionMismatch(t *testing.T) {
	srv := embeddingServer(t, "BAAI/bge-m3", "BAAI/bge-m3@deadbeef", 1024, 2, &embeddingRequest{})
	defer srv.Close()

	err := Preflight(context.Background(), preflightCfg(srv.URL, "BAAI/bge-m3", pinnedRevision, 1024))
	if err == nil || !strings.Contains(err.Error(), "revision mismatch") {
		t.Fatalf("expected revision mismatch error, got %v", err)
	}
}

func TestPreflightRejectsNonNativeDimensions(t *testing.T) {
	srv := embeddingServer(t, "BAAI/bge-m3", pinnedRevision, 512, 2, &embeddingRequest{})
	defer srv.Close()

	err := Preflight(context.Background(), preflightCfg(srv.URL, "BAAI/bge-m3", pinnedRevision, 1024))
	if err == nil || !strings.Contains(err.Error(), "expected native 1024") {
		t.Fatalf("expected native dimension error, got %v", err)
	}
}

func TestPreflightRejectsNonFiniteVector(t *testing.T) {
	// JSON cannot carry NaN/Inf into a float32 slice, so the provable path
	// is the shared contract validator itself: a buggy resize/pad path can
	// hand it Inf/NaN values directly, and it must reject them.
	vec := nativeVector(1024)
	vec[7] = float32(math.Inf(1))
	err := ValidateEmbeddingContract(pinnedRevision, 1024, vec)
	if err == nil || !strings.Contains(err.Error(), "non-finite") {
		t.Fatalf("expected non-finite error for +Inf, got %v", err)
	}
	vec[7] = float32(math.NaN())
	err = ValidateEmbeddingContract(pinnedRevision, 1024, vec)
	if err == nil || !strings.Contains(err.Error(), "non-finite") {
		t.Fatalf("expected non-finite error for NaN, got %v", err)
	}
}

func TestPreflightRejectsMissingVectorCount(t *testing.T) {
	srv := embeddingServer(t, "BAAI/bge-m3", pinnedRevision, 1024, 1, &embeddingRequest{})
	defer srv.Close()

	err := Preflight(context.Background(), preflightCfg(srv.URL, "BAAI/bge-m3", pinnedRevision, 1024))
	if err == nil || !strings.Contains(err.Error(), "1 vectors for 2 inputs") {
		t.Fatalf("expected vector count error, got %v", err)
	}
}

func TestPreflightRejectsFloatingRevision(t *testing.T) {
	err := ValidateEmbeddingContract("BAAI/bge-m3@main", 1024, nativeVector(1024))
	if err == nil || !strings.Contains(err.Error(), "immutable commit") {
		t.Fatalf("expected immutable revision error, got %v", err)
	}
}

func TestPreflightRejectsDimensionsParamDroppedWhenNativeRequired(t *testing.T) {
	// The service responds 400 "dimensions unsupported"; with
	// RequireNativeDimensions=true the client must NOT retry without
	// dimensions — the preflight must fail closed.
	var recorded embeddingRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "model": "BAAI/bge-m3", "model_revision": pinnedRevision})
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&recorded)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"requested dimensions 1024 are unsupported by model"}}`))
	}))
	defer srv.Close()

	err := Preflight(context.Background(), preflightCfg(srv.URL, "BAAI/bge-m3", pinnedRevision, 1024))
	if err == nil {
		t.Fatal("expected preflight failure when provider rejects dimensions")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected dimensions-unsupported error, got %v", err)
	}
	if recorded.Dimensions == 0 {
		t.Fatal("preflight must not silently drop the dimensions parameter")
	}
}

func TestPreflightDisabledWhenRequireNativeDimensionsFalse(t *testing.T) {
	cfg := preflightCfg("http://127.0.0.1:1", "BAAI/bge-m3", pinnedRevision, 1024)
	cfg.RequireNativeDimensions = false
	// Unreachable base URL: without the requirement the preflight is a no-op.
	if err := Preflight(context.Background(), cfg); err != nil {
		t.Fatalf("Preflight() with requirement disabled error = %v", err)
	}
}

func TestCreateEmbeddingsDoesNotRetryWithoutDimensionsWhenNativeRequired(t *testing.T) {
	// When the native-dimension contract is enforced, a provider that
	// rejects the dimensions parameter must fail closed instead of silently
	// retrying without it (which would produce non-native vectors).
	attempts := 0
	var recorded embeddingRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		_ = json.NewDecoder(r.Body).Decode(&recorded)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"requested dimensions 1024 are unsupported by model"}}`))
	}))
	defer srv.Close()

	cfg := serverconfig.EmbeddingConfig{
		Model:                   "BAAI/bge-m3",
		ModelRevision:           pinnedRevision,
		BaseURL:                 srv.URL,
		Dimensions:              1024,
		RequireNativeDimensions: true,
	}
	_, err := NewClient(cfg).CreateEmbeddings(context.Background(), []string{"text"})
	if err == nil {
		t.Fatal("expected error when provider rejects dimensions")
	}
	if attempts != 1 {
		t.Fatalf("expected exactly 1 attempt, got %d", attempts)
	}
	if recorded.Dimensions != 1024 {
		t.Fatalf("dimensions must not be dropped: request = %+v", recorded)
	}
}
