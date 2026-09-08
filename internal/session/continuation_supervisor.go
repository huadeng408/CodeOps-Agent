package session

import (
	"context"
	"sync"
	"time"
)

type continuationHealth interface {
	Health(context.Context) error
}

// ContinuationSupervisor maintains one live continuation module behind a
// stable slot. Unhealthy modules are detached and closed; replacements are
// recovered before attach so the canonical ledger and run lineage are kept.
type ContinuationSupervisor struct {
	slot     *ContinuationSlot
	interval time.Duration
	connect  func(context.Context) (ContinuationModule, error)
	mu       sync.Mutex
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	closed   bool
}

func NewContinuationSupervisor(slot *ContinuationSlot, interval time.Duration, connect func(context.Context) (ContinuationModule, error)) *ContinuationSupervisor {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &ContinuationSupervisor{slot: slot, interval: interval, connect: connect}
}

func (s *ContinuationSupervisor) Start(ctx context.Context) {
	if s == nil || s.slot == nil || s.connect == nil {
		return
	}
	s.mu.Lock()
	if s.closed || s.cancel != nil {
		s.mu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	go s.loop(loopCtx)
}

func (s *ContinuationSupervisor) loop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	s.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcile(ctx)
		}
	}
}

func (s *ContinuationSupervisor) reconcile(ctx context.Context) {
	if current := s.slot.current(); current != nil {
		if health, ok := current.(continuationHealth); ok {
			healthCtx, cancel := context.WithTimeout(ctx, s.interval)
			err := health.Health(healthCtx)
			cancel()
			if err == nil {
				return
			}
			s.slot.Detach(current)
			_ = current.Close()
		}
		return
	}
	module, err := s.connect(ctx)
	if err != nil || module == nil {
		return
	}
	if health, ok := module.(continuationHealth); ok {
		healthCtx, cancel := context.WithTimeout(ctx, s.interval)
		err = health.Health(healthCtx)
		cancel()
		if err != nil {
			_ = module.Close()
			return
		}
	}
	if err := module.Recover(ctx); err != nil {
		_ = module.Close()
		return
	}
	s.slot.Attach(module)
}

func (s *ContinuationSupervisor) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	if s.slot != nil {
		return s.slot.Close()
	}
	return nil
}
