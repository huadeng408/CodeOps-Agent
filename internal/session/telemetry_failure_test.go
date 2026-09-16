package session

import (
	"context"
	"strings"
	"testing"

	"code-agent/internal/orchestrator"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestIndependentAgentAndArtifactFailuresSetContentFreeStatus(t *testing.T) {
	t.Run("agent", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		tracer := newSessionTelemetryTracer(recorder)
		t.Cleanup(func() { _ = tracer.Shutdown(context.Background()) })
		runner, _, actor := agentTestRunner(t, &independentAgentFixture{mode: "failed"})
		runner.options.Tracer = tracer

		task := agentTestCall(t, runner, actor, "trace-failure", "SpawnAgent", `{"kind":"explore","title":"trace","objective":"AGENT_FAILURE_SECRET_SENTINEL"}`)
		if task.Status != "failed" {
			t.Fatalf("task status = %s", task.Status)
		}
		assertContentFreeErrorSpan(t, recorder.Ended(), "agent.subagent")
	})

	t.Run("artifact", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		tracer := newSessionTelemetryTracer(recorder)
		t.Cleanup(func() { _ = tracer.Shutdown(context.Background()) })
		runner, _, actor := agentTestRunner(t, &independentAgentFixture{})
		runner.options.Tracer = tracer

		result := runner.ExecuteAgentTool(context.Background(), actor, actor.SessionID, orchestrator.ToolCall{
			ID: "invalid-artifact", Name: "PublishArtifact",
			ParametersJSON: `{"artifact":{"name":"ARTIFACT_FAILURE_SECRET_SENTINEL","parts":[{"text":"invalid parent"}]}}`,
		})
		if result.ExitCode == 0 || result.Error == "" {
			t.Fatalf("invalid artifact unexpectedly succeeded: %+v", result)
		}
		assertContentFreeErrorSpan(t, recorder.Ended(), "artifact.publish")
	})
}

func assertContentFreeErrorSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) {
	t.Helper()
	var found sdktrace.ReadOnlySpan
	for _, span := range spans {
		if span.Name() == name {
			found = span
			break
		}
	}
	if found == nil {
		t.Fatalf("missing %s span; ended=%v", name, spanNames(spans))
	}
	if status := found.Status(); status.Code != codes.Error || status.Description != "" {
		t.Fatalf("%s status = %+v, want content-free error", name, status)
	}
	for _, event := range found.Events() {
		if strings.Contains(event.Name, "SECRET_SENTINEL") {
			t.Fatalf("%s leaked failure text in event", name)
		}
	}
}
