package genai_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"code-agent/internal/telemetry/genai"
)

func exporterBoundaryTelemetry(t *testing.T, endpoint string) genai.Tracer {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "OTEL_") {
			t.Setenv(key, "")
		}
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "60000")
	t.Setenv("OTEL_BSP_EXPORT_TIMEOUT", "100")
	return genai.NewTelemetry(context.Background())
}

func TestTelemetryDoesNotRetryPermanentRejection(t *testing.T) {
	var requests atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer receiver.Close()
	tracer := exporterBoundaryTelemetry(t, receiver.URL)
	_, span := tracer.StartSpan(context.Background(), "rejected", genai.OperationExecuteTool, genai.SystemGenAI)
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tracer.Shutdown(ctx)
	if count := requests.Load(); count != 1 {
		t.Fatalf("permanent rejection received %d requests, want one", count)
	}
}

func TestTelemetryDoesNotRetryExpiredExport(t *testing.T) {
	var requests atomic.Int64
	canceled := make(chan struct{}, 2)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error("receiver could not read OTLP request")
			return
		}
		select {
		case <-r.Context().Done():
			canceled <- struct{}{}
		case <-time.After(3 * time.Second):
		}
	}))
	defer receiver.Close()
	tracer := exporterBoundaryTelemetry(t, receiver.URL)
	_, span := tracer.StartSpan(context.Background(), "expired", genai.OperationExecuteTool, genai.SystemGenAI)
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tracer.Shutdown(ctx)
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("export deadline did not cancel the receiver request")
	}
	if count := requests.Load(); count != 1 {
		t.Fatalf("expired export received %d requests, want one", count)
	}
}
