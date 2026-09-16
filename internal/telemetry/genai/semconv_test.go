package genai

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestOperationNameConstants(t *testing.T) {
	if OperationInvokeAgent != "invoke_agent" {
		t.Errorf("OperationInvokeAgent = %q, want %q", OperationInvokeAgent, "invoke_agent")
	}
	if OperationInference != "inference" {
		t.Errorf("OperationInference = %q, want %q", OperationInference, "inference")
	}
	if OperationExecuteTool != "execute_tool" {
		t.Errorf("OperationExecuteTool = %q, want %q", OperationExecuteTool, "execute_tool")
	}
	if OperationPlan != "plan" {
		t.Errorf("OperationPlan = %q, want %q", OperationPlan, "plan")
	}
}

func TestGenAIAttributesIncludesOperationAndProvider(t *testing.T) {
	attrs := GenAIAttributes("invoke_agent", "gen_ai")
	keys := map[string]string{}
	for _, attr := range attrs {
		keys[string(attr.Key)] = attr.Value.AsString()
	}
	if v, ok := keys[AttrOperationName]; !ok || v != "invoke_agent" {
		t.Errorf("expected %s=invoke_agent, got %v", AttrOperationName, v)
	}
	if v, ok := keys[AttrProviderName]; !ok || v != "gen_ai" {
		t.Errorf("expected %s=gen_ai, got %v", AttrProviderName, v)
	}
}

func TestConvenienceConstructors(t *testing.T) {
	tests := []struct {
		name string
		kv   func() attribute.KeyValue
		key  attribute.Key
		val  string
	}{
		{"AgentNameKV", func() attribute.KeyValue { return AgentNameKV("test-agent") }, attribute.Key("gen_ai.agent.name"), "test-agent"},
		{"ToolNameKV", func() attribute.KeyValue { return ToolNameKV("Bash") }, attribute.Key("gen_ai.tool.name"), "Bash"},
		{"ToolCallIDKV", func() attribute.KeyValue { return ToolCallIDKV("call-1") }, attribute.Key("gen_ai.tool.call.id"), "call-1"},
		{"RequestModelKV", func() attribute.KeyValue { return RequestModelKV("claude-sonnet-4-5") }, attribute.Key("gen_ai.request.model"), "claude-sonnet-4-5"},
		{"ToolTypeKV", func() attribute.KeyValue { return ToolTypeKV("function") }, attribute.Key("gen_ai.tool.type"), "function"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kv := tt.kv()
			if string(kv.Key) != string(tt.key) {
				t.Errorf("key = %q, want %q", kv.Key, tt.key)
			}
			if kv.Value.AsString() != tt.val {
				t.Errorf("value = %q, want %q", kv.Value.AsString(), tt.val)
			}
		})
	}
}

func TestEvalJoinAttributesReadsOnlyPresentBaggageKeys(t *testing.T) {
	run, err := baggage.NewMember(AttrEvalRunID, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	instance, err := baggage.NewMember(AttrEvalInstanceID, "instance-1")
	if err != nil {
		t.Fatal(err)
	}
	bag, err := baggage.New(run, instance)
	if err != nil {
		t.Fatal(err)
	}
	attrs := EvalJoinAttributes(baggage.ContextWithBaggage(context.Background(), bag))
	if len(attrs) != 2 {
		t.Fatalf("attribute count = %d, want 2", len(attrs))
	}
	values := map[attribute.Key]attribute.Value{}
	for _, attr := range attrs {
		values[attr.Key] = attr.Value
	}
	if got := values[attribute.Key(AttrEvalRunID)].AsString(); got != "run-1" {
		t.Fatalf("run ID = %q, want run-1", got)
	}
	if got := values[attribute.Key(AttrEvalInstanceID)].AsString(); got != "instance-1" {
		t.Fatalf("instance ID = %q, want instance-1", got)
	}
}

func TestEvalJoinRejectsUnsafeIdentifiers(t *testing.T) {
	ctx := WithEvalJoinBaggage(context.Background(), "secret value with spaces", "instance,inject=1")
	if attrs := EvalJoinAttributes(ctx); len(attrs) != 0 {
		t.Fatalf("unsafe join attributes = %+v, want none", attrs)
	}
	if ValidEvalJoinID("") || ValidEvalJoinID(" leading") || ValidEvalJoinID("run,other=value") || ValidEvalJoinID(string(make([]byte, 97))) {
		t.Fatal("unsafe evaluation join ID was accepted")
	}
	if !ValidEvalJoinID("run-1.safe:case_2") {
		t.Fatal("safe evaluation join ID was rejected")
	}
}

func TestDetachedTraceContextKeepsOnlyParentAndAdmittedBaggage(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	tracer := provider.Tracer("test")

	parentCtx, parent := tracer.Start(context.Background(), "parent")
	untrusted, err := baggage.NewMember("untrusted", "must-not-cross")
	if err != nil {
		t.Fatal(err)
	}
	bag, err := baggage.New(untrusted)
	if err != nil {
		t.Fatal(err)
	}
	type privateKey struct{}
	parentCtx = context.WithValue(baggage.ContextWithBaggage(parentCtx, bag), privateKey{}, "private-value")
	parentCtx = WithEvalJoinBaggage(parentCtx, "run-1", "caller-instance")

	detached := DetachedTraceContext(parentCtx, "child-session")
	if got := detached.Value(privateKey{}); got != nil {
		t.Fatalf("private context value crossed queue: %v", got)
	}
	if got := baggage.FromContext(detached).Member("untrusted").Value(); got != "" {
		t.Fatalf("untrusted baggage crossed queue: %q", got)
	}
	if got := baggage.FromContext(detached).Member(AttrEvalRunID).Value(); got != "run-1" {
		t.Fatalf("run ID = %q, want run-1", got)
	}
	if got := baggage.FromContext(detached).Member(AttrEvalInstanceID).Value(); got != "child-session" {
		t.Fatalf("instance ID = %q, want child-session", got)
	}

	_, child := tracer.Start(detached, "child")
	child.End()
	parent.End()
	var childSpan tracetest.SpanStub
	for _, span := range recorder.Ended() {
		if span.Name() == "child" {
			childSpan = tracetest.SpanStubFromReadOnlySpan(span)
		}
	}
	if !childSpan.SpanContext.IsValid() || childSpan.Parent.SpanID() != trace.SpanContextFromContext(parentCtx).SpanID() {
		t.Fatalf("child parent = %s, want %s", childSpan.Parent.SpanID(), trace.SpanContextFromContext(parentCtx).SpanID())
	}
}

func TestGenAITelemetryStartSpanAddsEvalJoinAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	telemetry := &GenAITelemetry{provider: provider, tracer: provider.Tracer("test")}
	ctx := WithEvalJoinBaggage(context.Background(), "run-1", "instance-1")
	_, span := telemetry.StartSpan(ctx, "agent.main", OperationInvokeAgent, SystemGenAI)
	span.End()
	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	attrs := map[string]string{}
	for _, attr := range ended[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	if attrs[AttrEvalRunID] != "run-1" || attrs[AttrEvalInstanceID] != "instance-1" {
		t.Fatalf("eval join attributes = %+v", attrs)
	}
}

func TestMarkSpanErrorSetsContentFreeOTelStatus(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	telemetry := &GenAITelemetry{provider: provider, tracer: provider.Tracer("test")}
	_, span := telemetry.StartSpan(context.Background(), "tool.mcp", OperationExecuteTool, SystemGenAI)

	MarkSpanError(span)
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	if status := ended[0].Status(); status.Code != codes.Error || status.Description != "" {
		t.Fatalf("status = %+v, want content-free error", status)
	}
	if len(ended[0].Events()) != 0 {
		t.Fatalf("error status unexpectedly recorded an event: %+v", ended[0].Events())
	}
}

func TestTruncateAttribute(t *testing.T) {
	kv := ToolCallResultKV("short result")
	if kv.Value.AsString() != "short result" {
		t.Errorf("short value was truncated: %q", kv.Value.AsString())
	}

	long := make([]byte, 2048)
	for i := range long {
		long[i] = 'x'
	}
	kv = ToolCallResultKV(string(long))
	if len(kv.Value.AsString()) > 1024 {
		t.Errorf("long value not truncated; len=%d", len(kv.Value.AsString()))
	}
}

func TestNoopTracer(t *testing.T) {
	tracer := &NoopTracer{}
	ctx, span := tracer.StartSpan(nil, "test", OperationInvokeAgent, SystemGenAI)
	if ctx == nil {
		t.Error("noop tracer returned nil ctx")
	}
	if span == nil {
		t.Error("noop tracer returned nil span")
	}
	// Must not panic
	span.SetAttributes(AgentNameKV("x"))
	span.RecordError(nil)
	span.AddEvent("ev")
	span.End()
	if err := tracer.Shutdown(nil); err != nil {
		t.Errorf("noop shutdown returned error: %v", err)
	}
}

func TestParseOTLPEndpoint(t *testing.T) {
	tests := []struct {
		raw      string
		host     string
		insecure bool
		wantErr  bool
	}{
		{"http://localhost:6006", "localhost:6006", true, false},
		{"https://collector.example.com:4318", "collector.example.com:4318", false, false},
		{"http://127.0.0.1:4317", "127.0.0.1:4317", true, false},
		{"no-scheme:6006", "", false, true},
		{"", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			host, insecure, err := parseOTLPEndpoint(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for %q", tt.raw)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if host != tt.host {
				t.Errorf("host = %q, want %q", host, tt.host)
			}
			if insecure != tt.insecure {
				t.Errorf("insecure = %v, want %v", insecure, tt.insecure)
			}
		})
	}
}
