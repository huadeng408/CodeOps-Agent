package session

import (
	"context"
	"errors"
	"sync"
)

var ErrNotImplemented = errors.New("sqlite store is not wired yet")

type Store interface {
	Save(ctx context.Context, session Session) error
	Load(ctx context.Context, id string) (*Session, error)
	List(ctx context.Context) ([]Session, error)
}

type MemoryStore struct {
	mu       sync.RWMutex
	sessions  map[string]Session
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
		return nil, errors.New("session not found")
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
	return out, nil
}

type SQLiteStore struct {
	Path string
}

func NewSQLiteStore(path string) *SQLiteStore {
	return &SQLiteStore{Path: path}
}

func (s *SQLiteStore) Save(context.Context, Session) error {
	return ErrNotImplemented
}

func (s *SQLiteStore) Load(context.Context, string) (*Session, error) {
	return nil, ErrNotImplemented
}

func (s *SQLiteStore) List(context.Context) ([]Session, error) {
	return nil, ErrNotImplemented
}
