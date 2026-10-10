package genai

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// ---------------------------------------------------------------------------
// Public interfaces (mockable)
// ---------------------------------------------------------------------------

// Span is a lightweight abstraction over an OTel span so that tests can
// inject their own recorder.
type Span interface {
	End()
	SetAttributes(attrs ...attribute.KeyValue)
	RecordError(err error)
	AddEvent(name string)
}

// MarkSpanError sets a content-free failure status on production OTel spans.
// Test or adapter spans that do not expose status fall back to a fixed error
// marker so callers never need to pass credential-bearing error text.
func MarkSpanError(span Span) {
	if span == nil {
		return
	}
	if setter, ok := span.(interface{ SetStatus(codes.Code, string) }); ok {
		setter.SetStatus(codes.Error, "")
		return
	}
	span.RecordError(errSpanFailed)
}

var errSpanFailed = fmt.Errorf("operation failed")

// Tracer is the public interface for creating gen_ai spans. Both the real
// GenAITelemetry and NoopTracer implement it.
type Tracer interface {
	StartSpan(ctx context.Context, name string, operation string, provider string) (context.Context, Span)
	Shutdown(ctx context.Context) error
}

// ---------------------------------------------------------------------------
// Real tracer backed by OTel SDK + OTLP HTTP exporter
// ---------------------------------------------------------------------------

// GenAITelemetry holds the OTel tracer provider and a convenience tracer
// instance. Create it via NewTelemetry.
type GenAITelemetry struct {
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
}

// otelSpan wraps an OTel trace.Span so it satisfies our Span interface.
type otelSpan struct{ s trace.Span }

func (w *otelSpan) End()                                  { w.s.End() }
func (w *otelSpan) SetAttributes(a ...attribute.KeyValue) { w.s.SetAttributes(a...) }
func (w *otelSpan) RecordError(err error)                 { w.s.RecordError(err) }
func (w *otelSpan) AddEvent(n string)                     { w.s.AddEvent(n) }
func (w *otelSpan) SetStatus(code codes.Code, description string) {
	w.s.SetStatus(code, description)
}

// NewTelemetry initialises the OTel SDK with an OTLP HTTP exporter pointed at
// the given endpoint (or the OTEL_EXPORTER_OTLP_ENDPOINT / default localhost:6006).
// If the endpoint is not reachable and no explicit env var was set, it logs a
// warning and returns a NoopTracer so the harness keeps working.
func NewTelemetry(ctx context.Context) Tracer {
	otel.SetLogger(logr.New(telemetryLogSink{}))
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {
		log.Print("[telemetry] span export failed")
	}))
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		endpoint = "http://localhost:6006"
	}

	host, insecure, err := parseOTLPEndpoint(endpoint)
	if err != nil {
		log.Print("[telemetry] invalid OTLP endpoint; using noop")
		return &NoopTracer{}
	}

	explicit := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
	if !explicit && !isReachable(host) {
		log.Printf("[telemetry] Phoenix not reachable at %s; traces disabled (set OTEL_EXPORTER_OTLP_ENDPOINT to override)", host)
		return &NoopTracer{}
	}

	opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(host)}
	if insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		log.Print("[telemetry] OTLP exporter creation failed; using noop")
		return &NoopTracer{}
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", "code-agent"),
			attribute.String("service.target", "code-agent-harness"),
		),
	)
	if err != nil {
		log.Print("[telemetry] resource creation failed; using noop")
		return &NoopTracer{}
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)

	t := &GenAITelemetry{
		provider: provider,
		tracer:   provider.Tracer("code-agent"),
	}
	log.Printf("[telemetry] OTLP exporter ready, sending traces to %s", host)
	return t
}

// StartSpan creates a new gen_ai span with the standard attribute set. The
// returned context carries the new span and can be passed to child-span
// helpers.
func (t *GenAITelemetry) StartSpan(ctx context.Context, name string, operation string, provider string) (context.Context, Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, sp := t.tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindInternal))
	attrs := GenAIAttributes(operation, provider)
	attrs = append(attrs, EvalJoinAttributes(ctx)...)
	sp.SetAttributes(attrs...)
	return ctx, &otelSpan{s: sp}
}

// Shutdown flushes pending spans and tears down the exporter.
func (t *GenAITelemetry) Shutdown(ctx context.Context) error {
	if t.provider == nil {
		return nil
	}
	return t.provider.Shutdown(ctx)
}

// ---------------------------------------------------------------------------
// Noop tracer — used when OTel is unavailable
// ---------------------------------------------------------------------------

// NoopTracer implements Tracer as a silent no-op so the harness never crashes
// because of missing OTel infrastructure.
type NoopTracer struct{}

func (n *NoopTracer) StartSpan(ctx context.Context, name string, operation string, provider string) (context.Context, Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	return ctx, &noopSpan{}
}

func (n *NoopTracer) Shutdown(ctx context.Context) error { return nil }

type noopSpan struct{}

func (n *noopSpan) End()                                      {}
func (n *noopSpan) SetAttributes(attrs ...attribute.KeyValue) {}
func (n *noopSpan) RecordError(err error)                     {}
func (n *noopSpan) AddEvent(name string)                      {}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

// parseOTLPEndpoint splits an URL like http://localhost:6006 into a host:port
// and an insecure flag.
func parseOTLPEndpoint(raw string) (host string, insecure bool, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("parse endpoint URL: %w", err)
	}
	switch u.Scheme {
	case "http":
		insecure = true
	case "https":
		insecure = false
	case "":
		return "", false, fmt.Errorf("endpoint %q has no scheme (use http:// or https://)", raw)
	default:
		return "", false, fmt.Errorf("unsupported scheme %q in endpoint %q", u.Scheme, raw)
	}
	host = u.Host
	if host == "" {
		return "", false, fmt.Errorf("endpoint %q has no host", raw)
	}
	return host, insecure, nil
}

// isReachable probes the endpoint with a brief TCP dial.
func isReachable(host string) bool {
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(context.Background(), "tcp", host)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
