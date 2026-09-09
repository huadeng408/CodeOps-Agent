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
	calls      sync.RWMutex
	module     ContinuationModule
	generation uint64
	closed     bool
}

func NewContinuationSlot() *ContinuationSlot { return &ContinuationSlot{} }

// Available reports whether a live continuation module is currently attached.
func (s *ContinuationSlot) Available() bool { return s.current() != nil }

func (s *ContinuationSlot) isClosed() bool {
	if s == nil {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

func (s *ContinuationSlot) Attach(module ContinuationModule) uint64 {
	if s == nil || module == nil {
		return 0
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		// The slot owns the lifecycle of attached modules. A late attach
		// racing with shutdown must not resurrect a continuation after Close.
		_ = module.Close()
		return 0
	}
	previous := s.module
	s.module = module
	s.generation++
	generation := s.generation
	s.mu.Unlock()
	if previous != nil && previous != module {
		s.calls.Lock()
		_ = previous.Close()
		s.calls.Unlock()
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
		s.generation++
	}
	s.mu.Unlock()
}

// detachIf removes module only when it is still the module observed at
// generation. This prevents a stale health check from detaching a replacement.
func (s *ContinuationSlot) detachIf(module ContinuationModule, generation uint64) (uint64, bool) {
	if s == nil || module == nil {
		return 0, false
	}
	s.mu.Lock()
	if s.module != module || s.generation != generation {
		currentGeneration := s.generation
		s.mu.Unlock()
		return currentGeneration, false
	}
	s.module = nil
	s.generation++
	currentGeneration := s.generation
	s.mu.Unlock()
	// No later slot call can select the detached module. Wait for calls that
	// selected it before the detach to finish before its owner closes it.
	s.calls.Lock()
	s.calls.Unlock()
	return currentGeneration, true
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
	if s == nil {
		return RunView{}, ErrContinuationUnavailable
	}
	s.calls.RLock()
	defer s.calls.RUnlock()
	m := s.current()
	if m == nil {
		return RunView{}, ErrContinuationUnavailable
	}
	return m.RequestContinuation(ctx, cmd)
}

func (s *ContinuationSlot) Recover(ctx context.Context) error {
	if s == nil {
		return ErrContinuationUnavailable
	}
	s.calls.RLock()
	defer s.calls.RUnlock()
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
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	m := s.module
	s.module = nil
	if m != nil {
		s.generation++
	}
	s.mu.Unlock()
	if m == nil {
		return nil
	}
	s.calls.Lock()
	defer s.calls.Unlock()
	return m.Close()
}
