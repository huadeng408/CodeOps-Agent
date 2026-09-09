package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type continuationHealth interface {
	Health(context.Context) error
}

// ContinuationSupervisorStatus is a read-only runtime projection. It is not
// persisted and never competes with the canonical session ledger.
type ContinuationSupervisorStatus struct {
	Attached            bool
	Generation          uint64
	LastHealthError     string
	LastRecoveryAt      *time.Time
	LastTransitionAt    *time.Time
	ConsecutiveFailures int
	NextRetryAt         *time.Time
}

// ContinuationSupervisor maintains one live continuation module behind a
// stable slot. Unhealthy modules are detached and closed; replacements are
// recovered before attach so the canonical ledger and run lineage are kept.
type ContinuationSupervisor struct {
	slot            *ContinuationSlot
	interval        time.Duration
	connect         func(context.Context) (ContinuationModule, error)
	mu              sync.Mutex
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	closed          bool
	runGeneration   uint64
	status          ContinuationSupervisorStatus
	connectFailures int
	nextConnectAt   time.Time
	partialWarning  string
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
	if s.slot.isClosed() {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	if s.closed || s.cancel != nil || ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	s.runGeneration++
	runGeneration := s.runGeneration
	s.cancel = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	go s.loop(loopCtx, runGeneration)
}

func (s *ContinuationSupervisor) loop(ctx context.Context, runGeneration uint64) {
	defer s.wg.Done()
	defer s.clearRunAfterExit(runGeneration)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	if ctx.Err() != nil {
		return
	}
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

func (s *ContinuationSupervisor) clearRunAfterExit(runGeneration uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed && s.runGeneration == runGeneration {
		s.cancel = nil
	}
}

func (s *ContinuationSupervisor) reconcile(ctx context.Context) {
	if s == nil || s.slot == nil || s.connect == nil {
		return
	}
	if s.slot.isClosed() {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return
	}
	if current, generation := s.slot.snapshot(); current != nil {
		if health, ok := current.(continuationHealth); ok {
			healthCtx, cancel := context.WithTimeout(ctx, s.interval)
			err := health.Health(healthCtx)
			cancel()
			if err == nil {
				s.recordHealth(nil, generation)
				return
			}
			s.recordHealth(err, generation)
			if detachedGeneration, detached := s.slot.detachIf(current, generation); detached {
				s.recordTransition(false, detachedGeneration)
				_ = current.Close()
			}
		}
		return
	}
	if !s.shouldAttemptConnect(time.Now()) {
		return
	}
	connectCtx, cancelConnect := context.WithTimeout(ctx, s.interval)
	module, err := s.connect(connectCtx)
	cancelConnect()
	if err != nil || module == nil {
		if err != nil {
			s.recordHealth(err, 0)
		}
		s.scheduleConnectRetry()
		return
	}
	if health, ok := module.(continuationHealth); ok {
		healthCtx, cancel := context.WithTimeout(ctx, s.interval)
		err = health.Health(healthCtx)
		cancel()
		if err != nil {
			s.recordHealth(err, 0)
			_ = module.Close()
			s.scheduleConnectRetry()
			return
		}
	}
	recoverCtx, cancelRecover := context.WithTimeout(ctx, s.interval)
	err = module.Recover(recoverCtx)
	cancelRecover()
	if err != nil {
		var partial *PartialRecoveryError
		if errors.As(err, &partial) {
			// A corrupt or unreadable session is isolated by SessionRunner;
			// healthy sessions are already queued and the transport remains
			// usable. Attach while retaining the warning in health status.
			if s.attachRecovered(module) {
				s.recordPartialRecovery(err, s.Status().Generation)
				return
			}
			_ = module.Close()
			return
		}
		s.recordHealth(err, 0)
		_ = module.Close()
		s.scheduleConnectRetry()
		return
	}
	s.attachRecovered(module)
}

// attachRecovered owns module once called. It makes the shutdown check and
// slot attach one atomic supervisor transition and closes rejected modules
// exactly once. Close cannot mark the supervisor closed between the check and
// attach, so a late Recover result never resurrects a closed slot.
func (s *ContinuationSupervisor) attachRecovered(module ContinuationModule) bool {
	s.mu.Lock()
	if s.closed || module == nil {
		s.mu.Unlock()
		if module != nil {
			_ = module.Close()
		}
		return false
	}
	generation := s.slot.Attach(module)
	if generation == 0 {
		// The slot may have been closed independently while recovery was
		// in flight. Treat a rejected attach as a failed transition so the
		// caller closes the late module and status never claims attachment.
		s.mu.Unlock()
		return false
	}
	now := time.Now().UTC()
	s.connectFailures = 0
	s.nextConnectAt = time.Time{}
	s.status.Attached = true
	s.status.Generation = generation
	s.status.LastHealthError = ""
	s.partialWarning = ""
	s.status.LastRecoveryAt = &now
	s.status.LastTransitionAt = &now
	s.status.ConsecutiveFailures = 0
	s.status.NextRetryAt = nil
	s.mu.Unlock()
	return true
}

func (s *ContinuationSupervisor) shouldAttemptConnect(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextConnectAt.IsZero() || !now.Before(s.nextConnectAt)
}

func (s *ContinuationSupervisor) scheduleConnectRetry() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connectFailures++
	delay := s.interval
	for i := 1; i < s.connectFailures && delay < time.Minute; i++ {
		delay *= 2
	}
	if delay > time.Minute {
		delay = time.Minute
	}
	s.nextConnectAt = time.Now().Add(delay)
	s.status.ConsecutiveFailures = s.connectFailures
	nextRetryAt := s.nextConnectAt.UTC()
	s.status.NextRetryAt = &nextRetryAt
}

func (s *ContinuationSupervisor) recordHealth(err error, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil && s.partialWarning == "" {
		s.status.LastHealthError = ""
	} else if err != nil {
		s.status.LastHealthError = fmt.Sprint(err)
	}
	if generation != 0 {
		s.status.Generation = generation
	}
}

func (s *ContinuationSupervisor) recordPartialRecovery(err error, generation uint64) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partialWarning = fmt.Sprint(err)
	s.status.LastHealthError = s.partialWarning
	if generation != 0 {
		s.status.Generation = generation
	}
}

func (s *ContinuationSupervisor) recordTransition(attached bool, generation uint64) {
	now := time.Now().UTC()
	s.mu.Lock()
	s.status.Attached = attached
	s.status.Generation = generation
	s.status.LastTransitionAt = &now
	s.mu.Unlock()
}

// Status returns a defensive copy of the runtime projection.
func (s *ContinuationSupervisor) Status() ContinuationSupervisorStatus {
	if s == nil {
		return ContinuationSupervisorStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status
	if s.closed {
		status.Attached = false
		status.LastHealthError = "continuation supervisor is closed"
		status.LastRecoveryAt = nil
		status.LastTransitionAt = nil
		status.ConsecutiveFailures = 0
		status.NextRetryAt = nil
		return status
	}
	if status.LastRecoveryAt != nil {
		t := *status.LastRecoveryAt
		status.LastRecoveryAt = &t
	}
	if status.LastTransitionAt != nil {
		t := *status.LastTransitionAt
		status.LastTransitionAt = &t
	}
	if status.NextRetryAt != nil {
		t := *status.NextRetryAt
		status.NextRetryAt = &t
	}
	if s.slot != nil {
		_, generation := s.slot.snapshot()
		status.Attached = s.slot.Available()
		status.Generation = generation
	}
	return status
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
