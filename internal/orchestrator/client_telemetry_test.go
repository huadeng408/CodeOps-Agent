package orchestrator

import (
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

type toolTelemetrySpan struct {
	attrs  []attribute.KeyValue
	errors []error
	events []string
	ended  bool
}

func (s *toolTelemetrySpan) End() { s.ended = true }
func (s *toolTelemetrySpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.attrs = append(s.attrs, attrs...)
}
func (s *toolTelemetrySpan) RecordError(err error) { s.errors = append(s.errors, err) }
func (s *toolTelemetrySpan) AddEvent(name string)  { s.events = append(s.events, name) }

func TestFinishToolSpanDoesNotRecordResultOrErrorText(t *testing.T) {
	span := &toolTelemetrySpan{}
	finishToolSpan(span, ToolResult{
		Output:   "TOOL_RESULT_SECRET_SENTINEL",
		Error:    "PROVIDER_CONFIG_SECRET_SENTINEL",
		ExitCode: 1,
	})
	if !span.ended {
		t.Fatal("tool span was not ended")
	}
	if len(span.attrs) != 0 || len(span.errors) != 1 || span.errors[0].Error() != "operation failed" {
		t.Fatalf("tool payload was recorded: attrs=%v errors=%v", span.attrs, span.errors)
	}
	if len(span.events) != 1 || span.events[0] != "tool.error" {
		t.Fatalf("events = %v", span.events)
	}
	for _, event := range span.events {
		if strings.Contains(event, "SECRET_SENTINEL") {
			t.Fatalf("secret leaked in event %q", event)
		}
	}
	if joined := errors.Join(span.errors...); joined == nil || strings.Contains(joined.Error(), "SECRET_SENTINEL") {
		t.Fatalf("failure marker leaked raw error: %v", joined)
	}
}
