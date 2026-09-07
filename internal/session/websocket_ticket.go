package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrWebSocketTicketInvalid = errors.New("websocket ticket is invalid")
	ErrWebSocketTicketExpired = errors.New("websocket ticket is expired")
)

type websocketTicketRecord struct {
	ownerID   uint
	sessionID string
	expiresAt time.Time
}

// WebSocketTickets is a process-local, one-time capability store. Only a
// SHA-256 digest is retained, so a database dump cannot be used as a bearer
// token. The ticket is intentionally short-lived and session-bound.
type WebSocketTickets struct {
	ttl   time.Duration
	now   func() time.Time
	mu    sync.Mutex
	items map[[32]byte]websocketTicketRecord
}

func NewWebSocketTickets(ttl time.Duration) *WebSocketTickets {
	return NewWebSocketTicketsWithClock(ttl, time.Now)
}

// NewWebSocketTicketsWithClock is primarily useful for deterministic expiry
// tests; production callers should use NewWebSocketTickets.
func NewWebSocketTicketsWithClock(ttl time.Duration, now func() time.Time) *WebSocketTickets {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	if now == nil {
		now = time.Now
	}
	return &WebSocketTickets{ttl: ttl, now: now, items: make(map[[32]byte]websocketTicketRecord)}
}

// Issue creates an opaque raw ticket. The raw value is returned exactly once
// to the authenticated HTTP caller; only its digest is kept in memory.
func (t *WebSocketTickets) Issue(ownerID uint, sessionID string) (string, time.Time, error) {
	if t == nil || ownerID == 0 || strings.TrimSpace(sessionID) == "" {
		return "", time.Time{}, ErrWebSocketTicketInvalid
	}
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", time.Time{}, err
	}
	raw := base64.RawURLEncoding.EncodeToString(rawBytes)
	digest := sha256.Sum256([]byte(raw))
	now := t.now().UTC()
	expires := now.Add(t.ttl)
	t.mu.Lock()
	for key, item := range t.items {
		if !item.expiresAt.After(now) {
			delete(t.items, key)
		}
	}
	t.items[digest] = websocketTicketRecord{ownerID: ownerID, sessionID: sessionID, expiresAt: expires}
	t.mu.Unlock()
	return raw, expires, nil
}

// Consume atomically validates and spends a ticket. A ticket cannot be used
// for another session or replayed after a successful consume.
func (t *WebSocketTickets) Consume(raw, sessionID string) (uint, error) {
	if t == nil || strings.TrimSpace(raw) == "" || strings.TrimSpace(sessionID) == "" {
		return 0, ErrWebSocketTicketInvalid
	}
	digest := sha256.Sum256([]byte(raw))
	now := t.now().UTC()
	t.mu.Lock()
	defer t.mu.Unlock()
	record, ok := t.items[digest]
	if !ok {
		return 0, ErrWebSocketTicketInvalid
	}
	if !record.expiresAt.After(now) {
		delete(t.items, digest)
		return 0, ErrWebSocketTicketExpired
	}
	if record.sessionID != sessionID || record.ownerID == 0 {
		return 0, ErrWebSocketTicketInvalid
	}
	delete(t.items, digest)
	return record.ownerID, nil
}
