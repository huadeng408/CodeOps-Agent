package session

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestWebSocketTicketIsSessionBoundAndOneTime(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	tickets := NewWebSocketTicketsWithClock(time.Minute, func() time.Time { return now })
	raw, expires, err := tickets.Issue(7, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if !expires.Equal(now.Add(time.Minute)) {
		t.Fatalf("expires at %s, want %s", expires, now.Add(time.Minute))
	}
	if _, err := tickets.Consume(raw, "session-b"); !errors.Is(err, ErrWebSocketTicketInvalid) {
		t.Fatalf("foreign session error = %v, want invalid", err)
	}
	if _, err := tickets.Consume(raw, "session-a"); err != nil {
		t.Fatalf("session-bound ticket was consumed after wrong-session probe: %v", err)
	}
}

func TestWebSocketTicketExpiresAndCannotBeReused(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	tickets := NewWebSocketTicketsWithClock(time.Minute, func() time.Time { return now })
	raw, _, err := tickets.Issue(7, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := tickets.Consume(raw, "session-a"); !errors.Is(err, ErrWebSocketTicketExpired) {
		t.Fatalf("expired ticket error = %v, want expired", err)
	}
	if _, err := tickets.Consume(raw, "session-a"); !errors.Is(err, ErrWebSocketTicketInvalid) {
		t.Fatalf("expired ticket remained reusable: %v", err)
	}
}

func TestWebSocketTicketConcurrentConsumeSucceedsOnce(t *testing.T) {
	tickets := NewWebSocketTickets(time.Minute)
	raw, _, err := tickets.Issue(7, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	const consumers = 16
	results := make(chan error, consumers)
	var wg sync.WaitGroup
	wg.Add(consumers)
	for i := 0; i < consumers; i++ {
		go func() {
			defer wg.Done()
			_, consumeErr := tickets.Consume(raw, "session-a")
			results <- consumeErr
		}()
	}
	wg.Wait()
	close(results)
	var success, invalid int
	for consumeErr := range results {
		switch {
		case consumeErr == nil:
			success++
		case errors.Is(consumeErr, ErrWebSocketTicketInvalid):
			invalid++
		default:
			t.Fatalf("unexpected concurrent consume error: %v", consumeErr)
		}
	}
	if success != 1 || invalid != consumers-1 {
		t.Fatalf("concurrent consume results = success:%d invalid:%d, want 1/%d", success, invalid, consumers-1)
	}
}
