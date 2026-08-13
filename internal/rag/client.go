package rag

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	searchPath         = "/internal/orchestrator/knowledge-search"
	ingestPath         = "/internal/orchestrator/knowledge-ingest"
	pipelineStatusPath = "/internal/orchestrator/pipeline-status"
)

// ErrUnavailable identifies clients that are disabled or incompletely configured.
var ErrUnavailable = errors.New("RAG unavailable")

// Searcher searches the configured RAG knowledge base.
type Searcher interface {
	Search(context.Context, SearchOptions) ([]SearchResult, error)
}

// Ingester queues one local file for ingestion into the configured RAG service.
type Ingester interface {
	Ingest(context.Context, string) (*IngestResult, error)
}

// OpenFileIngester streams an already-open regular file without taking ownership
// of its handle.
type OpenFileIngester interface {
	IngestFile(context.Context, *os.File, string) (*IngestResult, error)
}

// OpenFileOptionsIngester streams an already-open regular file with explicit
// per-document provenance.
type OpenFileOptionsIngester interface {
	IngestFileWithOptions(context.Context, *os.File, string, IngestOptions) (*IngestResult, error)
}

// Config configures the RAG HTTP client.
type Config struct {
	Enabled          bool
	BaseURL          string
	InternalToken    string
	UserID           uint
	OrgTag           string
	IngestPublic     bool
	IngestProvenance IngestProvenanceConfig
	HTTPClient       *http.Client
}

// IngestProvenanceConfig pins the corpus identity and server routing selected
// by configuration. These values are never guessed by the HTTP client.
type IngestProvenanceConfig struct {
	SourceID         string
	SourcePathPrefix string
	SourceURL        string
	SourceCommit     string
	TargetIndex      string
	CorpusGeneration string
	RunID            string
}

// IngestOptions carries the document-specific, auditable source identity.
// Non-empty SourceURL and RunID values override their configured defaults.
type IngestOptions struct {
	SourcePath string
	SourceURL  string
	RunID      string
}

// SearchOptions controls one knowledge search.
type SearchOptions struct {
	Query         string
	TopK          int
	Mode          string
	DisableRerank bool
}

// SearchResult is one knowledge chunk returned by the RAG service.
type SearchResult struct {
	FileMD5     string  `json:"fileMd5"`
	FileName    string  `json:"fileName"`
	ChunkID     int     `json:"chunkId"`
	TextContent string  `json:"textContent"`
	Score       float64 `json:"score"`
	UserID      string  `json:"userId"`
	OrgTag      string  `json:"orgTag"`
	IsPublic    bool    `json:"isPublic"`
}

// IngestResult identifies a file accepted by the ingestion endpoint.
type IngestResult struct {
	FileMD5   string `json:"fileMd5"`
	FileName  string `json:"fileName"`
	ObjectURL string `json:"objectUrl"`
	Message   string `json:"-"`
}

// PipelineStageStatus is one run-scoped processing stage returned by the server.
type PipelineStageStatus struct {
	Stage     string `json:"stage"`
	Status    string `json:"status"`
	LastError string `json:"lastError,omitempty"`
}

// PipelineStatusResult reports whether all required stages completed for one run/file.
type PipelineStatusResult struct {
	RunID    string                `json:"runId"`
	FileMD5  string                `json:"fileMd5"`
	Complete bool                  `json:"complete"`
	Stages   []PipelineStageStatus `json:"stages"`
}

// Client implements Searcher and Ingester over the internal RAG HTTP API.
type Client struct {
	config     Config
	httpClient *http.Client
}

// NewClient creates a RAG client. Configuration is validated before each call.
func NewClient(config Config) *Client {
	httpClient := http.Client{Timeout: 10 * time.Minute}
	if config.HTTPClient != nil {
		httpClient = *config.HTTPClient
	}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{config: config, httpClient: &httpClient}
}

// NewUnavailableError returns an error recognizable with errors.Is.
func NewUnavailableError(reason string) error {
	return fmt.Errorf("%w: %s", ErrUnavailable, reason)
}

// Search executes one knowledge search through the internal endpoint.
func (c *Client) Search(ctx context.Context, options SearchOptions) ([]SearchResult, error) {
	if err := c.validateAvailable(); err != nil {
		return nil, err
	}

	query := strings.TrimSpace(options.Query)
	if query == "" {
		return nil, errors.New("RAG search query must not be blank")
	}
	if !isSupportedMode(options.Mode) {
		return nil, fmt.Errorf("unsupported RAG search mode %q", options.Mode)
	}

	payload := struct {
		User struct {
			ID         uint   `json:"id"`
			OrgTags    string `json:"orgTags"`
			PrimaryOrg string `json:"primaryOrg"`
		} `json:"user"`
		Query         string `json:"query"`
		TopK          int    `json:"topK"`
		Mode          string `json:"mode"`
		DisableRerank bool   `json:"disableRerank"`
		RunID         string `json:"runId,omitempty"`
	}{
		Query:         query,
		TopK:          options.TopK,
		Mode:          options.Mode,
		DisableRerank: options.DisableRerank,
		RunID:         strings.TrimSpace(c.config.IngestProvenance.RunID),
	}
	payload.User.ID = c.config.UserID
	payload.User.OrgTags = c.config.OrgTag
	payload.User.PrimaryOrg = c.config.OrgTag

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode RAG search request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(searchPath), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create RAG search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send RAG search request: %w", err)
	}
	defer resp.Body.Close()
	if err := requireHTTPSuccess(resp); err != nil {
		return nil, fmt.Errorf("RAG search failed: %w", err)
	}

	var envelope struct {
		Code int `json:"code"`
		Data *struct {
			Results []SearchResult `json:"results"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode RAG search response: %w", err)
	}
	if envelope.Code < 200 || envelope.Code >= 300 {
		return nil, fmt.Errorf("RAG search response code %d: %s", envelope.Code, envelope.Message)
	}
	if envelope.Data == nil {
		return nil, errors.New("RAG search response is missing data")
	}
	return envelope.Data.Results, nil
}

// PipelineStatus reads the exact run-scoped pipeline state for one ingested file.
func (c *Client) PipelineStatus(ctx context.Context, runID, fileMD5 string) (*PipelineStatusResult, error) {
	if err := c.validateAvailable(); err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("runId", strings.TrimSpace(runID))
	query.Set("fileMd5", strings.TrimSpace(fileMD5))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(pipelineStatusPath)+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create RAG pipeline status request: %w", err)
	}
	c.authorize(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send RAG pipeline status request: %w", err)
	}
	defer resp.Body.Close()
	if err := requireHTTPSuccess(resp); err != nil {
		return nil, fmt.Errorf("RAG pipeline status failed: %w", err)
	}
	var result PipelineStatusResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode RAG pipeline status response: %w", err)
	}
	if err := validatePipelineStatus(&result, strings.TrimSpace(runID), strings.TrimSpace(fileMD5)); err != nil {
		return nil, err
	}
	return &result, nil
}

func validatePipelineStatus(result *PipelineStatusResult, runID, fileMD5 string) error {
	if result.RunID != runID || result.FileMD5 != fileMD5 {
		return fmt.Errorf("RAG pipeline status scope mismatch")
	}
	expected := [...]string{"parse", "chunk", "embed", "index"}
	if len(result.Stages) != len(expected) {
		return fmt.Errorf("RAG pipeline status must contain exactly four stages")
	}
	for index, stage := range result.Stages {
		if stage.Stage != expected[index] {
			return fmt.Errorf("RAG pipeline status stage order is invalid")
		}
		if result.Complete && stage.Status != "SUCCESS" {
			return fmt.Errorf("RAG pipeline completed with non-SUCCESS stage %s", stage.Stage)
		}
	}
	return nil
}

// WaitForPipelineCompletion waits for this run's stages and fails immediately on a failed stage.
func (c *Client) WaitForPipelineCompletion(ctx context.Context, runID, fileMD5 string, pollInterval time.Duration) (*PipelineStatusResult, error) {
	for {
		status, err := c.PipelineStatus(ctx, runID, fileMD5)
		if err != nil {
			return nil, err
		}
		for _, stage := range status.Stages {
			if stage.Status == "FAILED" {
				return nil, fmt.Errorf("RAG pipeline stage %s failed: %s", stage.Stage, stage.LastError)
			}
		}
		if status.Complete {
			return status, nil
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, fmt.Errorf("wait for RAG pipeline run %s file %s: %w", runID, fileMD5, ctx.Err())
		case <-timer.C:
		}
	}
}

// Ingest streams one regular file to the internal ingestion endpoint.
func (c *Client) Ingest(ctx context.Context, path string) (*IngestResult, error) {
	return c.IngestWithOptions(ctx, path, IngestOptions{SourcePath: c.deriveSourcePath(path)})
}

// IngestWithOptions streams one regular file with explicit document provenance.
func (c *Client) IngestWithOptions(ctx context.Context, path string, options IngestOptions) (*IngestResult, error) {
	if err := c.validateAvailable(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open RAG ingestion file %q: %w", path, err)
	}
	defer file.Close()
	if strings.TrimSpace(options.SourcePath) == "" {
		options.SourcePath = c.deriveSourcePath(path)
	}
	return c.IngestFileWithOptions(ctx, file, filepath.Base(path), options)
}

// IngestFile streams a caller-owned regular file from offset zero.
func (c *Client) IngestFile(ctx context.Context, file *os.File, name string) (*IngestResult, error) {
	return c.IngestFileWithOptions(ctx, file, name, IngestOptions{SourcePath: c.deriveSourcePath(name)})
}

// IngestFileWithOptions streams a caller-owned regular file from offset zero
// after hashing the exact bytes that will be uploaded.
func (c *Client) IngestFileWithOptions(ctx context.Context, file *os.File, name string, options IngestOptions) (*IngestResult, error) {
	if err := c.validateAvailable(); err != nil {
		return nil, err
	}
	if file == nil {
		return nil, errors.New("RAG ingestion file handle is nil")
	}
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat RAG ingestion file handle: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("RAG ingestion file handle is not a regular file")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind RAG ingestion file handle: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, fmt.Errorf("hash RAG ingestion file: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind hashed RAG ingestion file handle: %w", err)
	}
	provenance, runID, err := c.resolveIngestProvenance(options, hex.EncodeToString(hash.Sum(nil)))
	if err != nil {
		return nil, err
	}

	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(ingestPath), reader)
	if err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("create RAG ingestion request: %w", err)
	}
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	c.authorize(req)

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writeMultipart(multipartWriter, writer, file, filepath.Base(name), c.config, provenance, runID)
	}()

	resp, requestErr := c.httpClient.Do(req)
	_ = req.Body.Close()
	writeErr := <-writeDone
	if requestErr != nil {
		return nil, fmt.Errorf("send RAG ingestion request: %w", requestErr)
	}
	defer resp.Body.Close()
	if err := requireHTTPSuccess(resp); err != nil {
		return nil, fmt.Errorf("RAG ingestion failed: %w", err)
	}
	if writeErr != nil && !errors.Is(writeErr, io.ErrClosedPipe) {
		return nil, fmt.Errorf("stream RAG ingestion request: %w", writeErr)
	}

	var envelope struct {
		Code    int           `json:"code"`
		Data    *IngestResult `json:"data"`
		Message string        `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode RAG ingestion response: %w", err)
	}
	if envelope.Code < 200 || envelope.Code >= 300 {
		return nil, fmt.Errorf("RAG ingestion response code %d: %s", envelope.Code, envelope.Message)
	}
	if envelope.Data == nil {
		return nil, errors.New("RAG ingestion response is missing data")
	}
	envelope.Data.Message = envelope.Message
	return envelope.Data, nil
}

type resolvedIngestProvenance struct {
	SourceID         string
	SourcePath       string
	SourceURL        string
	SourceCommit     string
	SourceSHA256     string
	TargetIndex      string
	CorpusGeneration string
}

func (c *Client) resolveIngestProvenance(options IngestOptions, sourceSHA256 string) (resolvedIngestProvenance, string, error) {
	configured := c.config.IngestProvenance
	provenance := resolvedIngestProvenance{
		SourceID:         strings.TrimSpace(configured.SourceID),
		SourcePath:       strings.TrimSpace(options.SourcePath),
		SourceURL:        strings.TrimSpace(options.SourceURL),
		SourceCommit:     strings.TrimSpace(configured.SourceCommit),
		SourceSHA256:     sourceSHA256,
		TargetIndex:      strings.TrimSpace(configured.TargetIndex),
		CorpusGeneration: strings.TrimSpace(configured.CorpusGeneration),
	}
	if provenance.SourceURL == "" {
		provenance.SourceURL = strings.TrimSpace(configured.SourceURL)
	}
	for field, value := range map[string]string{
		"sourceId": provenance.SourceID, "sourcePath": provenance.SourcePath,
		"sourceCommit": provenance.SourceCommit, "targetIndex": provenance.TargetIndex,
		"corpusGeneration": provenance.CorpusGeneration,
	} {
		if value == "" {
			return resolvedIngestProvenance{}, "", NewUnavailableError("rag ingestion " + field + " is empty")
		}
	}
	if !isLowerHex(provenance.SourceCommit, 40) {
		return resolvedIngestProvenance{}, "", NewUnavailableError("rag ingestion sourceCommit must be 40 lowercase hex chars")
	}
	runID := strings.TrimSpace(options.RunID)
	if runID == "" {
		runID = strings.TrimSpace(configured.RunID)
	}
	return provenance, runID, nil
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for i := range value {
		if (value[i] < '0' || value[i] > '9') && (value[i] < 'a' || value[i] > 'f') {
			return false
		}
	}
	return true
}

func (c *Client) deriveSourcePath(filePath string) string {
	prefix := strings.Trim(strings.TrimSpace(c.config.IngestProvenance.SourcePathPrefix), "/\\")
	if prefix == "" {
		return filepath.Base(filepath.Clean(filePath))
	}
	return strings.TrimRight(filepath.ToSlash(prefix), "/") + "/" + filepath.Base(filePath)
}

func (c *Client) validateAvailable() error {
	switch {
	case !c.config.Enabled:
		return NewUnavailableError("rag_enabled is false")
	case strings.TrimSpace(c.config.BaseURL) == "":
		return NewUnavailableError("rag_server_url is empty")
	case strings.TrimSpace(c.config.InternalToken) == "":
		return NewUnavailableError("rag_internal_secret is empty")
	case c.config.UserID == 0:
		return NewUnavailableError("rag_user_id must be greater than zero")
	case strings.Contains(c.config.OrgTag, ","):
		return NewUnavailableError("rag_org_tag must contain a single organization tag")
	default:
		return nil
	}
}

func (c *Client) endpoint(path string) string {
	return strings.TrimRight(strings.TrimSpace(c.config.BaseURL), "/") + path
}

func (c *Client) authorize(req *http.Request) {
	req.Header.Set("X-Internal-Token", c.config.InternalToken)
}

func isSupportedMode(mode string) bool {
	switch mode {
	case "hybrid", "bm25", "vector":
		return true
	default:
		return false
	}
}

func writeMultipart(multipartWriter *multipart.Writer, pipeWriter *io.PipeWriter, file *os.File, name string, config Config, provenance resolvedIngestProvenance, runID string) error {
	fail := func(err error) error {
		_ = pipeWriter.CloseWithError(err)
		return err
	}
	for field, value := range map[string]string{
		"userId":           strconv.FormatUint(uint64(config.UserID), 10),
		"orgTag":           config.OrgTag,
		"isPublic":         strconv.FormatBool(config.IngestPublic),
		"sourceId":         provenance.SourceID,
		"sourcePath":       provenance.SourcePath,
		"sourceUrl":        provenance.SourceURL,
		"sourceCommit":     provenance.SourceCommit,
		"sourceSha256":     provenance.SourceSHA256,
		"targetIndex":      provenance.TargetIndex,
		"corpusGeneration": provenance.CorpusGeneration,
		"runId":            runID,
	} {
		if err := multipartWriter.WriteField(field, value); err != nil {
			return fail(err)
		}
	}
	part, err := multipartWriter.CreateFormFile("file", name)
	if err != nil {
		return fail(err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fail(err)
	}
	if err := multipartWriter.Close(); err != nil {
		return fail(err)
	}
	return pipeWriter.Close()
}

func requireHTTPSuccess(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		detail = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, detail)
}

var _ Searcher = (*Client)(nil)
var _ Ingester = (*Client)(nil)
var _ OpenFileIngester = (*Client)(nil)
var _ OpenFileOptionsIngester = (*Client)(nil)
