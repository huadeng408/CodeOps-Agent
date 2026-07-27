package cli

import (
	"testing"
	"time"
)

// TestRecordInterrupt mirrors App.handleInterrupt's counter semantics so a
// triple Ctrl+C at the idle prompt force-quits exactly like it does mid-turn.
func TestRecordInterrupt(t *testing.T) {
	tests := []struct {
		name      string
		gaps      []time.Duration // delay added before each press
		wantForce bool            // result expected on the final press
		wantCount int             // expected counter value after the sequence
	}{
		{
			name:      "single press does not force-quit",
			gaps:      []time.Duration{0},
			wantForce: false,
			wantCount: 1,
		},
		{
			name:      "two rapid presses do not force-quit",
			gaps:      []time.Duration{0, 0},
			wantForce: false,
			wantCount: 2,
		},
		{
			name:      "three rapid presses force-quit",
			gaps:      []time.Duration{0, 0, 0},
			wantForce: true,
			wantCount: 3,
		},
		{
			name:      "gap larger than window resets the counter",
			gaps:      []time.Duration{0, interruptWindow + 50 * time.Millisecond, 0},
			wantForce: false,
			wantCount: 2,
		},
		{
			name:      "third press after a slow gap does not force-quit",
			gaps:      []time.Duration{0, 0, interruptWindow + 50 * time.Millisecond},
			wantForce: false,
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &InputBuffer{}
			now := time.Now()
			var force bool
			for _, gap := range tt.gaps {
				now = now.Add(gap)
				force = b.recordInterrupt(now)
			}
			if force != tt.wantForce {
				t.Errorf("force-quit = %v, want %v", force, tt.wantForce)
			}
			if b.interrupts != tt.wantCount {
				t.Errorf("counter = %d, want %d", b.interrupts, tt.wantCount)
			}
		})
	}
}

// TestSetInterruptHandlerStoresCallback verifies the optional override hook
// used to share the interrupt counter with App.handleInterrupt.
func TestSetInterruptHandlerStoresCallback(t *testing.T) {
	b := &InputBuffer{}
	if b.onInterrupt != nil {
		t.Fatal("onInterrupt should start nil")
	}

	calls := 0
	b.SetInterruptHandler(func() bool {
		calls++
		return calls >= interruptForceThreshold
	})

	if b.onInterrupt == nil {
		t.Fatal("onInterrupt not installed by SetInterruptHandler")
	}
	for i := 1; i <= interruptForceThreshold; i++ {
		got := b.onInterrupt()
		want := i >= interruptForceThreshold
		if got != want {
			t.Errorf("call %d: force = %v, want %v", i, got, want)
		}
	}
}
