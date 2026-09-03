package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"code-agent/internal/permission"
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

type ToolResultRecord struct {
	Name          string
	ExitCode      int
	Error         string
	Output        string
	Truncated     bool
	ModifiedFiles []string
}

type TodoItem struct {
	Content    string `json:"content"`
	ActiveForm string `json:"active_form,omitempty"`
	Status     string `json:"status"`
}

type PlanState struct {
	Steps        []string `json:"steps,omitempty"`
	CurrentIndex int      `json:"current_index,omitempty"`
	Mode         string   `json:"mode,omitempty"`
}

type AgentSpawnRecord struct {
	Kind        string    `json:"kind"`
	Task        string    `json:"task"`
	ContextJSON string    `json:"context_json,omitempty"`
	Parallel    bool      `json:"parallel,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type UndoChange struct {
	Path   string `json:"path"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type UndoEntry struct {
	ID          string       `json:"id"`
	Description string       `json:"description"`
	Changes     []UndoChange `json:"changes"`
	CreatedAt   time.Time    `json:"created_at"`
}

type WorktreeState struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	BaseRef string `json:"base_ref,omitempty"`
	Active  bool   `json:"active,omitempty"`
}

type Session struct {
	ID            string             `json:"id"`
	WorkingDir    string             `json:"working_dir"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	Messages      []Message          `json:"messages"`
	Metadata      map[string]string  `json:"metadata,omitempty"`
	Metrics       SessionMetrics     `json:"metrics,omitempty"`
	Todos         []TodoItem         `json:"todos,omitempty"`
	Plan          PlanState          `json:"plan,omitempty"`
	Mode          string             `json:"mode,omitempty"`
	Agents        []AgentSpawnRecord `json:"agents,omitempty"`
	Undo          []UndoEntry        `json:"undo,omitempty"`
	ApprovedTools []string           `json:"approved_tools,omitempty"`
	Worktrees     []WorktreeState    `json:"worktrees,omitempty"`
	// ApprovalHistory 持久化用户对工具调用的批准记录，用于在会话恢复后继续
	// 推导允许规则建议。仅作为数据载体，session 包本身不解释其含义。
	ApprovalHistory []permission.ApprovalRecord `json:"approval_history,omitempty"`
}

type SessionMetrics struct {
	TotalTokensIn     int      `json:"total_tokens_in,omitempty"`
	TotalTokensOut    int      `json:"total_tokens_out,omitempty"`
	TotalCachedTokens int      `json:"total_cached_tokens,omitempty"`
	TotalCost         float64  `json:"total_cost,omitempty"`
	ToolCalls         int      `json:"tool_calls,omitempty"`
	FilesModified     []string `json:"files_modified,omitempty"`
}

type Summary struct {
	ID                string
	WorkingDir        string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	MessageCount      int
	LastMessage       string
	Metrics           SessionMetrics
	TodoCount         int
	PlanSteps         int
	Mode              string
	AgentCount        int
	UndoCount         int
	ApprovedToolCount int
	WorktreeCount     int
}

type Manager struct {
	mu      sync.Mutex
	store   Store
	current Session
}

// Close releases the underlying store when it owns an external resource such
// as the SQLite event ledger. In-memory stores remain a no-op.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	closer, ok := m.store.(interface{ Close() error })
	if !ok {
		return nil
	}
	return closer.Close()
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
	candidate := Session{
		ID:         randomID(),
		WorkingDir: workingDir,
		CreatedAt:  now,
		UpdatedAt:  now,
		Messages:   []Message{},
		Metadata:   map[string]string{},
	}
	if err := m.store.Save(context.Background(), candidate); err != nil {
		return Session{}
	}
	m.current = candidate
	return cloneSession(candidate)
}

func (m *Manager) Current() Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneSession(m.current)
}

func (m *Manager) Append(role Role, content string) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

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
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

func (m *Manager) commitLocked(ctx context.Context, previous Session) bool {
	if err := m.store.Save(ctx, m.current); err != nil {
		m.current = previous
		return false
	}
	return true
}

func (m *Manager) SetMetadata(key, value string) Session {
	return m.MergeMetadata(map[string]string{key: value})
}

func (m *Manager) SetMode(mode string) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	now := time.Now()
	m.ensureCurrentLocked(now)
	m.ensureMetadataLocked()
	mode = normalizeMode(mode)
	m.current.Mode = mode
	m.current.Metadata["mode"] = mode
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

func (m *Manager) SetWorkingDir(workingDir string) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	now := time.Now()
	m.ensureCurrentLocked(now)
	m.current.WorkingDir = strings.TrimSpace(workingDir)
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

func (m *Manager) AddLLMUsage(tokensIn, tokensOut int, cost float64) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	now := time.Now()
	m.ensureCurrentLocked(now)
	if tokensIn > 0 {
		m.current.Metrics.TotalTokensIn += tokensIn
	}
	if tokensOut > 0 {
		m.current.Metrics.TotalTokensOut += tokensOut
	}
	if cost > 0 {
		m.current.Metrics.TotalCost += cost
	}
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

// AddCachedTokens accumulates prompt-cache hit tokens into the session metrics.
func (m *Manager) AddCachedTokens(tokens int) {
	if tokens <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	now := time.Now()
	m.ensureCurrentLocked(now)
	m.current.Metrics.TotalCachedTokens += tokens
	m.current.UpdatedAt = now
	_ = m.commitLocked(context.Background(), previous)
}

func (m *Manager) RecordToolCall(modifiedFiles ...string) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	now := time.Now()
	m.ensureCurrentLocked(now)
	m.current.Metrics.ToolCalls++
	m.current.Metrics.FilesModified = mergeFiles(m.current.Metrics.FilesModified, modifiedFiles)
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

func (m *Manager) AppendToolResult(record ToolResultRecord) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	now := time.Now()
	m.ensureCurrentLocked(now)
	m.current.Metrics.ToolCalls++
	m.current.Metrics.FilesModified = mergeFiles(m.current.Metrics.FilesModified, record.ModifiedFiles)
	m.current.Messages = append(m.current.Messages, Message{
		Role:      RoleTool,
		Content:   formatToolResult(record),
		CreatedAt: now,
	})
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

func (m *Manager) SetTodos(items []TodoItem) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	now := time.Now()
	m.ensureCurrentLocked(now)
	m.current.Todos = cloneTodos(items)
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

func (m *Manager) SetPlan(plan PlanState) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	now := time.Now()
	m.ensureCurrentLocked(now)
	m.current.Plan = clonePlan(plan)
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

func (m *Manager) AppendAgentSpawn(record AgentSpawnRecord) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	now := time.Now()
	m.ensureCurrentLocked(now)
	record.Kind = strings.TrimSpace(record.Kind)
	record.Task = strings.TrimSpace(record.Task)
	record.ContextJSON = strings.TrimSpace(record.ContextJSON)
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	m.current.Agents = append(m.current.Agents, record)
	m.current.Messages = append(m.current.Messages, Message{
		Role:      RoleSystem,
		Content:   formatAgentSpawn(record),
		CreatedAt: now,
	})
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

func (m *Manager) SetUndo(entries []UndoEntry) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	now := time.Now()
	m.ensureCurrentLocked(now)
	m.current.Undo = cloneUndo(entries)
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

func (m *Manager) SetApprovedTools(tools []string) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	now := time.Now()
	m.ensureCurrentLocked(now)
	m.current.ApprovedTools = normalizeToolList(tools)
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

// SetApprovalHistory 持久化批准历史，使其在会话恢复后仍可用于推导允许规则建议。
func (m *Manager) SetApprovalHistory(records []permission.ApprovalRecord) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	now := time.Now()
	m.ensureCurrentLocked(now)
	m.current.ApprovalHistory = cloneApprovalHistory(records)
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

func (m *Manager) SetWorktrees(trees []WorktreeState) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	now := time.Now()
	m.ensureCurrentLocked(now)
	m.current.Worktrees = cloneWorktrees(trees)
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

func (m *Manager) MergeMetadata(values map[string]string) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

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
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

func (m *Manager) Compact(keep int) (Session, int, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

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
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous), 0, ""
	}
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

func (m *Manager) Resume(ctx context.Context, id string) (Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Session{}, ErrNotFound
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)

	loaded, err := m.store.Load(ctx, id)
	if err != nil {
		return Session{}, err
	}
	if loaded == nil {
		return Session{}, ErrNotFound
	}
	m.current = cloneSession(*loaded)
	// Guarantee a strictly-increasing UpdatedAt so recency ordering stays
	// deterministic even when the OS clock hasn't advanced between calls
	// (coarse timer resolution under fast test/prod execution). Otherwise
	// equal timestamps make the recency sort fall back to nondeterministic
	// map iteration order.
	now := time.Now()
	if !now.After(loaded.UpdatedAt) {
		now = loaded.UpdatedAt.Add(time.Nanosecond)
	}
	m.current.UpdatedAt = now
	if err := m.store.Save(ctx, m.current); err != nil {
		m.current = previous
		return Session{}, err
	}
	return cloneSession(m.current), nil
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

func (m *Manager) ListRecent(ctx context.Context, limit int) ([]Summary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessions, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
	}

	out := make([]Summary, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, Summary{
			ID:                session.ID,
			WorkingDir:        session.WorkingDir,
			CreatedAt:         session.CreatedAt,
			UpdatedAt:         session.UpdatedAt,
			MessageCount:      len(session.Messages),
			LastMessage:       lastMessagePreview(session.Messages),
			Metrics:           cloneMetrics(session.Metrics),
			TodoCount:         len(session.Todos),
			PlanSteps:         len(session.Plan.Steps),
			Mode:              sessionMode(session),
			AgentCount:        len(session.Agents),
			UndoCount:         len(session.Undo),
			ApprovedToolCount: len(session.ApprovedTools),
			WorktreeCount:     len(session.Worktrees),
		})
	}
	return out, nil
}

func (m *Manager) AutoSave(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current.ID == "" {
		return ErrNotFound
	}
	m.current.UpdatedAt = time.Now()
	return m.store.Save(ctx, m.current)
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
	out.Metrics = cloneMetrics(session.Metrics)
	out.Todos = cloneTodos(session.Todos)
	out.Plan = clonePlan(session.Plan)
	out.Agents = cloneAgents(session.Agents)
	out.Undo = cloneUndo(session.Undo)
	out.ApprovedTools = normalizeToolList(session.ApprovedTools)
	out.Worktrees = cloneWorktrees(session.Worktrees)
	out.ApprovalHistory = cloneApprovalHistory(session.ApprovalHistory)
	return out
}

func cloneWorktrees(in []WorktreeState) []WorktreeState {
	if len(in) == 0 {
		return nil
	}
	out := make([]WorktreeState, len(in))
	copy(out, in)
	return out
}

func cloneApprovalHistory(in []permission.ApprovalRecord) []permission.ApprovalRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]permission.ApprovalRecord, len(in))
	copy(out, in)
	return out
}

func normalizeToolList(tools []string) []string {
	if len(tools) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		out = append(out, tool)
	}
	return out
}

func cloneUndo(in []UndoEntry) []UndoEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]UndoEntry, len(in))
	for i, entry := range in {
		out[i] = entry
		if len(entry.Changes) > 0 {
			out[i].Changes = append([]UndoChange(nil), entry.Changes...)
		}
	}
	return out
}

func cloneAgents(in []AgentSpawnRecord) []AgentSpawnRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]AgentSpawnRecord, len(in))
	copy(out, in)
	return out
}

func clonePlan(in PlanState) PlanState {
	out := in
	if len(in.Steps) > 0 {
		out.Steps = append([]string(nil), in.Steps...)
	}
	return out
}

func cloneTodos(in []TodoItem) []TodoItem {
	if len(in) == 0 {
		return nil
	}
	out := make([]TodoItem, len(in))
	copy(out, in)
	return out
}

func cloneMetrics(in SessionMetrics) SessionMetrics {
	out := in
	if len(in.FilesModified) > 0 {
		out.FilesModified = append([]string(nil), in.FilesModified...)
	}
	return out
}

func normalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "plan":
		return "plan"
	default:
		return "chat"
	}
}

func sessionMode(session Session) string {
	if strings.TrimSpace(session.Mode) != "" {
		return normalizeMode(session.Mode)
	}
	if session.Metadata != nil {
		if mode := strings.TrimSpace(session.Metadata["mode"]); mode != "" {
			return normalizeMode(mode)
		}
	}
	return "chat"
}

func mergeFiles(existing []string, files []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(files))
	out := make([]string, 0, len(existing)+len(files))
	for _, file := range existing {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		out = append(out, file)
	}
	for _, file := range files {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		out = append(out, file)
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

func lastMessagePreview(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}
	for i := len(messages) - 1; i >= 0; i-- {
		line := firstContentLine(messages[i].Content)
		if line != "" {
			return fmt.Sprintf("%s: %s", messages[i].Role, line)
		}
	}
	return ""
}

const maxStoredToolOutput = 2000

func formatToolResult(record ToolResultRecord) string {
	name := strings.TrimSpace(record.Name)
	if name == "" {
		name = "unknown"
	}

	lines := []string{
		"tool: " + name,
		fmt.Sprintf("exit_code: %d", record.ExitCode),
		fmt.Sprintf("truncated: %t", record.Truncated),
	}
	if len(record.ModifiedFiles) > 0 {
		lines = append(lines, "modified_files: "+strings.Join(mergeFiles(nil, record.ModifiedFiles), ", "))
	}
	if strings.TrimSpace(record.Error) != "" {
		lines = append(lines, "error:", strings.TrimSpace(record.Error))
	}
	output := truncateToolOutput(record.Output)
	if output != "" {
		lines = append(lines, "output:", output)
	}
	return strings.Join(lines, "\n")
}

func truncateToolOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	if len(output) <= maxStoredToolOutput {
		return output
	}
	return strings.TrimSpace(output[:maxStoredToolOutput]) + "\n[stored tool output truncated]"
}

func formatAgentSpawn(record AgentSpawnRecord) string {
	kind := strings.TrimSpace(record.Kind)
	if kind == "" {
		kind = "agent"
	}
	task := strings.TrimSpace(record.Task)
	if task == "" {
		task = "unspecified task"
	}
	lines := []string{
		"agent_spawn:",
		"kind: " + kind,
		"task: " + task,
		fmt.Sprintf("parallel: %t", record.Parallel),
	}
	if strings.TrimSpace(record.ContextJSON) != "" {
		lines = append(lines, "context_json: "+strings.TrimSpace(record.ContextJSON))
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
