package cli

import (
	"context"
	"testing"

	"code-agent/internal/telemetry/genai"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

func TestCLITelemetryContextAdmitsOnlySafeExplicitRunID(t *testing.T) {
	t.Setenv("CODE_AGENT_EVAL_RUN_ID", "run-cli-1")
	t.Setenv("CODE_AGENT_EVAL_TRACEPARENT", "00-11111111111111111111111111111111-2222222222222222-01")
	t.Setenv("EVAL_RUN_ID", "must-not-be-read")
	ctx := cliTelemetryContext(context.Background(), "session-cli-1")
	bag := baggage.FromContext(ctx)
	if got := bag.Member(genai.AttrEvalRunID).Value(); got != "run-cli-1" {
		t.Fatalf("eval.run_id = %q", got)
	}
	if got := bag.Member(genai.AttrEvalInstanceID).Value(); got != "session-cli-1" {
		t.Fatalf("eval.instance_id = %q", got)
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() || !spanContext.IsRemote() || spanContext.TraceID().String() != "11111111111111111111111111111111" {
		t.Fatalf("trace parent was not admitted: %v", spanContext)
	}

	t.Setenv("CODE_AGENT_EVAL_RUN_ID", "unsafe run,id")
	ctx = cliTelemetryContext(context.Background(), "session-cli-1")
	if got := baggage.FromContext(ctx).Member(genai.AttrEvalRunID).Value(); got != "" {
		t.Fatalf("unsafe eval.run_id admitted: %q", got)
	}
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		t.Fatalf("trace parent admitted without a safe run id: %v", spanContext)
	}
}

func TestCLITelemetryContextRejectsMalformedTraceParent(t *testing.T) {
	t.Setenv("CODE_AGENT_EVAL_RUN_ID", "run-cli-1")
	t.Setenv("CODE_AGENT_EVAL_TRACEPARENT", "malformed")
	ctx := cliTelemetryContext(context.Background(), "session-cli-1")
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		t.Fatalf("malformed trace parent admitted: %v", spanContext)
	}
	if got := baggage.FromContext(ctx).Member(genai.AttrEvalRunID).Value(); got != "run-cli-1" {
		t.Fatalf("safe eval.run_id = %q", got)
	}
}
