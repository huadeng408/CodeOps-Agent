package es

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/elastic/go-elasticsearch/v8"
)

// VisualIndexManager owns the visual pilot index lifecycle, physically
// SEPARATE from the text index (design spec §4.2): visual vectors never live
// in the text mapping and vice versa. A production alias is only created
// after the quality gates pass.
type VisualIndexManager struct {
	client  *elasticsearch.Client
	baseURL string
}

// NewVisualIndexManager creates a manager bound to an ES client/base URL.
func NewVisualIndexManager(client *elasticsearch.Client, baseURL string) *VisualIndexManager {
	return &VisualIndexManager{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// VisualV2Mapping returns the mapping for visual page/crop embeddings. The
// page_id/element_id/bbox/asset_ref fields link back to text documents; there
// is no text_content and no text vector here.
func VisualV2Mapping(dimensions int) map[string]any {
	return map[string]any{"mappings": map[string]any{"properties": map[string]any{
		"document_id":    map[string]any{"type": "keyword"},
		"page_id":        map[string]any{"type": "keyword"},
		"page_index":     map[string]any{"type": "integer"},
		"page_ref":       map[string]any{"type": "keyword"},
		"asset_ref":      map[string]any{"type": "keyword"},
		"asset_sha256":   map[string]any{"type": "keyword"},
		"source_sha256":  map[string]any{"type": "keyword"},
		"element_id":     map[string]any{"type": "keyword"},
		"element_type":   map[string]any{"type": "keyword"},
		"bbox":           map[string]any{"type": "float"},
		"bbox_scaled":    map[string]any{"type": "float"},
		"kind":           map[string]any{"type": "keyword"},
		"model":          map[string]any{"type": "keyword"},
		"model_revision": map[string]any{"type": "keyword"},
		"visual_vector":  map[string]any{"type": "dense_vector", "dims": dimensions, "index": true, "similarity": "cosine"},
	}}}
}

// EnsureVisualIndex creates the pilot physical index with the visual mapping
// when missing; refuses incompatible existing mappings (fail closed).
func (m *VisualIndexManager) EnsureVisualIndex(ctx context.Context, index string, dimensions int) error {
	if strings.TrimSpace(index) == "" {
		return fmt.Errorf("visual index name is required")
	}
	if dimensions <= 0 {
		return fmt.Errorf("visual dimensions must be positive: %d", dimensions)
	}
	res, err := m.client.Indices.Exists([]string{index})
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode == http.StatusOK {
		return m.verifyVisualMapping(ctx, index, dimensions)
	}
	body, _ := json.Marshal(VisualV2Mapping(dimensions))
	create, err := m.client.Indices.Create(index, m.client.Indices.Create.WithContext(ctx), m.client.Indices.Create.WithBody(bytes.NewReader(body)))
	if err != nil {
		return err
	}
	defer create.Body.Close()
	if create.IsError() {
		b, _ := io.ReadAll(create.Body)
		return fmt.Errorf("create visual index: %s", strings.TrimSpace(string(b)))
	}
	return nil
}

func (m *VisualIndexManager) verifyVisualMapping(ctx context.Context, index string, dimensions int) error {
	res, err := m.client.Indices.GetMapping(m.client.Indices.GetMapping.WithContext(ctx), m.client.Indices.GetMapping.WithIndex(index))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("read visual mapping for %s: %s", index, strings.TrimSpace(string(b)))
	}
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode visual mapping for %s: %w", index, err)
	}
	doc, ok := payload[index].(map[string]any)
	if !ok {
		return fmt.Errorf("visual mapping response missing index %s", index)
	}
	mappings, _ := doc["mappings"].(map[string]any)
	props, _ := mappings["properties"].(map[string]any)
	vectorField, _ := props["visual_vector"].(map[string]any)
	fieldType, _ := vectorField["type"].(string)
	if fieldType != "dense_vector" {
		return fmt.Errorf("visual index %s vector field is %q, want dense_vector", index, fieldType)
	}
	existingDims, ok := vectorField["dims"].(float64)
	if !ok || int(existingDims) != dimensions {
		return fmt.Errorf("visual index %s vector dims = %v, want %d; refusing incompatible mapping", index, vectorField["dims"], dimensions)
	}
	return nil
}

// ReadVisualAlias returns the physical indices behind a visual alias.
func (m *VisualIndexManager) ReadVisualAlias(ctx context.Context, alias string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.baseURL+"/_alias/"+alias, nil)
	if err != nil {
		return nil, err
	}
	res, err := m.client.Perform(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("read visual alias: %s", strings.TrimSpace(string(b)))
	}
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, err
	}
	indices := make([]string, 0, len(payload))
	for index := range payload {
		indices = append(indices, index)
	}
	sort.Strings(indices)
	return indices, nil
}

// SwitchVisualAlias atomically moves a visual alias (one aliases API call).
func (m *VisualIndexManager) SwitchVisualAlias(ctx context.Context, alias, target string, previous []string) error {
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("visual alias target index is required")
	}
	actions := make([]map[string]any, 0, len(previous)+1)
	for _, index := range previous {
		actions = append(actions, map[string]any{"remove": map[string]any{"index": index, "alias": alias}})
	}
	actions = append(actions, map[string]any{"add": map[string]any{"index": target, "alias": alias}})
	body, _ := json.Marshal(map[string]any{"actions": actions})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/_aliases", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := m.client.Perform(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("visual alias operation failed: %s", strings.TrimSpace(string(b)))
	}
	return nil
}
