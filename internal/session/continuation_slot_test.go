package session

import (
	"context"
	"errors"
	"testing"
)

type slotModule struct{ called bool }

func (m *slotModule) RequestContinuation(context.Context, ContinueCommand) (RunView, error) {
	m.called = true
	return RunView{RunID: "run-slot"}, nil
}
func (m *slotModule) Recover(context.Context) error { return nil }
func (m *slotModule) Close() error                  { return nil }

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
