package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role      Role      `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type Session struct {
	ID         string            `json:"id"`
	WorkingDir string            `json:"working_dir"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Messages   []Message         `json:"messages"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type Manager struct {
	mu      sync.Mutex
	store   Store
	current Session
}

func NewManager(store Store) *Manager {
	if store == nil {
		store = NewMemoryStore()
	}
	return &Manager{store: store}
}

func (m *Manager) NewSession(workingDir string) Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	m.current = Session{
		ID:         randomID(),
		WorkingDir: workingDir,
		CreatedAt:  now,
		UpdatedAt:  now,
		Messages:   []Message{},
		Metadata:   map[string]string{},
	}
	_ = m.store.Save(context.Background(), m.current)
	return cloneSession(m.current)
}

func (m *Manager) Current() Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneSession(m.current)
}

func (m *Manager) Append(role Role, content string) Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current.ID == "" {
		m.current = Session{
			ID:        randomID(),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Metadata:  map[string]string{},
		}
	}

	now := time.Now()
	m.current.Messages = append(m.current.Messages, Message{
		Role:      role,
		Content:   content,
		CreatedAt: now,
	})
	m.current.UpdatedAt = now
	_ = m.store.Save(context.Background(), m.current)
	return cloneSession(m.current)
}

func (m *Manager) Reset() Session {
	return m.NewSession(m.current.WorkingDir)
}

func (m *Manager) Messages() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Message, len(m.current.Messages))
	copy(out, m.current.Messages)
	return out
}

func cloneSession(session Session) Session {
	out := session
	if len(session.Messages) > 0 {
		out.Messages = make([]Message, len(session.Messages))
		copy(out.Messages, session.Messages)
	}
	if session.Metadata != nil {
		out.Metadata = make(map[string]string, len(session.Metadata))
		for k, v := range session.Metadata {
			out.Metadata[k] = v
		}
	}
	return out
}

func randomID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000")))
	}
	return hex.EncodeToString(buf[:])
}
