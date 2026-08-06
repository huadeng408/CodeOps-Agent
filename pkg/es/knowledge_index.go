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

// KnowledgeIndexManager owns the versioned text index lifecycle: physical
// index creation, alias reads, atomic alias switch, and inverse rollback.
// It holds its own client and base URL explicitly — no package globals — so
// tests can run isolated httptest servers in parallel.
type KnowledgeIndexManager struct {
	client  *elasticsearch.Client
	baseURL string
}

// NewKnowledgeIndexManager creates a manager bound to a specific ES client
// and base URL. The base URL must be the raw ES root (e.g. http://127.0.0.1:9200).
func NewKnowledgeIndexManager(client *elasticsearch.Client, baseURL string) *KnowledgeIndexManager {
	return &KnowledgeIndexManager{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// KnowledgeV2Mapping returns the mapping for the text-only structured corpus.
// Provenance and ACL fields are keyword/long/boolean; the vector is a native
// dense_vector with exactly `dimensions` dims. No visual vectors belong here —
// the visual pilot uses a physically separate index (design spec §4.1/§4.2).
func KnowledgeV2Mapping(dimensions int) map[string]any {
	return map[string]any{"mappings": map[string]any{"properties": map[string]any{
		"vector_id": map[string]any{"type": "keyword"}, "file_md5": map[string]any{"type": "keyword"},
		"chunk_id": map[string]any{"type": "integer"}, "text_content": map[string]any{"type": "text"},
		"embedding_text": map[string]any{"type": "text"}, "vector": map[string]any{"type": "dense_vector", "dims": dimensions, "index": true, "similarity": "cosine"},
		"model_version": map[string]any{"type": "keyword"}, "document_id": map[string]any{"type": "keyword"},
		"source_sha256": map[string]any{"type": "keyword"}, "source_url": map[string]any{"type": "keyword"},
		"source_path": map[string]any{"type": "keyword"}, "source_commit": map[string]any{"type": "keyword"}, "source_id": map[string]any{"type": "keyword"},
		"parent_chunk_id": map[string]any{"type": "keyword"}, "section_path": map[string]any{"type": "keyword"},
		"page_id": map[string]any{"type": "keyword"}, "page_span": map[string]any{"type": "integer"},
		"element_ids": map[string]any{"type": "keyword"}, "element_types": map[string]any{"type": "keyword"},
		"bbox_refs": map[string]any{"type": "keyword"}, "asset_refs": map[string]any{"type": "keyword"},
		"token_count": map[string]any{"type": "integer"}, "tokenizer_id": map[string]any{"type": "keyword"},
		"parser_name": map[string]any{"type": "keyword"}, "parser_version": map[string]any{"type": "keyword"},
		"corpus_generation": map[string]any{"type": "keyword"}, "target_index": map[string]any{"type": "keyword"},
		"user_id": map[string]any{"type": "long"}, "org_tag": map[string]any{"type": "keyword"}, "is_public": map[string]any{"type": "boolean"},
	}}}
}

// EnsurePhysicalIndex creates the physical index with the v2 mapping when it
// does not exist. When it already exists, the existing mapping is checked for
// compatibility: the vector field must be a dense_vector with exactly
// `dimensions` dims, otherwise the caller is refused (fail closed) rather than
// silently indexing into an incompatible mapping.
func (m *KnowledgeIndexManager) EnsurePhysicalIndex(ctx context.Context, index string, dimensions int) error {
	if strings.TrimSpace(index) == "" {
		return fmt.Errorf("index name is required")
	}
	if dimensions <= 0 {
		return fmt.Errorf("dimensions must be positive: %d", dimensions)
	}
	res, err := m.client.Indices.Exists([]string{index})
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode == http.StatusOK {
		return m.verifyExistingMapping(ctx, index, dimensions)
	}
	body, _ := json.Marshal(KnowledgeV2Mapping(dimensions))
	create, err := m.client.Indices.Create(index, m.client.Indices.Create.WithContext(ctx), m.client.Indices.Create.WithBody(bytes.NewReader(body)))
	if err != nil {
		return err
	}
	defer create.Body.Close()
	if create.IsError() {
		b, _ := io.ReadAll(create.Body)
		return fmt.Errorf("create knowledge index: %s", strings.TrimSpace(string(b)))
	}
	return nil
}

func (m *KnowledgeIndexManager) verifyExistingMapping(ctx context.Context, index string, dimensions int) error {
	res, err := m.client.Indices.GetMapping(m.client.Indices.GetMapping.WithContext(ctx), m.client.Indices.GetMapping.WithIndex(index))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("read mapping for %s: %s", index, strings.TrimSpace(string(b)))
	}
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode mapping for %s: %w", index, err)
	}
	doc, ok := payload[index].(map[string]any)
	if !ok {
		return fmt.Errorf("mapping response missing index %s", index)
	}
	mappings, _ := doc["mappings"].(map[string]any)
	props, _ := mappings["properties"].(map[string]any)
	vectorField, _ := props["vector"].(map[string]any)
	fieldType, _ := vectorField["type"].(string)
	if fieldType != "dense_vector" {
		return fmt.Errorf("index %s vector field is %q, want dense_vector", index, fieldType)
	}
	existingDims, ok := vectorField["dims"].(float64)
	if !ok || int(existingDims) != dimensions {
		return fmt.Errorf("index %s vector dims = %v, want %d; refusing incompatible mapping", index, vectorField["dims"], dimensions)
	}
	return nil
}

// ReadAlias returns all physical indices currently targeted by an alias,
// sorted for deterministic callers.
func (m *KnowledgeIndexManager) ReadAlias(ctx context.Context, alias string) ([]string, error) {
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
		return nil, fmt.Errorf("read alias: %s", strings.TrimSpace(string(b)))
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

// SwitchAlias atomically moves an alias from the previous targets to a new
// physical index using ONE aliases API call (remove + add in the same body).
// It never issues a separate delete request for the old index — the previous
// physical indices stay in place as a rollback source.
func (m *KnowledgeIndexManager) SwitchAlias(ctx context.Context, alias, target string, previous []string) error {
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("target index is required")
	}
	actions := make([]map[string]any, 0, len(previous)+1)
	for _, index := range previous {
		actions = append(actions, map[string]any{"remove": map[string]any{"index": index, "alias": alias}})
	}
	actions = append(actions, map[string]any{"add": map[string]any{"index": target, "alias": alias}})
	return m.performAliasActions(ctx, actions)
}

// RollbackAlias produces the exact inverse of a previous switch: it removes
// the alias from `current` targets and re-adds it to `previous`.
func (m *KnowledgeIndexManager) RollbackAlias(ctx context.Context, alias, previous string, current []string) error {
	return m.SwitchAlias(ctx, alias, previous, current)
}

func (m *KnowledgeIndexManager) performAliasActions(ctx context.Context, actions []map[string]any) error {
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
		return fmt.Errorf("alias operation failed: %s", strings.TrimSpace(string(b)))
	}
	return nil
}
