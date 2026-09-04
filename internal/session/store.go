package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("session not found")
var ErrHistoryUnsupported = errors.New("session history operations unsupported")
var ErrLegacySnapshotReadOnly = errors.New("legacy session snapshots are read-only")

type Store interface {
	Save(ctx context.Context, session Session) error
	Load(ctx context.Context, id string) (*Session, error)
	List(ctx context.Context) ([]Session, error)
}

// HistoryStore exposes durable fork and rewind operations without widening the
// basic Store contract used by in-memory and legacy adapters.
type HistoryStore interface {
	Store
	Fork(ctx context.Context, sourceSessionID, targetSessionID string, targetSeq int64) (*Session, error)
	Rewind(ctx context.Context, sessionID string, targetSeq int64) (*Session, error)
}

type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: make(map[string]Session)}
}

func (s *MemoryStore) Save(_ context.Context, session Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[session.ID] = cloneSession(session)
	return nil
}

func (s *MemoryStore) Load(_ context.Context, id string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	clone := cloneSession(session)
	return &clone, nil
}

func (s *MemoryStore) List(_ context.Context) ([]Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		out = append(out, cloneSession(session))
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		// Deterministic tiebreakers when timestamps collide (coarse OS clock):
		// CreatedAt, then ID — avoids falling back to nondeterministic map
		// iteration order.
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.ID > b.ID
	})
	return out, nil
}

type SQLiteStore struct {
	Path string
	db   *sql.DB
	mu   sync.Mutex
}

func NewSQLiteStore(path string) *SQLiteStore {
	return &SQLiteStore{Path: path}
}

func (s *SQLiteStore) Save(ctx context.Context, session Session) error {
	return ErrLegacySnapshotReadOnly
}

func (s *SQLiteStore) Load(ctx context.Context, id string) (*Session, error) {
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}

	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM sessions WHERE id = ?`, id).Scan(&payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load session: %w", err)
	}

	var session Session
	if err := json.Unmarshal([]byte(payload), &session); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &session, nil
}

func (s *SQLiteStore) List(ctx context.Context) ([]Session, error) {
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM sessions ORDER BY updated_at DESC`)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return []Session{}, nil
		}
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		var session Session
		if err := json.Unmarshal([]byte(payload), &session); err != nil {
			return nil, fmt.Errorf("unmarshal session: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		a, b := sessions[i], sessions[j]
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.ID > b.ID
	})
	return sessions, nil
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

func (s *SQLiteStore) ensure(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db != nil {
		return nil
	}
	if s.Path == "" {
		return errors.New("session sqlite path is required")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return fmt.Errorf("create session store dir: %w", err)
	}

	db, err := sql.Open("sqlite", s.Path)
	if err != nil {
		return fmt.Errorf("open sqlite session store: %w", err)
	}
	s.db = db
	return nil
}
