package session

import (
	"errors"
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
