package admission_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/session"
)

func evaluationBudgetRoot(t *testing.T, ledger session.EventLog, tokens int64, seconds float64) string {
	t.Helper()
	view, err := session.NewWorkbench(ledger, nil).Create(context.Background(), 7, "fixture", "bounded evaluation", "")
	if err != nil {
		t.Fatal(err)
	}
	policy := session.EvaluationPolicy{ContextStrategy: "metadata_on_demand", ExecutionStrategy: "single", MaxToolRounds: 4, ContextMaxBytes: 12000}
	if err := session.BindEvaluation(context.Background(), ledger, view.ID, session.EvaluationBinding{SchemaVersion: 1, RunID: "budget-fixture", TaskID: "budget-fixture", MaxTokens: tokens, WallClockSeconds: seconds, Policy: policy}); err != nil {
		t.Fatal(err)
	}
	return view.ID
}

func TestEvaluationReservationRetainsUnknownAndSeparatesIndependentRoots(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	root := evaluationBudgetRoot(t, ledger, 600, 30)
	if _, err := budget.Reserve(ctx, 7, root, "unknown-attempt", 300); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.MarkUnknown(ctx, 7, "unknown-attempt"); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Reserve(ctx, 7, root, "over-remaining", 301); !errors.Is(err, admission.ErrBudgetExhausted) {
		t.Fatalf("unknown reservation disappeared from the trial cap: %v", err)
	}
	if _, err := budget.YieldTask(ctx, 7, root); err != nil {
		t.Fatal(err)
	}
	other := evaluationBudgetRoot(t, ledger, 600, 30)
	if _, err := budget.Reserve(ctx, 7, other, "independent-attempt", 600); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Settle(ctx, 7, "independent-attempt", 600); err != nil {
		t.Fatal(err)
	}
	state, err := budget.Inspect(ctx, 7)
	if err != nil || state.UsedTokens != 600 || state.ReservedTokens != 300 || !state.UnknownUsage {
		t.Fatalf("independent task erased or inherited another root's usage: %+v %v", state, err)
	}
}

func TestGatewayEvaluationWallClockUsesFrozenSessionStart(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "expired-before-dispatch", true: "active-http-is-cancelled"}[active], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			seconds := 0.01
			if active {
				seconds = 1.5
			}
			root := evaluationBudgetRoot(t, ledger, 1000, seconds)
			budget := admission.NewBudget(ledger)
			var calls atomic.Int32
			stopped := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if active {
					_, _ = io.Copy(io.Discard, r.Body)
					<-r.Context().Done()
					close(stopped)
					return
				}
				io.WriteString(w, `{"model":"fixture-model","usage":{"prompt_tokens":10,"completion_tokens":3}}`)
			}))
			defer upstream.Close()
			gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 10})
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			binding, release, err := gateway.Bind(7, root, root)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if !active {
				time.Sleep(20 * time.Millisecond)
			}
			start := time.Now()
			response, err := gateway.Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability, CallId: "deadline-attempt", PayloadJson: []byte(`{"model":"fixture-model","messages":[]}`)})
			elapsed := time.Since(start)
			state, inspectErr := budget.Inspect(context.Background(), 7)
			if err != nil || inspectErr != nil {
				t.Fatalf("deadline invocation failed outside its typed result: %v %v", err, inspectErr)
			}
			if !active {
				if response.GetErrorCode() != "wall_clock_budget_exhausted" || calls.Load() != 0 || state.ReservedTokens != 0 || state.UsedTokens != 0 {
					t.Fatal("expired frozen deadline reached HTTP or created usage")
				}
			} else {
				if elapsed > 3*time.Second || response.GetErrorCode() != "provider_timeout_unknown" || calls.Load() != 1 || state.UsedTokens != 0 || state.ReservedTokens != 110 || !state.UnknownUsage || state.ActiveCalls != 0 {
					t.Fatalf("HTTP did not respect the frozen deadline and retain unknown usage: elapsed=%s code=%s state=%+v", elapsed, response.GetErrorCode(), state)
				}
				select {
				case <-stopped:
				case <-ctx.Done():
					t.Fatal("deadline did not stop the local HTTP transport")
				}
			}
		})
	}
}

func TestGatewayEvaluationCapRechecksSettledUsageAfterStaleAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	root := evaluationBudgetRoot(t, ledger, 600, 30)
	budget := admission.NewBudget(ledger)
	if _, err := budget.Reserve(ctx, 7, root, "earlier-call", 90); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Settle(ctx, 7, "earlier-call", 90); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		input := 10
		if calls.Add(1) > 1 {
			input = 490
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fixture-model", "usage": map[string]int{"prompt_tokens": input, "completion_tokens": 10}})
	}))
	defer upstream.Close()
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL, Model: "fixture-model", APIKey: "fixture-only", InputLimit: 490, OutputLimit: 10})
	firstChecked, bothChecked, releaseSecond := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var checks atomic.Int32
	gateway.SetTaskAdmission(func(ctx context.Context, taskID, _ string) error {
		state, err := budget.Inspect(ctx, 7)
		if err != nil || taskID != root || state.UsedTokens != 90 {
			return errors.New("fixture stale checkpoint did not observe ninety tokens")
		}
		if checks.Add(1) == 1 {
			close(firstChecked)
			select {
			case <-bothChecked:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		close(bothChecked)
		select {
		case <-releaseSecond:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	first, releaseFirst, err := gateway.Bind(7, root, root)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	second, releaseLast, err := gateway.Bind(7, root, root)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLast()
	type outcome struct {
		response *pb.ModelCallResponse
		err      error
	}
	invoke := func(binding *pb.ModelGatewayBinding, id string, out chan<- outcome) {
		response, err := gateway.Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability, CallId: id, PayloadJson: []byte(`{"model":"fixture-model","messages":[]}`)})
		out <- outcome{response, err}
	}
	one, two := make(chan outcome, 1), make(chan outcome, 1)
	go invoke(first, "first-evaluation-call", one)
	select {
	case <-firstChecked:
	case <-ctx.Done():
		t.Fatal("first checkpoint not reached")
	}
	go invoke(second, "second-evaluation-call", two)
	firstResult := <-one
	if firstResult.err != nil || firstResult.response.GetErrorCode() != "" {
		t.Fatal("first permitted invocation failed")
	}
	close(releaseSecond)
	secondResult := <-two
	state, err := budget.Inspect(ctx, 7)
	if secondResult.err != nil || secondResult.response.GetErrorCode() != "token_budget_exhausted" || calls.Load() != 1 || err != nil || state.UsedTokens != 110 || state.ReservedTokens != 0 {
		t.Fatalf("stale admission bypassed the frozen cap: code=%s provider_calls=%d state=%+v err=%v", secondResult.response.GetErrorCode(), calls.Load(), state, err)
	}
}
