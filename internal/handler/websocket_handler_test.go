package handler

import (
	"net/http/httptest"
	"testing"
	"time"

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
