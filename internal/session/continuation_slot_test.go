package session

import (
	"context"
	"errors"
	"testing"
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
