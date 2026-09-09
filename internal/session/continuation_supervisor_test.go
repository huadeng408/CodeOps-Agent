package session

import (
	"context"
	"errors"
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
	if slot.detachIf(first, firstGeneration) {
		t.Fatal("stale module detached replacement")
	}
	if !slot.Available() || slot.current() != second {
		t.Fatal("replacement module was lost after stale detach")
	}
	if !slot.detachIf(second, secondGeneration) {
		t.Fatal("current module was not detached")
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
