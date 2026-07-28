// Package es contains Elasticsearch helpers.
package es

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"code-agent/internal/serverconfig"
	"code-agent/internal/model"
	"code-agent/pkg/log"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// ESClient stores the shared Elasticsearch client instance.
var ESClient *elasticsearch.Client

// InitES initializes the shared Elasticsearch client and ensures the main document index exists.
func InitES(esCfg serverconfig.ElasticsearchConfig, vectorDims int) error {
	if vectorDims <= 0 {
		vectorDims = 2048
	}

	cfg := elasticsearch.Config{
		Addresses: []string{esCfg.Addresses},
		Username:  esCfg.Username,
		Password:  esCfg.Password,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	client, err := elasticsearch.NewClient(cfg)
	if err != nil {
		return err
	}
	ESClient = client
	return createIndexIfNotExists(esCfg.IndexName, vectorDims)
}

// EnsureMemoryIndex creates the long-term memory index when memory support is enabled.
func EnsureMemoryIndex(indexName string, vectorDims int) error {
	if strings.TrimSpace(indexName) == "" {
		return nil
	}
	if vectorDims <= 0 {
		vectorDims = 2048
	}

	res, err := ESClient.Indices.Exists([]string{indexName})
	if err != nil {
		log.Errorf("failed to check memory index existence: %v", err)
		return err
	}
	defer res.Body.Close()

	if !res.IsError() && res.StatusCode == http.StatusOK {
		log.Infof("memory index '%s' already exists", indexName)
		return nil
	}
	if res.StatusCode != http.StatusNotFound {
		return fmt.Errorf("unexpected status when checking memory index existence: %d", res.StatusCode)
	}

	mapping := fmt.Sprintf(`{
		"mappings": {
			"properties": {
				"memory_id": { "type": "keyword" },
				"user_id": { "type": "long" },
				"conversation_id": { "type": "keyword" },
				"memory_type": { "type": "keyword" },
				"text_content": {
					"type": "text",
					"analyzer": "ik_max_word",
					"search_analyzer": "ik_smart"
				},
				"vector": {
					"type": "dense_vector",
					"dims": %d,
					"index": true,
					"similarity": "cosine"
				},
				"importance": { "type": "float" },
				"created_at": { "type": "date" }
			}
		}
	}`, vectorDims)

	res, err = ESClient.Indices.Create(indexName, ESClient.Indices.Create.WithBody(strings.NewReader(mapping)))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		bodyBytes, _ := io.ReadAll(res.Body)
		return fmt.Errorf("failed to create memory index: status=%s body=%s", res.Status(), strings.TrimSpace(string(bodyBytes)))
	}
	return nil
}

// createIndexIfNotExists creates the main document index if it does not already exist.
func createIndexIfNotExists(indexName string, vectorDims int) error {
	res, err := ESClient.Indices.Exists([]string{indexName})
	if err != nil {
		log.Errorf("failed to check index existence: %v", err)
		return err
	}
	defer res.Body.Close()

	if !res.IsError() && res.StatusCode == http.StatusOK {
		log.Infof("index '%s' already exists", indexName)
		dims, dimErr := GetIndexVectorDims(context.Background(), indexName, "vector")
		if dimErr == nil && dims > 0 && dims != vectorDims {
			log.Warnf("index '%s' vector dims=%d but embedding dims=%d; vector search may degrade to keyword-only fallback", indexName, dims, vectorDims)
		}
		return nil
	}
	if res.StatusCode != http.StatusNotFound {
		return fmt.Errorf("unexpected status when checking index existence: %d", res.StatusCode)
	}

	mapping := fmt.Sprintf(`{
		"mappings": {
			"properties": {
				"vector_id": { "type": "keyword" },
				"file_md5": { "type": "keyword" },
				"chunk_id": { "type": "integer" },
				"text_content": {
					"type": "text",
					"analyzer": "ik_max_word",
					"search_analyzer": "ik_smart"
				},
				"vector": {
					"type": "dense_vector",
					"dims": %d,
					"index": true,
					"similarity": "cosine"
				},
				"model_version": { "type": "keyword" },
				"user_id": { "type": "long" },
				"org_tag": { "type": "keyword" },
				"is_public": { "type": "boolean" }
			}
		}
	}`, vectorDims)

	res, err = ESClient.Indices.Create(indexName, ESClient.Indices.Create.WithBody(strings.NewReader(mapping)))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return errors.New("failed to create index")
	}
	return nil
}

// GetIndexVectorDims reads the configured dense-vector dimension from an index mapping.
func GetIndexVectorDims(ctx context.Context, indexName, fieldName string) (int, error) {
	res, err := ESClient.Indices.GetMapping(ESClient.Indices.GetMapping.WithContext(ctx), ESClient.Indices.GetMapping.WithIndex(indexName))
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return 0, fmt.Errorf("get mapping failed: status=%s body=%s", res.Status(), strings.TrimSpace(string(body)))
	}

	var mapping map[string]struct {
		Mappings struct {
			Properties map[string]struct {
				Dims any `json:"dims"`
			} `json:"properties"`
		} `json:"mappings"`
	}
	if err := json.NewDecoder(res.Body).Decode(&mapping); err != nil {
		return 0, err
	}
	idx, ok := mapping[indexName]
	if !ok {
		for _, v := range mapping {
			idx = v
			break
		}
	}
	field, ok := idx.Mappings.Properties[fieldName]
	if !ok {
		return 0, nil
	}
	switch d := field.Dims.(type) {
	case float64:
		return int(d), nil
	case int:
		return d, nil
	case json.Number:
		v, _ := d.Int64()
		return int(v), nil
	case string:
		v, convErr := strconv.Atoi(d)
		if convErr != nil {
			return 0, convErr
		}
		return v, nil
	default:
		return 0, nil
	}
}

// IndexDocument writes one knowledge document into Elasticsearch.
func IndexDocument(ctx context.Context, indexName string, doc model.EsDocument) error {
	docBytes, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	req := esapi.IndexRequest{
		Index:      indexName,
		DocumentID: doc.VectorID,
		Body:       bytes.NewReader(docBytes),
		Refresh:    "true",
	}
	res, err := req.Do(ctx, ESClient)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return errors.New("failed to index document")
	}
	return nil
}

// IndexMemoryDocument writes one memory document into Elasticsearch.
func IndexMemoryDocument(ctx context.Context, indexName string, doc model.MemoryEsDocument) error {
	docBytes, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	req := esapi.IndexRequest{
		Index:      indexName,
		DocumentID: doc.MemoryID,
		Body:       bytes.NewReader(docBytes),
		Refresh:    "true",
	}
	res, err := req.Do(ctx, ESClient)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		bodyBytes, _ := io.ReadAll(res.Body)
		return fmt.Errorf("failed to index memory document: status=%s body=%s", res.Status(), strings.TrimSpace(string(bodyBytes)))
	}
	return nil
}

// BulkIndexDocuments writes multiple knowledge documents with the Elasticsearch bulk API.
func BulkIndexDocuments(ctx context.Context, indexName string, docs []model.EsDocument) error {
	if len(docs) == 0 {
		return nil
	}

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	for _, doc := range docs {
		metaLine := fmt.Sprintf("{\"index\":{\"_index\":\"%s\",\"_id\":\"%s\"}}\n", indexName, doc.VectorID)
		if _, err := w.WriteString(metaLine); err != nil {
			return err
		}
		bodyBytes, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		if _, err := w.Write(bodyBytes); err != nil {
			return err
		}
		if _, err := w.WriteString("\n"); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}

	req := esapi.BulkRequest{Index: indexName, Body: &buf, Refresh: "true"}
	res, err := req.Do(ctx, ESClient)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		bodyBytes, _ := io.ReadAll(res.Body)
		log.Errorf("bulk index failed: status=%s body=%s", res.Status(), string(bodyBytes))
		return errors.New("failed to bulk index documents")
	}

	var bulkResp struct {
		Errors bool `json:"errors"`
	}
	if err := json.NewDecoder(res.Body).Decode(&bulkResp); err != nil {
		return err
	}
	if bulkResp.Errors {
		return errors.New("bulk index completed with partial failures")
	}
	return nil
}

// DeleteDocumentsByFileMD5 removes all indexed chunks for one uploaded file.
func DeleteDocumentsByFileMD5(ctx context.Context, indexName, fileMD5 string) error {
	if strings.TrimSpace(indexName) == "" {
		return errors.New("elasticsearch index name is empty")
	}
	if strings.TrimSpace(fileMD5) == "" {
		return errors.New("file md5 is empty")
	}

	body := map[string]any{
		"query": map[string]any{
			"term": map[string]any{
				"file_md5": fileMD5,
			},
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	refresh := true
	allowNoIndices := true
	ignoreUnavailable := true
	req := esapi.DeleteByQueryRequest{
		Index:             []string{indexName},
		Body:              bytes.NewReader(bodyBytes),
		Conflicts:         "proceed",
		Refresh:           &refresh,
		AllowNoIndices:    &allowNoIndices,
		IgnoreUnavailable: &ignoreUnavailable,
	}
	res, err := req.Do(ctx, ESClient)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		bodyBytes, _ := io.ReadAll(res.Body)
		return fmt.Errorf("delete documents by file_md5 failed: status=%s body=%s", res.Status(), strings.TrimSpace(string(bodyBytes)))
	}
	return nil
}
