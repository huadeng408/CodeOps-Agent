package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
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
	"google.golang.org/protobuf/encoding/protojson"
)

type startupRecoveryReadLog struct {
	session.EventLog
	mu     sync.Mutex
	reads  map[string]int
	onRead func(context.Context, string, []session.Event, int) ([]session.Event, error)
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
