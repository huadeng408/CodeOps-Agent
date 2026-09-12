package worktree

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"code-agent/internal/safety"
)

const (
	AgentWorktreeActive   = "active"
	AgentWorktreeReleased = "released"
	AgentWorktreeReaped   = "reaped"
	defaultAgentLeaseTTL  = 30 * time.Minute
)

type Worktree struct {
	Name            string
	Path            string
	BaseRef         string
	Active          bool
	RequestID       string    `json:"request_id,omitempty"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	ChildSessionID  string    `json:"child_session_id,omitempty"`
	LeaseID         string    `json:"lease_id,omitempty"`
	LeaseExpiresAt  time.Time `json:"lease_expires_at,omitempty"`
	Status          string    `json:"status,omitempty"`
}

// AgentSpawnRequest is the authenticated boundary between SpawnAgent and the
// Harness worktree manager. WorktreeName is supplied by the orchestrator but
// is always validated and resolved beneath the manager's controlled root.
type AgentSpawnRequest struct {
	RequestID       string
	ParentSessionID string
	ChildSessionID  string
	WorktreeName    string
	BaseRef         string
}

type Manager struct {
	mu       sync.Mutex
	root     string
	baseRef  string
	leaseTTL time.Duration
	trees    map[string]Worktree
}

func NewManager(root, baseRef string) *Manager {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	if strings.TrimSpace(baseRef) == "" {
		baseRef = "HEAD"
	}
	return &Manager{
		root:     filepath.Clean(root),
		baseRef:  baseRef,
		leaseTTL: defaultAgentLeaseTTL,
		trees:    make(map[string]Worktree),
	}
}

// SetAgentLeaseTTL overrides the default lease duration for tests and hosts
// with a shorter lifecycle. Non-positive values are ignored.
func (m *Manager) SetAgentLeaseTTL(ttl time.Duration) {
	if m == nil || ttl <= 0 {
		return
	}
	m.mu.Lock()
	m.leaseTTL = ttl
	m.mu.Unlock()
}

// SpawnAgent creates or idempotently resumes an isolated worktree for one
// SpawnAgent request. It never falls back to the parent working tree.
func (m *Manager) SpawnAgent(ctx context.Context, request AgentSpawnRequest) (Worktree, error) {
	if m == nil {
		return Worktree{}, errors.New("worktree manager is nil")
	}
	if err := validateAgentSpawnRequest(request); err != nil {
		return Worktree{}, err
	}
	name, err := normalizeName(request.WorktreeName)
	if err != nil {
		return Worktree{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !isGitRepository(ctx, m.root) {
		return Worktree{}, errors.New("agent worktree requires a git repository")
	}
	for _, existing := range m.trees {
		if existing.RequestID == request.RequestID {
			if existing.ParentSessionID != request.ParentSessionID || existing.ChildSessionID != request.ChildSessionID || existing.Name != name {
				return Worktree{}, errors.New("agent request identity conflicts with existing worktree")
			}
			if existing.Status == "" {
				existing.Status = AgentWorktreeActive
			}
			return existing, nil
		}
	}
	if existing, ok := m.trees[name]; ok {
		return Worktree{}, fmt.Errorf("worktree %s already exists", existing.Name)
	}
	baseRef := strings.TrimSpace(request.BaseRef)
	if baseRef == "" {
		baseRef = strings.TrimSpace(m.baseRef)
	}
	if baseRef == "" {
		baseRef = "HEAD"
	}
	if err := validateBaseRef(baseRef); err != nil {
		return Worktree{}, fmt.Errorf("agent base revision %q is invalid: %w", baseRef, err)
	}
	if _, err := gitOutput(ctx, m.root, "rev-parse", "--verify", baseRef+"^{commit}"); err != nil {
		return Worktree{}, fmt.Errorf("agent base revision %q is invalid: %w", baseRef, err)
	}
	path := filepath.Join(m.root, ".agent", "worktrees", name)
	if err := ensureContainedPath(m.root, path); err != nil {
		return Worktree{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Worktree{}, fmt.Errorf("create agent worktree base dir: %w", err)
	}
	if _, err := gitOutput(ctx, m.root, "worktree", "add", "-b", "agent/"+name, path, baseRef); err != nil {
		return Worktree{}, fmt.Errorf("create agent worktree: %w", err)
	}
	leaseID, err := newLeaseID()
	if err != nil {
		_ = m.removeGitWorktree(ctx, Worktree{Name: name, Path: path, BaseRef: baseRef}, true)
		return Worktree{}, err
	}
	for key, existing := range m.trees {
		existing.Active = false
		m.trees[key] = existing
	}
	tree := Worktree{
		Name:            name,
		Path:            path,
		BaseRef:         baseRef,
		Active:          true,
		RequestID:       request.RequestID,
		ParentSessionID: request.ParentSessionID,
		ChildSessionID:  request.ChildSessionID,
		LeaseID:         leaseID,
		LeaseExpiresAt:  time.Now().Add(m.leaseTTL),
		Status:          AgentWorktreeActive,
	}
	m.trees[name] = tree
	return tree, nil
}

// RenewAgentLease extends an active agent lease without changing its path.
func (m *Manager) RenewAgentLease(ctx context.Context, leaseID string) (Worktree, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return Worktree{}, err
		}
	}
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return Worktree{}, errors.New("agent lease id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, tree := range m.trees {
		if tree.LeaseID != leaseID {
			continue
		}
		if tree.Status != "" && tree.Status != AgentWorktreeActive {
			return Worktree{}, errors.New("agent worktree lease is not active")
		}
		tree.LeaseExpiresAt = time.Now().Add(m.leaseTTL)
		tree.Status = AgentWorktreeActive
		m.trees[name] = tree
		return tree, nil
	}
	return Worktree{}, errors.New("agent lease not found")
}

// FindAgent returns a defensive copy of an active agent worktree by request
// identity. It is used by lifecycle handlers to validate the lease before
// cleanup and to persist the terminal metadata.
func (m *Manager) FindAgent(requestID string) (Worktree, bool) {
	if m == nil {
		return Worktree{}, false
	}
	requestID = strings.TrimSpace(requestID)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tree := range m.trees {
		if tree.RequestID == requestID {
			return tree, true
		}
	}
	return Worktree{}, false
}

// CleanupAgent releases one agent worktree. A missing request is treated as
// an idempotent success so duplicate completion/recovery messages are safe.
func (m *Manager) CleanupAgent(ctx context.Context, requestID string, discard bool, reason string) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return errors.New("agent request id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, tree := range m.trees {
		if tree.RequestID != requestID {
			continue
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := m.removeGitWorktree(ctx, tree, discard); err != nil {
			return fmt.Errorf("cleanup agent worktree %s: %w", name, err)
		}
		delete(m.trees, name)
		return nil
	}
	_ = reason
	return nil
}

// MarkAgentCompleted records a terminal child result without deleting the
// checkout. The parent conversation may still need to read or modify files
// in the managed worktree before its own run reaches a terminal state.
func (m *Manager) MarkAgentCompleted(requestID string, reason string) (Worktree, error) {
	if m == nil {
		return Worktree{}, errors.New("worktree manager is nil")
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return Worktree{}, errors.New("agent request id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, tree := range m.trees {
		if tree.RequestID != requestID {
			continue
		}
		tree.Active = false
		tree.Status = "completed"
		m.trees[name] = tree
		_ = reason
		return tree, nil
	}
	return Worktree{}, errors.New("agent worktree not found")
}

// ReapExpired removes abandoned leases with discard semantics. It is used by
// startup recovery and crash cleanup, so dirty child worktrees cannot block
// the parent session forever.
func (m *Manager) ReapExpired(ctx context.Context, now time.Time) ([]Worktree, error) {
	if m == nil {
		return nil, errors.New("worktree manager is nil")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if now.IsZero() {
		now = time.Now()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var reaped []Worktree
	for name, tree := range m.trees {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return reaped, err
			}
		}
		if (tree.Status != AgentWorktreeActive && tree.Status != "completed") || tree.LeaseExpiresAt.IsZero() || tree.LeaseExpiresAt.After(now) {
			continue
		}
		if err := m.removeGitWorktree(ctx, tree, true); err != nil {
			return reaped, fmt.Errorf("reap agent worktree %s: %w", name, err)
		}
		tree.Status = AgentWorktreeReaped
		tree.Active = false
		reaped = append(reaped, tree)
		delete(m.trees, name)
	}
	sort.Slice(reaped, func(i, j int) bool { return reaped[i].Name < reaped[j].Name })
	return reaped, nil
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

func (m *Manager) CleanupDiscard(name string) error {
	return m.CleanupContextDiscard(context.Background(), name)
}

func (m *Manager) CleanupContext(ctx context.Context, name string) error {
	return m.cleanupContext(ctx, name, false)
}

func (m *Manager) CleanupContextDiscard(ctx context.Context, name string) error {
	return m.cleanupContext(ctx, name, true)
}

func (m *Manager) cleanupContext(ctx context.Context, name string, discard bool) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
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
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if err := m.removeGitWorktree(ctx, removed, discard); err != nil {
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
	valid := make([]Worktree, 0, len(trees))
	for _, tree := range trees {
		if _, err := normalizeName(tree.Name); err != nil {
			continue
		}
		valid = append(valid, tree)
	}
	_ = m.RestoreChecked(valid)
}

// RestoreChecked restores persisted state only when every agent checkout is
// inside the controlled root. The legacy Restore adapter intentionally drops
// invalid entries; new callers should use this error-returning form.
func (m *Manager) RestoreChecked(trees []Worktree) error {
	if m == nil {
		return errors.New("worktree manager is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate the entire snapshot before replacing the current state.
	restored := make(map[string]Worktree, len(trees))
	activeSeen := false
	for _, tree := range trees {
		name, err := normalizeName(tree.Name)
		if err != nil {
			return fmt.Errorf("restore worktree %q: %w", tree.Name, err)
		}
		if _, exists := restored[name]; exists {
			return fmt.Errorf("restore worktree %q: duplicate name", name)
		}
		tree.Name = name
		if strings.TrimSpace(tree.Path) == "" {
			tree.Path = filepath.Join(m.root, ".agent", "worktrees", tree.Name)
		}
		if strings.TrimSpace(tree.BaseRef) == "" {
			tree.BaseRef = m.baseRef
		}
		if err := validateBaseRef(tree.BaseRef); err != nil {
			return fmt.Errorf("restore worktree %q: %w", tree.Name, err)
		}
		if err := ensureContainedPath(m.root, tree.Path); err != nil {
			return err
		}
		if tree.RequestID != "" {
			if err := validateAgentSpawnRequest(AgentSpawnRequest{
				RequestID: tree.RequestID, ParentSessionID: tree.ParentSessionID,
				ChildSessionID: tree.ChildSessionID, WorktreeName: tree.Name,
			}); err != nil {
				return fmt.Errorf("restore agent worktree %q: %w", tree.Name, err)
			}
			if err := validateLeaseID(tree.LeaseID); err != nil {
				return fmt.Errorf("restore agent worktree %q: %w", tree.Name, err)
			}
			if tree.LeaseExpiresAt.IsZero() {
				return fmt.Errorf("restore agent worktree %q: lease expiry is required", tree.Name)
			}
			if tree.Status == "" {
				tree.Status = AgentWorktreeActive
			}
			if tree.Status != AgentWorktreeActive {
				return fmt.Errorf("restore agent worktree %q: status must be active", tree.Name)
			}
		}
		if tree.Active {
			if activeSeen {
				tree.Active = false
			} else {
				activeSeen = true
			}
		}
		restored[tree.Name] = tree
	}
	m.trees = restored
	return nil
}

func (m *Manager) createGitWorktree(ctx context.Context, tree Worktree) error {
	if !isGitRepository(ctx, m.root) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(tree.Path), 0o755); err != nil {
		return fmt.Errorf("create worktree base dir: %w", err)
	}
	if err := ensureContainedPath(m.root, tree.Path); err != nil {
		return err
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
	if err := validateBaseRef(baseRef); err != nil {
		return "HEAD"
	}
	if !isGitRepository(ctx, m.root) {
		return baseRef
	}
	if _, err := gitOutput(ctx, m.root, "rev-parse", "--verify", baseRef+"^{commit}"); err == nil {
		return baseRef
	}
	return "HEAD"
}

func (m *Manager) removeGitWorktree(ctx context.Context, tree Worktree, discard bool) error {
	if !isGitRepository(ctx, m.root) {
		return nil
	}
	if strings.TrimSpace(tree.Path) == "" {
		return nil
	}
	if _, err := os.Stat(tree.Path); errors.Is(err, os.ErrNotExist) {
		return m.deleteGitBranch(ctx, tree.Name, discard)
	}
	if err := m.ensureRemovable(ctx, tree, discard); err != nil {
		return err
	}
	args := []string{"worktree", "remove"}
	if discard {
		args = append(args, "--force")
	}
	args = append(args, tree.Path)
	if _, err := gitOutput(ctx, m.root, args...); err != nil {
		return err
	}
	if err := m.deleteGitBranch(ctx, tree.Name, discard); err != nil {
		return err
	}
	return nil
}

func (m *Manager) ensureRemovable(ctx context.Context, tree Worktree, discard bool) error {
	if discard {
		return nil
	}
	status, err := gitOutput(ctx, tree.Path, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return fmt.Errorf("worktree %s has uncommitted changes; use discard cleanup to remove it", tree.Name)
	}
	branch := "agent/" + tree.Name
	baseRef := strings.TrimSpace(tree.BaseRef)
	if baseRef == "" {
		baseRef = m.baseRef
	}
	if baseRef == "" {
		baseRef = "HEAD"
	}
	if err := validateBaseRef(baseRef); err != nil {
		return fmt.Errorf("worktree %s base revision is invalid: %w", tree.Name, err)
	}
	if _, err := gitOutput(ctx, m.root, "rev-parse", "--verify", branch); err != nil {
		return nil
	}
	if _, err := gitOutput(ctx, m.root, "rev-parse", "--verify", baseRef+"^{commit}"); err != nil {
		baseRef = "HEAD"
	}
	unmerged, err := gitOutput(ctx, m.root, "log", "--oneline", baseRef+".."+branch)
	if err != nil {
		return err
	}
	if strings.TrimSpace(unmerged) != "" {
		return fmt.Errorf("worktree %s has commits not merged into %s; use discard cleanup to remove it", tree.Name, baseRef)
	}
	return nil
}

func (m *Manager) deleteGitBranch(ctx context.Context, name string, discard bool) error {
	branch := "agent/" + name
	if _, err := gitOutput(ctx, m.root, "rev-parse", "--verify", branch); err != nil {
		return nil
	}
	deleteFlag := "-d"
	if discard {
		deleteFlag = "-D"
	}
	if _, err := gitOutput(ctx, m.root, "branch", deleteFlag, branch); err != nil {
		return err
	}
	return nil
}

func (m *Manager) DiffLines(ctx context.Context) ([]string, error) {
	if m == nil {
		return nil, errors.New("worktree manager is nil")
	}
	return diffLinesAt(ctx, m.root)
}

// DiffLinesAt returns a bounded, read-only git status summary for a managed
// worktree checkout. Callers should only pass paths returned by List; the
// manager still verifies containment before invoking git.
func (m *Manager) DiffLinesAt(ctx context.Context, path string) ([]string, error) {
	if m == nil {
		return nil, errors.New("worktree manager is nil")
	}
	if err := ensureContainedPath(m.root, path); err != nil {
		return nil, err
	}
	m.mu.Lock()
	managed := false
	for _, tree := range m.trees {
		if samePath(tree.Path, path) {
			managed = true
			break
		}
	}
	m.mu.Unlock()
	if !managed {
		return nil, fmt.Errorf("worktree path is not managed: %s", path)
	}
	return diffLinesAt(ctx, path)
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}

func diffLinesAt(ctx context.Context, root string) ([]string, error) {
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

func validateAgentSpawnRequest(request AgentSpawnRequest) error {
	for field, value := range map[string]string{
		"request id":        request.RequestID,
		"parent session id": request.ParentSessionID,
		"child session id":  request.ChildSessionID,
	} {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("agent %s is required", field)
		}
		if len(value) > 128 {
			return fmt.Errorf("agent %s is too long", field)
		}
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("agent %s contains a NUL byte", field)
		}
		for _, r := range value {
			if r < 0x20 || r == 0x7f {
				return fmt.Errorf("agent %s contains a control character", field)
			}
		}
	}
	return nil
}

func validateBaseRef(baseRef string) error {
	baseRef = strings.TrimSpace(baseRef)
	if baseRef == "" {
		return errors.New("base revision is empty")
	}
	if len(baseRef) > 256 || strings.HasPrefix(baseRef, "-") || strings.Contains(baseRef, "..") || strings.Contains(baseRef, "^{") || strings.ContainsAny(baseRef, "\\:*?[\x00") || strings.IndexFunc(baseRef, unicode.IsSpace) >= 0 {
		return errors.New("base revision contains unsafe syntax")
	}
	for _, r := range baseRef {
		if r < 0x20 || r == 0x7f {
			return errors.New("base revision contains a control character")
		}
	}
	return nil
}

func validateLeaseID(leaseID string) error {
	leaseID = strings.TrimSpace(leaseID)
	if len(leaseID) != 38 || !strings.HasPrefix(leaseID, "lease-") {
		return errors.New("lease id is invalid")
	}
	for _, r := range leaseID[len("lease-"):] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return errors.New("lease id is invalid")
		}
	}
	return nil
}

func ensureContainedPath(root, target string) error {
	rootReal, err := resolvePathWithExistingParent(root)
	if err != nil {
		return fmt.Errorf("resolve agent worktree root: %w", err)
	}
	controlledRoot := filepath.Join(filepath.Clean(root), ".agent", "worktrees")
	controlledReal, err := resolvePathWithExistingParent(controlledRoot)
	if err != nil || !pathWithin(rootReal, controlledReal, true) {
		return errors.New("agent worktree path escapes repository root")
	}
	targetReal, err := resolvePathWithExistingParent(target)
	if err != nil || !pathWithin(controlledReal, targetReal, false) {
		return errors.New("agent worktree path escapes repository root")
	}
	return nil
}

func pathWithin(root, target string, allowRoot bool) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return allowRoot || rel != "."
}

func resolvePathWithExistingParent(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	current := abs
	suffix := make([]string, 0, 4)
	for {
		_, statErr := os.Lstat(current)
		if statErr == nil {
			real, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return filepath.Clean(real), nil
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", statErr
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func newLeaseID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate agent lease id: %w", err)
	}
	return "lease-" + hex.EncodeToString(raw[:]), nil
}

func isGitRepository(ctx context.Context, root string) bool {
	out, err := gitOutput(ctx, root, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("git command is required")
	}
	cmdArgs := safety.HardenedGitArgs(root, args[0], args[1:])
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = scrubGitEnvironment()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func scrubGitEnvironment() []string {
	return safety.ScrubGitEnvironment(os.Environ())
}
