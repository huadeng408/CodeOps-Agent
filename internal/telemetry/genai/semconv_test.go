package genai

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
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
