package orchestrator

import (
	"context"
	"strings"
	"testing"

	"code-agent/internal/telemetry/genai"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"
)

func TestInjectTraceMetadataFormatsW3CTraceparent(t *testing.T) {
	traceID, err := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	untrusted, err := baggage.NewMember("untrusted", "discard-me")
	if err != nil {
		t.Fatal(err)
	}
	untrustedBag, err := baggage.New(untrusted)
	if err != nil {
		t.Fatal(err)
	}
	ctx = baggage.ContextWithBaggage(ctx, untrustedBag)
	ctx = genai.WithEvalJoinBaggage(ctx, "run-1", "instance-1")

	ctx = (&Client{}).injectTraceMetadata(ctx)
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("outgoing metadata missing")
	}
	want := "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	if got := md.Get("traceparent"); len(got) != 1 || got[0] != want {
		t.Fatalf("traceparent = %q, want %q", got, want)
	}
	gotBaggage := strings.Join(md.Get("baggage"), ",")
	if !strings.Contains(gotBaggage, "eval.run_id=run-1") || !strings.Contains(gotBaggage, "eval.instance_id=instance-1") {
		t.Fatalf("baggage = %q", gotBaggage)
	}
	if strings.Contains(gotBaggage, "untrusted") {
		t.Fatalf("untrusted baggage propagated: %q", gotBaggage)
	}
}
