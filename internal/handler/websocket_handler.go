package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"code-agent/internal/session"
	"code-agent/pkg/log"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	websocketQueueCapacity = 64
	websocketReplayPage    = 256
	websocketHintCapacity  = 16
	websocketCloseCode     = websocket.CloseTryAgainLater
)

var sessionUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     checkWebSocketOrigin,
}

func checkWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	return isLoopbackHost(u.Hostname()) && isLoopbackHost(requestHostname(r.Host))
}

func requestHostname(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return strings.Trim(hostport, "[]")
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

type connectionCursor struct {
	seq     int64
	eventID string
}

// WebSocketHub owns bounded per-connection queues. A full queue evicts the
// subscriber and closes it with a replayable cursor; events are never silently
// dropped.
type WebSocketHub struct {
	connections map[string]map[*websocket.Conn]chan []byte
	done        map[*websocket.Conn]chan struct{}
	ready       map[*websocket.Conn]chan struct{}
	initial     map[*websocket.Conn][][]byte
	hints       map[*websocket.Conn]chan struct{}
	writeMu     map[*websocket.Conn]*sync.Mutex
	cursors     map[*websocket.Conn]connectionCursor
	closed      map[*websocket.Conn]bool
	mu          sync.RWMutex
}

func NewWebSocketHub() *WebSocketHub {
	return &WebSocketHub{
		connections: make(map[string]map[*websocket.Conn]chan []byte),
		done:        make(map[*websocket.Conn]chan struct{}),
		ready:       make(map[*websocket.Conn]chan struct{}),
		initial:     make(map[*websocket.Conn][][]byte),
		hints:       make(map[*websocket.Conn]chan struct{}),
		writeMu:     make(map[*websocket.Conn]*sync.Mutex),
		cursors:     make(map[*websocket.Conn]connectionCursor),
		closed:      make(map[*websocket.Conn]bool),
	}
}

func (h *WebSocketHub) AddConnection(sessionID string, conn *websocket.Conn) {
	if h == nil || conn == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connections[sessionID] == nil {
		h.connections[sessionID] = make(map[*websocket.Conn]chan []byte)
	}
	if oldDone, exists := h.done[conn]; exists {
		select {
		case <-oldDone:
		default:
			close(oldDone)
		}
	}
	h.connections[sessionID][conn] = make(chan []byte, websocketQueueCapacity)
	h.done[conn] = make(chan struct{})
	h.ready[conn] = make(chan struct{})
	h.hints[conn] = make(chan struct{}, websocketHintCapacity)
	h.writeMu[conn] = &sync.Mutex{}
	h.cursors[conn] = connectionCursor{seq: -1}
	h.closed[conn] = false
	log.Infof("WebSocket connected for session %s, total: %d", sessionID, len(h.connections[sessionID]))
}

func (h *WebSocketHub) RemoveConnection(sessionID string, conn *websocket.Conn) {
	if h == nil || conn == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	conns := h.connections[sessionID]
	if _, ok := conns[conn]; ok {
		delete(conns, conn)
		if done := h.done[conn]; done != nil {
			select {
			case <-done:
			default:
				close(done)
			}
		}
		if ready := h.ready[conn]; ready != nil {
			select {
			case <-ready:
			default:
				close(ready)
			}
		}
		delete(h.done, conn)
		delete(h.ready, conn)
		delete(h.hints, conn)
		delete(h.initial, conn)
		delete(h.writeMu, conn)
		delete(h.cursors, conn)
		delete(h.closed, conn)
	}
	if len(conns) == 0 {
		delete(h.connections, sessionID)
	}
	log.Infof("WebSocket disconnected for session %s, remaining: %d", sessionID, len(h.connections[sessionID]))
}

// Notify is the EventNotifier implementation used by Workbench. It carries
// only a coalescible hint; the handler reads canonical ledger events.
func (h *WebSocketHub) Notify(sessionID string) {
	if h == nil {
		return
	}
	h.mu.RLock()
	conns := make([]*websocket.Conn, 0, len(h.connections[sessionID]))
	for conn := range h.connections[sessionID] {
		conns = append(conns, conn)
	}
	h.mu.RUnlock()
	for _, conn := range conns {
		h.mu.RLock()
		hint, done := h.hints[conn], h.done[conn]
		h.mu.RUnlock()
		if hint == nil {
			continue
		}
		select {
		case hint <- struct{}{}:
		case <-done:
		default:
			h.evictSlow(sessionID, conn)
		}
	}
}

func (h *WebSocketHub) evictSlow(sessionID string, conn *websocket.Conn) {
	if h == nil || conn == nil {
		return
	}
	h.mu.Lock()
	if h.closed[conn] {
		h.mu.Unlock()
		return
	}
	h.closed[conn] = true
	cursor := h.cursors[conn]
	h.mu.Unlock()
	h.RemoveConnection(sessionID, conn)
	go func() {
		defer func() { _ = recover() }()
		payload, _ := json.Marshal(map[string]any{
			"reason":      "slow_consumer",
			"lastSeq":     cursor.seq,
			"lastEventId": cursor.eventID,
		})
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocketCloseCode, string(payload)), time.Now().Add(time.Second))
		_ = conn.Close()
	}()
}

func (h *WebSocketHub) Queue(sessionID string, conn *websocket.Conn) chan []byte {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.connections[sessionID][conn]
}

func (h *WebSocketHub) Done(conn *websocket.Conn) chan struct{} {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.done[conn]
}

func (h *WebSocketHub) Ready(conn *websocket.Conn) chan struct{} {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.ready[conn]
}

func (h *WebSocketHub) Hint(conn *websocket.Conn) chan struct{} {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.hints[conn]
}

func (h *WebSocketHub) Release(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ready := h.ready[conn]; ready != nil {
		select {
		case <-ready:
		default:
			close(ready)
		}
		delete(h.ready, conn)
	}
}

func (h *WebSocketHub) QueueInitial(conn *websocket.Conn, message []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.writeMu[conn]; !ok {
		return
	}
	h.initial[conn] = append(h.initial[conn], append([]byte(nil), message...))
}

func (h *WebSocketHub) Initial(conn *websocket.Conn) [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	messages := h.initial[conn]
	delete(h.initial, conn)
	return messages
}

func (h *WebSocketHub) SetCursor(conn *websocket.Conn, seq int64, eventID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.writeMu[conn]; ok {
		h.cursors[conn] = connectionCursor{seq: seq, eventID: eventID}
	}
}

func (h *WebSocketHub) Cursor(conn *websocket.Conn) (int64, string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	cursor := h.cursors[conn]
	return cursor.seq, cursor.eventID
}

func (h *WebSocketHub) Write(conn *websocket.Conn, kind int, payload []byte) error {
	h.mu.RLock()
	mu := h.writeMu[conn]
	h.mu.RUnlock()
	if mu == nil {
		return websocket.ErrCloseSent
	}
	mu.Lock()
	defer mu.Unlock()
	return conn.WriteMessage(kind, payload)
}

type WebSocketHandler struct {
	hub       *WebSocketHub
	workbench session.WorkbenchModule
	tickets   *session.WebSocketTickets
}

func NewWebSocketHandlerWithWorkbench(hub *WebSocketHub, workbench session.WorkbenchModule, tickets *session.WebSocketTickets) *WebSocketHandler {
	return &WebSocketHandler{hub: hub, workbench: workbench, tickets: tickets}
}

func (h *WebSocketHandler) HandleWebSocket(c *gin.Context) {
	if h == nil || h.hub == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": http.StatusInternalServerError, "message": "websocket hub unavailable"})
		return
	}
	sessionID := strings.TrimSpace(c.Param("id"))
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "message": "session id is required"})
		return
	}
	if c.Query("token") != "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": http.StatusUnauthorized, "message": "query token is not supported"})
		return
	}
	owner, authErr := h.authenticate(c, sessionID)
	if authErr != nil {
		status := http.StatusUnauthorized
		if errors.Is(authErr, session.ErrSessionNotFound) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"code": status, "message": "websocket authorization failed"})
		return
	}
	cursor := int64(-1)
	if rawCursor := strings.TrimSpace(c.Query("after")); rawCursor != "" {
		parsed, parseErr := strconv.ParseInt(rawCursor, 10, 64)
		if parseErr != nil || parsed < -1 {
			c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "message": "invalid event cursor"})
			return
		}
		cursor = parsed
	}
	conn, err := sessionUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Errorf("failed to upgrade websocket: %v", err)
		return
	}
	h.hub.AddConnection(sessionID, conn)
	defer func() {
		h.hub.RemoveConnection(sessionID, conn)
		_ = conn.Close()
	}()
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	if err := h.replay(c.Request.Context(), conn, sessionID, owner, cursor); err != nil {
		log.Warnf("websocket replay failed for session %s: %v", sessionID, err)
		return
	}
	h.hub.Release(conn)
	done, queue, hints := h.hub.Done(conn), h.hub.Queue(sessionID, conn), h.hub.Hint(conn)
	go h.writeLoop(c.Request.Context(), sessionID, owner, conn, queue, hints, done)
	go h.keepAlive(conn, done)
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	for {
		if _, _, readErr := conn.ReadMessage(); readErr != nil {
			if websocket.IsUnexpectedCloseError(readErr, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Errorf("websocket error: %v", readErr)
			}
			return
		}
	}
}

func (h *WebSocketHandler) authenticate(c *gin.Context, sessionID string) (uint, error) {
	if h.tickets != nil {
		raw := strings.TrimSpace(c.Query("ticket"))
		if raw == "" {
			return 0, session.ErrSessionOwnerRequired
		}
		owner, err := h.tickets.Consume(raw, sessionID)
		if err != nil {
			return 0, err
		}
		if h.workbench != nil {
			if _, err := h.workbench.Get(c.Request.Context(), owner, sessionID); err != nil {
				return 0, err
			}
		}
		return owner, nil
	}
	if h.workbench != nil {
		owner, err := authenticatedOwner(c)
		if err != nil {
			return 0, err
		}
		if _, err := h.workbench.Get(c.Request.Context(), owner, sessionID); err != nil {
			return 0, err
		}
		return owner, nil
	}
	return 0, nil
}

func (h *WebSocketHandler) replay(ctx context.Context, conn *websocket.Conn, sessionID string, owner uint, cursor int64) error {
	head := cursor
	if h.workbench != nil {
		view, err := h.workbench.Get(ctx, owner, sessionID)
		if err != nil {
			return err
		}
		head = int64(view.EventCount - 1)
		cursor = normalizeReplayCursor(head, cursor)
	}
	h.hub.SetCursor(conn, cursor, "")
	last := cursor
	for {
		batch, err := h.listAfter(ctx, sessionID, owner, last, websocketReplayPage)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			if h.workbench == nil || last >= head {
				break
			}
			continue
		}
		if err := validateReplayBatch(last, batch); err != nil {
			return err
		}
		for _, event := range batch {
			if event.Seq <= last {
				continue
			}
			raw, err := json.Marshal(event)
			if err != nil {
				return err
			}
			h.hub.QueueInitial(conn, raw)
			last = event.Seq
			h.hub.SetCursor(conn, event.Seq, event.ID)
		}
		if h.workbench != nil && last >= head {
			follow, err := h.listAfter(ctx, sessionID, owner, last, websocketReplayPage)
			if err != nil {
				return err
			}
			if len(follow) == 0 {
				break
			}
		}
	}
	return nil
}

// normalizeReplayCursor treats a client cursor beyond the immutable ledger
// head as corrupt rather than authoritative. Restarting from -1 rebuilds the
// browser's canonical surface and prevents all later events being skipped.
func normalizeReplayCursor(head, cursor int64) int64 {
	if cursor > head {
		return -1
	}
	return cursor
}

func (h *WebSocketHandler) listAfter(ctx context.Context, sessionID string, owner uint, after int64, limit int) ([]session.EventView, error) {
	if h.workbench != nil {
		return h.workbench.EventsAfter(ctx, owner, sessionID, after, limit)
	}
	return nil, errors.New("canonical session workbench is required")
}

func (h *WebSocketHandler) writeLoop(ctx context.Context, sessionID string, owner uint, conn *websocket.Conn, queue chan []byte, hints chan struct{}, done chan struct{}) {
	seenIDs := make(map[string]struct{})
	// Replay was queued while the gate was closed. Its cursor already records
	// the last canonical sequence included in that replay, so these frames are
	// written directly before live suffix processing applies cursor de-duplication.
	for _, raw := range h.hub.Initial(conn) {
		if err := h.hub.Write(conn, websocket.TextMessage, raw); err != nil {
			return
		}
		var event session.EventView
		if json.Unmarshal(raw, &event) == nil && event.ID != "" {
			seenIDs[event.ID] = struct{}{}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-hints:
			if err := h.flushSuffix(ctx, sessionID, owner, conn, seenIDs); err != nil {
				return
			}
		case raw, ok := <-queue:
			if !ok {
				return
			}
			if err := h.writeRawEvent(conn, raw, seenIDs); err != nil {
				return
			}
		}
	}
}

func (h *WebSocketHandler) flushSuffix(ctx context.Context, sessionID string, owner uint, conn *websocket.Conn, seenIDs map[string]struct{}) error {
	last, _ := h.hub.Cursor(conn)
	for {
		batch, err := h.listAfter(ctx, sessionID, owner, last, websocketReplayPage)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		if err := validateReplayBatch(last, batch); err != nil {
			return err
		}
		for _, event := range batch {
			if event.Seq <= last {
				continue
			}
			raw, err := json.Marshal(event)
			if err != nil {
				return err
			}
			if err := h.writeEvent(conn, event.Seq, event.ID, raw, seenIDs); err != nil {
				return err
			}
			last = event.Seq
		}
	}
}

func validateReplayBatch(last int64, batch []session.EventView) error {
	expected := last + 1
	for _, event := range batch {
		if event.Seq != expected {
			return fmt.Errorf("websocket replay sequence gap: expected %d, got %d", expected, event.Seq)
		}
		expected++
	}
	return nil
}

func (h *WebSocketHandler) writeRawEvent(conn *websocket.Conn, raw []byte, seenIDs map[string]struct{}) error {
	var event session.EventView
	if err := json.Unmarshal(raw, &event); err == nil && event.ID != "" {
		return h.writeEvent(conn, event.Seq, event.ID, raw, seenIDs)
	}
	return h.hub.Write(conn, websocket.TextMessage, raw)
}

func (h *WebSocketHandler) writeEvent(conn *websocket.Conn, seq int64, eventID string, raw []byte, seenIDs map[string]struct{}) error {
	last, lastEventID := h.hub.Cursor(conn)
	if err := validateLiveEventCursor(last, lastEventID, seq, eventID); err != nil {
		return err
	}
	if eventID != "" {
		if _, exists := seenIDs[eventID]; exists {
			return nil
		}
	}
	if seq >= 0 && seq <= last {
		return nil
	}
	if err := h.hub.Write(conn, websocket.TextMessage, raw); err != nil {
		return err
	}
	if eventID != "" {
		seenIDs[eventID] = struct{}{}
	}
	h.hub.SetCursor(conn, seq, eventID)
	return nil
}

// validateLiveEventCursor keeps the live stream fail-closed when a producer
// attempts to reuse a sequence number for a different immutable event.
// Re-delivery of the same event remains idempotent so reconnects are safe.
func validateLiveEventCursor(lastSeq int64, lastEventID string, seq int64, eventID string) error {
	if seq < 0 {
		return fmt.Errorf("websocket live sequence is invalid: %d", seq)
	}
	if lastSeq < 0 {
		if seq != 0 {
			return fmt.Errorf("websocket live sequence gap: expected 0, got %d", seq)
		}
		return nil
	}
	if seq == lastSeq {
		if strings.TrimSpace(lastEventID) == "" || strings.TrimSpace(eventID) == "" || lastEventID == eventID {
			return nil
		}
		return fmt.Errorf("websocket live sequence conflict: seq %d has event ids %q and %q", seq, lastEventID, eventID)
	}
	if seq > lastSeq+1 {
		return fmt.Errorf("websocket live sequence gap: expected %d, got %d", lastSeq+1, seq)
	}
	return nil
}

func (h *WebSocketHandler) keepAlive(conn *websocket.Conn, done chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := h.hub.Write(conn, websocket.PingMessage, nil); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

func (h *WebSocketHandler) GetHub() *WebSocketHub { return h.hub }

var _ session.EventNotifier = (*WebSocketHub)(nil)
