package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	eventSchemaVersion = 1
	eventTimeFormat    = time.RFC3339Nano
)

var (
	// ErrSequenceConflict means that the caller's expected sequence is stale.
	ErrSequenceConflict = errors.New("session event sequence conflict")
	// ErrEventIntegrity means that a persisted event or hash chain is invalid.
	ErrEventIntegrity = errors.New("session event integrity failure")
	// ErrLegacySnapshotNotFound means that no readable legacy sessions row exists.
	ErrLegacySnapshotNotFound = errors.New("legacy session snapshot not found")
	// ErrLegacySnapshotChanged prevents a second import from silently changing history.
	ErrLegacySnapshotChanged = errors.New("legacy session snapshot changed")
	// ErrLegacySnapshotInvalid means that the source payload is not valid JSON.
	ErrLegacySnapshotInvalid = errors.New("legacy session snapshot is invalid")
	ErrForkTargetInvalid     = errors.New("session fork target is invalid")
	ErrSessionExists         = errors.New("session event history already exists")
	ErrRewindTargetInvalid   = errors.New("session rewind target is invalid")
)

// Event is the immutable record written to the session event ledger.
// Payload is canonical JSON and is never interpreted by the ledger itself.
type Event struct {
	SessionID    string            `json:"session_id"`
	Seq          int64             `json:"seq"`
	EventID      string            `json:"event_id"`
	Version      int               `json:"version"`
	Type         string            `json:"type"`
	CreatedAt    time.Time         `json:"created_at"`
	Payload      json.RawMessage   `json:"payload"`
	SurfaceOp    *SurfaceOperation `json:"surface_op,omitempty"`
	PrevChecksum string            `json:"prev_checksum,omitempty"`
	Checksum     string            `json:"checksum"`
}

// SurfaceOperation describes how one event changes the model-visible surface.
// Append adds the event; replace hides active events whose original sequence
// lies in the inclusive [Start, End] range before adding the replacement.
type SurfaceOperation struct {
	Op    string `json:"op"`
	Start int64  `json:"start,omitempty"`
	End   int64  `json:"end,omitempty"`
}

// LegacyImportPayload records the source identity and original snapshot bytes
// carried by a legacy/import event. SourceSHA256 is computed from normalized
// JSON, while Snapshot retains the source payload for faithful replay.
type LegacyImportPayload struct {
	SessionID    string          `json:"session_id"`
	SourceSHA256 string          `json:"source_sha256"`
	Snapshot     json.RawMessage `json:"snapshot"`
}

type rewindPayload struct {
	TargetSeq int64 `json:"target_seq"`
}

type pendingEvent struct {
	eventType string
	payload   json.RawMessage
	surfaceOp *SurfaceOperation
}

// EventLog is the durable seam used by the Harness for append-only session
// events. Implementations must reject stale expected sequences atomically.
type EventLog interface {
	Append(ctx context.Context, sessionID string, expectedSeq int64, eventType string, payload any) (Event, error)
	AppendSurface(ctx context.Context, sessionID string, expectedSeq int64, eventType string, payload any, operation SurfaceOperation) (Event, error)
	ImportLegacySnapshot(ctx context.Context, sessionID string) (Event, bool, error)
	Fork(ctx context.Context, sourceSessionID, targetSessionID string, targetSeq int64) error
	Rewind(ctx context.Context, sessionID string, targetSeq int64) (Event, error)
	RewindExpected(ctx context.Context, sessionID string, targetSeq, expectedSeq int64) (Event, error)
	Events(ctx context.Context, sessionID string) ([]Event, error)
	EventsAfter(ctx context.Context, sessionID string, afterSeq int64, limit int) ([]Event, error)
	SessionIDs(ctx context.Context) ([]string, error)
	Surface(ctx context.Context, sessionID string) ([]Event, error)
	Verify(ctx context.Context, sessionID string) error
	Close() error
}

// SQLiteEventLog stores events in one append-only SQLite table. The database
// remains independent from the legacy sessions snapshot table so migration can
// be performed without dual-writing mutable state.
type SQLiteEventLog struct {
	Path     string
	db       *sql.DB
	mu       sync.Mutex
	appendMu sync.Mutex
}

// OpenSQLiteEventLog opens or creates a durable event ledger at path.
func OpenSQLiteEventLog(path string) (*SQLiteEventLog, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("session event log path is required")
	}
	log := &SQLiteEventLog{Path: path}
	if err := log.ensure(context.Background()); err != nil {
		return nil, err
	}
	return log, nil
}

// Append atomically appends one event when expectedSeq equals the next sequence.
func (l *SQLiteEventLog) Append(
	ctx context.Context,
	sessionID string,
	expectedSeq int64,
	eventType string,
	payload any,
) (Event, error) {
	return l.append(ctx, sessionID, expectedSeq, eventType, payload, nil)
}

// AppendSurface appends one event and records its model-surface operation.
func (l *SQLiteEventLog) AppendSurface(
	ctx context.Context,
	sessionID string,
	expectedSeq int64,
	eventType string,
	payload any,
	operation SurfaceOperation,
) (Event, error) {
	if err := validateSurfaceOperation(eventType, operation); err != nil {
		return Event{}, err
	}
	return l.append(ctx, sessionID, expectedSeq, eventType, payload, &operation)
}

func (l *SQLiteEventLog) append(
	ctx context.Context,
	sessionID string,
	expectedSeq int64,
	eventType string,
	payload any,
	surfaceOp *SurfaceOperation,
) (Event, error) {
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	return l.appendUnlocked(ctx, sessionID, expectedSeq, eventType, payload, surfaceOp)
}

func (l *SQLiteEventLog) appendUnlocked(
	ctx context.Context,
	sessionID string,
	expectedSeq int64,
	eventType string,
	payload any,
	surfaceOp *SurfaceOperation,
) (Event, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("marshal session event payload: %w", err)
	}
	appended, err := l.appendBatchUnlocked(ctx, sessionID, expectedSeq, []pendingEvent{{
		eventType: eventType,
		payload:   payloadJSON,
		surfaceOp: surfaceOp,
	}})
	if err != nil {
		return Event{}, err
	}
	return appended[0], nil
}

// appendBatchUnlocked commits a sequence of events in one SQLite transaction.
// The caller must hold appendMu; every event is still validated against the
// current hash chain before the transaction is committed.
func (l *SQLiteEventLog) appendBatchUnlocked(
	ctx context.Context,
	sessionID string,
	expectedSeq int64,
	pending []pendingEvent,
) ([]Event, error) {
	if err := validateEventInput(sessionID, expectedSeq, "batch"); err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		return nil, errors.New("session event batch must not be empty")
	}
	for _, item := range pending {
		if strings.TrimSpace(item.eventType) == "" {
			return nil, errors.New("session event type is required")
		}
		if !json.Valid(item.payload) {
			return nil, errors.New("session event payload must be valid JSON")
		}
		if item.surfaceOp != nil {
			if err := validateSurfaceOperation(item.eventType, *item.surfaceOp); err != nil {
				return nil, err
			}
		}
	}
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	if err := l.Verify(ctx, sessionID); err != nil {
		return nil, err
	}

	conn, err := l.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open session event connection: %w", err)
	}
	defer conn.Close()
	// BEGIN IMMEDIATE takes the SQLite write reservation before reading the
	// current sequence. This avoids the deferred-transaction upgrade race where
	// two processes both read the same sequence and one receives SQLITE_BUSY
	// instead of the documented sequence conflict.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, fmt.Errorf("begin session event transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var nextSeq int64
	var previousChecksum string
	if err := conn.QueryRowContext(ctx, `
SELECT COALESCE(MAX(seq) + 1, 0), COALESCE((
  SELECT checksum FROM session_events
  WHERE session_id = ? ORDER BY seq DESC LIMIT 1
), '')
	FROM session_events WHERE session_id = ?`, sessionID, sessionID).Scan(&nextSeq, &previousChecksum); err != nil {
		return nil, fmt.Errorf("read session event sequence: %w", err)
	}
	if nextSeq != expectedSeq {
		return nil, fmt.Errorf("%w: session=%s expected=%d actual=%d", ErrSequenceConflict, sessionID, expectedSeq, nextSeq)
	}

	appended := make([]Event, 0, len(pending))
	for index, item := range pending {
		eventID, err := newEventID()
		if err != nil {
			return nil, err
		}
		event := Event{
			SessionID:    sessionID,
			Seq:          expectedSeq + int64(index),
			EventID:      eventID,
			Version:      eventSchemaVersion,
			Type:         item.eventType,
			CreatedAt:    time.Now().UTC(),
			Payload:      append(json.RawMessage(nil), item.payload...),
			SurfaceOp:    cloneSurfaceOperation(item.surfaceOp),
			PrevChecksum: previousChecksum,
		}
		event.Checksum = checksumEvent(event)
		if err := insertEvent(ctx, conn, event); err != nil {
			return nil, err
		}
		appended = append(appended, event)
		previousChecksum = event.Checksum
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, fmt.Errorf("commit session event: %w", err)
	}
	committed = true
	return appended, nil
}

func insertEvent(ctx context.Context, conn *sql.Conn, event Event) error {
	if _, err := conn.ExecContext(ctx, `
INSERT INTO session_events
		(session_id, seq, event_id, version, type, created_at, payload, surface_op, prev_checksum, checksum)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.SessionID, event.Seq, event.EventID, event.Version, event.Type,
		event.CreatedAt.Format(eventTimeFormat), string(event.Payload), surfaceOperationJSON(event.SurfaceOp), event.PrevChecksum, event.Checksum,
	); err != nil {
		return fmt.Errorf("append session event: %w", err)
	}
	return nil
}

// ImportLegacySnapshot creates one idempotent log-only import event from the
// existing sessions table. It never updates or deletes the legacy row.
func (l *SQLiteEventLog) ImportLegacySnapshot(ctx context.Context, sessionID string) (Event, bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return Event{}, false, errors.New("legacy session session_id is required")
	}
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return Event{}, false, err
	}

	var rawSnapshot string
	if err := l.db.QueryRowContext(ctx, `SELECT payload FROM sessions WHERE id = ?`, sessionID).Scan(&rawSnapshot); err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return Event{}, false, fmt.Errorf("%w: %s", ErrLegacySnapshotNotFound, sessionID)
		}
		return Event{}, false, fmt.Errorf("read legacy session snapshot: %w", err)
	}
	normalized, err := normalizeSnapshot([]byte(rawSnapshot))
	if err != nil {
		return Event{}, false, err
	}
	digest := sha256.Sum256(normalized)
	sourceSHA256 := hex.EncodeToString(digest[:])

	events, err := l.Events(ctx, sessionID)
	if err != nil {
		return Event{}, false, err
	}
	for _, event := range events {
		if event.Type != "legacy/import" {
			continue
		}
		var imported LegacyImportPayload
		if err := json.Unmarshal(event.Payload, &imported); err != nil {
			return Event{}, false, fmt.Errorf("%w: decode legacy import event", ErrEventIntegrity)
		}
		if imported.SessionID != sessionID || imported.SourceSHA256 != sourceSHA256 {
			return Event{}, false, fmt.Errorf("%w: source checksum differs for %s", ErrLegacySnapshotChanged, sessionID)
		}
		return event, false, nil
	}

	payload := LegacyImportPayload{
		SessionID:    sessionID,
		SourceSHA256: sourceSHA256,
		Snapshot:     json.RawMessage(append([]byte(nil), []byte(rawSnapshot)...)),
	}
	event, err := l.appendUnlocked(ctx, sessionID, int64(len(events)), "legacy/import", payload, nil)
	return event, true, err
}

// Fork copies a trusted event prefix into a new session with freshly derived
// event IDs and checksums. The source and target histories remain independent.
func (l *SQLiteEventLog) Fork(ctx context.Context, sourceSessionID, targetSessionID string, targetSeq int64) error {
	return l.fork(ctx, sourceSessionID, targetSessionID, targetSeq, nil, true)
}

// ForkWithState atomically copies a trusted prefix and appends a normalized
// state snapshot for the child. Legacy import markers are detached so the
// child never depends on the mutable source snapshot table.
func (l *SQLiteEventLog) ForkWithState(ctx context.Context, sourceSessionID, targetSessionID string, targetSeq int64, state Session) error {
	return l.fork(ctx, sourceSessionID, targetSessionID, targetSeq, &state, true)
}

func (l *SQLiteEventLog) fork(
	ctx context.Context,
	sourceSessionID, targetSessionID string,
	targetSeq int64,
	state *Session,
	detachLegacyImport bool,
) error {
	if strings.TrimSpace(sourceSessionID) == "" || strings.TrimSpace(targetSessionID) == "" || sourceSessionID == targetSessionID || targetSeq < 0 {
		return ErrForkTargetInvalid
	}
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	if err := l.Verify(ctx, sourceSessionID); err != nil {
		return err
	}
	source, err := l.Events(ctx, sourceSessionID)
	if err != nil {
		return err
	}
	if targetSeq >= int64(len(source)) {
		return fmt.Errorf("%w: target seq %d exceeds source history", ErrForkTargetInvalid, targetSeq)
	}

	conn, err := l.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open fork transaction connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin fork transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var targetCount int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_events WHERE session_id = ?`, targetSessionID).Scan(&targetCount); err != nil {
		return fmt.Errorf("check fork target history: %w", err)
	}
	if targetCount != 0 {
		return fmt.Errorf("%w: target session %s", ErrSessionExists, targetSessionID)
	}

	previousChecksum := ""
	for index, sourceEvent := range source[:targetSeq+1] {
		eventType := sourceEvent.Type
		if detachLegacyImport && eventType == "legacy/import" {
			eventType = "session/fork/import"
		}
		eventID, err := newEventID()
		if err != nil {
			return err
		}
		event := Event{
			SessionID:    targetSessionID,
			Seq:          int64(index),
			EventID:      eventID,
			Version:      eventSchemaVersion,
			Type:         eventType,
			CreatedAt:    time.Now().UTC(),
			Payload:      append(json.RawMessage(nil), sourceEvent.Payload...),
			SurfaceOp:    cloneSurfaceOperation(sourceEvent.SurfaceOp),
			PrevChecksum: previousChecksum,
		}
		event.Checksum = checksumEvent(event)
		if err := insertEvent(ctx, conn, event); err != nil {
			return err
		}
		previousChecksum = event.Checksum
	}
	if state != nil {
		payload, err := json.Marshal(*state)
		if err != nil {
			return fmt.Errorf("marshal forked session state: %w", err)
		}
		eventID, err := newEventID()
		if err != nil {
			return err
		}
		event := Event{
			SessionID:    targetSessionID,
			Seq:          targetSeq + 1,
			EventID:      eventID,
			Version:      eventSchemaVersion,
			Type:         sessionStateEventType,
			CreatedAt:    time.Now().UTC(),
			Payload:      payload,
			PrevChecksum: previousChecksum,
		}
		event.Checksum = checksumEvent(event)
		if err := insertEvent(ctx, conn, event); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit fork transaction: %w", err)
	}
	committed = true
	return nil
}

// Rewind appends a log-only marker. Surface replay honors the marker by
// removing active nodes newer than targetSeq while retaining the raw history.
func (l *SQLiteEventLog) Rewind(ctx context.Context, sessionID string, targetSeq int64) (Event, error) {
	if strings.TrimSpace(sessionID) == "" || targetSeq < 0 {
		return Event{}, ErrRewindTargetInvalid
	}
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	events, err := l.Events(ctx, sessionID)
	if err != nil {
		return Event{}, err
	}
	return l.rewindExpectedUnlocked(ctx, sessionID, targetSeq, int64(len(events)), events)
}

// RewindExpected appends one rewind marker only when expectedSeq is still the
// next canonical sequence. Callers use it to prevent a restore racing past a
// newly appended Session fact.
func (l *SQLiteEventLog) RewindExpected(ctx context.Context, sessionID string, targetSeq, expectedSeq int64) (Event, error) {
	if strings.TrimSpace(sessionID) == "" || targetSeq < 0 {
		return Event{}, ErrRewindTargetInvalid
	}
	if expectedSeq < 0 {
		return Event{}, errors.New("session rewind expected sequence must be non-negative")
	}
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	events, err := l.Events(ctx, sessionID)
	if err != nil {
		return Event{}, err
	}
	return l.rewindExpectedUnlocked(ctx, sessionID, targetSeq, expectedSeq, events)
}

func (l *SQLiteEventLog) rewindExpectedUnlocked(ctx context.Context, sessionID string, targetSeq, expectedSeq int64, events []Event) (Event, error) {
	if targetSeq >= int64(len(events)) {
		return Event{}, fmt.Errorf("%w: target seq %d exceeds history", ErrRewindTargetInvalid, targetSeq)
	}
	payload, err := json.Marshal(rewindPayload{TargetSeq: targetSeq})
	if err != nil {
		return Event{}, fmt.Errorf("marshal rewind marker: %w", err)
	}
	appended, err := l.appendBatchUnlocked(ctx, sessionID, expectedSeq, []pendingEvent{{
		eventType: "session/rewind",
		payload:   payload,
	}})
	if err != nil {
		return Event{}, err
	}
	return appended[0], nil
}

// RewindWithState atomically appends a rewind marker and the projected state
// selected by the caller. A failed state insert rolls back the marker too.
func (l *SQLiteEventLog) RewindWithState(ctx context.Context, sessionID string, targetSeq int64, state Session) (Event, Event, error) {
	if strings.TrimSpace(sessionID) == "" || targetSeq < 0 {
		return Event{}, Event{}, ErrRewindTargetInvalid
	}
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	events, err := l.Events(ctx, sessionID)
	if err != nil {
		return Event{}, Event{}, err
	}
	if targetSeq >= int64(len(events)) {
		return Event{}, Event{}, fmt.Errorf("%w: target seq %d exceeds history", ErrRewindTargetInvalid, targetSeq)
	}
	markerPayload, err := json.Marshal(rewindPayload{TargetSeq: targetSeq})
	if err != nil {
		return Event{}, Event{}, fmt.Errorf("marshal rewind marker: %w", err)
	}
	statePayload, err := json.Marshal(state)
	if err != nil {
		return Event{}, Event{}, fmt.Errorf("marshal rewound session state: %w", err)
	}
	appended, err := l.appendBatchUnlocked(ctx, sessionID, int64(len(events)), []pendingEvent{
		{eventType: "session/rewind", payload: markerPayload},
		{eventType: sessionStateEventType, payload: statePayload},
	})
	if err != nil {
		return Event{}, Event{}, err
	}
	return appended[0], appended[1], nil
}

// Surface replays only surface-marked events. Log-only events remain durable
// and auditable but never become model input. Rewind and continuation markers
// select the exact historical projection at their target sequence, allowing a
// later marker to recover a branch hidden by an earlier marker.
func (l *SQLiteEventLog) Surface(ctx context.Context, sessionID string) ([]Event, error) {
	if err := l.Verify(ctx, sessionID); err != nil {
		return nil, err
	}
	events, err := l.Events(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return projectSurface(events)
}

func projectSurface(events []Event) ([]Event, error) {
	active := make([]Event, 0, len(events))
	projections := make([][]Event, len(events))
	continuationCount := 0
	for index, event := range events {
		if event.SurfaceOp == nil {
			switch event.Type {
			case "session/rewind":
				var marker rewindPayload
				if err := json.Unmarshal(event.Payload, &marker); err != nil || marker.TargetSeq < 0 || marker.TargetSeq >= int64(index) {
					return nil, fmt.Errorf("%w: invalid rewind marker at seq %d", ErrEventIntegrity, event.Seq)
				}
				active = cloneEvents(projections[marker.TargetSeq])
			case continuationEventType:
				continuationCount++
				if err := validateContinuation(events, event, continuationCount); err != nil {
					return nil, err
				}
				var receipt continuationPayload
				_ = json.Unmarshal(event.Payload, &receipt)
				active = cloneEvents(projections[receipt.TargetSeq])
			}
			projections[index] = cloneEvents(active)
			continue
		}
		switch event.SurfaceOp.Op {
		case "compact":
			var err error
			active, err = applyCompaction(active, event)
			if err != nil {
				return nil, err
			}
		case "append":
			active = append(active, event)
		case "replace":
			kept := active[:0]
			for _, current := range active {
				if current.Seq < event.SurfaceOp.Start || current.Seq > event.SurfaceOp.End {
					kept = append(kept, current)
				}
			}
			active = append(kept, event)
		}
		projections[index] = cloneEvents(active)
	}
	return active, nil
}

func cloneEvents(events []Event) []Event {
	return append([]Event(nil), events...)
}

// Events returns an ordered immutable copy of a session's event stream.
func (l *SQLiteEventLog) Events(ctx context.Context, sessionID string) ([]Event, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session event session_id is required")
	}
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	rows, err := l.db.QueryContext(ctx, `
		SELECT session_id, seq, event_id, version, type, created_at, payload, surface_op, prev_checksum, checksum
FROM session_events WHERE session_id = ? ORDER BY seq ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read session events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var event Event
		var createdAt, payload, surfaceOp string
		if err := rows.Scan(
			&event.SessionID, &event.Seq, &event.EventID, &event.Version, &event.Type,
			&createdAt, &payload, &surfaceOp, &event.PrevChecksum, &event.Checksum,
		); err != nil {
			return nil, fmt.Errorf("scan session event: %w", err)
		}
		event.CreatedAt, err = time.Parse(eventTimeFormat, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse session event time: %w", err)
		}
		event.Payload = json.RawMessage(payload)
		if surfaceOp != "" {
			var operation SurfaceOperation
			if err := json.Unmarshal([]byte(surfaceOp), &operation); err != nil {
				return nil, fmt.Errorf("%w: invalid surface operation at seq %d", ErrEventIntegrity, event.Seq)
			}
			event.SurfaceOp = &operation
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session events: %w", err)
	}
	return events, nil
}

// EventsAfter returns the verified suffix after an inclusive sequence cursor.
// It is intended for reconnecting consumers that already persisted the last
// observed sequence and avoids replaying the entire session history.
func (l *SQLiteEventLog) EventsAfter(ctx context.Context, sessionID string, afterSeq int64, limit int) ([]Event, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session event session_id is required")
	}
	if afterSeq < -1 {
		return nil, errors.New("session event cursor is invalid")
	}
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	if err := l.Verify(ctx, sessionID); err != nil {
		return nil, err
	}
	query := `SELECT session_id, seq, event_id, version, type, created_at, payload, surface_op, prev_checksum, checksum
FROM session_events WHERE session_id = ? AND seq > ? ORDER BY seq ASC`
	args := []any{sessionID, afterSeq}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read session event suffix: %w", err)
	}
	defer rows.Close()

	events := make([]Event, 0)
	for rows.Next() {
		var event Event
		var createdAt, payload, surfaceOp string
		if err := rows.Scan(&event.SessionID, &event.Seq, &event.EventID, &event.Version, &event.Type, &createdAt, &payload, &surfaceOp, &event.PrevChecksum, &event.Checksum); err != nil {
			return nil, fmt.Errorf("scan session event suffix: %w", err)
		}
		event.CreatedAt, err = time.Parse(eventTimeFormat, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse session event suffix time: %w", err)
		}
		event.Payload = json.RawMessage(payload)
		if surfaceOp != "" {
			var operation SurfaceOperation
			if err := json.Unmarshal([]byte(surfaceOp), &operation); err != nil {
				return nil, fmt.Errorf("%w: invalid surface operation at seq %d", ErrEventIntegrity, event.Seq)
			}
			event.SurfaceOp = &operation
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session event suffix: %w", err)
	}
	if len(events) == 0 {
		return events, nil
	}
	var previous string
	if err := l.db.QueryRowContext(ctx, `SELECT checksum FROM session_events WHERE session_id = ? AND seq = ?`, sessionID, events[0].Seq-1).Scan(&previous); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read session event suffix anchor: %w", err)
	}
	for _, event := range events {
		if event.SessionID != sessionID || event.Version != eventSchemaVersion || event.Seq < 0 || !json.Valid(event.Payload) || event.PrevChecksum != previous || event.Checksum != checksumEvent(event) {
			return nil, fmt.Errorf("%w: invalid event suffix at seq %d", ErrEventIntegrity, event.Seq)
		}
		previous = event.Checksum
	}
	return events, nil
}

// Verify checks sequence continuity, payload validity and every hash link.
func (l *SQLiteEventLog) Verify(ctx context.Context, sessionID string) error {
	events, err := l.Events(ctx, sessionID)
	if err != nil {
		return err
	}
	var previous string
	for index, event := range events {
		if event.Seq != int64(index) || event.SessionID != sessionID || event.Version != eventSchemaVersion {
			return fmt.Errorf("%w: invalid sequence or version at seq %d", ErrEventIntegrity, event.Seq)
		}
		if strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.Type) == "" {
			return fmt.Errorf("%w: missing event identity at seq %d", ErrEventIntegrity, event.Seq)
		}
		if !json.Valid(event.Payload) {
			return fmt.Errorf("%w: invalid payload at seq %d", ErrEventIntegrity, event.Seq)
		}
		if event.SurfaceOp != nil {
			if err := validateSurfaceOperation(event.Type, *event.SurfaceOp); err != nil {
				return fmt.Errorf("%w: %v", ErrEventIntegrity, err)
			}
		}
		if event.PrevChecksum != previous {
			return fmt.Errorf("%w: previous checksum mismatch at seq %d", ErrEventIntegrity, event.Seq)
		}
		if event.Checksum != checksumEvent(event) {
			return fmt.Errorf("%w: checksum mismatch at seq %d", ErrEventIntegrity, event.Seq)
		}
		previous = event.Checksum
	}
	return nil
}

func (l *SQLiteEventLog) Close() error {
	if l == nil || l.db == nil {
		return nil
	}
	return l.db.Close()
}

func (l *SQLiteEventLog) ensure(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return fmt.Errorf("create session event log directory: %w", err)
	}
	db, err := sql.Open("sqlite", l.Path)
	if err != nil {
		return fmt.Errorf("open session event log: %w", err)
	}
	// One connection per process keeps the read/insert transaction on the same
	// SQLite connection while cross-process writers are serialized by SQLite.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	// Busy timeout lets SQLite writers from separate Harness processes wait for
	// the current transaction instead of turning normal contention into loss.
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return fmt.Errorf("configure session event log: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS session_events (
  session_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  event_id TEXT NOT NULL UNIQUE,
  version INTEGER NOT NULL,
  type TEXT NOT NULL,
  created_at TEXT NOT NULL,
  payload TEXT NOT NULL,
  surface_op TEXT NOT NULL DEFAULT '',
  prev_checksum TEXT NOT NULL,
  checksum TEXT NOT NULL,
  PRIMARY KEY (session_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_session_events_event_id ON session_events(event_id);
`); err != nil {
		_ = db.Close()
		return fmt.Errorf("initialize session event log: %w", err)
	}
	// Upgrade an event database created by the first ledger revision. SQLite
	// reports a duplicate-column error when this migration already ran.
	if _, err := db.ExecContext(ctx, `ALTER TABLE session_events ADD COLUMN surface_op TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		_ = db.Close()
		return fmt.Errorf("upgrade session event log: %w", err)
	}
	l.db = db
	return nil
}

func validateSurfaceOperation(eventType string, operation SurfaceOperation) error {
	if !surfaceEligibleEventType(eventType) {
		return fmt.Errorf("event type %q is not surface-eligible", eventType)
	}
	switch operation.Op {
	case "compact":
		if eventType != compactionEventType {
			return errors.New("compact operation requires a compaction event")
		}
		return nil
	case "append":
		return nil
	case "replace":
		if operation.Start < 0 || operation.End < operation.Start {
			return errors.New("replace surface range is invalid")
		}
		return nil
	default:
		return fmt.Errorf("invalid surface operation %q", operation.Op)
	}
}

func surfaceEligibleEventType(eventType string) bool {
	switch eventType {
	case "user/message", "assistant/message", "tool/call", "tool/result", compactionEventType:
		return true
	default:
		return false
	}
}

func cloneSurfaceOperation(operation *SurfaceOperation) *SurfaceOperation {
	if operation == nil {
		return nil
	}
	copy := *operation
	return &copy
}

func surfaceOperationJSON(operation *SurfaceOperation) string {
	if operation == nil {
		return ""
	}
	payload, err := json.Marshal(operation)
	if err != nil {
		return ""
	}
	return string(payload)
}

func normalizeSnapshot(raw []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLegacySnapshotInvalid, err)
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: normalize JSON: %v", ErrLegacySnapshotInvalid, err)
	}
	return normalized, nil
}

func validateEventInput(sessionID string, expectedSeq int64, eventType string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("session event session_id is required")
	}
	if expectedSeq < 0 {
		return errors.New("session event expected sequence must be non-negative")
	}
	if strings.TrimSpace(eventType) == "" {
		return errors.New("session event type is required")
	}
	return nil
}

func newEventID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate session event id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func checksumEvent(event Event) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
		event.SessionID,
		event.Seq,
		event.Version,
		event.EventID,
		event.Type,
		event.CreatedAt.UTC().Format(eventTimeFormat),
		event.Payload,
		surfaceOperationJSON(event.SurfaceOp),
		event.PrevChecksum,
	)
	return hex.EncodeToString(hash.Sum(nil))
}
