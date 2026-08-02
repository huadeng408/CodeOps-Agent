package es

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elastic/go-elasticsearch/v8"
)

func newVisualTestManager(t *testing.T, handler http.HandlerFunc) (*VisualIndexManager, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		handler(w, r)
	}))
	client, err := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	return NewVisualIndexManager(client, srv.URL), srv
}

func TestVisualV2MappingIsIsolatedFromText(t *testing.T) {
	mapping := VisualV2Mapping(128)
	props := mapping["mappings"].(map[string]any)["properties"].(map[string]any)
	vector, ok := props["visual_vector"].(map[string]any)
	if !ok {
		t.Fatal("visual mapping missing visual_vector")
	}
	if vector["dims"] != 128 || vector["type"] != "dense_vector" {
		t.Fatalf("unexpected visual vector mapping: %v", vector)
	}
	// The visual index must NOT contain text retrieval fields.
	for _, textField := range []string{"text_content", "embedding_text", "vector", "token_count", "parser_name"} {
		if _, exists := props[textField]; exists {
			t.Fatalf("text field %q leaked into visual mapping", textField)
		}
	}
	// And the text mapping must not contain visual fields (checked on the
	// other side, asserted here for the shared test).
	textProps := KnowledgeV2Mapping(1024)["mappings"].(map[string]any)["properties"].(map[string]any)
	if _, exists := textProps["visual_vector"]; exists {
		t.Fatal("visual_vector leaked into text mapping")
	}
}

func TestEnsureVisualIndexCreatesWhenMissing(t *testing.T) {
	var createdBodies []map[string]any
	mgr, srv := newVisualTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			createdBodies = append(createdBodies, body)
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	defer srv.Close()

	if err := mgr.EnsureVisualIndex(context.Background(), "knowledge_page_visual_pilot_v1", 128); err != nil {
		t.Fatal(err)
	}
	if len(createdBodies) != 1 {
		t.Fatalf("expected 1 create, got %d", len(createdBodies))
	}
	dims := createdBodies[0]["mappings"].(map[string]any)["properties"].(map[string]any)["visual_vector"].(map[string]any)["dims"]
	if dimsValue, ok := dims.(float64); !ok || int(dimsValue) != 128 {
		t.Fatalf("visual dims = %v, want 128", dims)
	}
}

func TestEnsureVisualIndexRefusesIncompatibleMapping(t *testing.T) {
	mgr, srv := newVisualTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/_mapping") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"kb_visual":{"mappings":{"properties":{"visual_vector":{"type":"dense_vector","dims":512}}}}}`))
			return
		}
		t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
	})
	defer srv.Close()

	err := mgr.EnsureVisualIndex(context.Background(), "kb_visual", 128)
	if err == nil || !strings.Contains(err.Error(), "refusing incompatible mapping") {
		t.Fatalf("expected incompatible visual mapping refusal, got %v", err)
	}
}

func TestSwitchVisualAliasAtomicAndNoDelete(t *testing.T) {
	var bodies []map[string]any
	var deleteCalls int
	mgr, srv := newVisualTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleteCalls++
			t.Fatal("visual alias switch must not delete")
		}
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/_aliases") {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	err := mgr.SwitchVisualAlias(context.Background(), "knowledge_page_visual_current", "knowledge_page_visual_pilot_v1", []string{"old-visual-index"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 {
		t.Fatalf("expected 1 aliases call, got %d", len(bodies))
	}
	actions := bodies[0]["actions"].([]any)
	if len(actions) != 2 {
		t.Fatalf("expected remove+add, got %d", len(actions))
	}
	if deleteCalls != 0 {
		t.Fatal("no delete allowed")
	}
}

func TestReadVisualAliasMissingReturnsNil(t *testing.T) {
	mgr, srv := newVisualTestManager(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	targets, err := mgr.ReadVisualAlias(context.Background(), "knowledge_page_visual_current")
	if err != nil {
		t.Fatal(err)
	}
	if targets != nil {
		t.Fatalf("targets = %v, want nil", targets)
	}
}

func TestVisualIndexManagerRejectsEmptyTarget(t *testing.T) {
	mgr, srv := newVisualTestManager(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("no request expected")
	})
	defer srv.Close()

	if err := mgr.SwitchVisualAlias(context.Background(), "alias", " ", nil); err == nil {
		t.Fatal("expected error for empty target")
	}
}
