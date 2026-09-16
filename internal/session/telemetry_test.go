package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/telemetry/genai"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type sessionTelemetryTracer struct {
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
}

type sessionTelemetrySpan struct{ span trace.Span }

func newSessionTelemetryTracer(recorder *tracetest.SpanRecorder) *sessionTelemetryTracer {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	return &sessionTelemetryTracer{provider: provider, tracer: provider.Tracer("session-test")}
}

func (t *sessionTelemetryTracer) StartSpan(ctx context.Context, name, operation, provider string) (context.Context, genai.Span) {
	ctx, span := t.tracer.Start(ctx, name)
	attrs := genai.GenAIAttributes(operation, provider)
	attrs = append(attrs, genai.EvalJoinAttributes(ctx)...)
	span.SetAttributes(attrs...)
	return ctx, &sessionTelemetrySpan{span: span}
}

func (t *sessionTelemetryTracer) Shutdown(ctx context.Context) error {
	return t.provider.Shutdown(ctx)
}

func (s *sessionTelemetrySpan) End() { s.span.End() }

func (s *sessionTelemetrySpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.span.SetAttributes(attrs...)
}

func (s *sessionTelemetrySpan) RecordError(err error) { s.span.RecordError(err) }
func (s *sessionTelemetrySpan) AddEvent(name string)  { s.span.AddEvent(name) }
func (s *sessionTelemetrySpan) SetStatus(code codes.Code, description string) {
	s.span.SetStatus(code, description)
}

func TestIndependentAgentTraceKeepsAsyncParentAndConcreteSession(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tracer := newSessionTelemetryTracer(recorder)
	t.Cleanup(func() { _ = tracer.Shutdown(context.Background()) })

	runner, parent, actor := agentTestRunner(t, &independentAgentFixture{})
	runner.options.Tracer = tracer
	ctx := genai.WithEvalJoinBaggage(context.Background(), "run-telemetry-1", parent.ID)
	ctx, root := tracer.StartSpan(ctx, "agent.main", genai.OperationInvokeAgent, genai.SystemGenAI)
	root.SetAttributes(genai.AgentNameKV("main"))
	result := runner.ExecuteAgentTool(ctx, actor, actor.SessionID, orchestrator.ToolCall{
		ID:   "trace-spawn",
		Name: "SpawnAgent",
		ParametersJSON: `{"kind":"explore","title":"trace","objective":"PROMPT_SECRET_SENTINEL",` +
			`"context":{"private":"TOOL_ARGUMENT_SECRET_SENTINEL"}}`,
	})
	root.End()
	if result.Error != "" || result.ExitCode != 0 {
		t.Fatalf("spawn result = %+v", result)
	}
	task, _, err := runner.readAgentTask(context.Background(), 7, "agent-"+agentDigest([]byte(actor.SessionID + "\x00trace-spawn"))[:32], actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}

	requiredSpans := []string{"agent.main", "agent.subagent", "artifact.publish"}
	spans := map[string]sdktrace.ReadOnlySpan{}
	deadline := time.Now().Add(time.Second)
	for {
		for _, span := range recorder.Ended() {
			spans[span.Name()] = span
		}
		complete := true
		for _, name := range requiredSpans {
			complete = complete && spans[name] != nil
		}
		if complete || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for _, name := range requiredSpans {
		if spans[name] == nil {
			t.Fatalf("missing %s span; ended=%v", name, spanNames(recorder.Ended()))
		}
	}
	mainSpan, childSpan, artifactSpan := spans["agent.main"], spans["agent.subagent"], spans["artifact.publish"]
	if childSpan.Parent().SpanID() != mainSpan.SpanContext().SpanID() {
		t.Fatalf("subagent parent = %s, want %s", childSpan.Parent().SpanID(), mainSpan.SpanContext().SpanID())
	}
	if artifactSpan.Parent().SpanID() != childSpan.SpanContext().SpanID() {
		t.Fatalf("artifact parent = %s, want %s", artifactSpan.Parent().SpanID(), childSpan.SpanContext().SpanID())
	}
	for _, span := range []sdktrace.ReadOnlySpan{mainSpan, childSpan, artifactSpan} {
		if span.SpanContext().TraceID() != mainSpan.SpanContext().TraceID() {
			t.Fatalf("%s trace ID = %s, want %s", span.Name(), span.SpanContext().TraceID(), mainSpan.SpanContext().TraceID())
		}
		if got := spanStringAttribute(span, genai.AttrEvalRunID); got != "run-telemetry-1" {
			t.Fatalf("%s eval.run_id = %q", span.Name(), got)
		}
		wantInstance := parent.ID
		if span.Name() != "agent.main" {
			wantInstance = task.ChildSessionId
		}
		if got := spanStringAttribute(span, genai.AttrEvalInstanceID); got != wantInstance {
			t.Fatalf("%s eval.instance_id = %q, want %q", span.Name(), got, wantInstance)
		}
		assertSpanHasNoSentinel(t, span)
	}
}

func TestMainAgentTraceKeepsQueuedRequestParent(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tracer := newSessionTelemetryTracer(recorder)
	t.Cleanup(func() { _ = tracer.Shutdown(context.Background()) })
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(context.Background(), 7, "repo", "trace", "goal")
	if err != nil {
		t.Fatal(err)
	}
	runner := NewSessionRunner(workbench, &recordingConversationAdapter{reply: "done"}, nil, SessionRunnerOptions{
		WorkerID: "trace-main", WorkerCount: 1, AgentWorkerCount: 1, Tracer: tracer,
	})
	t.Cleanup(func() { _ = runner.Close() })
	parentCtx, transport := tracer.tracer.Start(context.Background(), "transport.request")
	parentCtx = genai.WithEvalJoinBaggage(parentCtx, "run-main-1", "caller-instance")
	run, err := runner.SubmitMessage(parentCtx, SubmitMessageCommand{
		RequestID: "trace-main-request", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: int64(created.EventCount), Content: "PROMPT_SECRET_SENTINEL", Actor: identity.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, runner, created.ID, run.RunID, RunCompleted)
	transport.End()

	var mainSpan, transportSpan sdktrace.ReadOnlySpan
	deadline := time.Now().Add(time.Second)
	for mainSpan == nil && time.Now().Before(deadline) {
		for _, span := range recorder.Ended() {
			switch span.Name() {
			case "agent.main":
				mainSpan = span
			case "transport.request":
				transportSpan = span
			}
		}
		if mainSpan == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if mainSpan == nil || transportSpan == nil {
		t.Fatalf("ended spans = %v", spanNames(recorder.Ended()))
	}
	if mainSpan.Parent().SpanID() != transportSpan.SpanContext().SpanID() {
		t.Fatalf("main parent = %s, want %s", mainSpan.Parent().SpanID(), transportSpan.SpanContext().SpanID())
	}
	if got := spanStringAttribute(mainSpan, genai.AttrEvalInstanceID); got != created.ID {
		t.Fatalf("main eval.instance_id = %q, want %q", got, created.ID)
	}
	if got := spanStringAttribute(mainSpan, genai.AttrEvalRunID); got != "run-main-1" {
		t.Fatalf("main eval.run_id = %q", got)
	}
	assertSpanHasNoSentinel(t, mainSpan)
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.Name())
	}
	return names
}

func spanStringAttribute(span sdktrace.ReadOnlySpan, key string) string {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	return ""
}

func assertSpanHasNoSentinel(t *testing.T, span sdktrace.ReadOnlySpan) {
	t.Helper()
	for _, attr := range span.Attributes() {
		value := attr.Value.Emit()
		if strings.Contains(value, "SECRET_SENTINEL") {
			t.Fatalf("%s leaked sentinel in attribute %s", span.Name(), attr.Key)
		}
	}
	for _, event := range span.Events() {
		if strings.Contains(event.Name, "SECRET_SENTINEL") {
			t.Fatalf("%s leaked sentinel in event name", span.Name())
		}
		for _, attr := range event.Attributes {
			if strings.Contains(attr.Value.Emit(), "SECRET_SENTINEL") {
				t.Fatalf("%s leaked sentinel in event attribute %s", span.Name(), attr.Key)
			}
		}
	}
}
