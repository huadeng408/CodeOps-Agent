package worktree

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
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

func (m *Manager) DiffLines(ctx context.Context) ([]string, error) {
	root := m.root
	if strings.TrimSpace(root) == "" {
		root = "."
	}

	staged, err := gitOutput(ctx, root, "diff", "--cached", "--stat", "--")
	if err != nil {
		return nil, err
	}
	unstaged, err := gitOutput(ctx, root, "diff", "--stat", "--")
	if err != nil {
		return nil, err
	}
	status, err := gitOutput(ctx, root, "status", "--short", "--untracked-files=all")
	if err != nil {
		return nil, err
	}

	staged = strings.TrimSpace(staged)
	unstaged = strings.TrimSpace(unstaged)
	status = strings.TrimSpace(status)
	if staged == "" && unstaged == "" && status == "" {
		return []string{"working tree clean"}, nil
	}

	lines := make([]string, 0, 16)
	if staged != "" {
		lines = append(lines, "staged changes:")
		lines = append(lines, strings.Split(staged, "\n")...)
	}
	if unstaged != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "unstaged changes:")
		lines = append(lines, strings.Split(unstaged, "\n")...)
	}
	if status != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "status:")
		lines = append(lines, strings.Split(status, "\n")...)
	}
	return lines, nil
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
