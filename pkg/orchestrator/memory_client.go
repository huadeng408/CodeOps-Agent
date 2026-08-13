package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/log"
)

// MemoryClient defines orchestrator-backed memory planning operations.
type MemoryClient interface {
	Enabled() bool
	SummarizeWorkingMemory(ctx context.Context, history []model.ChatMessage, workingHistoryMessages, workingMaxFacts int) (*model.OrchestratorMemorySummaryResponse, error)
	ExtractLongTermMemory(ctx context.Context, question, answer, workingSummary string) (*model.OrchestratorMemoryWriteResponse, error)
}

type noopMemoryClient struct{}

// Enabled reports whether the noop memory client is active.
func (noopMemoryClient) Enabled() bool { return false }

// SummarizeWorkingMemory implements the disabled memory client behavior.
func (noopMemoryClient) SummarizeWorkingMemory(ctx context.Context, history []model.ChatMessage, workingHistoryMessages, workingMaxFacts int) (*model.OrchestratorMemorySummaryResponse, error) {
	return nil, fmt.Errorf("orchestrator memory client is disabled")
}

// ExtractLongTermMemory implements the disabled memory client behavior.
func (noopMemoryClient) ExtractLongTermMemory(ctx context.Context, question, answer, workingSummary string) (*model.OrchestratorMemoryWriteResponse, error) {
	return nil, fmt.Errorf("orchestrator memory client is disabled")
}

type httpMemoryClient struct {
	cfg    serverconfig.AIOrchestratorConfig
	client *http.Client
}

// NewMemoryClient constructs the orchestrator-backed memory client.
func NewMemoryClient(cfg serverconfig.AIOrchestratorConfig) MemoryClient {
	if !cfg.Enabled || strings.TrimSpace(cfg.BaseURL) == "" {
		return noopMemoryClient{}
	}
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &httpMemoryClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Enabled reports whether the HTTP memory client is active.
func (c *httpMemoryClient) Enabled() bool { return true }

// SummarizeWorkingMemory asks the orchestrator to summarize recent dialogue into a working-memory snapshot.
func (c *httpMemoryClient) SummarizeWorkingMemory(ctx context.Context, history []model.ChatMessage, workingHistoryMessages, workingMaxFacts int) (*model.OrchestratorMemorySummaryResponse, error) {
	resp, err := c.doJSON(ctx, "/v1/memory/summarize", model.OrchestratorMemorySummaryRequest{
		History:                history,
		WorkingHistoryMessages: workingHistoryMessages,
		WorkingMaxFacts:        workingMaxFacts,
	})
	if err != nil {
		return nil, err
	}
	var parsed model.OrchestratorMemorySummaryResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

// ExtractLongTermMemory asks the orchestrator whether the latest turn should be persisted as long-term memory.
func (c *httpMemoryClient) ExtractLongTermMemory(ctx context.Context, question, answer, workingSummary string) (*model.OrchestratorMemoryWriteResponse, error) {
	resp, err := c.doJSON(ctx, "/v1/memory/extract", model.OrchestratorMemoryWriteRequest{
		Question:       question,
		Answer:         answer,
		WorkingSummary: workingSummary,
	})
	if err != nil {
		return nil, err
	}
	var parsed model.OrchestratorMemoryWriteResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func (c *httpMemoryClient) doJSON(ctx context.Context, path string, payload any) ([]byte, error) {
	ctx, traceID := EnsureTraceID(ctx)
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal memory request failed: %w", err)
	}

	baseURL := strings.TrimRight(strings.TrimSpace(c.cfg.BaseURL), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create memory request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(c.cfg.SharedSecret); token != "" {
		req.Header.Set("X-Internal-Token", token)
	}
	req.Header.Set("X-Trace-ID", traceID)

	start := time.Now()
	log.Infow("[OrchestratorMemoryClient] request start",
		"trace_id", traceID,
		"path", path,
	)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call memory worker failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read memory response failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("memory worker returned status=%s body_bytes=%d", resp.Status, len(raw))
	}

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode memory response failed: %w", err)
	}
	if len(envelope.Data) == 0 {
		log.Infow("[OrchestratorMemoryClient] request success",
			"trace_id", traceID,
			"path", path,
			"latency_ms", time.Since(start).Milliseconds(),
		)
		return []byte("{}"), nil
	}
	log.Infow("[OrchestratorMemoryClient] request success",
		"trace_id", traceID,
		"path", path,
		"latency_ms", time.Since(start).Milliseconds(),
	)
	return envelope.Data, nil
}
