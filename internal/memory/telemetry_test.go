package memory

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/telemetry/genai"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type memoryTelemetryTracer struct{ spans []*memoryTelemetrySpan }

type memoryTelemetrySpan struct {
	name              string
	attrs             []attribute.KeyValue
	events            []string
	recordedErrors    []string
	status            codes.Code
	statusDescription string
	ended             bool
}

func (t *memoryTelemetryTracer) StartSpan(ctx context.Context, name, operation, provider string) (context.Context, genai.Span) {
	span := &memoryTelemetrySpan{name: name, attrs: genai.GenAIAttributes(operation, provider)}
	span.attrs = append(span.attrs, genai.EvalJoinAttributes(ctx)...)
	t.spans = append(t.spans, span)
	return ctx, span
}

func (t *memoryTelemetryTracer) Shutdown(context.Context) error { return nil }

func (s *memoryTelemetrySpan) End() { s.ended = true }
func (s *memoryTelemetrySpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.attrs = append(s.attrs, attrs...)
}
func (s *memoryTelemetrySpan) RecordError(err error) {
	s.recordedErrors = append(s.recordedErrors, err.Error())
}
func (s *memoryTelemetrySpan) AddEvent(name string) { s.events = append(s.events, name) }
func (s *memoryTelemetrySpan) SetStatus(code codes.Code, description string) {
	s.status = code
	s.statusDescription = description
}

func TestLedgerMemorySpansUseSessionJoinWithoutContent(t *testing.T) {
	ledger := openMemoryLedger(t)
	sessionID := runMemoryFixture(t, ledger, 7, "MEMORY_SOURCE_SECRET_SENTINEL", nil, &memoryConversationFixture{})
	var calls atomic.Int32
	module := NewLedgerMemory(ledger, reflectionFixture(&calls, false))
	tracer := &memoryTelemetryTracer{}
	module.SetTracer(tracer)
	ctx := genai.WithEvalJoinBaggage(context.Background(), "run-memory-1", "caller-instance")
	if err := module.Commit(ctx, sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := module.Recall(ctx, 7, "MEMORY_QUERY_SECRET_SENTINEL", 1200); err != nil {
		t.Fatal(err)
	}
	if len(tracer.spans) != 2 || tracer.spans[0].name != "memory.reflect" || tracer.spans[1].name != "memory.recall" {
		t.Fatalf("spans = %+v", tracer.spans)
	}
	for _, span := range tracer.spans {
		if !span.ended {
			t.Fatalf("span %s was not ended", span.name)
		}
		attrs := map[string]string{}
		for _, attr := range span.attrs {
			if attr.Key == "memory.reflection.failure_stage" || attr.Key == "memory.reflection.error_code" {
				t.Fatalf("successful span %s has failure diagnostics", span.name)
			}
			value := attr.Value.Emit()
			if strings.Contains(value, "SECRET_SENTINEL") {
				t.Fatalf("span %s leaked Memory content in %s", span.name, attr.Key)
			}
			attrs[string(attr.Key)] = attr.Value.AsString()
		}
		if attrs[genai.AttrEvalRunID] != "run-memory-1" {
			t.Fatalf("%s eval.run_id = %q", span.name, attrs[genai.AttrEvalRunID])
		}
		wantInstance := "caller-instance"
		if span.name == "memory.reflect" {
			wantInstance = sessionID
		}
		if attrs[genai.AttrEvalInstanceID] != wantInstance {
			t.Fatalf("%s eval.instance_id = %q, want %q", span.name, attrs[genai.AttrEvalInstanceID], wantInstance)
		}
	}
}

func TestLedgerMemoryFailureSpansUseContentFreeErrorStatus(t *testing.T) {
	t.Run("recall", func(t *testing.T) {
		module := NewLedgerMemory(nil)
		tracer := &memoryTelemetryTracer{}
		module.SetTracer(tracer)
		if _, err := module.Recall(context.Background(), 7, "MEMORY_QUERY_SECRET_SENTINEL", 1200); err == nil {
			t.Fatal("missing ledger unexpectedly succeeded")
		}
		assertMemoryErrorSpan(t, tracer.spans, "memory.recall")
	})

	t.Run("reflection", func(t *testing.T) {
		ledger := openMemoryLedger(t)
		sessionID := runMemoryFixture(t, ledger, 7, "MEMORY_SOURCE_SECRET_SENTINEL", nil, &memoryConversationFixture{})
		module := NewLedgerMemory(ledger, func(context.Context, *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
			return nil, errors.New("PROVIDER_SECRET_SENTINEL")
		})
		tracer := &memoryTelemetryTracer{}
		module.SetTracer(tracer)
		if err := module.Commit(context.Background(), sessionID); err == nil {
			t.Fatal("failed reflection unexpectedly succeeded")
		}
		assertMemoryErrorSpan(t, tracer.spans, "memory.reflect")
	})
}

func TestLedgerMemoryReflectionFailureDiagnosticsAreBounded(t *testing.T) {
	for _, tc := range []struct {
		name, responseCode, wantStage, wantCode  string
		rpcError, missingResponse, invalidSource bool
	}{
		{name: "RPC failure", rpcError: true, wantStage: "reflector", wantCode: "reflection_unavailable"},
		{name: "missing response", missingResponse: true, wantStage: "reflector", wantCode: "reflection_unavailable"},
		{name: "invalid sources", responseCode: "invalid_reflection_sources", wantStage: "reflector", wantCode: "invalid_reflection_sources"},
		{name: "provider missing", responseCode: "reflection_provider_unavailable", wantStage: "reflector", wantCode: "reflection_provider_unavailable"},
		{name: "invalid output", responseCode: "reflection_invalid_or_unavailable", wantStage: "reflector", wantCode: "reflection_invalid_or_unavailable"},
		{name: "invalid gateway output", responseCode: "model_gateway_response_invalid", wantStage: "reflector", wantCode: "model_gateway_response_invalid"},
		{name: "private code", responseCode: "PROVIDER_SECRET_SENTINEL", wantStage: "reflector", wantCode: "reflection_error"},
		{name: "unrecognized code", responseCode: "provider_secret_sentinel", wantStage: "reflector", wantCode: "reflection_error"},
		{name: "Go provenance rejection", invalidSource: true, wantStage: "validation", wantCode: "reflection_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ledger := openMemoryLedger(t)
			sessionID := runMemoryFixture(t, ledger, 7, "MEMORY_SOURCE_SECRET_SENTINEL", nil, &memoryConversationFixture{})
			module := NewLedgerMemory(ledger, func(_ context.Context, request *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
				if tc.rpcError {
					return nil, errors.New("PROVIDER_SECRET_SENTINEL")
				}
				if tc.missingResponse {
					return nil, nil
				}
				response := &pb.MemoryReflectionResponse{SourceChecksum: request.SourceChecksum, ErrorCode: tc.responseCode}
				if tc.invalidSource {
					response.SourceChecksum = strings.Repeat("0", 64)
				}
				return response, nil
			})
			tracer := &memoryTelemetryTracer{}
			module.SetTracer(tracer)
			err := module.Commit(context.Background(), sessionID)
			if err == nil || strings.Contains(err.Error(), "SECRET_SENTINEL") {
				t.Fatal("reflection failure was accepted or exposed private error text")
			}
			if tc.invalidSource && !errors.Is(err, ErrTrajectoryIntegrity) {
				t.Fatalf("provenance rejection = %v, want integrity error", err)
			}
			assertMemoryErrorSpan(t, tracer.spans, "memory.reflect")
			attrs := map[string]string{}
			for _, attr := range tracer.spans[0].attrs {
				attrs[string(attr.Key)] = attr.Value.AsString()
			}
			if attrs["memory.reflection.failure_stage"] != tc.wantStage || attrs["memory.reflection.error_code"] != tc.wantCode {
				t.Fatalf("failure diagnostics = (%q, %q), want (%q, %q)", attrs["memory.reflection.failure_stage"], attrs["memory.reflection.error_code"], tc.wantStage, tc.wantCode)
			}
		})
	}
}

func assertMemoryErrorSpan(t *testing.T, spans []*memoryTelemetrySpan, name string) {
	t.Helper()
	if len(spans) != 1 || spans[0].name != name || !spans[0].ended {
		t.Fatalf("spans = %+v, want one ended %s span", spans, name)
	}
	if spans[0].status != codes.Error || spans[0].statusDescription != "" {
		t.Fatalf("status = %v %q, want content-free error", spans[0].status, spans[0].statusDescription)
	}
	for _, attr := range spans[0].attrs {
		if strings.Contains(attr.Value.Emit(), "SECRET_SENTINEL") {
			t.Fatalf("span leaked failure content in %s", attr.Key)
		}
	}
	for _, event := range spans[0].events {
		if strings.Contains(event, "SECRET_SENTINEL") {
			t.Fatalf("span leaked failure content in event %q", event)
		}
	}
	for _, recorded := range spans[0].recordedErrors {
		if strings.Contains(recorded, "SECRET_SENTINEL") {
			t.Fatal("span leaked failure content in recorded error")
		}
	}
}
