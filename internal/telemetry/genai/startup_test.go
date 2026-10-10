package genai

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTelemetryRejectsInvalidEndpointWithoutLoggingCredentials(t *testing.T) {
	const secret = "fixture-trace-credential"
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://"+secret+"@invalid%host")
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	tracer := NewTelemetry(context.Background())
	if _, ok := tracer.(*NoopTracer); !ok {
		t.Fatal("invalid trace endpoint was admitted")
	}
	if strings.Contains(output.String(), secret) {
		t.Fatal("trace startup diagnostic exposed endpoint credentials")
	}
}

func TestTelemetrySDKDiagnosticsDoNotExposeCredentials(t *testing.T) {
	if os.Getenv("CODE_AGENT_TRACE_DIAGNOSTIC_CHILD") == "1" {
		tracer := NewTelemetry(context.Background())
		_, span := tracer.StartSpan(context.Background(), "diagnostic", "test", "fixture")
		span.End()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tracer.Shutdown(ctx)
		return
	}
	const secret = "fixture-otel-sdk-auth"
	for _, name := range []string{"malformed-header", "rejected-export"} {
		t.Run(name, func(t *testing.T) {
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if name == "rejected-export" {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(secret))
				}
			}))
			defer receiver.Close()
			header := secret
			if name == "rejected-export" {
				header = "Authorization=" + secret
			}
			command := exec.Command(os.Args[0], "-test.run=^TestTelemetrySDKDiagnosticsDoNotExposeCredentials$")
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(entry), "OTEL_") {
					command.Env = append(command.Env, entry)
				}
			}
			command.Env = append(command.Env, "CODE_AGENT_TRACE_DIAGNOSTIC_CHILD=1", "OTEL_EXPORTER_OTLP_ENDPOINT="+receiver.URL, "OTEL_EXPORTER_OTLP_HEADERS="+header)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatal("trace diagnostic child failed")
			}
			if bytes.Contains(output, []byte(secret)) {
				t.Fatal("SDK diagnostic exposed a credential")
			}
		})
	}
}
