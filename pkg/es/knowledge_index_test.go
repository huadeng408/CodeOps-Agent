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

// newTestManager spins up a fake ES server and binds a manager to it.
func newTestManager(t *testing.T, handler http.HandlerFunc) (*KnowledgeIndexManager, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// go-elasticsearch product-checks the server on first contact.
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		handler(w, r)
	}))
	client, err := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	return NewKnowledgeIndexManager(client, srv.URL), srv
}

func TestKnowledgeV2MappingHasNativeVectorAndNoVisualFields(t *testing.T) {
	mapping := KnowledgeV2Mapping(1024)
	props := mapping["mappings"].(map[string]any)["properties"].(map[string]any)
	vector, ok := props["vector"].(map[string]any)
	if !ok {
		t.Fatal("mapping missing vector field")
	}
	if vector["dims"] != 1024 || vector["type"] != "dense_vector" || vector["similarity"] != "cosine" {
		t.Fatalf("unexpected vector mapping: %v", vector)
	}
	for _, field := range []string{"sheet_name", "cell_range"} {
		if props[field].(map[string]any)["type"] != "keyword" {
			t.Fatalf("spreadsheet coordinate field %q must be keyword: %v", field, props[field])
		}
	}
	// The text index must be physically separate from the visual pilot.
	for _, visualField := range []string{"image_embedding", "patch_embedding", "visual_embedding"} {
		if _, exists := props[visualField]; exists {
			t.Fatalf("visual field %q leaked into text mapping", visualField)
		}
	}
}

func TestEnsurePhysicalIndexCreatesWhenMissing(t *testing.T) {
	var createdBodies []map[string]any
	var deleteCalls int
	mgr, srv := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusNotFound) // index does not exist yet
		case http.MethodPut:
			if !strings.HasSuffix(r.URL.Path, "/knowledge_base_v2_bge_m3") {
				t.Fatalf("unexpected create path: %s", r.URL.Path)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			createdBodies = append(createdBodies, body)
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			deleteCalls++
			t.Fatal("EnsurePhysicalIndex must never delete an index")
		default:
			t.Fatalf("unexpected method %s on %s", r.Method, r.URL.Path)
		}
	})
	defer srv.Close()

	if err := mgr.EnsurePhysicalIndex(context.Background(), "knowledge_base_v2_bge_m3", 1024); err != nil {
		t.Fatal(err)
	}
	if len(createdBodies) != 1 {
		t.Fatalf("expected 1 create request, got %d", len(createdBodies))
	}
	dims := createdBodies[0]["mappings"].(map[string]any)["properties"].(map[string]any)["vector"].(map[string]any)["dims"]
	if dimsValue, ok := dims.(float64); !ok || int(dimsValue) != 1024 {
		t.Fatalf("create body dims = %v, want 1024", dims)
	}
	if deleteCalls != 0 {
		t.Fatal("no delete request may be issued during ensure")
	}
}

func TestEnsurePhysicalIndexRefusesIncompatibleExistingMapping(t *testing.T) {
	mgr, srv := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK) // exists
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/_mapping") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"knowledge_base_v2_bge_m3":{"mappings":{"properties":{"vector":{"type":"dense_vector","dims":512}}}}}`))
			return
		}
		t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
	})
	defer srv.Close()

	err := mgr.EnsurePhysicalIndex(context.Background(), "knowledge_base_v2_bge_m3", 1024)
	if err == nil || !strings.Contains(err.Error(), "refusing incompatible mapping") {
		t.Fatalf("expected incompatible mapping refusal, got %v", err)
	}
}

func TestReadAliasReturnsSortedTargets(t *testing.T) {
	mgr, srv := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/_alias/knowledge_base_current") {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"knowledge_base_v2_bge_m3":{},"knowledge_base":{}}`))
	})
	defer srv.Close()

	targets, err := mgr.ReadAlias(context.Background(), "knowledge_base_current")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0] != "knowledge_base" || targets[1] != "knowledge_base_v2_bge_m3" {
		t.Fatalf("targets = %v, want sorted [knowledge_base knowledge_base_v2_bge_m3]", targets)
	}
}

func TestSwitchAliasSingleAtomicCallRemovesAndAdds(t *testing.T) {
	var bodies []map[string]any
	var deleteCalls int
	mgr, srv := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleteCalls++
			t.Fatal("alias switch must not issue a delete request")
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

	err := mgr.SwitchAlias(context.Background(), "knowledge_base_current", "knowledge_base_v2_bge_m3", []string{"knowledge_base"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 {
		t.Fatalf("expected exactly 1 aliases call, got %d", len(bodies))
	}
	actions := bodies[0]["actions"].([]any)
	if len(actions) != 2 {
		t.Fatalf("expected remove+add actions, got %d", len(actions))
	}
	first := actions[0].(map[string]any)["remove"].(map[string]any)
	if first["index"] != "knowledge_base" || first["alias"] != "knowledge_base_current" {
		t.Fatalf("unexpected remove action: %v", first)
	}
	second := actions[1].(map[string]any)["add"].(map[string]any)
	if second["index"] != "knowledge_base_v2_bge_m3" || second["alias"] != "knowledge_base_current" {
		t.Fatalf("unexpected add action: %v", second)
	}
	if deleteCalls != 0 {
		t.Fatal("no delete request may be issued during switch")
	}
}

func TestRollbackAliasProducesInverseActions(t *testing.T) {
	var bodies []map[string]any
	mgr, srv := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	err := mgr.RollbackAlias(context.Background(), "knowledge_base_current", "knowledge_base", []string{"knowledge_base_v2_bge_m3"})
	if err != nil {
		t.Fatal(err)
	}
	actions := bodies[0]["actions"].([]any)
	first := actions[0].(map[string]any)["remove"].(map[string]any)
	if first["index"] != "knowledge_base_v2_bge_m3" {
		t.Fatalf("rollback remove action = %v, want v2 index", first)
	}
	second := actions[1].(map[string]any)["add"].(map[string]any)
	if second["index"] != "knowledge_base" {
		t.Fatalf("rollback add action = %v, want legacy index", second)
	}
}

func TestSwitchAliasRejectsEmptyTarget(t *testing.T) {
	mgr, srv := newTestManager(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("no request expected for empty target")
	})
	defer srv.Close()

	if err := mgr.SwitchAlias(context.Background(), "knowledge_base_current", " ", nil); err == nil {
		t.Fatal("expected error for empty target")
	}
}

func TestReadAliasMissingReturnsNil(t *testing.T) {
	mgr, srv := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	targets, err := mgr.ReadAlias(context.Background(), "knowledge_base_current")
	if err != nil {
		t.Fatal(err)
	}
	if targets != nil {
		t.Fatalf("targets = %v, want nil for missing alias", targets)
	}
}
