// Package reranker contains reranker client integration.
package reranker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"code-agent/internal/serverconfig"
)

// Document represents a document.
type Document struct {
	ID   string
	Text string
}

// Result stores the  result.
type Result struct {
	Index int
	Score float64
}

// Client defines the  client.
type Client interface {
	Enabled() bool
	Rerank(ctx context.Context, query string, documents []Document, topN int) ([]Result, error)
}

// noopClient stores the state for the noop client.
type noopClient struct{}

// Enabled handles enabled.
func (noopClient) Enabled() bool {
	return false
}

// Rerank handles rerank.
func (noopClient) Rerank(ctx context.Context, query string, documents []Document, topN int) ([]Result, error) {
	return nil, fmt.Errorf("reranker is disabled")
}

// httpClient stores the state for the http client.
type httpClient struct {
	cfg    serverconfig.RerankerConfig
	client *http.Client
}

// rerankRequest describes the rerank request payload.
type rerankRequest struct {
	Model     string   `json:"model,omitempty"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"`
}

// rerankResponse describes the rerank response payload.
type rerankResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
		Score          float64 `json:"score"`
	} `json:"results"`
	Data []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
		Score          float64 `json:"score"`
	} `json:"data"`
}

// NewClient creates a client.
func NewClient(cfg serverconfig.RerankerConfig) Client {
	if !cfg.Enabled {
		return noopClient{}
	}
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Model) == "" {
		return noopClient{}
	}
	return &httpClient{
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Enabled handles enabled.
func (c *httpClient) Enabled() bool {
	return true
}

// Rerank handles rerank.
func (c *httpClient) Rerank(ctx context.Context, query string, documents []Document, topN int) ([]Result, error) {
	if len(documents) == 0 {
		return []Result{}, nil
	}
	if topN <= 0 || topN > len(documents) {
		topN = len(documents)
	}

	docTexts := make([]string, 0, len(documents))
	for _, doc := range documents {
		docTexts = append(docTexts, doc.Text)
	}

	reqBody := rerankRequest{
		Model:     c.cfg.Model,
		Query:     query,
		Documents: docTexts,
		TopN:      topN,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal rerank request failed: %w", err)
	}

	baseURL := strings.TrimRight(c.cfg.BaseURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildEndpointURL(baseURL), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create rerank request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(c.cfg.APIKey); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call rerank api failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("rerank api returned status=%s body=%s", resp.Status, strings.TrimSpace(string(raw)))
	}

	var parsed rerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode rerank response failed: %w", err)
	}

	results := make([]Result, 0, topN)
	appendResult := func(index int, relevanceScore, score float64) {
		finalScore := relevanceScore
		if finalScore == 0 {
			finalScore = score
		}
		results = append(results, Result{
			Index: index,
			Score: finalScore,
		})
	}

	for _, item := range parsed.Results {
		appendResult(item.Index, item.RelevanceScore, item.Score)
	}
	for _, item := range parsed.Data {
		appendResult(item.Index, item.RelevanceScore, item.Score)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("rerank response is empty")
	}
	if len(results) > topN {
		results = results[:topN]
	}
	return results, nil
}

// buildEndpointURL builds endpoint URL.
func buildEndpointURL(baseURL string) string {
	switch {
	case strings.HasSuffix(baseURL, "/rerank"), strings.HasSuffix(baseURL, "/reranks"):
		return baseURL
	case strings.Contains(baseURL, "/compatible-api/v1"):
		return baseURL + "/reranks"
	default:
		return baseURL + "/rerank"
	}
}
