package worktree

import (
	"errors"
	"path/filepath"
	"sync"
)

type Worktree struct {
	Name    string
	Path    string
	BaseRef string
	Active  bool
}

type Manager struct {
	mu      sync.Mutex
	root    string
	baseRef string
	trees   map[string]Worktree
}

func NewManager(root, baseRef string) *Manager {
	return &Manager{
		root:    root,
		baseRef: baseRef,
		trees:   make(map[string]Worktree),
	}
}

func (m *Manager) Create(name string) (Worktree, error) {
	if name == "" {
		return Worktree{}, errors.New("worktree name required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	tree := Worktree{
		Name:    name,
		Path:    filepath.Join(m.root, ".worktrees", name),
		BaseRef: m.baseRef,
		Active:  true,
	}
	for key, existing := range m.trees {
		existing.Active = false
		m.trees[key] = existing
	}
	m.trees[name] = tree
	return tree, nil
}

func (m *Manager) Switch(name string) (Worktree, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tree, ok := m.trees[name]
	if !ok {
		return Worktree{}, errors.New("worktree not found")
	}
	for key, existing := range m.trees {
		existing.Active = false
		m.trees[key] = existing
	}
	tree.Active = true
	m.trees[name] = tree
	return tree, nil
}

func (m *Manager) Cleanup(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.trees[name]; !ok {
		return errors.New("worktree not found")
	}
	delete(m.trees, name)
	return nil
}

func (m *Manager) List() []Worktree {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Worktree, 0, len(m.trees))
	for _, tree := range m.trees {
		out = append(out, tree)
	}
	return out
}
