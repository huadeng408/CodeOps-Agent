package memory

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Memory struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Stats struct {
	Count int
	Dir   string
	File  string
}

type Manager struct {
	mu     sync.Mutex
	dir    string
	file   string
	items  []Memory
}

func NewManager(dir string) *Manager {
	if dir == "" {
		dir = filepath.Join(".agent", "memory")
	}
	manager := &Manager{
		dir:  dir,
		file: filepath.Join(dir, "memory.jsonl"),
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = manager.load()
	return manager
}

func (m *Manager) Add(content string, tags ...string) Memory {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	item := Memory{
		ID:        nextID(),
		Content:   content,
		Tags:      append([]string(nil), tags...),
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.items = append(m.items, item)
	_ = m.append(item)
	return item
}

func (m *Manager) LoadRelevant(query string) []Memory {
	m.mu.Lock()
	defer m.mu.Unlock()

	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		out := make([]Memory, len(m.items))
		copy(out, m.items)
		return out
	}

	results := make([]Memory, 0)
	for _, item := range m.items {
		if strings.Contains(strings.ToLower(item.Content), query) {
			results = append(results, item)
			continue
		}
		for _, tag := range item.Tags {
			if strings.Contains(strings.ToLower(tag), query) {
				results = append(results, item)
				break
			}
		}
	}
	return results
}

func (m *Manager) Snapshot() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()

	return Stats{
		Count: len(m.items),
		Dir:   m.dir,
		File:  m.file,
	}
}

func (m *Manager) load() error {
	file, err := os.Open(m.file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var item Memory
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return fmt.Errorf("load memory: %w", err)
		}
		m.items = append(m.items, item)
	}
	return scanner.Err()
}

func (m *Manager) append(item Memory) error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(m.file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func nextID() string {
	return time.Now().Format("20060102T150405.000000000")
}
