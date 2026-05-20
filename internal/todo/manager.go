package todo

import (
	"fmt"
	"strings"
	"sync"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
)

type Item struct {
	Content    string
	ActiveForm string
	Status     string
}

type Manager struct {
	mu    sync.Mutex
	items []Item
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Update(items []Item) {
	m.mu.Lock()
	defer m.mu.Unlock()

	normalized := make([]Item, 0, len(items))
	seenInProgress := false
	for _, item := range items {
		content := strings.TrimSpace(item.Content)
		activeForm := strings.TrimSpace(item.ActiveForm)
		if activeForm == "" {
			activeForm = content
		}
		status := normalizeStatus(item.Status)
		if status == StatusInProgress {
			if seenInProgress {
				status = StatusPending
			} else {
				seenInProgress = true
			}
		}
		normalized = append(normalized, Item{
			Content:    content,
			ActiveForm: activeForm,
			Status:     string(status),
		})
	}
	m.items = normalized
}

func (m *Manager) MarkComplete(index int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if index < 0 || index >= len(m.items) {
		return
	}
	m.items[index].Status = string(StatusCompleted)
}

func (m *Manager) MarkInProgress(index int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if index < 0 || index >= len(m.items) {
		return
	}
	for i := range m.items {
		if m.items[i].Status == string(StatusInProgress) {
			m.items[i].Status = string(StatusPending)
		}
	}
	m.items[index].Status = string(StatusInProgress)
}

func (m *Manager) Snapshot() []Item {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Item, len(m.items))
	copy(out, m.items)
	return out
}

func (m *Manager) Lines() []string {
	items := m.Snapshot()
	if len(items) == 0 {
		return []string{"no active tasks"}
	}

	lines := make([]string, 0, len(items))
	for i, item := range items {
		prefix := "[ ]"
		switch normalizeStatus(item.Status) {
		case StatusCompleted:
			prefix = "[x]"
		case StatusInProgress:
			prefix = "[~]"
		}
		label := strings.TrimSpace(item.ActiveForm)
		if label == "" {
			label = strings.TrimSpace(item.Content)
		}
		lines = append(lines, fmt.Sprintf("%d. %s %s", i+1, prefix, label))
	}
	return lines
}

func normalizeStatus(status string) Status {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case string(StatusCompleted):
		return StatusCompleted
	case string(StatusInProgress):
		return StatusInProgress
	default:
		return StatusPending
	}
}
