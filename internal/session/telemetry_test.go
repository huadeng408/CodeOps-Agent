package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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

type rejectedTraceLeaseLog struct {
	EventLog
	rejectBlock bool
	blocked     chan struct{}
}

func (l rejectedTraceLeaseLog) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	if kind == runHeartbeatEventType {
		return Event{}, errors.New("fixture root persistence failure")
	}
	if kind == "memory/commit-blocked" && l.rejectBlock {
		close(l.blocked)
		return Event{}, errors.New("fixture blocking receipt failure")
	}
	return l.EventLog.Append(ctx, id, seq, kind, payload)
}

func TestRootPersistenceFailureDoesNotCallModelsOrMemory(t *testing.T) {
	for _, rejectBlock := range []bool{false, true} {
		t.Run(fmt.Sprintf("block-write-fails=%t", rejectBlock), func(t *testing.T) {
			ctx := context.Background()
			tracer := newSessionTelemetryTracer(tracetest.NewSpanRecorder())
			defer tracer.Shutdown(ctx)
			ledger := openWorkbenchTestLedger(t)
			blocked := make(chan struct{})
			workbench := NewWorkbench(rejectedTraceLeaseLog{EventLog: ledger, rejectBlock: rejectBlock, blocked: blocked}, nil)
			created, err := workbench.Create(ctx, 7, "repo", "failed root", "")
			if err != nil {
				t.Fatal(err)
			}
			conversation := &recordingConversationAdapter{reply: "must not run"}
			memory := &tracedCommitMemory{tracer: tracer, committed: make(chan context.Context, 1)}
			runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{Tracer: tracer, Memory: memory})
			defer runner.Close()
			run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "failed-root", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "work", Actor: identity.Default()})
			if err != nil {
				t.Fatal(err)
			}
			if rejectBlock {
				select {
				case <-blocked:
				case <-time.After(5 * time.Second):
					t.Fatal("root failure did not attempt its durable block")
				}
			} else {
				waitForRunStatus(t, runner, created.ID, run.RunID, RunFailed)
			}
			if err := runner.Close(); err != nil {
				t.Fatal(err)
			}
			if len(conversation.requests) != 0 || len(memory.committed) != 0 {
				t.Fatalf("failed pre-dispatch proof called conversation=%d memory=%d", len(conversation.requests), len(memory.committed))
			}
			if rejectBlock {
				events, err := ledger.Events(ctx, created.ID)
				if err != nil {
					t.Fatal(err)
				}
				projection, err := projectRun(events, run.RunID)
				if err != nil || projection.terminal || projection.traceContext != nil {
					t.Fatalf("failed block invented a terminal/root: %+v %v", projection, err)
				}
				return
			}
			recovered := NewSessionRunner(NewWorkbench(ledger, nil), conversation, nil, SessionRunnerOptions{Tracer: tracer, Memory: memory})
			defer recovered.Close()
			if err := recovered.RecoverSession(ctx, 7, created.ID); err != nil {
				t.Fatal(err)
			}
			select {
			case <-memory.committed:
				t.Fatal("reopening bypassed the durable pre-dispatch failure")
			case <-time.After(100 * time.Millisecond):
			}
			events, err := ledger.Events(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state, _, found := latestMemoryCommitState(events, run.RunID); !found || state != "permanently_blocked" {
				t.Fatalf("no durable Memory stop after failed root: %q", state)
			}
		})
	}
}

func TestCancellationKeepsDurableAgentRoot(t *testing.T) {
	ctx := context.Background()
	recorder := tracetest.NewSpanRecorder()
	tracer := newSessionTelemetryTracer(recorder)
	defer tracer.Shutdown(ctx)
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "repo", "cancel trace", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation := repeatedBlockingConversation{started: make(chan string, 1)}
	runner := NewSessionRunner(workbench, conversation, nil, SessionRunnerOptions{Tracer: tracer})
	defer runner.Close()
	actor := identity.Default()
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "traced-turn", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "work", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not start")
	}
	before, err := ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectRun(before.Events, run.RunID)
	if err != nil || projection.traceContext == nil {
		t.Errorf("active run has no durable root: %v", err)
	}
	if _, err := runner.CancelRun(ctx, CancelRunCommand{RequestID: "traced-cancel", SessionID: created.ID, RunID: run.RunID, OwnerID: 7, Actor: actor}); err != nil {
		t.Fatal(err)
	}
	waitForRunStatus(t, runner, created.ID, run.RunID, RunCanceled)
	after, err := ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, event := range before.Events {
		if event.Checksum != after.Events[i].Checksum {
			t.Fatal("cancellation changed original native prefix")
		}
	}
	var terminal runTerminalPayload
	for _, event := range after.Events {
		if event.Type == runCanceledEventType {
			if err := json.Unmarshal(event.Payload, &terminal); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, span := range recorder.Ended() {
		if span.Name() == "agent.main" {
			sc := span.SpanContext()
			want := fmt.Sprintf("00-%s-%s-%s", sc.TraceID(), sc.SpanID(), sc.TraceFlags())
			if terminal.TraceContext == nil || terminal.TraceContext.TraceParent != want || terminal.TraceContext.SessionID != created.ID || projection.traceContext == nil || projection.traceContext.TraceParent != want {
				t.Fatalf("canceled terminal and durable lease must retain actual agent root: %+v", terminal.TraceContext)
			}
			return
		}
	}
	t.Fatal("actual agent root was not exported")
}

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

	requiredSpans := []string{"agent.main", "agent.subagent", "execute_tool PublishArtifact", "artifact.publish"}
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
	toolSpan := spans["execute_tool PublishArtifact"]
	if childSpan.Parent().SpanID() != mainSpan.SpanContext().SpanID() {
		t.Fatalf("subagent parent = %s, want %s", childSpan.Parent().SpanID(), mainSpan.SpanContext().SpanID())
	}
	if toolSpan.Parent().SpanID() != childSpan.SpanContext().SpanID() {
		t.Fatalf("tool parent = %s, want %s", toolSpan.Parent().SpanID(), childSpan.SpanContext().SpanID())
	}
	if artifactSpan.Parent().SpanID() != toolSpan.SpanContext().SpanID() {
		t.Fatalf("artifact parent = %s, want %s", artifactSpan.Parent().SpanID(), toolSpan.SpanContext().SpanID())
	}
	for _, span := range []sdktrace.ReadOnlySpan{mainSpan, childSpan, toolSpan, artifactSpan} {
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
	if got := spanStringAttribute(mainSpan, "session.id"); got != created.ID {
		t.Fatalf("main session.id = %q, want %q", got, created.ID)
	}
	if got := spanStringAttribute(mainSpan, "session.run_id"); got != run.RunID {
		t.Fatalf("main session.run_id = %q, want %q", got, run.RunID)
	}
	assertSpanHasNoSentinel(t, mainSpan)
}

type tracedCommitMemory struct {
	tracer        genai.Tracer
	committed     chan context.Context
	waitForCancel bool
	err           error
}

func (m *tracedCommitMemory) Commit(ctx context.Context, _ string) error {
	_, span := m.tracer.StartSpan(ctx, "memory.reflect", genai.OperationInvokeAgent, genai.SystemGenAI)
	defer span.End()
	m.committed <- ctx
	if m.waitForCancel {
		<-ctx.Done()
		return ctx.Err()
	}
	return m.err
}

func (*tracedCommitMemory) Recall(context.Context, uint, string, int) (string, error) {
	return "", nil
}

func TestRunnerReflectionKeepsTerminalRunTrace(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		err           error
		status        RunStatus
		waitForCancel bool
	}{
		{"completed", nil, RunCompleted, false},
		{"failed", errors.New("tool round limit reached; retry this task"), RunFailed, false},
		{"failed-shutdown", errors.New("tool round limit reached; retry this task"), RunFailed, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			tracer := newSessionTelemetryTracer(recorder)
			t.Cleanup(func() { _ = tracer.Shutdown(context.Background()) })
			workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
			created, err := workbench.Create(context.Background(), 7, "repo", "reflection trace", "")
			if err != nil {
				t.Fatal(err)
			}
			memory := &tracedCommitMemory{tracer: tracer, committed: make(chan context.Context, 1), waitForCancel: scenario.waitForCancel}
			runner := NewSessionRunner(workbench, &recordingConversationAdapter{reply: "done", err: scenario.err}, nil, SessionRunnerOptions{
				Tracer: tracer, Memory: memory, MemoryCommitTimeout: 5 * time.Second,
			})
			t.Cleanup(func() { _ = runner.Close() })
			ctx := genai.WithEvalJoinBaggage(context.Background(), "reflection-eval-run", created.ID)
			run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{
				RequestID: "reflection-trace", SessionID: created.ID, OwnerID: 7,
				ExpectedSeq: int64(created.EventCount), Content: "complete the task", Actor: identity.Default(),
			})
			if err != nil {
				t.Fatal(err)
			}
			waitForRunStatus(t, runner, created.ID, run.RunID, scenario.status)
			select {
			case commitCtx := <-memory.committed:
				if _, ok := commitCtx.Deadline(); !ok {
					t.Fatal("reflection lost its independent deadline")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("terminal run did not commit Memory")
			}
			closed := make(chan error, 1)
			go func() { closed <- runner.Close() }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("reflection did not stop with runner shutdown")
			}
			var mainSpan, reflection sdktrace.ReadOnlySpan
			for _, span := range recorder.Ended() {
				switch span.Name() {
				case "agent.main":
					mainSpan = span
				case "memory.reflect":
					reflection = span
				}
			}
			if mainSpan == nil || reflection == nil {
				t.Fatalf("missing terminal spans: %v", spanNames(recorder.Ended()))
			}
			if reflection.Parent().SpanID() != mainSpan.SpanContext().SpanID() || reflection.SpanContext().TraceID() != mainSpan.SpanContext().TraceID() {
				t.Fatalf("reflection became a separate root: parent=%s trace=%s, want agent=%s trace=%s", reflection.Parent().SpanID(), reflection.SpanContext().TraceID(), mainSpan.SpanContext().SpanID(), mainSpan.SpanContext().TraceID())
			}
			if got := spanStringAttribute(reflection, genai.AttrEvalRunID); got != "reflection-eval-run" {
				t.Fatalf("reflection lost admitted run identity: %q", got)
			}
			if scenario.status == RunFailed && mainSpan.Status().Code != codes.Error {
				t.Fatal("failed Agent outcome was erased")
			}
		})
	}
}

func TestRecoveredReflectionKeepsTerminalRunTrace(t *testing.T) {
	for _, scenario := range []struct {
		name           string
		err            error
		status         RunStatus
		legacy         bool
		noop           bool
		cancelRecovery bool
	}{
		{"completed", nil, RunCompleted, false, false, false},
		{"failed", errors.New("tool round limit reached; retry this task"), RunFailed, false, false, false},
		{"legacy", nil, RunCompleted, true, false, false},
		{"noop", nil, RunCompleted, true, true, false},
		{"failed-shutdown", errors.New("tool round limit reached; retry this task"), RunFailed, false, false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			firstTracer := newSessionTelemetryTracer(recorder)
			t.Cleanup(func() { _ = firstTracer.Shutdown(context.Background()) })
			ledgerPath := filepath.Join(t.TempDir(), "sessions.sqlite")
			ledger, err := OpenSQLiteEventLog(ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ledger.Close() })
			workbench := NewWorkbench(ledger, nil)
			created, err := workbench.Create(context.Background(), 7, "repo", "recovered reflection trace", "")
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			firstMemory := &tracedCommitMemory{tracer: firstTracer, committed: make(chan context.Context, 1), err: errors.New("reflection unavailable")}
			options := SessionRunnerOptions{Tracer: firstTracer, Memory: firstMemory, Now: func() time.Time { return now }}
			if scenario.legacy {
				options.Tracer = nil
			}
			if scenario.noop {
				options.Tracer = &genai.NoopTracer{}
			}
			first := NewSessionRunner(workbench, &recordingConversationAdapter{reply: "done", err: scenario.err}, nil, options)
			t.Cleanup(func() { _ = first.Close() })
			ctx := genai.WithEvalJoinBaggage(context.Background(), "recovered-reflection-eval", created.ID)
			var external genai.Span
			if scenario.legacy {
				ctx, external = firstTracer.StartSpan(ctx, "external-request", genai.OperationInvokeAgent, genai.SystemGenAI)
			}
			run, err := first.SubmitMessage(ctx, SubmitMessageCommand{
				RequestID: "recovered-reflection-trace", SessionID: created.ID, OwnerID: 7,
				ExpectedSeq: int64(created.EventCount), Content: "complete the task", Actor: identity.Default(),
			})
			if err != nil {
				t.Fatal(err)
			}
			waitForRunStatus(t, first, created.ID, run.RunID, scenario.status)
			select {
			case <-firstMemory.committed:
			case <-time.After(3 * time.Second):
				t.Fatal("initial terminal reflection did not run")
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			if external != nil {
				external.End()
			}
			if err := firstTracer.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := ledger.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenSQLiteEventLog(ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })

			recoveryTracer := newSessionTelemetryTracer(recorder)
			t.Cleanup(func() { _ = recoveryTracer.Shutdown(context.Background()) })
			memory := &tracedCommitMemory{tracer: recoveryTracer, committed: make(chan context.Context, 1), waitForCancel: scenario.cancelRecovery}
			conversation := &recordingConversationAdapter{reply: "must not replay"}
			recovered := NewSessionRunner(NewWorkbench(reopened, nil), conversation, nil, SessionRunnerOptions{
				Tracer: recoveryTracer, Memory: memory, MemoryCommitTimeout: 5 * time.Second,
				Now: func() time.Time { return now.Add(time.Hour) },
			})
			t.Cleanup(func() { _ = recovered.Close() })
			if err := recovered.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-memory.committed:
			case <-time.After(3 * time.Second):
				t.Fatal("terminal reflection was not recovered")
			}
			closed := make(chan error, 1)
			go func() { closed <- recovered.Close() }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("recovered reflection did not stop with its runner")
			}
			waitForRunStatus(t, recovered, created.ID, run.RunID, scenario.status)
			conversation.mu.Lock()
			requests := len(conversation.requests)
			conversation.mu.Unlock()
			if requests != 0 {
				t.Fatal("Memory repair replayed the conversation")
			}
			var main, reflection sdktrace.ReadOnlySpan
			for _, span := range recorder.Ended() {
				if span.Name() == "agent.main" {
					main = span
				} else if span.Name() == "memory.reflect" {
					reflection = span
				}
			}
			if reflection == nil {
				t.Fatal("recovered reflection span is missing")
			}
			if scenario.legacy {
				if main != nil || reflection.Parent().IsValid() {
					t.Fatal("legacy recovery invented an Agent parent")
				}
				return
			}
			if main == nil || reflection.Parent().SpanID() != main.SpanContext().SpanID() || reflection.SpanContext().TraceID() != main.SpanContext().TraceID() {
				t.Fatal("recovered reflection lost its terminal Agent parent")
			}
			if got := spanStringAttribute(reflection, genai.AttrEvalRunID); got != "recovered-reflection-eval" {
				t.Fatalf("recovered reflection lost admitted evaluation identity: %q", got)
			}
		})
	}
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
