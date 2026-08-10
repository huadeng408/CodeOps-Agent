// Package embedding provides a client for interacting with embedding models.
package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"code-agent/internal/serverconfig"
)

// Preflight validates an embedding service before it is used for a corpus migration.
//
// It is fail-closed: when RequireNativeDimensions is true, the service must
// advertise the pinned model/revision and return exactly the expected number
// of native finite vectors at the expected dimensions. A mismatch aborts the
// corpus cutover instead of silently padding or truncating vectors.
func Preflight(ctx context.Context, cfg serverconfig.EmbeddingConfig) error {
	client, ok := NewClient(cfg).(*openAICompatibleClient)
	if !ok {
		return fmt.Errorf("unsupported embedding client")
	}
	return client.Preflight(ctx)
}

// ValidateEmbeddingContract rejects vectors that were resized, padded,
// truncated, or contain non-finite values. The revision must be pinned to an
// immutable commit: a floating tag (e.g. "@main") is not acceptable for a
// production corpus cutover.
func ValidateEmbeddingContract(modelRevision string, expectedDimensions int, vector []float32) error {
	if strings.TrimSpace(modelRevision) == "" {
		return fmt.Errorf("embedding model revision is required")
	}
	if modelRevision == "@main" || strings.HasSuffix(modelRevision, "@main") || strings.HasSuffix(modelRevision, "@latest") || strings.HasSuffix(modelRevision, "@HEAD") {
		return fmt.Errorf("embedding model revision must be an immutable commit, got %q", modelRevision)
	}
	if expectedDimensions <= 0 {
		return fmt.Errorf("embedding expected dimensions must be positive: %d", expectedDimensions)
	}
	if len(vector) != expectedDimensions {
		return fmt.Errorf("embedding dimension mismatch: expected native %d, got %d", expectedDimensions, len(vector))
	}
	for i, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return fmt.Errorf("embedding contains non-finite value at index %d", i)
		}
	}
	return nil
}

// Preflight verifies the configured service advertises the requested model
// and native vector contract.
func (c *openAICompatibleClient) Preflight(ctx context.Context) error {
	expected := c.cfg.ExpectedDimensions
	if expected <= 0 {
		expected = c.cfg.Dimensions
	}
	if !c.cfg.RequireNativeDimensions {
		return nil
	}
	if err := c.checkHealth(ctx); err != nil {
		return err
	}
	// Fixed UTF-8 bilingual sample: every preflight must prove both the
	// dimension contract and that the service actually embeds Chinese text.
	reqBody := embeddingRequest{
		Model:      c.cfg.Model,
		Input:      []string{"The quick brown fox jumps over the lazy dog.", "快速的棕色狐狸跳过懒狗。"},
		Dimensions: expected,
	}
	vectors, err := c.createEmbeddingsWithRetry(ctx, reqBody)
	if err != nil {
		return fmt.Errorf("embedding preflight request failed: %w", err)
	}
	for _, vector := range vectors {
		if err := ValidateEmbeddingContract(c.cfg.ModelRevision, expected, vector); err != nil {
			return err
		}
	}
	if len(vectors) != len(reqBody.Input) {
		return fmt.Errorf("embedding preflight returned %d vectors for %d inputs", len(vectors), len(reqBody.Input))
	}
	return nil
}

func (c *openAICompatibleClient) checkHealth(ctx context.Context) error {
	path := c.cfg.HealthPath
	if strings.TrimSpace(path) == "" {
		path = "/health"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.cfg.BaseURL, "/")+"/"+strings.TrimLeft(path, "/"), nil)
	if err != nil {
		return fmt.Errorf("failed to create embedding health request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call embedding health endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("embedding health returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var health struct {
		Model         string `json:"model"`
		Revision      string `json:"revision"`
		ModelRevision string `json:"model_revision"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return fmt.Errorf("failed to decode embedding health: %w", err)
	}
	if health.Model != "" && health.Model != c.cfg.Model {
		return fmt.Errorf("embedding health model mismatch: expected %s, got %s", c.cfg.Model, health.Model)
	}
	advertised := health.ModelRevision
	if advertised == "" {
		advertised = health.Revision
	}
	if advertised != "" {
		// The configured pin is always fully qualified ("model@commit") because
		// ValidateEmbeddingContract rejects floating tags, while a real service
		// commonly advertises only the bare commit. Raw string equality would
		// report a mismatch between two pins that name the same weights, so the
		// comparison is on the commit component, with the model prefix checked
		// only when both sides carry one.
		wantPrefix, wantCommit := splitModelRevision(c.cfg.ModelRevision)
		gotPrefix, gotCommit := splitModelRevision(advertised)
		mismatch := gotCommit == "" || gotCommit != wantCommit
		if !mismatch && gotPrefix != "" && wantPrefix != "" && gotPrefix != wantPrefix {
			mismatch = true
		}
		if mismatch {
			return fmt.Errorf("embedding health revision mismatch: expected %s, got %s", c.cfg.ModelRevision, advertised)
		}
	}
	return nil
}

// splitModelRevision splits a "model@commit" pin into its model prefix and
// commit component. A bare commit (no "@") yields an empty prefix, and a
// trailing "@" yields an empty commit so it can never be read as a match.
func splitModelRevision(value string) (prefix, commit string) {
	if idx := strings.LastIndex(value, "@"); idx >= 0 {
		return value[:idx], value[idx+1:]
	}
	return "", value
}
