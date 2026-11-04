package admission

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/session"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type cleanupRoundTripper func(*http.Request) (*http.Response, error)

func (transport cleanupRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func cleanupLedger(t *testing.T) *session.SQLiteEventLog {
	t.Helper()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	return ledger
}

func cleanupGateway(t *testing.T, ledger session.EventLog) (*Gateway, *pb.ModelCallRequest) {
	t.Helper()
	gateway := NewGateway(NewBudget(ledger), ProviderConfig{
		Protocol: "openai", BaseURL: "https://fixture.invalid/v1", APIKey: "fixture-only", Model: "fixture-model", InputLimit: 100, OutputLimit: 3,
	})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	binding, release, err := gateway.Bind(7, "cleanup-task", "cleanup-session")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return gateway, &pb.ModelCallRequest{
		Capability: binding.Capability, CallId: "cleanup-call", PayloadJson: []byte(`{"model":"fixture-model","messages":[]}`),
	}
}

type afterReservationLedger struct {
	session.EventLog
	cancel    context.CancelFunc
	readError error
	committed bool
	appends   int
}

func (ledger *afterReservationLedger) Append(ctx context.Context, id string, seq int64, kind string, payload any) (session.Event, error) {
	event, err := ledger.EventLog.Append(ctx, id, seq, kind, payload)
	if err == nil && kind == "admission/call_reserved" {
		ledger.committed = true
		ledger.appends++
		if ledger.cancel != nil {
			ledger.cancel()
		}
	}
	return event, err
}

func (ledger *afterReservationLedger) Events(ctx context.Context, id string) ([]session.Event, error) {
	if ledger.committed && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if ledger.committed && ledger.readError != nil {
		return nil, ledger.readError
	}
	return ledger.EventLog.Events(ctx, id)
}

func TestReserveReturnsCommittedBatchWithoutAnotherLedgerRead(t *testing.T) {
	for _, scenario := range []string{"caller canceled", "read unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			persisted := cleanupLedger(t)
			budget := NewBudget(persisted)
			ctx := context.Background()
			if _, err := budget.Reserve(ctx, 7, "cleanup-task", "known-call", 20); err != nil {
				t.Fatal(err)
			}
			if _, err := budget.Settle(ctx, 7, "known-call", 11); err != nil {
				t.Fatal(err)
			}
			if _, err := budget.Reserve(ctx, 7, "cleanup-task", "unknown-call", 30); err != nil {
				t.Fatal(err)
			}
			before, err := budget.MarkUnknown(ctx, 7, "unknown-call")
			if err != nil {
				t.Fatal(err)
			}
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			ledger := &afterReservationLedger{EventLog: persisted}
			if scenario == "caller canceled" {
				ledger.cancel = cancel
			} else {
				ledger.readError = errors.New("fixture post-commit read unavailable")
			}
			batch, err := NewBudget(ledger).Reserve(callCtx, 7, "cleanup-task", "cleanup-call", 103)
			if err != nil || ledger.appends != 1 || batch.ID != before.ID || batch.UsedTokens != 11 || batch.ReservedTokens != 133 || batch.ActiveCalls != 1 || !batch.UnknownUsage || batch.CostStatus != "unknown" || batch.TaskID != "cleanup-task" || batch.TaskOwnerID != 7 {
				t.Fatalf("committed reservation was hidden or changed accounting: batch=%+v appends=%d err=%v", batch, ledger.appends, err)
			}
			actual, err := budget.Inspect(ctx, 7)
			if err != nil || actual != batch {
				t.Fatalf("returned Batch differs from committed Ledger: actual=%+v returned=%+v err=%v", actual, batch, err)
			}
			if _, err := budget.Reserve(ctx, 7, "cleanup-task", "cleanup-call", 103); !errors.Is(err, ErrCallReserved) {
				t.Fatalf("committed reservation permitted a second admission: %v", err)
			}
		})
	}
}

func TestGatewayFinalizesReservationCanceledImmediatelyAfterCommit(t *testing.T) {
	persisted := cleanupLedger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ledger := &afterReservationLedger{EventLog: persisted, cancel: cancel}
	gateway, request := cleanupGateway(t, ledger)
	var dispatched atomic.Int32
	gateway.http.Transport = cleanupRoundTripper(func(request *http.Request) (*http.Response, error) {
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		dispatched.Add(1)
		return nil, errors.New("unexpected uncanceled dispatch")
	})
	response, err := gateway.Invoke(ctx, request)
	if err != nil || response.GetErrorCode() != "provider_transport_unknown" || ledger.appends != 1 || dispatched.Load() != 0 {
		t.Fatalf("post-commit cancellation skipped cleanup or dispatched: response=%+v appends=%d dispatched=%d err=%v", response, ledger.appends, dispatched.Load(), err)
	}
	batch, err := NewBudget(persisted).Inspect(context.Background(), 7)
	if err != nil || batch.ActiveCalls != 0 || !batch.UnknownUsage || batch.ReservedTokens != 103 || batch.UsedTokens != 0 || batch.CostStatus != "unknown" {
		t.Fatalf("post-commit cancellation lost retained unknown accounting: %+v %v", batch, err)
	}
}

func TestGatewayFinalizesAfterLateTransportReturn(t *testing.T) {
	ledger := cleanupLedger(t)
	gateway, request := cleanupGateway(t, ledger)
	var calls atomic.Int32
	gateway.http.Transport = cleanupRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		deadline, ok := request.Context().Deadline()
		if !ok {
			return nil, errors.New("fixture transport has no deadline")
		}
		<-request.Context().Done()
		// Return after the old caller-deadline-plus-five-seconds audit cap.
		timer := time.NewTimer(time.Until(deadline.Add(5100 * time.Millisecond)))
		defer timer.Stop()
		<-timer.C
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := gateway.Invoke(ctx, request)
	if err != nil || response.GetErrorCode() != "provider_timeout_unknown" || calls.Load() != 1 {
		t.Fatalf("late transport changed the outcome or retried: response=%+v calls=%d err=%v", response, calls.Load(), err)
	}
	batch, err := gateway.budget.Inspect(context.Background(), 7)
	if err != nil || batch.ActiveCalls != 0 || !batch.UnknownUsage || batch.ReservedTokens != 103 || batch.UsedTokens != 0 {
		t.Fatalf("late cleanup lost its fresh audit window or reservation: %+v %v", batch, err)
	}
}

func TestGatewayEachRetryGetsItsOwnFinalizationWindow(t *testing.T) {
	ledger := cleanupLedger(t)
	gateway, request := cleanupGateway(t, ledger)
	var calls atomic.Int32
	gateway.http.Transport = cleanupRoundTripper(func(request *http.Request) (*http.Response, error) {
		response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: request,
			Body: io.NopCloser(strings.NewReader(`{"model":"fixture-model","usage":{"prompt_tokens":8,"completion_tokens":3}}`))}
		if calls.Add(1) == 1 {
			response.StatusCode = http.StatusTooManyRequests
			response.Header.Set("Retry-After", "6")
		}
		return response, nil
	})
	response, err := gateway.Invoke(context.Background(), request)
	if err != nil || response.GetErrorCode() != "" || calls.Load() != 2 || response.GetInputTokens() != 16 || response.GetOutputTokens() != 6 {
		t.Fatalf("a retry inherited an expired accounting window: response=%+v calls=%d err=%v", response, calls.Load(), err)
	}
	batch, err := gateway.budget.Inspect(context.Background(), 7)
	if err != nil || batch.ActiveCalls != 0 || batch.UnknownUsage || batch.ReservedTokens != 0 || batch.UsedTokens != 22 {
		t.Fatalf("retry settlement lost its complete accounting: %+v %v", batch, err)
	}
}

type failedUnknownLedger struct{ session.EventLog }

func (ledger failedUnknownLedger) Append(ctx context.Context, id string, seq int64, kind string, payload any) (session.Event, error) {
	if kind == "admission/call_unknown" {
		return session.Event{}, errors.New("fixture-secret-never-log-this")
	}
	return ledger.EventLog.Append(ctx, id, seq, kind, payload)
}

func TestGatewayReportsUnknownFinalizationFailureAndKeepsFence(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		_ = provider.Shutdown(context.Background())
	})
	ledger := cleanupLedger(t)
	gateway, request := cleanupGateway(t, failedUnknownLedger{ledger})
	gateway.http.Transport = cleanupRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("fixture transport stopped")
	})
	var output bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previousOutput) })
	response, err := gateway.Invoke(context.Background(), request)
	if err != nil || response.GetErrorCode() != "provider_transport_unknown" {
		t.Fatalf("accounting failure changed the transport outcome: %+v %v", response, err)
	}
	if !strings.Contains(output.String(), "model finalization failed: stage=unknown_accounting") || strings.Contains(output.String(), "fixture-secret-never-log-this") {
		t.Fatalf("missing safe accounting failure diagnostic: %q", output.String())
	}
	failureMarked := false
	for _, span := range recorder.Ended() {
		if span.Name() != "provider.attempt" {
			continue
		}
		for _, attribute := range span.Attributes() {
			if attribute.Key == "model.finalization_failed" && attribute.Value.AsBool() {
				failureMarked = true
			}
		}
	}
	if !failureMarked {
		t.Fatal("ended attempt span lost the accounting failure marker")
	}
	before, err := gateway.budget.Inspect(context.Background(), 7)
	if err != nil || before.ActiveCalls != 1 || before.UnknownUsage || before.ReservedTokens != 103 || before.UsedTokens != 0 {
		t.Fatalf("failed finalization lost its fail-closed reservation: %+v %v", before, err)
	}
	for _, scope := range gateway.scopes {
		gateway.releaseScope(scope)
	}
	if len(gateway.scopes) != 0 {
		t.Fatal("stopped Invoke did not release its scope")
	}
	if err := gateway.budget.RecoverStoppedScopes(context.Background(), 7, "cleanup-task"); !errors.Is(err, ErrUsageUnknown) {
		t.Fatalf("a released scope let its live process clear unconfirmed accounting: %v", err)
	}
	after, err := gateway.budget.Inspect(context.Background(), 7)
	if err != nil || after != before {
		t.Fatalf("refused recovery rewrote accounting: before=%+v after=%+v err=%v", before, after, err)
	}
}

type boundFinalizationLedger struct {
	session.EventLog
	failFirst bool
	writes    int
}

func (ledger *boundFinalizationLedger) Append(ctx context.Context, id string, seq int64, kind string, payload any) (session.Event, error) {
	if kind == "admission/call_unknown" {
		ledger.writes++
		if ledger.failFirst && ledger.writes == 1 {
			return session.Event{}, errors.New("fixture first bound write unavailable")
		}
	}
	return ledger.EventLog.Append(ctx, id, seq, kind, payload)
}

func TestGatewayWritesBoundObservationOnceWithoutGenericFallback(t *testing.T) {
	for _, failFirst := range []bool{false, true} {
		name := "written"
		if failFirst {
			name = "append unavailable once"
		}
		t.Run(name, func(t *testing.T) {
			persisted := cleanupLedger(t)
			ledger := &boundFinalizationLedger{EventLog: persisted, failFirst: failFirst}
			gateway, request := cleanupGateway(t, ledger)
			var calls atomic.Int32
			gateway.http.Transport = cleanupRoundTripper(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: request,
					Body: io.NopCloser(strings.NewReader(`{"model":"fixture-model","usage":{"prompt_tokens":8,"completion_tokens":4}}`))}, nil
			})
			response, err := gateway.Invoke(context.Background(), request)
			if err != nil || response.GetErrorCode() != "provider_bound_violated" || calls.Load() != 1 || ledger.writes != 1 {
				t.Fatalf("bound observation was retried or downgraded: response=%+v calls=%d writes=%d err=%v", response, calls.Load(), ledger.writes, err)
			}
			batch, err := NewBudget(persisted).Inspect(context.Background(), 7)
			if err != nil || batch.ReservedTokens != 103 || batch.UsedTokens != 0 || batch.CostStatus != "unknown" {
				t.Fatalf("bound failure discarded reservation or invented usage: %+v %v", batch, err)
			}
			if failFirst {
				if batch.ActiveCalls != 1 || batch.UnknownUsage {
					t.Fatalf("failed bound write was hidden by a generic unknown: %+v", batch)
				}
				return
			}
			if batch.ActiveCalls != 0 || !batch.UnknownUsage {
				t.Fatalf("successful bound observation was not retained: %+v", batch)
			}
			if _, err := NewBudget(persisted).Settle(context.Background(), 7, "cleanup-call", 12); !errors.Is(err, ErrUsageUnknown) {
				t.Fatalf("a smaller settlement erased the observed directional bound violation: %v", err)
			}
		})
	}
}
