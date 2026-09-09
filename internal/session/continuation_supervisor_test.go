package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type supervisorModule struct {
	mu       sync.Mutex
	healthy  bool
	healths  int
	recovers int
	closed   int
}

func (m *supervisorModule) RequestContinuation(context.Context, ContinueCommand) (RunView, error) {
	return RunView{RunID: "supervisor-run"}, nil
}
func (m *supervisorModule) Recover(context.Context) error {
	m.mu.Lock()
	m.recovers++
	m.mu.Unlock()
	return nil
}
func (m *supervisorModule) Close() error {
	m.mu.Lock()
	m.closed++
	m.mu.Unlock()
	return nil
}
func (m *supervisorModule) Health(context.Context) error {
	m.mu.Lock()
	m.healths++
	healthy := m.healthy
	m.mu.Unlock()
	if !healthy {
		return errors.New("transport unavailable")
	}
	return nil
}

func (m *supervisorModule) snapshot() (healths, recovers, closed int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.healths, m.recovers, m.closed
}

func TestContinuationSupervisorReplacesUnhealthyModuleAndRecoversNewModule(t *testing.T) {
	slot := NewContinuationSlot()
	first := &supervisorModule{healthy: false}
	second := &supervisorModule{healthy: true}
	var mu sync.Mutex
	modules := []*supervisorModule{first, second}
	supervisor := NewContinuationSupervisor(slot, time.Millisecond, func(context.Context) (ContinuationModule, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(modules) == 0 {
			return nil, errors.New("connector exhausted")
		}
		module := modules[0]
		modules = modules[1:]
		return module, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor.Start(ctx)
	defer supervisor.Close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if slot.Available() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !slot.Available() {
		t.Fatal("supervisor did not attach a module")
	}
	// The first module is unhealthy, so the next reconciliation must replace it.
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if slot.current() == second {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if slot.current() != second {
		t.Fatal("supervisor did not replace unhealthy module")
	}
	_, _, firstClosed := first.snapshot()
	if firstClosed != 1 {
		t.Fatalf("first module close count = %d, want 1", firstClosed)
	}
	_, secondRecovers, _ := second.snapshot()
	if secondRecovers != 1 {
		t.Fatalf("second module recover count = %d, want 1", secondRecovers)
	}
}

func TestContinuationSlotDetachIfRejectsStaleGeneration(t *testing.T) {
	slot := NewContinuationSlot()
	first := &slotModule{}
	second := &slotModule{}
	firstGeneration := slot.Attach(first)
	secondGeneration := slot.Attach(second)
	if firstGeneration == secondGeneration {
		t.Fatalf("generation did not advance: first=%d second=%d", firstGeneration, secondGeneration)
	}
	if _, detached := slot.detachIf(first, firstGeneration); detached {
		t.Fatal("stale module detached replacement")
	}
	if !slot.Available() || slot.current() != second {
		t.Fatal("replacement module was lost after stale detach")
	}
	detachedGeneration, detached := slot.detachIf(second, secondGeneration)
	if !detached {
		t.Fatal("current module was not detached")
	}
	if detachedGeneration <= secondGeneration {
		t.Fatalf("detach generation = %d, want greater than %d", detachedGeneration, secondGeneration)
	}
	if slot.Available() {
		t.Fatal("slot remained available after current detach")
	}
}

func TestContinuationSupervisorStatusIsRuntimeProjection(t *testing.T) {
	slot := NewContinuationSlot()
	module := &supervisorModule{healthy: true}
	supervisor := NewContinuationSupervisor(slot, time.Hour, func(context.Context) (ContinuationModule, error) {
		return module, nil
	})
	supervisor.reconcile(context.Background())
	status := supervisor.Status()
	if !status.Attached || status.Generation == 0 || status.LastRecoveryAt == nil {
		t.Fatalf("unexpected attached status: %+v", status)
	}
	status.LastRecoveryAt = nil
	if supervisor.Status().LastRecoveryAt == nil {
		t.Fatal("status returned a mutable pointer into supervisor state")
	}
	if got := slot.Available(); !got {
		t.Fatal("slot unexpectedly unavailable")
	}
	_ = supervisor.Close()
}

func TestContinuationSupervisorCloseClosesAttachedModuleOnce(t *testing.T) {
	slot := NewContinuationSlot()
	module := &supervisorModule{healthy: true}
	supervisor := NewContinuationSupervisor(slot, time.Hour, func(context.Context) (ContinuationModule, error) {
		return module, nil
	})
	supervisor.reconcile(context.Background())
	if err := supervisor.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := supervisor.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	_, _, closed := module.snapshot()
	if closed != 1 {
		t.Fatalf("module close count = %d, want 1", closed)
	}
}

func TestContinuationSupervisorBacksOffFailedConnections(t *testing.T) {
	slot := NewContinuationSlot()
	connects := 0
	supervisor := NewContinuationSupervisor(slot, time.Second, func(context.Context) (ContinuationModule, error) {
		connects++
		return nil, errors.New("orchestrator unavailable")
	})
	supervisor.reconcile(context.Background())
	status := supervisor.Status()
	if status.ConsecutiveFailures != 1 || status.NextRetryAt == nil {
		t.Fatalf("retry status = %+v, want one failure and next retry", status)
	}
	supervisor.reconcile(context.Background())
	if connects != 1 {
		t.Fatalf("connect attempts = %d, want 1 while backoff is active", connects)
	}
	supervisor.mu.Lock()
	supervisor.nextConnectAt = time.Time{}
	supervisor.mu.Unlock()
	supervisor.reconcile(context.Background())
	if connects != 2 {
		t.Fatalf("connect attempts after backoff reset = %d, want 2", connects)
	}
}

func TestContinuationSupervisorBacksOffHealthAndRecoveryFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		module     *supervisorModule
		recoverErr error
	}{
		{name: "health", module: &supervisorModule{healthy: false}},
		{name: "recovery", module: &supervisorModule{healthy: true}, recoverErr: errors.New("recover failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slot := NewContinuationSlot()
			connects := 0
			module := tc.module
			if tc.recoverErr != nil {
				module = &supervisorModule{healthy: true}
			}
			supervisor := NewContinuationSupervisor(slot, time.Second, func(context.Context) (ContinuationModule, error) {
				connects++
				return module, nil
			})
			if tc.recoverErr == nil {
				supervisor.reconcile(context.Background())
			} else {
				// A recover failure is injected through a small wrapper module.
				supervisor.connect = func(context.Context) (ContinuationModule, error) {
					connects++
					return &recoverFailModule{supervisorModule: module, err: tc.recoverErr}, nil
				}
				supervisor.reconcile(context.Background())
			}
			supervisor.reconcile(context.Background())
			if connects != 1 {
				t.Fatalf("connect attempts = %d, want 1 while backoff is active", connects)
			}
		})
	}
}

func TestContinuationSupervisorKeepsExponentialBackoffUntilAttach(t *testing.T) {
	slot := NewContinuationSlot()
	supervisor := NewContinuationSupervisor(slot, time.Second, func(context.Context) (ContinuationModule, error) {
		return &recoverFailModule{
			supervisorModule: &supervisorModule{healthy: true},
			err:              errors.New("recover failed"),
		}, nil
	})

	supervisor.reconcile(context.Background())
	supervisor.mu.Lock()
	firstFailures := supervisor.connectFailures
	supervisor.nextConnectAt = time.Time{}
	supervisor.mu.Unlock()
	supervisor.reconcile(context.Background())
	supervisor.mu.Lock()
	secondFailures := supervisor.connectFailures
	supervisor.mu.Unlock()

	if firstFailures != 1 || secondFailures != 2 {
		t.Fatalf("connect failures = (%d, %d), want (1, 2)", firstFailures, secondFailures)
	}
}

type recoverFailModule struct {
	*supervisorModule
	err error
}

func (m *recoverFailModule) Recover(context.Context) error { return m.err }

type blockingRecoverModule struct {
	*supervisorModule
}

type gatedRecoverModule struct {
	*supervisorModule
	started chan struct{}
	release chan struct{}
}

func (m *gatedRecoverModule) Recover(context.Context) error {
	close(m.started)
	<-m.release
	return nil
}

func TestContinuationSupervisorDoesNotAttachAfterClose(t *testing.T) {
	slot := NewContinuationSlot()
	module := &gatedRecoverModule{
		supervisorModule: &supervisorModule{healthy: true},
		started:          make(chan struct{}),
		release:          make(chan struct{}),
	}
	supervisor := NewContinuationSupervisor(slot, time.Second, func(context.Context) (ContinuationModule, error) {
		return module, nil
	})
	done := make(chan struct{})
	go func() {
		supervisor.reconcile(context.Background())
		close(done)
	}()
	<-module.started
	if err := supervisor.Close(); err != nil {
		t.Fatal(err)
	}
	close(module.release)
	<-done
	if slot.Available() {
		t.Fatal("late recovery attached after supervisor close")
	}
	_, _, closed := module.snapshot()
	if closed != 1 {
		t.Fatalf("late module close count = %d, want 1", closed)
	}
}

func TestContinuationSupervisorDoesNotStartWithCanceledContext(t *testing.T) {
	slot := NewContinuationSlot()
	connects := 0
	supervisor := NewContinuationSupervisor(slot, time.Millisecond, func(context.Context) (ContinuationModule, error) {
		connects++
		return nil, errors.New("should not connect")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	supervisor.Start(ctx)
	time.Sleep(5 * time.Millisecond)
	if connects != 0 {
		t.Fatalf("connect attempts = %d, want 0", connects)
	}
	if supervisor.Status().Attached {
		t.Fatal("canceled supervisor became attached")
	}
}

func (m *blockingRecoverModule) Recover(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestContinuationSupervisorBoundsRecovery(t *testing.T) {
	slot := NewContinuationSlot()
	module := &blockingRecoverModule{supervisorModule: &supervisorModule{healthy: true}}
	supervisor := NewContinuationSupervisor(slot, 10*time.Millisecond, func(context.Context) (ContinuationModule, error) {
		return module, nil
	})

	started := time.Now()
	supervisor.reconcile(context.Background())
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("recovery exceeded bound: %s", elapsed)
	}
	if slot.Available() {
		t.Fatal("module attached after recovery timeout")
	}
	status := supervisor.Status()
	if !strings.Contains(status.LastHealthError, context.DeadlineExceeded.Error()) {
		t.Fatalf("last health error = %q, want deadline exceeded", status.LastHealthError)
	}
	_, _, closed := module.snapshot()
	if closed != 1 {
		t.Fatalf("module close count = %d, want 1", closed)
	}
}
