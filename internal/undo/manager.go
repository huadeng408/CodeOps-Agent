package undo

import (
	"sync"
	"time"
)

type Change struct {
	Path  string `json:"path"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type Entry struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Changes     []Change  `json:"changes"`
	CreatedAt   time.Time `json:"created_at"`
}

type Manager struct {
	mu      sync.Mutex
	entries []Entry
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Record(description string, changes []Change) Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry := Entry{
		ID:          nextID(),
		Description: description,
		Changes:     append([]Change(nil), changes...),
		CreatedAt:   time.Now(),
	}
	m.entries = append(m.entries, entry)
	return entry
}

func (m *Manager) Latest() (Entry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.entries) == 0 {
		return Entry{}, false
	}
	return m.entries[len(m.entries)-1], true
}

func (m *Manager) RevertLast() (Entry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.entries) == 0 {
		return Entry{}, false
	}
	entry := m.entries[len(m.entries)-1]
	m.entries = m.entries[:len(m.entries)-1]
	return entry, true
}

func (m *Manager) List() []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Entry, len(m.entries))
	copy(out, m.entries)
	return out
}

func nextID() string {
	return time.Now().Format("20060102T150405.000000000")
}
