package session

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type slotModule struct {
	called     bool
	closeCount int
}

func (m *slotModule) RequestContinuation(context.Context, ContinueCommand) (RunView, error) {
	m.called = true
	return RunView{RunID: "run-slot"}, nil
}
func (m *slotModule) Recover(context.Context) error { return nil }
func (m *slotModule) Close() error                  { m.closeCount++; return nil }

func TestContinuationSlotDelegatesAfterAttachAndFailsClosedBeforeAttach(t *testing.T) {
	slot := NewContinuationSlot()
	if _, err := slot.RequestContinuation(context.Background(), ContinueCommand{}); !errors.Is(err, ErrContinuationUnavailable) {
		t.Fatalf("request before attach error = %v", err)
	}
	module := &slotModule{}
	slot.Attach(module)
	view, err := slot.RequestContinuation(context.Background(), ContinueCommand{})
	if err != nil || view.RunID != "run-slot" || !module.called {
		t.Fatalf("delegated request = view=%+v err=%v called=%v", view, err, module.called)
	}
	slot.Detach(module)
	if _, err := slot.RequestContinuation(context.Background(), ContinueCommand{}); !errors.Is(err, ErrContinuationUnavailable) {
		t.Fatalf("request after detach error = %v", err)
	}
}

func TestContinuationSlotRejectsAttachAfterClose(t *testing.T) {
	slot := NewContinuationSlot()
	if err := slot.Close(); err != nil {
		t.Fatalf("close empty slot: %v", err)
	}
	late := &slotModule{}
	if generation := slot.Attach(late); generation != 0 {
		t.Fatalf("late attach generation = %d, want 0", generation)
	}
	if late.closeCount != 1 {
		t.Fatalf("late module close count = %d, want 1", late.closeCount)
	}
	if slot.Available() {
		t.Fatal("closed slot reports available")
	}
	// Close is idempotent and must not alter the closed state.
	if err := slot.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

type activeSlotModule struct {
	started             chan struct{}
	release             chan struct{}
	requestActive       atomic.Bool
	closedDuringRequest atomic.Bool
}

func (m *activeSlotModule) RequestContinuation(context.Context, ContinueCommand) (RunView, error) {
	m.requestActive.Store(true)
	close(m.started)
	<-m.release
	m.requestActive.Store(false)
	return RunView{RunID: "active-run"}, nil
}

func (m *activeSlotModule) Recover(context.Context) error { return nil }
func (m *activeSlotModule) Close() error {
	if m.requestActive.Load() {
		m.closedDuringRequest.Store(true)
	}
	return nil
}

func TestContinuationSlotReplacementWaitsForActiveRequest(t *testing.T) {
	slot := NewContinuationSlot()
	active := &activeSlotModule{started: make(chan struct{}), release: make(chan struct{})}
	slot.Attach(active)
	requestDone := make(chan error, 1)
	go func() {
		_, err := slot.RequestContinuation(context.Background(), ContinueCommand{})
		requestDone <- err
	}()
	<-active.started
	attachDone := make(chan struct{})
	go func() {
		slot.Attach(&slotModule{})
		close(attachDone)
	}()
	select {
	case <-attachDone:
		t.Fatal("replacement completed while request was active")
	case <-time.After(20 * time.Millisecond):
	}
	close(active.release)
	if err := <-requestDone; err != nil {
		t.Fatalf("active request: %v", err)
	}
	<-attachDone
	if active.closedDuringRequest.Load() {
		t.Fatal("replacement closed module while request was active")
	}
}

func TestContinuationSlotDetachClosesModuleOnce(t *testing.T) {
	slot := NewContinuationSlot()
	module := &slotModule{}
	slot.Attach(module)
	slot.Detach(module)
	if module.closeCount != 1 {
		t.Fatalf("detached module close count = %d, want 1", module.closeCount)
	}
	slot.Detach(module)
	if module.closeCount != 1 {
		t.Fatalf("detached module close count after repeat = %d, want 1", module.closeCount)
	}
}
