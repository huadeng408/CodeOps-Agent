package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
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

func (m *Manager) SetMetadata(key, value string) Session {
	return m.MergeMetadata(map[string]string{key: value})
}

func (m *Manager) MergeMetadata(values map[string]string) Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	m.ensureCurrentLocked(now)
	m.ensureMetadataLocked()
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		m.current.Metadata[key] = value
	}
	m.current.UpdatedAt = now
	_ = m.store.Save(context.Background(), m.current)
	return cloneSession(m.current)
}

func (m *Manager) Compact(keep int) (Session, int, string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if keep < 0 {
		keep = 0
	}
	total := len(m.current.Messages)
	if total <= keep {
		return cloneSession(m.current), 0, ""
	}

	dropCount := total - keep
	dropped := append([]Message(nil), m.current.Messages[:dropCount]...)
	kept := append([]Message(nil), m.current.Messages[dropCount:]...)
	summary := summarizeMessages(dropped)

	now := time.Now()
	compacted := make([]Message, 0, len(kept)+1)
	if strings.TrimSpace(summary) != "" {
		compacted = append(compacted, Message{
			Role:      RoleSystem,
			Content:   "[Conversation summary]\n" + summary,
			CreatedAt: now,
		})
	}
	compacted = append(compacted, kept...)

	m.ensureCurrentLocked(now)
	m.ensureMetadataLocked()
	m.current.Messages = compacted
	m.current.UpdatedAt = now
	m.current.Metadata["last_compacted_at"] = now.Format(time.RFC3339)
	m.current.Metadata["last_compacted_removed"] = fmt.Sprint(dropCount)
	m.current.Metadata["last_compacted_keep"] = fmt.Sprint(keep)
	_ = m.store.Save(context.Background(), m.current)
	return cloneSession(m.current), dropCount, summary
}

func (m *Manager) Reset() Session {
	return m.NewSession(m.current.WorkingDir)
}

func (m *Manager) ResumeLatest(ctx context.Context) (Session, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessions, err := m.store.List(ctx)
	if err != nil {
		return Session{}, false, err
	}
	for _, candidate := range sessions {
		if candidate.ID == "" || candidate.ID == m.current.ID {
			continue
		}
		m.current = cloneSession(candidate)
		return cloneSession(m.current), true, nil
	}
	return Session{}, false, nil
}

func (m *Manager) Load(ctx context.Context, id string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	loaded, err := m.store.Load(ctx, id)
	if err != nil {
		return Session{}, err
	}
	if loaded == nil {
		return Session{}, ErrNotFound
	}
	m.current = cloneSession(*loaded)
	return cloneSession(m.current), nil
}

func (m *Manager) List(ctx context.Context) ([]Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessions, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Session, len(sessions))
	for i, session := range sessions {
		out[i] = cloneSession(session)
	}
	return out, nil
}

func (m *Manager) Messages() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Message, len(m.current.Messages))
	copy(out, m.current.Messages)
	return out
}

func (m *Manager) ensureCurrentLocked(now time.Time) {
	if m.current.ID != "" {
		return
	}
	m.current = Session{
		ID:        randomID(),
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  map[string]string{},
	}
}

func (m *Manager) ensureMetadataLocked() {
	if m.current.Metadata == nil {
		m.current.Metadata = map[string]string{}
	}
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

func summarizeMessages(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}
	lines := make([]string, 0, 8)
	for _, message := range messages {
		content := firstContentLine(message.Content)
		if content == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", message.Role, content))
		if len(lines) >= 6 {
			break
		}
	}
	if len(lines) == 0 {
		return fmt.Sprintf("%d earlier messages omitted", len(messages))
	}
	if len(messages) > len(lines) {
		lines = append(lines, fmt.Sprintf("... %d earlier messages omitted", len(messages)-len(lines)))
	}
	return strings.Join(lines, "\n")
}

func firstContentLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 120 {
			return strings.TrimSpace(line[:117]) + "..."
		}
		return line
	}
	return ""
}
