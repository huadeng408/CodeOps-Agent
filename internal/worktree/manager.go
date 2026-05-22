package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	if strings.TrimSpace(baseRef) == "" {
		baseRef = "HEAD"
	}
	return &Manager{
		root:    filepath.Clean(root),
		baseRef: baseRef,
		trees:   make(map[string]Worktree),
	}
}

func (m *Manager) Create(name string) (Worktree, error) {
	return m.CreateContext(context.Background(), name)
}

func (m *Manager) CreateContext(ctx context.Context, name string) (Worktree, error) {
	name, err := normalizeName(name)
	if err != nil {
		return Worktree{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.trees[name]; exists {
		return Worktree{}, errors.New("worktree already exists")
	}

	tree := Worktree{
		Name:    name,
		Path:    filepath.Join(m.root, ".agent", "worktrees", name),
		BaseRef: m.resolveBaseRef(ctx),
		Active:  true,
	}
	if err := m.createGitWorktree(ctx, tree); err != nil {
		return Worktree{}, err
	}
	for key, existing := range m.trees {
		existing.Active = false
		m.trees[key] = existing
	}
	m.trees[name] = tree
	return tree, nil
}

func (m *Manager) Switch(name string) (Worktree, error) {
	name, err := normalizeName(name)
	if err != nil {
		return Worktree{}, err
	}
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
	return m.CleanupContext(context.Background(), name)
}

func (m *Manager) CleanupContext(ctx context.Context, name string) error {
	name, err := normalizeName(name)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	removed, ok := m.trees[name]
	if !ok {
		return errors.New("worktree not found")
	}
	if err := m.removeGitWorktree(ctx, removed); err != nil {
		return err
	}
	delete(m.trees, name)
	if removed.Active {
		names := make([]string, 0, len(m.trees))
		for key := range m.trees {
			names = append(names, key)
		}
		sort.Strings(names)
		if len(names) > 0 {
			next := m.trees[names[0]]
			next.Active = true
			m.trees[names[0]] = next
		}
	}
	return nil
}

func (m *Manager) List() []Worktree {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Worktree, 0, len(m.trees))
	for _, tree := range m.trees {
		out = append(out, tree)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func (m *Manager) Restore(trees []Worktree) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.trees = make(map[string]Worktree, len(trees))
	activeSeen := false
	for _, tree := range trees {
		name, err := normalizeName(tree.Name)
		if err != nil {
			continue
		}
		tree.Name = name
		if strings.TrimSpace(tree.Path) == "" {
			tree.Path = filepath.Join(m.root, ".agent", "worktrees", tree.Name)
		}
		if strings.TrimSpace(tree.BaseRef) == "" {
			tree.BaseRef = m.baseRef
		}
		if tree.Active {
			if activeSeen {
				tree.Active = false
			} else {
				activeSeen = true
			}
		}
		m.trees[tree.Name] = tree
	}
}

func (m *Manager) createGitWorktree(ctx context.Context, tree Worktree) error {
	if !isGitRepository(ctx, m.root) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(tree.Path), 0o755); err != nil {
		return fmt.Errorf("create worktree base dir: %w", err)
	}
	if _, err := gitOutput(ctx, m.root, "worktree", "add", "-b", "agent/"+tree.Name, tree.Path, tree.BaseRef); err != nil {
		return err
	}
	return nil
}

func (m *Manager) resolveBaseRef(ctx context.Context) string {
	baseRef := strings.TrimSpace(m.baseRef)
	if baseRef == "" {
		baseRef = "HEAD"
	}
	if !isGitRepository(ctx, m.root) {
		return baseRef
	}
	if _, err := gitOutput(ctx, m.root, "rev-parse", "--verify", baseRef+"^{commit}"); err == nil {
		return baseRef
	}
	return "HEAD"
}

func (m *Manager) removeGitWorktree(ctx context.Context, tree Worktree) error {
	if !isGitRepository(ctx, m.root) {
		return nil
	}
	if strings.TrimSpace(tree.Path) == "" {
		return nil
	}
	if _, err := os.Stat(tree.Path); errors.Is(err, os.ErrNotExist) {
		return m.deleteGitBranch(ctx, tree.Name)
	}
	if _, err := gitOutput(ctx, m.root, "worktree", "remove", "--force", tree.Path); err != nil {
		return err
	}
	if err := m.deleteGitBranch(ctx, tree.Name); err != nil {
		return err
	}
	return nil
}

func (m *Manager) deleteGitBranch(ctx context.Context, name string) error {
	branch := "agent/" + name
	if _, err := gitOutput(ctx, m.root, "rev-parse", "--verify", branch); err != nil {
		return nil
	}
	if _, err := gitOutput(ctx, m.root, "branch", "-D", branch); err != nil {
		return err
	}
	return nil
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

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("worktree name required")
	}
	if name == "." || name == ".." || strings.Contains(name, "..") {
		return "", errors.New("worktree name must not contain path traversal")
	}
	if strings.ContainsAny(name, `/\:`) {
		return "", errors.New("worktree name must not contain path separators")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' ||
			r == '_' {
			continue
		}
		return "", errors.New("worktree name may contain only letters, digits, dash, and underscore")
	}
	return name, nil
}

func isGitRepository(ctx context.Context, root string) bool {
	out, err := gitOutput(ctx, root, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
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
