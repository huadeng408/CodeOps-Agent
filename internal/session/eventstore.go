package session

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const sessionStateEventType = "session/state"

// SQLiteEventStore is the one-way compatibility adapter that preserves the
// existing Store interface while making the event ledger the mutable source of
// truth. Legacy sessions rows are read only and imported on first Load.
type SQLiteEventStore struct {
	Path string

	mu  sync.Mutex
	log *SQLiteEventLog
}

func NewSQLiteEventStore(path string) *SQLiteEventStore {
	return &SQLiteEventStore{Path: path}
}

func (s *SQLiteEventStore) Save(ctx context.Context, current Session) error {
	if strings.TrimSpace(current.ID) == "" {
		return errors.New("session id is required")
	}
	log, err := s.eventLog()
	if err != nil {
		return err
	}
	events, err := log.Events(ctx, current.ID)
	if err != nil {
		return err
	}
	if _, err := log.Append(ctx, current.ID, int64(len(events)), sessionStateEventType, current); err != nil {
		return fmt.Errorf("save event-backed session: %w", err)
	}
	return nil
}

func (s *SQLiteEventStore) Load(ctx context.Context, id string) (*Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrNotFound
	}
	log, err := s.eventLog()
	if err != nil {
		return nil, err
	}
	events, err := log.Events(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(events) > 0 {
		if err := log.Verify(ctx, id); err != nil {
			return nil, err
		}
		var imported *LegacyImportPayload
		for _, event := range events {
			if event.Type != "legacy/import" {
				continue
			}
			var payload LegacyImportPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return nil, fmt.Errorf("%w: decode legacy import payload: %v", ErrEventIntegrity, err)
			}
			imported = &payload
			break
		}
		if imported != nil {
			if err := verifyLegacySource(ctx, log, *imported); err != nil {
				return nil, err
			}
		}
		for index := len(events) - 1; index >= 0; index-- {
			if events[index].Type == sessionStateEventType {
				return decodeSessionState(events[index].Payload)
			}
		}
		if imported != nil {
			payload, marshalErr := json.Marshal(imported)
			if marshalErr != nil {
				return nil, fmt.Errorf("%w: encode legacy import payload: %v", ErrEventIntegrity, marshalErr)
			}
			return decodeLegacyImportSession(payload)
		}
	}

	imported, _, err := log.ImportLegacySnapshot(ctx, id)
	if err != nil {
		if errors.Is(err, ErrLegacySnapshotNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return decodeLegacyImportSession(imported.Payload)
}

func (s *SQLiteEventStore) List(ctx context.Context) ([]Session, error) {
	log, err := s.eventLog()
	if err != nil {
		return nil, err
	}
	ids, err := log.SessionIDs(ctx)
	if err != nil {
		return nil, err
	}
	sessions := make([]Session, 0, len(ids))
	for _, id := range ids {
		loaded, loadErr := s.Load(ctx, id)
		if loadErr != nil {
			return nil, loadErr
		}
		if loaded != nil {
			sessions = append(sessions, *loaded)
		}
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
		}
		if !sessions[i].CreatedAt.Equal(sessions[j].CreatedAt) {
			return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
		}
		return sessions[i].ID > sessions[j].ID
	})
	return sessions, nil
}

// Fork copies a trusted event prefix into a new session and returns the
// resulting projected state. The source remains untouched.
func (s *SQLiteEventStore) Fork(ctx context.Context, sourceSessionID, targetSessionID string, targetSeq int64) (*Session, error) {
	log, err := s.eventLog()
	if err != nil {
		return nil, err
	}
	if _, err := s.Load(ctx, sourceSessionID); err != nil {
		return nil, err
	}
	sourceEvents, err := log.Events(ctx, sourceSessionID)
	if err != nil {
		return nil, err
	}
	child, err := replaySessionState(sourceEvents, targetSeq)
	if err != nil {
		return nil, err
	}
	child.ID = strings.TrimSpace(targetSessionID)
	if err := rebindActorToSession(child); err != nil {
		return nil, err
	}
	if err := log.ForkWithState(ctx, sourceSessionID, child.ID, targetSeq, *child); err != nil {
		return nil, err
	}
	child, err = s.Load(ctx, child.ID)
	if err != nil {
		return nil, err
	}
	if child == nil {
		return nil, ErrNotFound
	}
	return child, nil
}

// Rewind appends an auditable marker and then records the state that was
// active at the requested sequence as a new state event. Raw history remains
// intact while subsequent loads observe the rewound projection.
func (s *SQLiteEventStore) Rewind(ctx context.Context, sessionID string, targetSeq int64) (*Session, error) {
	log, err := s.eventLog()
	if err != nil {
		return nil, err
	}
	events, err := log.Events(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if targetSeq < 0 || targetSeq >= int64(len(events)) {
		return nil, fmt.Errorf("%w: target seq %d exceeds history", ErrRewindTargetInvalid, targetSeq)
	}
	restored, err := replaySessionState(events, targetSeq)
	if err != nil {
		return nil, err
	}
	restored.ID = sessionID
	if err := rebindActorToSession(restored); err != nil {
		return nil, err
	}
	if _, _, err := log.RewindWithState(ctx, sessionID, targetSeq, *restored); err != nil {
		return nil, err
	}
	return restored, nil
}

// replaySessionState derives the state projection at an event sequence. A
// rewind marker points at the already-derived projection at its target, so
// repeated rewinds remain deterministic instead of selecting a stale snapshot.
func replaySessionState(events []Event, targetSeq int64) (*Session, error) {
	if targetSeq < 0 || targetSeq >= int64(len(events)) {
		return nil, fmt.Errorf("%w: target seq %d exceeds history", ErrRewindTargetInvalid, targetSeq)
	}
	projections := make([]*Session, len(events))
	var current *Session
	for index := 0; index <= int(targetSeq); index++ {
		event := events[index]
		switch event.Type {
		case sessionStateEventType:
			candidate, err := decodeSessionState(event.Payload)
			if err != nil {
				return nil, err
			}
			current = candidate
		case "legacy/import", "session/fork/import":
			candidate, err := decodeLegacyImportSession(event.Payload)
			if err != nil {
				return nil, err
			}
			current = candidate
		case "session/rewind":
			var marker rewindPayload
			if err := json.Unmarshal(event.Payload, &marker); err != nil || marker.TargetSeq < 0 || marker.TargetSeq >= int64(index) {
				return nil, fmt.Errorf("%w: invalid rewind marker at seq %d", ErrEventIntegrity, event.Seq)
			}
			base := projections[marker.TargetSeq]
			if base == nil {
				return nil, fmt.Errorf("%w: rewind target %d has no session projection", ErrRewindTargetInvalid, marker.TargetSeq)
			}
			copy := cloneSession(*base)
			current = &copy
		}
		if current != nil {
			copy := cloneSession(*current)
			projections[index] = &copy
		}
	}
	if current == nil {
		return nil, fmt.Errorf("%w: no session state at or before seq %d", ErrRewindTargetInvalid, targetSeq)
	}
	return current, nil
}

func rebindActorToSession(current *Session) error {
	if current == nil || current.ID == "" {
		return nil
	}
	actor := current.Actor
	if actor.SchemaVersion == 0 && actor.ActorID == "" && actor.Subject == "" && actor.TenantID == "" && len(actor.Roles) == 0 && actor.SessionID == "" {
		return nil
	}
	actor.SessionID = ""
	rebound, err := actor.BindSession(current.ID)
	if err != nil {
		return fmt.Errorf("bind session actor to %s: %w", current.ID, err)
	}
	current.Actor = rebound
	return nil
}

func (s *SQLiteEventStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.log == nil {
		return nil
	}
	err := s.log.Close()
	s.log = nil
	return err
}

func (s *SQLiteEventStore) eventLog() (*SQLiteEventLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.log != nil {
		return s.log, nil
	}
	log, err := OpenSQLiteEventLog(s.Path)
	if err != nil {
		return nil, err
	}
	s.log = log
	return log, nil
}

func decodeSessionState(payload json.RawMessage) (*Session, error) {
	var current Session
	if err := json.Unmarshal(payload, &current); err != nil {
		return nil, fmt.Errorf("decode event-backed session state: %w", err)
	}
	if strings.TrimSpace(current.ID) == "" {
		return nil, fmt.Errorf("%w: event-backed session has no id", ErrEventIntegrity)
	}
	return &current, nil
}

func decodeLegacyImportSession(payload json.RawMessage) (*Session, error) {
	var imported LegacyImportPayload
	if err := json.Unmarshal(payload, &imported); err != nil {
		return nil, fmt.Errorf("%w: decode legacy import payload: %v", ErrEventIntegrity, err)
	}
	if !json.Valid(imported.Snapshot) {
		return nil, fmt.Errorf("%w: legacy snapshot JSON is invalid", ErrEventIntegrity)
	}
	return decodeSessionState(imported.Snapshot)
}

func verifyLegacySource(ctx context.Context, log *SQLiteEventLog, imported LegacyImportPayload) error {
	var rawSnapshot string
	if err := log.db.QueryRowContext(ctx, `SELECT payload FROM sessions WHERE id = ?`, imported.SessionID).Scan(&rawSnapshot); err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return fmt.Errorf("%w: %s", ErrLegacySnapshotNotFound, imported.SessionID)
		}
		return fmt.Errorf("read legacy session snapshot: %w", err)
	}
	normalized, err := normalizeSnapshot([]byte(rawSnapshot))
	if err != nil {
		return err
	}
	digest := sha256.Sum256(normalized)
	if hex.EncodeToString(digest[:]) != imported.SourceSHA256 {
		return fmt.Errorf("%w: source checksum differs for %s", ErrLegacySnapshotChanged, imported.SessionID)
	}
	return nil
}

// SessionIDs returns all event-backed IDs and legacy IDs without modifying the
// legacy table. It is intentionally a read-side primitive for List callers.
func (l *SQLiteEventLog) SessionIDs(ctx context.Context) ([]string, error) {
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	rows, err := l.db.QueryContext(ctx, `SELECT DISTINCT session_id FROM session_events ORDER BY session_id`)
	if err != nil {
		return nil, fmt.Errorf("list event-backed session IDs: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan event-backed session ID: %w", err)
		}
		if strings.TrimSpace(id) != "" {
			seen[id] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate event-backed session IDs: %w", err)
	}
	_ = rows.Close()
	legacyRows, legacyErr := l.db.QueryContext(ctx, `SELECT id FROM sessions ORDER BY id`)
	if legacyErr == nil {
		defer legacyRows.Close()
		for legacyRows.Next() {
			var id string
			if err := legacyRows.Scan(&id); err != nil {
				return nil, fmt.Errorf("scan legacy session ID: %w", err)
			}
			if strings.TrimSpace(id) != "" {
				seen[id] = struct{}{}
			}
		}
		if err := legacyRows.Err(); err != nil {
			return nil, fmt.Errorf("iterate legacy session IDs: %w", err)
		}
	} else if !strings.Contains(strings.ToLower(legacyErr.Error()), "no such table") {
		return nil, fmt.Errorf("list legacy session IDs: %w", legacyErr)
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	// The query order is part of the adapter's deterministic output contract.
	sort.Strings(ids)
	return ids, nil
}

var _ Store = (*SQLiteEventStore)(nil)
