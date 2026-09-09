package session

import (
	"context"
	"errors"
	"sync"
)

var ErrContinuationUnavailable = errors.New("agent continuation is unavailable")

// ContinuationSlot keeps the HTTP continuation seam stable while the external
// orchestrator reconnects. It never creates a synthetic run or sidecar fact.
type ContinuationSlot struct {
	mu         sync.RWMutex
	module     ContinuationModule
	generation uint64
}

func NewContinuationSlot() *ContinuationSlot { return &ContinuationSlot{} }

// Available reports whether a live continuation module is currently attached.
func (s *ContinuationSlot) Available() bool { return s.current() != nil }

func (s *ContinuationSlot) Attach(module ContinuationModule) uint64 {
	if s == nil || module == nil {
		return 0
	}
	s.mu.Lock()
	previous := s.module
	s.module = module
	s.generation++
	generation := s.generation
	s.mu.Unlock()
	if previous != nil && previous != module {
		_ = previous.Close()
	}
	return generation
}

func (s *ContinuationSlot) Detach(module ContinuationModule) {
	if s == nil || module == nil {
		return
	}
	s.mu.Lock()
	if s.module == module {
		s.module = nil
	}
	s.mu.Unlock()
}

// detachIf removes module only when it is still the module observed at
// generation. This prevents a stale health check from detaching a replacement.
func (s *ContinuationSlot) detachIf(module ContinuationModule, generation uint64) bool {
	if s == nil || module == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.module != module || s.generation != generation {
		return false
	}
	s.module = nil
	s.generation++
	return true
}

func (s *ContinuationSlot) current() ContinuationModule {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.module
}

func (s *ContinuationSlot) snapshot() (ContinuationModule, uint64) {
	if s == nil {
		return nil, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.module, s.generation
}

func (s *ContinuationSlot) RequestContinuation(ctx context.Context, cmd ContinueCommand) (RunView, error) {
	m := s.current()
	if m == nil {
		return RunView{}, ErrContinuationUnavailable
	}
	return m.RequestContinuation(ctx, cmd)
}

func (s *ContinuationSlot) Recover(ctx context.Context) error {
	m := s.current()
	if m == nil {
		return ErrContinuationUnavailable
	}
	return m.Recover(ctx)
}

func (s *ContinuationSlot) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	m := s.module
	s.module = nil
	s.mu.Unlock()
	if m == nil {
		return nil
	}
	return m.Close()
}
