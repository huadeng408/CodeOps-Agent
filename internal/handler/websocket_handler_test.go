package handler

import (
	"net/http/httptest"
	"testing"
	"time"

	"code-agent/internal/session"

	"github.com/gorilla/websocket"
)

func TestCheckWebSocketOriginAllowsSameHostAndLocalDevPort(t *testing.T) {
	request := httptest.NewRequest("GET", "http://localhost:8081/api/v1/sessions/s/ws", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	if !checkWebSocketOrigin(request) {
		t.Fatal("local development origin was rejected")
	}
	request.Header.Set("Origin", "https://evil.example")
	if checkWebSocketOrigin(request) {
		t.Fatal("foreign origin was accepted")
	}
}

func TestWebSocketHubNotifyUsesPerConnectionHint(t *testing.T) {
	hub := NewWebSocketHub()
	var conn websocket.Conn
	hub.AddConnection("s", &conn)
	hub.Notify("s")
	select {
	case <-hub.Hint(&conn):
	case <-time.After(time.Second):
		t.Fatal("ledger notification did not reach connection hint")
	}
	hub.RemoveConnection("s", &conn)
}

func TestWebSocketHubReplayGatePreservesQueueOrder(t *testing.T) {
	hub := NewWebSocketHub()
	var conn websocket.Conn
	hub.AddConnection("s", &conn)
	// A live queue entry represents the write loop's pending ledger suffix;
	// payloads are never injected by an external broadcaster.
	hub.Queue("s", &conn) <- []byte(`{"seq":2}`)
	hub.QueueInitial(&conn, []byte(`{"seq":1}`))
	hub.Release(&conn)
	initial := hub.Initial(&conn)
	if len(initial) != 1 || string(initial[0]) != `{"seq":1}` {
		t.Fatalf("replay event=%v", initial)
	}
	if got := string(<-hub.Queue("s", &conn)); got != `{"seq":2}` {
		t.Fatalf("live event=%s", got)
	}
	hub.RemoveConnection("s", &conn)
}

func TestValidateReplayBatchRejectsSequenceGaps(t *testing.T) {
	batch := []session.EventView{{Seq: 3}, {Seq: 4}}
	if err := validateReplayBatch(1, batch); err == nil {
		t.Fatal("sequence gap was accepted")
	}
	if err := validateReplayBatch(2, batch); err != nil {
		t.Fatalf("contiguous replay rejected: %v", err)
	}
	if err := validateReplayBatch(-1, []session.EventView{{Seq: 0}}); err != nil {
		t.Fatalf("sequence zero should be valid after an empty cursor: %v", err)
	}
}

func TestNormalizeReplayCursorFallsBackWhenClientCursorExceedsLedgerHead(t *testing.T) {
	if got := normalizeReplayCursor(8, 99); got != -1 {
		t.Fatalf("cursor above head = %d, want -1 full replay", got)
	}
	if got := normalizeReplayCursor(8, 8); got != 8 {
		t.Fatalf("cursor at head = %d, want 8", got)
	}
	if got := normalizeReplayCursor(-1, 3); got != -1 {
		t.Fatalf("cursor above empty ledger head = %d, want -1", got)
	}
}

func TestValidateLiveEventCursorRejectsConflictingEventAtSameSequence(t *testing.T) {
	if err := validateLiveEventCursor(7, "event-7", 7, "other-event-7"); err == nil {
		t.Fatal("same sequence with a different event id must be rejected")
	}
	if err := validateLiveEventCursor(7, "event-7", 7, "event-7"); err != nil {
		t.Fatalf("duplicate delivery should remain idempotent: %v", err)
	}
}

func TestValidateLiveEventCursorRejectsLiveSequenceGap(t *testing.T) {
	if err := validateLiveEventCursor(7, "event-7", 9, "event-9"); err == nil {
		t.Fatal("live sequence gap must be rejected")
	}
	if err := validateLiveEventCursor(7, "event-7", 8, "event-8"); err != nil {
		t.Fatalf("next contiguous event should be accepted: %v", err)
	}
}

func TestValidateLiveEventCursorRejectsInvalidFirstLiveSequence(t *testing.T) {
	if err := validateLiveEventCursor(-1, "", 2, "event-2"); err == nil {
		t.Fatal("first live event must start at sequence zero")
	}
	if err := validateLiveEventCursor(-1, "", -1, "event-negative"); err == nil {
		t.Fatal("negative live sequence must be rejected")
	}
	if err := validateLiveEventCursor(-1, "", 0, "event-0"); err != nil {
		t.Fatalf("first sequence zero should be accepted: %v", err)
	}
}

func TestValidateLiveEventCursorRejectsMissingEventIDAtKnownSequence(t *testing.T) {
	if err := validateLiveEventCursor(7, "event-7", 7, ""); err == nil {
		t.Fatal("same sequence without an event id must be rejected")
	}
	if err := validateLiveEventCursor(7, "", 7, "event-7"); err == nil {
		t.Fatal("known sequence without a previous event id must be rejected")
	}
}

func TestIsLiveEventEnvelopeRecognizesLedgerFrameWithoutEventID(t *testing.T) {
	event := session.EventView{Type: "user/message", Seq: 8, Content: "hello"}
	if !isLiveEventEnvelope(event) {
		t.Fatal("ledger-shaped frame without event id must still enter cursor validation")
	}
	if isLiveEventEnvelope(session.EventView{}) {
		t.Fatal("an empty decoded object must remain a raw control payload")
	}
}
