package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/encoding/protojson"
)

type startupRecoveryReadLog struct {
	session.EventLog
	mu     sync.Mutex
	reads  map[string]int
	onRead func(context.Context, string, []session.Event, int) ([]session.Event, error)
}

type startupRecoveryTailLedger struct {
	session.EventLog
	last string
}

type startupSignalConversation struct {
	admissionConversation
	started chan struct{}
}

func (a *startupSignalConversation) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	select {
	case a.started <- struct{}{}:
	default:
	}
	return a.admissionConversation.RunConversation(ctx, request, handlers)
}

func (l startupRecoveryTailLedger) SessionIDs(ctx context.Context) ([]string, error) {
	ids, err := l.EventLog.SessionIDs(ctx)
	sort.SliceStable(ids, func(i, j int) bool { return ids[i] != l.last && ids[j] == l.last })
	return ids, err
}

func (l *startupRecoveryReadLog) Events(ctx context.Context, id string) ([]session.Event, error) {
	l.mu.Lock()
	l.reads[id]++
	count := l.reads[id]
	l.mu.Unlock()
	events, err := l.EventLog.Events(ctx, id)
	if err == nil && l.onRead != nil {
		return l.onRead(ctx, id, events, count)
	}
	return events, err
}

func TestServerStartupRecoveryRetainsFreshIntegrityAndAdmission(t *testing.T) {
	for _, mode := range []string{"changed_head", "corrupt_chain", "invalid_surface", "new_yield_fence", "yield_during_ancestry"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			path, workspace := filepath.Join(t.TempDir(), "isolated.sqlite"), t.TempDir()
			id, _ := startupYieldGuardStoppedTask(t, ctx, path, workspace)
			ledger, err := session.OpenSQLiteEventLog(path)
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			original, err := ledger.Events(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "invalid_surface" {
				ref := map[string]string{"event_id": "unrelated-fixture", "checksum": strings.Repeat("f", 64)}
				if _, err := ledger.AppendSurface(ctx, id, int64(len(original)), "context/compaction", map[string]any{
					"run_id": "fixture", "summary": "generic fixture summary", "sources": []any{ref},
					"reported_sources": []any{ref}, "input_event_id": "active-fixture",
				}, session.SurfaceOperation{Op: "compact"}); err != nil {
					t.Fatal(err)
				}
				original, err = ledger.Events(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
			}
			observed := &startupRecoveryReadLog{EventLog: ledger, reads: map[string]int{}}
			observed.onRead = func(ctx context.Context, current string, events []session.Event, count int) ([]session.Event, error) {
				if current != id {
					return events, nil
				}
				if mode == "corrupt_chain" && count == 1 {
					events[1].Payload = json.RawMessage(`{"fixture":"corrupted"}`)
				}
				if mode == "changed_head" && count == 2 {
					if err := session.NewWorkbench(ledger, nil).UpdateTitle(ctx, 7, id, int64(len(events)), "concurrent edit"); err != nil {
						return nil, err
					}
				}
				if (mode == "new_yield_fence" && count == 2) || (mode == "yield_during_ancestry" && count == 3) {
					if _, err := admission.NewBudget(ledger).YieldTask(ctx, admission.ActorOwner(identity.Default()), id); err != nil {
						return nil, err
					}
				}
				return events, nil
			}
			gateway := admission.NewGateway(admission.NewBudget(observed), admission.ProviderConfig{})
			conversation := &admissionConversation{}
			runner := session.NewSessionRunner(session.NewWorkbench(observed, nil), conversation, nil, session.SessionRunnerOptions{
				StartupRecoveryAllowed: gateway.StartupRecoveryAllowed,
				Now:                    func() time.Time { return time.Now().Add(time.Minute) },
			})
			defer runner.Close()
			err = runner.Recover(ctx)
			want := session.ErrEventIntegrity
			if mode == "changed_head" {
				want = session.ErrSequenceConflict
			}
			if mode == "new_yield_fence" || mode == "yield_during_ancestry" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, want) {
				t.Fatalf("Recover error = %v, want %v", err, want)
			}
			if err := runner.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := ledger.Events(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "changed_head" {
				if len(after) != len(original)+1 || after[len(after)-1].Type != "session/title-updated" {
					t.Fatal("recovery appended facts after the competing edit")
				}
				after = after[:len(original)]
			}
			if !reflect.DeepEqual(original, after) || conversation.calls.Load() != 0 {
				t.Fatalf("rejected recovery changed original facts or dispatched: calls=%d", conversation.calls.Load())
			}
		})
	}
}

func TestServerStartupRecoveryBoundsHistoricalProjectionCost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path, workspace := filepath.Join(t.TempDir(), "isolated.sqlite"), t.TempDir()
	yieldedID, _ := startupYieldGuardStoppedTask(t, ctx, path, workspace)
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	events, err := ledger.Events(ctx, yieldedID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 768; i++ {
		if _, err := ledger.AppendSurface(ctx, yieldedID, int64(len(events)+i), "user/message",
			map[string]string{"author": "user", "content": strings.Repeat("archived observation ", 64)},
			session.SurfaceOperation{Op: "append"}); err != nil {
			t.Fatal(err)
		}
	}
	budget := admission.NewBudget(ledger)
	if _, err := budget.YieldTask(ctx, admission.ActorOwner(identity.Default()), yieldedID); err != nil {
		t.Fatal(err)
	}
	eligibleID, eligibleRun := startupYieldGuardStoppedTask(t, ctx, path, workspace)
	before, err := session.ReadVerifiedSnapshot(ctx, ledger, yieldedID)
	if err != nil {
		t.Fatal(err)
	}
	observed := &startupRecoveryReadLog{EventLog: ledger, reads: map[string]int{}}
	w := session.NewWorkbench(observed, nil)
	gateway := admission.NewGateway(admission.NewBudget(observed), admission.ProviderConfig{})
	conversation := &admissionConversation{}
	runner := session.NewSessionRunner(w, conversation, nil, session.SessionRunnerOptions{
		WorkerCount: 1, StartupRecoveryAllowed: gateway.StartupRecoveryAllowed,
		Now: func() time.Time { return time.Now().Add(time.Minute) },
	})
	defer runner.Close()
	var initial, final runtime.MemStats
	runtime.ReadMemStats(&initial)
	if err := runner.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&final)
	allocated := final.TotalAlloc - initial.TotalAlloc
	observed.mu.Lock()
	yieldedReads := observed.reads[yieldedID]
	observed.mu.Unlock()
	t.Logf("Recover allocated=%d bytes; yielded_events=%d; yielded_reads=%d", allocated, len(before.Events), yieldedReads)
	// A coarse resource budget catches full Surface copies without a wall-clock assertion.
	if allocated > 64<<20 {
		t.Errorf("recover allocated %d bytes for one historical task; budget is 64 MiB", allocated)
	}
	for {
		run, err := runner.Run(ctx, eligibleID, eligibleRun)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == session.RunCompleted {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := session.ReadVerifiedSnapshot(ctx, ledger, yieldedID)
	if err != nil || !reflect.DeepEqual(before.Events, after.Events) {
		t.Fatalf("recovery changed the yielded canonical prefix: %v", err)
	}
	if conversation.calls.Load() != 1 {
		t.Fatalf("recovered conversations = %d, want only the eligible task", conversation.calls.Load())
	}
}

func TestServerStartupRecoveryRejectsInvalidAncestorBeforeCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "isolated.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	w := session.NewWorkbench(ledger, nil)
	parent, err := w.CreateWithWorkingDir(ctx, 7, "ancestor-fixture", "retain cancellation", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	budget := admission.NewBudget(ledger)
	if _, err := budget.AcquireTask(ctx, admission.ActorOwner(actor), parent.ID); err != nil {
		t.Fatal(err)
	}
	childDir := t.TempDir()
	var failedLifecycles atomic.Int64
	seed := session.NewSessionRunner(w, startupYieldGuardBlockedConversation{}, nil, session.SessionRunnerOptions{
		AgentWorkspace: func(context.Context, session.AgentWorkspaceRequest) (string, error) { return childDir, nil },
		Workspace: func(context.Context, uint, string) (session.TaskModelWorkspace, error) {
			return session.TaskModelWorkspace{WorkingDir: childDir}, nil
		},
		AgentLifecycle: func(_ context.Context, event *pb.AgentLifecycle) error {
			if event.Status == "cancelled" {
				failedLifecycles.Add(1)
				return errors.New("fixture retains unacknowledged cancellation")
			}
			return nil
		},
	})
	t.Cleanup(func() { _ = seed.Close() })
	spawned := seed.ExecuteAgentTool(ctx, actor, parent.ID, orchestrator.ToolCall{
		ID: "spawn-general", Name: "SpawnAgent", ParametersJSON: `{"kind":"general","title":"cancel","objective":"await cancellation","parallel":true}`,
	})
	var child pb.AgentTask
	if spawned.Error != "" || protojson.Unmarshal([]byte(spawned.Output), &child) != nil || child.Id == "" {
		t.Fatalf("signed child fixture failed: %s", spawned.Error)
	}
	for {
		view, err := w.Get(ctx, 7, child.Id)
		if err != nil {
			t.Fatal(err)
		}
		if view.Run != nil && view.Run.Status == session.RunRunning {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	_ = seed.ExecuteAgentTool(ctx, actor, parent.ID, orchestrator.ToolCall{
		ID: "cancel-general", Name: "AgentTask", ParametersJSON: `{"action":"cancel","task_id":"` + child.Id + `"}`,
	})
	for failedLifecycles.Load() == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("fixture did not retain an unfinished cancellation lifecycle")
		case <-time.After(time.Millisecond):
		}
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	parentEvents, err := ledger.Events(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	ref := map[string]string{"event_id": "unrelated-ancestor", "checksum": strings.Repeat("f", 64)}
	if _, err := ledger.AppendSurface(ctx, parent.ID, int64(len(parentEvents)), "context/compaction", map[string]any{
		"run_id": "fixture", "summary": "generic ancestor fixture", "sources": []any{ref},
		"reported_sources": []any{ref}, "input_event_id": "active-fixture",
	}, session.SurfaceOperation{Op: "compact"}); err != nil {
		t.Fatal(err)
	}
	before := map[string][]session.Event{}
	for _, id := range []string{parent.ID, child.Id} {
		before[id], err = ledger.Events(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	var lifecycles atomic.Int64
	conversation := &admissionConversation{}
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	recovered := session.NewSessionRunner(w, conversation, nil, session.SessionRunnerOptions{
		StartupRecoveryAllowed: gateway.StartupRecoveryAllowed,
		AgentLifecycle:         func(context.Context, *pb.AgentLifecycle) error { lifecycles.Add(1); return nil },
	})
	defer recovered.Close()
	if err := recovered.Recover(ctx); err == nil {
		t.Error("invalid ancestor was not reported")
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	for id, original := range before {
		after, err := ledger.Events(ctx, id)
		if err != nil || !reflect.DeepEqual(original, after) {
			t.Errorf("recovery changed a rejected ancestor or child: %s, %v", id, err)
		}
	}
	if lifecycles.Load() != 0 || conversation.calls.Load() != 0 {
		t.Fatalf("invalid ancestor reached recovery side effects: lifecycle=%d conversation=%d", lifecycles.Load(), conversation.calls.Load())
	}
}

func TestServerStartupRecoveryDefersDispatchAndRetainsLatestFence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	path, workspace := filepath.Join(t.TempDir(), "isolated.sqlite"), t.TempDir()
	first, _ := startupYieldGuardStoppedTask(t, ctx, path, workspace)
	t.Setenv("CODEOPS_STARTUP_YIELD_GUARD_NO_ADMISSION", "1")
	later, laterRun := startupYieldGuardStoppedTask(t, ctx, path, workspace)
	t.Setenv("CODEOPS_STARTUP_YIELD_GUARD_NO_ADMISSION", "")
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	before, err := ledger.Events(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	budget := admission.NewBudget(ledger)
	observed := &startupRecoveryReadLog{EventLog: ledger, reads: map[string]int{}}
	var fenced atomic.Bool
	observed.onRead = func(ctx context.Context, id string, events []session.Event, count int) ([]session.Event, error) {
		if id == later && count == 1 {
			if _, err := budget.YieldTask(ctx, admission.ActorOwner(identity.Default()), first); err != nil {
				return nil, err
			}
			fenced.Store(true)
		}
		return events, nil
	}
	w := session.NewWorkbench(startupYieldGuardOrderedLedger{EventLog: observed, first: first}, nil)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	conversation := &admissionConversation{}
	runner := session.NewSessionRunner(w, conversation, nil, session.SessionRunnerOptions{
		StartupRecoveryAllowed: gateway.StartupRecoveryAllowed, Workspace: startupYieldGuardWorkspace(w, budget),
		Now: func() time.Time { return time.Now().Add(time.Minute) },
	})
	defer runner.Close()
	if err := runner.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if !fenced.Load() {
		t.Fatal("fixture never reached the later stream")
	}
	for {
		view, err := runner.Run(ctx, later, laterRun)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == session.RunCompleted {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("healthy recovery work was lost")
		case <-time.After(time.Millisecond):
		}
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := ledger.Events(ctx, first)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("later scan yield resurrected an earlier candidate: %v", err)
	}
	view, err := w.Get(ctx, 7, later)
	if err != nil || view.Run == nil || view.Run.RunID != laterRun || view.Run.Status != session.RunCompleted || conversation.calls.Load() != 1 {
		t.Fatalf("healthy later candidate not retained: %v, calls=%d", err, conversation.calls.Load())
	}
}

func TestServerStartupRecoveryRetainsCancellationAtDeferredDispatch(t *testing.T) {
	for _, mode := range []string{"cancel_last_read", "close_last_read", "cancel_final_fence"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			setupCtx, setupCancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer setupCancel()
			path := filepath.Join(t.TempDir(), "isolated.sqlite")
			first, firstRun := startupYieldGuardStoppedTask(t, setupCtx, path, t.TempDir())
			ledger, err := session.OpenSQLiteEventLog(path)
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			empty, err := session.NewWorkbench(ledger, nil).Create(setupCtx, 7, "last", "no pending run", "")
			if err != nil {
				t.Fatal(err)
			}
			before, err := ledger.Events(setupCtx, first)
			if err != nil {
				t.Fatal(err)
			}
			observed := &startupRecoveryReadLog{EventLog: ledger, reads: map[string]int{}}
			w := session.NewWorkbench(startupRecoveryTailLedger{EventLog: observed, last: empty.ID}, nil)
			budget := admission.NewBudget(ledger)
			gateway := admission.NewGateway(budget, admission.ProviderConfig{})
			conversation := &admissionConversation{}
			var runner *session.SessionRunner
			var checks atomic.Int64
			options := session.SessionRunnerOptions{
				Workspace: startupYieldGuardWorkspace(w, budget), Now: func() time.Time { return time.Now().Add(time.Minute) },
				StartupRecoveryAllowed: func(ctx context.Context, actor identity.Actor) (bool, error) {
					allowed, err := gateway.StartupRecoveryAllowed(ctx, actor)
					if actor.SessionID == first && checks.Add(1) == 3 && mode == "cancel_final_fence" {
						cancel()
					}
					return allowed, err
				},
			}
			runner = session.NewSessionRunner(w, conversation, nil, options)
			defer func() { _ = runner.Close() }()
			observed.onRead = func(_ context.Context, id string, events []session.Event, count int) ([]session.Event, error) {
				if id == empty.ID && count == 2 {
					if mode == "cancel_last_read" {
						cancel()
					}
					if mode == "close_last_read" {
						_ = runner.Close()
					}
				}
				return events, nil
			}
			want := error(context.Canceled)
			if mode == "close_last_read" {
				want = session.ErrSessionRunnerClosed
			}
			if err := runner.Recover(ctx); !errors.Is(err, want) {
				t.Errorf("cancelled deferred dispatch returned %v, want %v", err, want)
			}
			if err := runner.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := ledger.Events(setupCtx, first)
			if err != nil || !reflect.DeepEqual(before, after) || conversation.calls.Load() != 0 {
				t.Fatalf("cancelled scan changed pending work or dispatched: %v, calls=%d", err, conversation.calls.Load())
			}
			// The interrupted scan retains the canonical work for the next runner.
			runner = session.NewSessionRunner(w, conversation, nil, options)
			if err := runner.Recover(setupCtx); err != nil {
				t.Fatal(err)
			}
			for {
				view, err := runner.Run(setupCtx, first, firstRun)
				if err != nil {
					t.Fatal(err)
				}
				if view.Status == session.RunCompleted {
					break
				}
				select {
				case <-setupCtx.Done():
					t.Fatal("pending work was lost on cancellation")
				case <-time.After(time.Millisecond):
				}
			}
		})
	}
}

func TestServerStartupRecoveryKeepsExplicitQueuedContinuation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "isolated.sqlite")
	id, runID := startupYieldGuardStoppedTask(t, ctx, path, t.TempDir())
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	events, err := ledger.Events(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var lease map[string]json.RawMessage
	for _, event := range events {
		if event.Type == "session/run-leased" {
			if err := json.Unmarshal(event.Payload, &lease); err != nil {
				t.Fatal(err)
			}
		}
	}
	if lease == nil {
		t.Fatal("fixture has no owned lease")
	}
	until := time.Now().Add(5 * time.Second)
	lease["lease_until"], _ = json.Marshal(until)
	if _, err := ledger.Append(ctx, id, int64(len(events)), "session/run-heartbeat", lease); err != nil {
		t.Fatal(err)
	}
	w := session.NewWorkbench(ledger, nil)
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	conversation := &admissionConversation{}
	runner := session.NewSessionRunner(w, conversation, nil, session.SessionRunnerOptions{
		StartupRecoveryAllowed: gateway.StartupRecoveryAllowed, Workspace: startupYieldGuardWorkspace(w, budget),
	})
	defer runner.Close()
	// The explicit no-span continuation is already queued for lease expiry.
	if err := runner.RecoverSession(ctx, 7, id); err != nil {
		t.Fatal(err)
	}
	startupCtx := trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
	}))
	if err := runner.Recover(startupCtx); err != nil {
		t.Fatal(err)
	}
	if !time.Now().Before(until) || conversation.calls.Load() != 0 {
		t.Fatal("fixture lease expired before the controlled queue conflict")
	}
	if _, err := budget.YieldTask(ctx, admission.ActorOwner(identity.Default()), id); err != nil {
		t.Fatal(err)
	}
	for {
		view, err := runner.Run(ctx, id, runID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == session.RunCompleted {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("startup replay displaced explicit queued work")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if conversation.calls.Load() != 1 {
		t.Fatalf("explicit conversations=%d, want 1", conversation.calls.Load())
	}
}

func TestServerStartupRecoveryDefersNewChildRunUntilScanCompletes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "isolated.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	w := session.NewWorkbench(ledger, nil)
	parent, err := w.CreateWithWorkingDir(ctx, 7, "parent", "pending child", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tail, err := w.Create(ctx, 7, "last", "scan boundary", "")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	budget := admission.NewBudget(ledger)
	if _, err := budget.AcquireTask(ctx, admission.ActorOwner(actor), parent.ID); err != nil {
		t.Fatal(err)
	}
	borrowed := func(context.Context, identity.Actor, string, orchestrator.ToolCall) (session.TaskModelWorkspace, error) {
		return session.TaskModelWorkspace{WorkingDir: parent.WorkingDir}, nil
	}
	seed := session.NewSessionRunner(w, &admissionConversation{}, nil, session.SessionRunnerOptions{
		Workspace: startupYieldGuardWorkspace(w, budget), ReadOnlyAgentWorkspace: borrowed,
	})
	defer seed.Close()
	created := seed.ExecuteAgentTool(ctx, actor, parent.ID, orchestrator.ToolCall{ID: "pending-child", Name: "SpawnAgent", ParametersJSON: `{"kind":"explore","title":"inspect","objective":"read explicit fixture"}`})
	if created.Error == "" {
		t.Fatal("fixture must stop after signed child creation, before run admission")
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	ids, err := ledger.SessionIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	child := ""
	for _, id := range ids {
		if strings.HasPrefix(id, "agent-") {
			child = id
		}
	}
	childView, err := w.Get(ctx, 7, child)
	if err != nil || childView.Run != nil {
		t.Fatalf("fixture did not retain a child without a Run: %v", err)
	}
	conversation := &startupSignalConversation{started: make(chan struct{}, 1)}
	observed := &startupRecoveryReadLog{EventLog: ledger, reads: map[string]int{}}
	var atFence []session.Event
	var premature bool
	observed.onRead = func(ctx context.Context, id string, events []session.Event, count int) ([]session.Event, error) {
		if id == tail.ID && count == 1 {
			// Hold the final scan boundary open so an eager worker is observable.
			select {
			case <-conversation.started:
				premature = true
			case <-time.After(100 * time.Millisecond):
			}
			var err error
			atFence, err = ledger.Events(ctx, child)
			if err != nil {
				return nil, err
			}
			for _, event := range atFence {
				if event.Type == "session/run-leased" {
					premature = true
				}
			}
			if _, err := budget.YieldTask(ctx, admission.ActorOwner(actor), parent.ID); err != nil {
				return nil, err
			}
		}
		return events, nil
	}
	recoveryWorkbench := session.NewWorkbench(startupRecoveryTailLedger{EventLog: observed, last: tail.ID}, nil)
	workspace := func(ctx context.Context, owner uint, id string) (session.TaskModelWorkspace, error) {
		bound, err := identity.Default().BindSession(id)
		if err != nil {
			return session.TaskModelWorkspace{}, err
		}
		root, err := session.ModelTaskRoot(ctx, ledger, bound)
		if err != nil {
			return session.TaskModelWorkspace{}, err
		}
		if _, err := budget.AcquireTask(ctx, admission.ActorOwner(bound), root); err != nil {
			return session.TaskModelWorkspace{}, err
		}
		view, err := w.Get(ctx, owner, id)
		return session.TaskModelWorkspace{WorkingDir: view.WorkingDir}, err
	}
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	options := session.SessionRunnerOptions{StartupRecoveryAllowed: gateway.StartupRecoveryAllowed, Workspace: workspace, ReadOnlyAgentWorkspace: borrowed}
	recovered := session.NewSessionRunner(recoveryWorkbench, conversation, nil, options)
	defer recovered.Close()
	if err := recovered.Recover(ctx); err != nil {
		t.Errorf("startup recovery failed before final fence observation: %v", err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := ledger.Events(ctx, child)
	if err != nil || atFence == nil || premature || conversation.calls.Load() != 0 || !reflect.DeepEqual(atFence, after) {
		t.Fatalf("new child dispatched before scan/final fence: early=%t calls=%d error=%v", premature, conversation.calls.Load(), err)
	}
	if _, err := budget.AcquireTask(ctx, admission.ActorOwner(actor), parent.ID); err != nil {
		t.Fatal(err)
	}
	recovered = session.NewSessionRunner(recoveryWorkbench, conversation, nil, options)
	defer recovered.Close()
	if err := recovered.RecoverSession(ctx, 7, child); err != nil {
		t.Fatal(err)
	}
	for {
		view, err := w.Get(ctx, 7, child)
		if err != nil {
			t.Fatal(err)
		}
		if view.Run != nil && view.Run.Status == session.RunCompleted {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("deferred new child work was lost")
		case <-time.After(time.Millisecond):
		}
	}
}
