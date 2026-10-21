package genai_test

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"code-agent/internal/telemetry/genai"
	"go.opentelemetry.io/otel/trace"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestTelemetryDeliversOriginalSpanAfterConnectionLoss(t *testing.T) {
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "OTEL_") {
			t.Setenv(key, "")
		}
	}
	var mu sync.Mutex
	requests := 0
	delivered := make(map[string]bool)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error("receiver could not read OTLP request")
			return
		}
		mu.Lock()
		requests++
		first := requests == 1
		mu.Unlock()
		if first {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error("receiver could not inject connection loss")
				return
			}
			_ = connection.Close()
			return
		}
		var exported collector.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &exported); err != nil {
			t.Error("receiver got malformed OTLP payload")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		for _, resource := range exported.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					delivered[hex.EncodeToString(span.TraceId)+"/"+hex.EncodeToString(span.SpanId)] = true
				}
			}
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", receiver.URL)
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "1")
	tracer := genai.NewTelemetry(context.Background())
	ctx, span := tracer.StartSpan(context.Background(), "execute_tool Read", genai.OperationExecuteTool, genai.SystemGenAI)
	identity := trace.SpanContextFromContext(ctx)
	want := identity.TraceID().String() + "/" + identity.SpanID().String()
	span.End()
	flush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownErr := tracer.Shutdown(flush)
	mu.Lock()
	defer mu.Unlock()
	t.Logf("OTLP requests=%d, delivered span identities=%d", requests, len(delivered))
	if shutdownErr != nil || !delivered[want] || len(delivered) != 1 {
		t.Fatalf("original span was not delivered after the receiver recovered (shutdown error=%t)", shutdownErr != nil)
	}
}
