// Package embedding provides a client for interacting with embedding models.
package embedding

import (
	"bytes"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/log"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client defines the interface for an embedding client.
type Client interface {
	CreateEmbedding(ctx context.Context, text string) ([]float32, error)
	CreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error)
}

// openAICompatibleClient stores the state for the open ai compatible client.
type openAICompatibleClient struct {
	cfg    serverconfig.EmbeddingConfig
	client *http.Client
}

// NewClient creates a new embedding client based on the provider in the config.
func NewClient(cfg serverconfig.EmbeddingConfig) Client {
	return &openAICompatibleClient{
		cfg:    cfg,
		client: &http.Client{},
	}
}

// embeddingRequest describes the embedding request payload.
type embeddingRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

// embeddingResponse describes the embedding response payload.
type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// apiError represents an API error.
type apiError struct {
	statusCode int
	statusText string
	body       string
}

// Error handles error.
func (e *apiError) Error() string {
	if strings.TrimSpace(e.body) == "" {
		return fmt.Sprintf("embedding api returned non-200 status: %s", e.statusText)
	}
	return fmt.Sprintf("embedding api returned non-200 status: %s, body: %s", e.statusText, strings.TrimSpace(e.body))
}

// CreateEmbedding calls the OpenAI-compatible API to get the vector for a given text.
func (c *openAICompatibleClient) CreateEmbedding(ctx context.Context, text string) ([]float32, error) {
	vectors, err := c.CreateEmbeddings(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("received empty embedding from api")
	}
	return vectors[0], nil
}

// CreateEmbeddings creates embeddings.
func (c *openAICompatibleClient) CreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	log.Infof("[EmbeddingClient] create embeddings start, model=%s batch=%d", c.cfg.Model, len(texts))
	reqBody := embeddingRequest{
		Model: c.cfg.Model,
		Input: texts,
	}
	if c.cfg.Dimensions > 0 {
		reqBody.Dimensions = c.cfg.Dimensions
	}

	vectors, err := c.createEmbeddingsWithRetry(ctx, reqBody)
	if err == nil {
		return vectors, nil
	}

	// When the native-dimension contract is enforced, dropping the
	// dimensions parameter would silently produce non-native vectors.
	// Fail closed instead of retrying without dimensions.
	if reqBody.Dimensions > 0 && !c.cfg.RequireNativeDimensions && isDimensionsUnsupported(err) {
		log.Warnf("[EmbeddingClient] dimensions=%d unsupported by provider, retrying without dimensions", reqBody.Dimensions)
		reqBody.Dimensions = 0
		return c.createEmbeddingsWithRetry(ctx, reqBody)
	}

	return nil, err
}

// createEmbeddingsWithRetry creates embeddings with retry.
func (c *openAICompatibleClient) createEmbeddingsWithRetry(ctx context.Context, reqBody embeddingRequest) ([][]float32, error) {
	const maxAttempts = 3
	baseBackoff := 250 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		vectors, err := c.createEmbeddingsOnce(ctx, reqBody)
		if err == nil {
			return vectors, nil
		}
		lastErr = err

		if !isRetriableEmbeddingError(err) || attempt == maxAttempts {
			break
		}

		backoff := baseBackoff * time.Duration(1<<(attempt-1))
		log.Warnf("[EmbeddingClient] transient error (attempt=%d/%d), retry in %s: %v", attempt, maxAttempts, backoff, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}

	return nil, lastErr
}

// createEmbeddingsOnce creates embeddings once.
func (c *openAICompatibleClient) createEmbeddingsOnce(ctx context.Context, reqBody embeddingRequest) ([][]float32, error) {
	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/embeddings", bytes.NewReader(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call embedding api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		apiErr := &apiError{
			statusCode: resp.StatusCode,
			statusText: resp.Status,
			body:       string(bodyBytes),
		}
		log.Errorf("[EmbeddingClient] %v", apiErr)
		return nil, apiErr
	}

	var embeddingResp embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embeddingResp); err != nil {
		return nil, fmt.Errorf("failed to decode embedding response: %w", err)
	}

	if len(embeddingResp.Data) == 0 || len(embeddingResp.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("received empty embedding from api")
	}

	vectors := make([][]float32, 0, len(embeddingResp.Data))
	for _, item := range embeddingResp.Data {
		if len(item.Embedding) == 0 {
			return nil, fmt.Errorf("received empty embedding item from api")
		}
		vectors = append(vectors, item.Embedding)
	}
	log.Infof("[EmbeddingClient] create embeddings success, count=%d dim=%d", len(vectors), len(vectors[0]))
	return vectors, nil
}

// isRetriableEmbeddingError reports whether retriable embedding error.
func isRetriableEmbeddingError(err error) bool {
	var apiErr *apiError
	if !asAPIError(err, &apiErr) {
		// network/decode errors are retriable
		return true
	}

	switch apiErr.statusCode {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// isDimensionsUnsupported reports whether dimensions unsupported.
func isDimensionsUnsupported(err error) bool {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "dimensions") && (strings.Contains(msg, "unsupported") || strings.Contains(msg, "unknown") || strings.Contains(msg, "invalid")) {
		return true
	}
	if strings.Contains(msg, "invalid_parameter") && strings.Contains(msg, "dimensions") {
		return true
	}
	if strings.Contains(msg, "requested dimensions") && strings.Contains(msg, "model outputs") {
		return true
	}
	if strings.Contains(msg, "dimension mismatch") {
		return true
	}
	return false
}

// asAPIError handles as API error.
func asAPIError(err error, out **apiError) bool {
	if err == nil {
		return false
	}
	v, ok := err.(*apiError)
	if !ok {
		return false
	}
	*out = v
	return true
}
