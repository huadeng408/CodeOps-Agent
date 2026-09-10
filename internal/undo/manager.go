package undo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Change struct {
	Path   string `json:"path"`
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
	for i, entry := range m.entries {
		out[i] = cloneEntry(entry)
	}
	return out
}

func (m *Manager) Restore(entries []Entry) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries = make([]Entry, len(entries))
	for i, entry := range entries {
		m.entries[i] = cloneEntry(entry)
	}
}

func ApplyEntry(root string, entry Entry) error {
	paths := make([]string, len(entry.Changes))
	for i, change := range entry.Changes {
		abs, err := workspacePath(root, change.Path)
		if err != nil {
			return err
		}
		paths[i] = abs
	}
	for i := len(entry.Changes) - 1; i >= 0; i-- {
		change := entry.Changes[i]
		abs := paths[i]
		if change.Before == "" {
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove %s: %w", change.Path, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return fmt.Errorf("create parent for %s: %w", change.Path, err)
		}
		if err := os.WriteFile(abs, []byte(change.Before), 0o644); err != nil {
			return fmt.Errorf("restore %s: %w", change.Path, err)
		}
	}
	return nil
}

func cloneEntry(entry Entry) Entry {
	out := entry
	if len(entry.Changes) > 0 {
		out.Changes = append([]Change(nil), entry.Changes...)
	}
	return out
}

func workspacePath(root, target string) (string, error) {
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("path is required")
	}
	var absTarget string
	if filepath.IsAbs(target) {
		absTarget = filepath.Clean(target)
	} else {
		absTarget = filepath.Join(absRoot, target)
	}
	absTarget, err = filepath.Abs(absTarget)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", target)
	}
	return absTarget, nil
}

func nextID() string {
	return time.Now().Format("20060102T150405.000000000")
}
