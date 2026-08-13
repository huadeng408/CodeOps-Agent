package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-agent/internal/middleware"
	"code-agent/internal/model"
	"code-agent/internal/service"
	"code-agent/internal/telemetry/genai"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
)

type telemetrySupportService struct{}

func (telemetrySupportService) LoadSession(context.Context, uint) (*model.OrchestratorSessionResponse, error) {
	return nil, nil
}
func (telemetrySupportService) RetrieveContext(context.Context, *model.OrchestratorRetrieveRequest) (*model.OrchestratorRetrieveResponse, error) {
	return nil, nil
}
func (telemetrySupportService) PreparePromptContext(context.Context, *model.OrchestratorPromptContextRequest) (*model.OrchestratorPromptContextResponse, error) {
	return nil, nil
}
func (telemetrySupportService) SearchKnowledge(context.Context, *model.OrchestratorKnowledgeSearchRequest) (*model.OrchestratorKnowledgeSearchResponse, error) {
	return &model.OrchestratorKnowledgeSearchResponse{}, nil
}
func (telemetrySupportService) SearchMemory(context.Context, *model.OrchestratorMemorySearchRequest) (*model.OrchestratorMemorySearchResponse, error) {
	return nil, nil
}
func (telemetrySupportService) RerankContext(context.Context, *model.OrchestratorRerankRequest) (*model.OrchestratorRerankResponse, error) {
	return nil, nil
}
func (telemetrySupportService) PersistTurn(context.Context, *model.OrchestratorPersistRequest) error {
	return nil
}

var _ service.OrchestratorSupportService = telemetrySupportService{}

type rerankTelemetrySupportService struct{ telemetrySupportService }

func (rerankTelemetrySupportService) SearchKnowledge(ctx context.Context, _ *model.OrchestratorKnowledgeSearchRequest) (*model.OrchestratorKnowledgeSearchResponse, error) {
	service.MarkRerankApplied(ctx)
	return &model.OrchestratorKnowledgeSearchResponse{}, nil
}

var _ service.OrchestratorSupportService = rerankTelemetrySupportService{}

type telemetrySpan struct{ attributes []attribute.KeyValue }

func (*telemetrySpan) End() {}
func (s *telemetrySpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.attributes = append(s.attributes, attrs...)
}
func (*telemetrySpan) RecordError(error) {}
func (*telemetrySpan) AddEvent(string)   {}

type telemetryTracer struct{ span *telemetrySpan }

func (t *telemetryTracer) StartSpan(ctx context.Context, _ string, _ string, _ string) (context.Context, genai.Span) {
	t.span = &telemetrySpan{}
	return ctx, t.span
}
func (*telemetryTracer) Shutdown(context.Context) error { return nil }

func TestSearchKnowledgeStampsInstanceIDFromW3CBaggage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tracer := &telemetryTracer{}
	handler := NewOrchestratorHandler(telemetrySupportService{})
	handler.SetTracer(tracer)
	router := gin.New()
	router.Use(middleware.TraceContextMiddleware())
	router.POST("/internal/orchestrator/knowledge-search", handler.SearchKnowledge)

	req := httptest.NewRequest(http.MethodPost, "/internal/orchestrator/knowledge-search", strings.NewReader(`{"user":{"id":7},"query":"interface","topK":5,"mode":"hybrid","runId":"run-1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
	req.Header.Set("baggage", "eval.run_id=run-1,eval.instance_id=instance-1")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}
	if tracer.span == nil {
		t.Fatal("expected retrieve span")
	}
	values := map[attribute.Key]attribute.Value{}
	for _, kv := range tracer.span.attributes {
		values[kv.Key] = kv.Value
	}
	if got, want := values[attribute.Key(genai.AttrEvalRunID)].AsString(), "run-1"; got != want {
		t.Fatalf("run ID = %q, want %q", got, want)
	}
	if got, want := values[attribute.Key(genai.AttrEvalInstanceID)].AsString(), "instance-1"; got != want {
		t.Fatalf("instance ID = %q, want %q", got, want)
	}
}

func TestSearchKnowledgeRejectsRunIDThatDisagreesWithW3CBaggage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tracer := &telemetryTracer{}
	handler := NewOrchestratorHandler(telemetrySupportService{})
	handler.SetTracer(tracer)
	router := gin.New()
	router.Use(middleware.TraceContextMiddleware())
	router.POST("/internal/orchestrator/knowledge-search", handler.SearchKnowledge)

	req := httptest.NewRequest(http.MethodPost, "/internal/orchestrator/knowledge-search", strings.NewReader(`{"user":{"id":7},"query":"interface","topK":5,"mode":"hybrid","runId":"payload-run"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
	req.Header.Set("baggage", "eval.run_id=baggage-run,eval.instance_id=instance-1")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusBadRequest)
	}
	if tracer.span != nil {
		t.Fatal("mismatched run ID must not create a retrieve span")
	}
}

func TestSearchKnowledgeRecordsConfiguredPinsAndActualRerankOutcome(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tracer := &telemetryTracer{}
	handler := NewOrchestratorHandler(rerankTelemetrySupportService{})
	handler.SetTracer(tracer)
	handler.SetRAGTracePins("techdocs-2026-07-30-v1", "knowledge_base_v2_bge_m3")
	router := gin.New()
	router.Use(middleware.TraceContextMiddleware())
	router.POST("/internal/orchestrator/knowledge-search", handler.SearchKnowledge)

	req := httptest.NewRequest(http.MethodPost, "/internal/orchestrator/knowledge-search", strings.NewReader(`{"user":{"id":7},"query":"interface","topK":5,"mode":"hybrid","runId":"run-1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
	req.Header.Set("baggage", "eval.run_id=run-1,eval.instance_id=instance-1")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}
	if tracer.span == nil {
		t.Fatal("expected retrieve span")
	}
	values := map[attribute.Key]attribute.Value{}
	for _, kv := range tracer.span.attributes {
		values[kv.Key] = kv.Value
	}
	if got, want := values[attribute.Key("rag.corpus_generation")].AsString(), "techdocs-2026-07-30-v1"; got != want {
		t.Fatalf("corpus generation = %q, want %q", got, want)
	}
	if got, want := values[attribute.Key("rag.index_name")].AsString(), "knowledge_base_v2_bge_m3"; got != want {
		t.Fatalf("physical index = %q, want %q", got, want)
	}
	if !values[attribute.Key(genai.AttrRerankerApplied)].AsBool() {
		t.Fatal("reranker outcome must report the real successful call")
	}
}
