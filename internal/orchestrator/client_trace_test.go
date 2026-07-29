package orchestrator

import (
	"context"
	"testing"

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

	ctx = (&Client{}).injectTraceMetadata(ctx)
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("outgoing metadata missing")
	}
	want := "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	if got := md.Get("traceparent"); len(got) != 1 || got[0] != want {
		t.Fatalf("traceparent = %q, want %q", got, want)
	}
}
