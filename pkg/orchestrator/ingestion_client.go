package orchestrator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/log"
	"code-agent/pkg/tasks"
)

// hashSHA256 returns the hex sha256 of data (source provenance for
// native-parser documents that have no MinerU payload hash).
func hashSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// IngestionClient defines the external ingestion worker client.
type IngestionClient interface {
	Enabled() bool
	Parse(ctx context.Context, task tasks.FileProcessingTask, objectURL string) (ParsedArtifact, error)
	Chunk(ctx context.Context, task tasks.FileProcessingTask, artifact ParsedArtifact, chunkSize, chunkOverlap int) (ChunkResult, error)
	Embed(ctx context.Context, task tasks.FileProcessingTask, texts []string) ([][]float32, error)
	Index(ctx context.Context, task tasks.FileProcessingTask, indexName string, docs []model.EsDocument) (int, error)
}

type noopIngestionClient struct{}

// Enabled reports whether the noop ingestion client is active.
func (noopIngestionClient) Enabled() bool { return false }

// Parse implements the disabled ingestion client behavior.
func (noopIngestionClient) Parse(ctx context.Context, task tasks.FileProcessingTask, objectURL string) (ParsedArtifact, error) {
	return ParsedArtifact{}, fmt.Errorf("external ingestion is disabled")
}

// Chunk implements the disabled ingestion client behavior.
func (noopIngestionClient) Chunk(ctx context.Context, task tasks.FileProcessingTask, artifact ParsedArtifact, chunkSize, chunkOverlap int) (ChunkResult, error) {
	return ChunkResult{}, fmt.Errorf("external ingestion is disabled")
}

// Embed implements the disabled ingestion client behavior.
func (noopIngestionClient) Embed(ctx context.Context, task tasks.FileProcessingTask, texts []string) ([][]float32, error) {
	return nil, fmt.Errorf("external ingestion is disabled")
}

// Index implements the disabled ingestion client behavior.
func (noopIngestionClient) Index(ctx context.Context, task tasks.FileProcessingTask, indexName string, docs []model.EsDocument) (int, error) {
	return 0, fmt.Errorf("external ingestion is disabled")
}

type httpIngestionClient struct {
	cfg    serverconfig.AIOrchestratorConfig
	client *http.Client
}

type parseRequest struct {
	Task      tasks.FileProcessingTask `json:"task"`
	ObjectURL string                   `json:"objectUrl"`
}

// ParsedArtifact is the structured parse contract returned by the Python worker.
type ParsedArtifact struct {
	ParsedText    string            `json:"parsedText"`
	DocumentID    string            `json:"documentId"`
	ParserName    string            `json:"parserName"`
	ParserVersion string            `json:"parserVersion"`
	SourceSHA256  string            `json:"sourceSha256"`
	Elements      []json.RawMessage `json:"elements"`
	Assets        []json.RawMessage `json:"assets"`
	RenderedPages []json.RawMessage `json:"renderedPages"`
}

type chunkRequest struct {
	Task          tasks.FileProcessingTask `json:"task"`
	Text          string                   `json:"text"`
	DocumentID    string                   `json:"documentId,omitempty"`
	ParserName    string                   `json:"parserName,omitempty"`
	ParserVersion string                   `json:"parserVersion,omitempty"`
	SourceSHA256  string                   `json:"sourceSha256,omitempty"`
	Elements      []json.RawMessage        `json:"elements,omitempty"`
	ChunkSize     int                      `json:"chunkSize"`
	ChunkOverlap  int                      `json:"chunkOverlap"`
}

// ChunkResult contains both the structured contract and the legacy text list.
// Legacy chunks remain available for non-PDF documents while structured paths
// persist the typed provenance returned by the worker.
type ChunkResult struct {
	Chunks           []string                `json:"chunks"`
	StructuredChunks []model.StructuredChunk `json:"structuredChunks"`
}

type embedRequest struct {
	Task  tasks.FileProcessingTask `json:"task"`
	Texts []string                 `json:"texts"`
}

type embedResponse struct {
	Vectors [][]float32 `json:"vectors"`
}

type indexRequest struct {
	Task      tasks.FileProcessingTask `json:"task"`
	IndexName string                   `json:"indexName"`
	Docs      []model.EsDocument       `json:"docs"`
}

type indexResponse struct {
	IndexedCount int `json:"indexedCount"`
}

// NewIngestionClient constructs the external ingestion worker client.
func NewIngestionClient(cfg serverconfig.AIOrchestratorConfig) IngestionClient {
	if !cfg.IngestionEnabled || strings.TrimSpace(cfg.BaseURL) == "" {
		return noopIngestionClient{}
	}
	timeout := time.Duration(cfg.IngestionTimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	return &httpIngestionClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Enabled reports whether the external ingestion worker is enabled.
func (c *httpIngestionClient) Enabled() bool { return true }

// Parse delegates parse-stage execution to the external ingestion worker.
func (c *httpIngestionClient) Parse(ctx context.Context, task tasks.FileProcessingTask, objectURL string) (ParsedArtifact, error) {
	resp, err := c.doJSON(ctx, "/v1/ingestion/parse", parseRequest{
		Task:      task,
		ObjectURL: objectURL,
	})
	if err != nil {
		return ParsedArtifact{}, err
	}
	var parsed ParsedArtifact
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return ParsedArtifact{}, err
	}
	return parsed, nil
}

// Chunk delegates chunk-stage execution to the external ingestion worker.
func (c *httpIngestionClient) Chunk(ctx context.Context, task tasks.FileProcessingTask, artifact ParsedArtifact, chunkSize, chunkOverlap int) (ChunkResult, error) {
	if err := validateStructuredArtifactProvenance(task, artifact); err != nil {
		return ChunkResult{}, err
	}
	resp, err := c.doJSON(ctx, "/v1/ingestion/chunk", chunkRequest{
		Task:          task,
		Text:          artifact.ParsedText,
		DocumentID:    artifact.DocumentID,
		ParserName:    artifact.ParserName,
		ParserVersion: artifact.ParserVersion,
		SourceSHA256:  artifact.SourceSHA256,
		Elements:      artifact.Elements,
		ChunkSize:     chunkSize,
		ChunkOverlap:  chunkOverlap,
	})
	if err != nil {
		return ChunkResult{}, err
	}
	var parsed ChunkResult
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return ChunkResult{}, err
	}
	if len(artifact.Elements) > 0 && len(parsed.StructuredChunks) == 0 {
		return ChunkResult{}, fmt.Errorf("structured chunk response is empty")
	}
	// Native text documents have no parser artifact metadata. Structured
	// parser responses must carry their own provenance and are never repaired
	// with synthetic native values.
	sourceHash := hashSHA256([]byte(artifact.ParsedText))
	structuredArtifact := len(artifact.Elements) > 0
	requireMinerU := strings.EqualFold(filepath.Ext(task.FileName), ".pdf") || claimsMinerUIdentity(artifact.ParserName)
	if requireMinerU && strings.TrimSpace(strings.ToLower(artifact.ParserName)) != "mineru" {
		return ChunkResult{}, fmt.Errorf("MinerU parser identity must be exact")
	}
	for index := range parsed.StructuredChunks {
		chunk := &parsed.StructuredChunks[index]
		if !structuredArtifact && strings.TrimSpace(chunk.SourceSHA256) == "" {
			chunk.SourceSHA256 = sourceHash
		}
		if !structuredArtifact && strings.TrimSpace(chunk.ParserVersion) == "" {
			chunk.ParserVersion = "native-text-v1"
		}
		if structuredArtifact {
			if strings.TrimSpace(artifact.DocumentID) != "" && chunk.DocumentID != artifact.DocumentID {
				return ChunkResult{}, fmt.Errorf("structured chunk %d document_id provenance does not match artifact", index)
			}
			if strings.TrimSpace(artifact.ParserName) != "" && !strings.EqualFold(strings.TrimSpace(chunk.ParserName), strings.TrimSpace(artifact.ParserName)) {
				return ChunkResult{}, fmt.Errorf("structured chunk %d parser provenance does not match artifact", index)
			}
			if strings.TrimSpace(artifact.ParserVersion) != "" && chunk.ParserVersion != artifact.ParserVersion {
				return ChunkResult{}, fmt.Errorf("structured chunk %d parser_version provenance does not match artifact", index)
			}
		}
		if requireMinerU && !isLowerSHA256(chunk.SourceSHA256) {
			return ChunkResult{}, fmt.Errorf("structured chunk %d source_sha256 must be a lowercase SHA-256", index)
		}
		if err := chunk.Validate(); err != nil {
			return ChunkResult{}, fmt.Errorf("structured chunk %d is invalid: %w", index, err)
		}
	}
	return parsed, nil
}

func claimsMinerUIdentity(parserName string) bool {
	normalized := strings.TrimSpace(strings.ToLower(parserName))
	return normalized == "mineru" || strings.HasPrefix(normalized, "mineru-")
}

type chunkElementProvenance struct {
	DocumentID    string `json:"document_id"`
	ParserName    string `json:"parser_name"`
	ParserVersion string `json:"parser_version"`
	SourceSHA256  string `json:"source_sha256"`
}

// validateStructuredArtifactProvenance checks the worker input at the Go
// boundary. The HTTP worker is not allowed to reinterpret an element as a
// different parser family or document, especially when a non-PDF extension
// hides a forged MinerU claim.
func validateStructuredArtifactProvenance(task tasks.FileProcessingTask, artifact ParsedArtifact) error {
	taskDocumentID := taskDocumentIdentity(task)
	if len(artifact.Elements) == 0 {
		if strings.EqualFold(filepath.Ext(task.FileName), ".pdf") || claimsMinerUIdentity(artifact.ParserName) {
			return fmt.Errorf("PDF chunks require MinerU element provenance")
		}
		if strings.TrimSpace(artifact.DocumentID) != "" || strings.TrimSpace(artifact.ParserName) != "" || strings.TrimSpace(artifact.ParserVersion) != "" {
			return fmt.Errorf("structured artifacts require element provenance")
		}
		return nil
	}
	if strings.TrimSpace(artifact.DocumentID) == "" || artifact.DocumentID != taskDocumentID {
		return fmt.Errorf("structured document provenance does not match task document identity")
	}
	if strings.TrimSpace(artifact.ParserName) == "" {
		return fmt.Errorf("structured parser name is required")
	}
	if strings.TrimSpace(artifact.ParserVersion) == "" {
		return fmt.Errorf("structured parser version is required")
	}
	requireMinerU := strings.EqualFold(filepath.Ext(task.FileName), ".pdf") || claimsMinerUIdentity(artifact.ParserName)
	if requireMinerU && strings.TrimSpace(strings.ToLower(artifact.ParserName)) != "mineru" {
		return fmt.Errorf("MinerU parser identity must be exact")
	}
	if !isLowerSHA256(artifact.SourceSHA256) {
		return fmt.Errorf("structured source_sha256 must be a lowercase SHA-256")
	}
	if task.Provenance != nil && strings.TrimSpace(task.Provenance.SourceSHA256) != "" && artifact.SourceSHA256 != task.Provenance.SourceSHA256 {
		return fmt.Errorf("structured source_sha256 does not match task provenance")
	}
	elements := make([]chunkElementProvenance, 0, len(artifact.Elements))
	payloadSHA256 := ""
	for index, raw := range artifact.Elements {
		var element chunkElementProvenance
		if err := json.Unmarshal(raw, &element); err != nil {
			return fmt.Errorf("structured element %d provenance is invalid: %w", index, err)
		}
		if element.DocumentID != artifact.DocumentID {
			return fmt.Errorf("structured element %d document_id provenance does not match artifact", index)
		}
		if strings.TrimSpace(element.ParserName) == "" {
			return fmt.Errorf("structured element %d parser name is required", index)
		}
		if claimsMinerUIdentity(element.ParserName) {
			requireMinerU = true
		}
		if !strings.EqualFold(strings.TrimSpace(element.ParserName), strings.TrimSpace(artifact.ParserName)) {
			if claimsMinerUIdentity(element.ParserName) || claimsMinerUIdentity(artifact.ParserName) {
				return fmt.Errorf("MinerU parser identity must be exact")
			}
			return fmt.Errorf("structured element %d parser provenance does not match artifact", index)
		}
		if element.ParserVersion != artifact.ParserVersion {
			return fmt.Errorf("structured element %d parser_version provenance does not match artifact", index)
		}
		if !isLowerSHA256(element.SourceSHA256) {
			return fmt.Errorf("structured element %d source_sha256 must be a lowercase SHA-256", index)
		}
		elements = append(elements, element)
		if payloadSHA256 == "" {
			payloadSHA256 = element.SourceSHA256
		} else if element.SourceSHA256 != payloadSHA256 {
			return fmt.Errorf("structured element %d payload hash does not match prior elements", index)
		}
	}
	if requireMinerU {
		for _, element := range elements {
			if strings.TrimSpace(strings.ToLower(element.ParserName)) != "mineru" {
				return fmt.Errorf("MinerU parser identity must be exact")
			}
		}
	} else if payloadSHA256 != artifact.SourceSHA256 {
		return fmt.Errorf("structured element payload hash does not match artifact source hash")
	}
	return nil
}

func taskDocumentIdentity(task tasks.FileProcessingTask) string {
	if documentID := strings.TrimSpace(task.DocumentID); documentID != "" {
		return documentID
	}
	return task.FileMD5
}

func isLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// Embed delegates embedding-stage execution to the external ingestion worker.
func (c *httpIngestionClient) Embed(ctx context.Context, task tasks.FileProcessingTask, texts []string) ([][]float32, error) {
	resp, err := c.doJSON(ctx, "/v1/ingestion/embed", embedRequest{
		Task:  task,
		Texts: texts,
	})
	if err != nil {
		return nil, err
	}
	var parsed embedResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return nil, err
	}
	return parsed.Vectors, nil
}

// Index delegates index-stage execution to the external ingestion worker.
func (c *httpIngestionClient) Index(ctx context.Context, task tasks.FileProcessingTask, indexName string, docs []model.EsDocument) (int, error) {
	resp, err := c.doJSON(ctx, "/v1/ingestion/index", indexRequest{
		Task:      task,
		IndexName: indexName,
		Docs:      docs,
	})
	if err != nil {
		return 0, err
	}
	var parsed indexResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return 0, err
	}
	return parsed.IndexedCount, nil
}

func (c *httpIngestionClient) doJSON(ctx context.Context, path string, payload any) ([]byte, error) {
	ctx, traceID := EnsureTraceID(ctx)
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal ingestion request failed: %w", err)
	}

	baseURL := strings.TrimRight(strings.TrimSpace(c.cfg.BaseURL), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create ingestion request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(c.cfg.SharedSecret); token != "" {
		req.Header.Set("X-Internal-Token", token)
	}
	req.Header.Set("X-Trace-ID", traceID)

	start := time.Now()
	log.Infow("[IngestionClient] request start",
		"trace_id", traceID,
		"path", path,
	)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call ingestion worker failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read ingestion response failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ingestion worker returned status=%s body_bytes=%d", resp.Status, len(raw))
	}

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode ingestion response failed: %w", err)
	}
	if len(envelope.Data) == 0 {
		log.Infow("[IngestionClient] request success",
			"trace_id", traceID,
			"path", path,
			"latency_ms", time.Since(start).Milliseconds(),
		)
		return []byte("{}"), nil
	}
	log.Infow("[IngestionClient] request success",
		"trace_id", traceID,
		"path", path,
		"latency_ms", time.Since(start).Milliseconds(),
	)
	return envelope.Data, nil
}
