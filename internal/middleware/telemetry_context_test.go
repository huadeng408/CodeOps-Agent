package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceContextMiddlewareExtractsW3CContextAndEvalJoinBaggage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(TraceContextMiddleware())
	router.GET("/", func(c *gin.Context) {
		sc := trace.SpanContextFromContext(c.Request.Context())
		if !sc.IsValid() || !sc.IsRemote() {
			t.Fatalf("expected remote W3C parent, got valid=%v remote=%v", sc.IsValid(), sc.IsRemote())
		}
		if got, want := sc.TraceID().String(), "11111111111111111111111111111111"; got != want {
			t.Fatalf("trace ID = %q, want %q", got, want)
		}
		if got, want := baggage.FromContext(c.Request.Context()).Member("eval.run_id").Value(), "run-1"; got != want {
			t.Fatalf("eval.run_id = %q, want %q", got, want)
		}
		if got, want := baggage.FromContext(c.Request.Context()).Member("eval.instance_id").Value(), "instance-1"; got != want {
			t.Fatalf("eval.instance_id = %q, want %q", got, want)
		}
		if baggage.FromContext(c.Request.Context()).Member("untrusted").Value() != "" {
			t.Fatal("middleware must not propagate arbitrary baggage")
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
	req.Header.Set("baggage", "eval.run_id=run-1,eval.instance_id=instance-1,untrusted=discard-me")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusNoContent)
	}
}
