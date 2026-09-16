package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/permission"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

const ConversationMessageSchemaVersion uint32 = 1

type ToolCall struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ArgumentsJSON string `json:"arguments_json,omitempty"`
}

type InvocationStatus string

const (
	InvocationPrepared   InvocationStatus = "prepared"
	InvocationDispatched InvocationStatus = "dispatched"
	InvocationCommitted  InvocationStatus = "result_committed"
	InvocationUnknown    InvocationStatus = "unknown"
)

type InvocationRecord struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	ArgumentsJSON string           `json:"arguments_json,omitempty"`
	Status        InvocationStatus `json:"status"`
	Output        string           `json:"output,omitempty"`
	Error         string           `json:"error,omitempty"`
	ExitCode      int              `json:"exit_code"`
	Truncated     bool             `json:"truncated,omitempty"`
	SpillLocator  string           `json:"spill_locator,omitempty"`
	SpillSHA256   string           `json:"spill_sha256,omitempty"`
	SpillBytes    int64            `json:"spill_bytes,omitempty"`
	ModifiedFiles []string         `json:"modified_files,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
	CompletedAt   time.Time        `json:"completed_at,omitempty"`
}

type Message struct {
	Role          Role       `json:"role"`
	Content       string     `json:"content"`
	CreatedAt     time.Time  `json:"created_at"`
	SchemaVersion uint32     `json:"schema_version,omitempty"`
	Name          string     `json:"name,omitempty"`
	ToolCallID    string     `json:"tool_call_id,omitempty"`
	ToolCalls     []ToolCall `json:"tool_calls,omitempty"`
	IsError       bool       `json:"is_error,omitempty"`
}

type ToolResultRecord struct {
	ToolCallID    string
	Name          string
	ExitCode      int
	Error         string
	Output        string
	Truncated     bool
	SpillLocator  string
	SpillSHA256   string
	SpillBytes    int64
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
	Kind            string    `json:"kind"`
	Task            string    `json:"task"`
	ContextJSON     string    `json:"context_json,omitempty"`
	Parallel        bool      `json:"parallel,omitempty"`
	ProtocolVersion string    `json:"protocol_version,omitempty"`
	RequestID       string    `json:"request_id,omitempty"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	ChildSessionID  string    `json:"child_session_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
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
	Name            string    `json:"name"`
	Path            string    `json:"path"`
	BaseRef         string    `json:"base_ref,omitempty"`
	Active          bool      `json:"active,omitempty"`
	RequestID       string    `json:"request_id,omitempty"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	ChildSessionID  string    `json:"child_session_id,omitempty"`
	LeaseID         string    `json:"lease_id,omitempty"`
	LeaseExpiresAt  time.Time `json:"lease_expires_at,omitempty"`
	Status          string    `json:"status,omitempty"`
	Retained        bool      `json:"retained,omitempty"`
}

// WorktreeLifecycle is an append-only audit record for an agent checkout.
// Current active leases live in Worktrees; terminal states remain here so a
// recovery or cleanup decision is visible after the directory is removed.
type WorktreeLifecycle struct {
	Name            string    `json:"name,omitempty"`
	Path            string    `json:"path,omitempty"`
	BaseRef         string    `json:"base_ref,omitempty"`
	RequestID       string    `json:"request_id"`
	ParentSessionID string    `json:"parent_session_id"`
	ChildSessionID  string    `json:"child_session_id"`
	LeaseID         string    `json:"lease_id"`
	Status          string    `json:"status"`
	Reason          string    `json:"reason,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type Session struct {
	ID               string              `json:"id"`
	Actor            identity.Actor      `json:"actor,omitempty"`
	WorkingDir       string              `json:"working_dir"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
	Messages         []Message           `json:"messages"`
	Invocations      []InvocationRecord  `json:"invocations,omitempty"`
	Metadata         map[string]string   `json:"metadata,omitempty"`
	Metrics          SessionMetrics      `json:"metrics,omitempty"`
	Todos            []TodoItem          `json:"todos,omitempty"`
	Plan             PlanState           `json:"plan,omitempty"`
	PlanTodoRevision uint64              `json:"plan_todo_revision,omitempty"`
	Mode             string              `json:"mode,omitempty"`
	Agents           []AgentSpawnRecord  `json:"agents,omitempty"`
	Undo             []UndoEntry         `json:"undo,omitempty"`
	ApprovedTools    []string            `json:"approved_tools,omitempty"`
	Worktrees        []WorktreeState     `json:"worktrees,omitempty"`
	WorktreeEvents   []WorktreeLifecycle `json:"worktree_events,omitempty"`
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

// ContextEnvelope returns a verified, metadata-only projection of the current
// Session Ledger and workspace for a Harness-managed model request.
func (m *Manager) ContextEnvelope(ctx context.Context) (*codeagentpb.ContextEnvelope, error) {
	m.mu.Lock()
	current := cloneSession(m.current)
	store := m.store
	m.mu.Unlock()
	if current.ID == "" {
		return nil, errors.New("context envelope requires a current session")
	}
	ledgerStore, ok := store.(interface{ Ledger() (EventLog, error) })
	if !ok {
		return nil, errors.New("context envelope requires a Session Ledger")
	}
	ledger, err := ledgerStore.Ledger()
	if err != nil {
		return nil, err
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, ledger, current.ID)
	if err != nil {
		return nil, err
	}
	return buildContextEnvelope(current.WorkingDir, snapshot.Events)
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
		Role: role, Content: content, CreatedAt: now,
		SchemaVersion: ConversationMessageSchemaVersion,
	})
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

// BeginInvocation records a tool call before any external side effect occurs.
// A committed or dispatched record is returned unchanged so callers can avoid
// executing an invocation twice after a transport retry.
func (m *Manager) BeginInvocation(id, name, argumentsJSON string) (InvocationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	if id == "" || name == "" {
		return InvocationRecord{}, fmt.Errorf("invocation id and name are required")
	}
	for _, existing := range m.current.Invocations {
		if existing.ID == id {
			if existing.Status == InvocationCommitted {
				return existing, nil
			}
			if existing.Name != name || existing.ArgumentsJSON != argumentsJSON {
				return InvocationRecord{}, fmt.Errorf("invocation %s conflicts with the persisted tool call", id)
			}
			return existing, nil
		}
	}
	previous := cloneSession(m.current)
	now := time.Now()
	m.ensureCurrentLocked(now)
	record := InvocationRecord{ID: id, Name: name, ArgumentsJSON: argumentsJSON, Status: InvocationPrepared, CreatedAt: now}
	m.current.Invocations = append(m.current.Invocations, record)
	m.current.Messages = append(m.current.Messages, Message{
		Role: RoleAssistant, CreatedAt: now, SchemaVersion: ConversationMessageSchemaVersion,
		ToolCalls: []ToolCall{{ID: id, Name: name, ArgumentsJSON: argumentsJSON}},
	})
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return InvocationRecord{}, errors.New("persist prepared invocation")
	}
	return record, nil
}

// MarkInvocationDispatched makes the handoff to the external executor
// durable. The operation is idempotent for an existing invocation ID.
func (m *Manager) MarkInvocationDispatched(id string) (InvocationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.current.Invocations {
		if m.current.Invocations[index].ID != strings.TrimSpace(id) {
			continue
		}
		if m.current.Invocations[index].Status == InvocationCommitted {
			return m.current.Invocations[index], nil
		}
		if m.current.Invocations[index].Status == InvocationUnknown {
			return InvocationRecord{}, fmt.Errorf("invocation %s is unknown; reconcile before dispatch", id)
		}
		previous := cloneSession(m.current)
		m.current.Invocations[index].Status = InvocationDispatched
		m.current.UpdatedAt = time.Now()
		if !m.commitLocked(context.Background(), previous) {
			return InvocationRecord{}, errors.New("persist dispatched invocation")
		}
		return m.current.Invocations[index], nil
	}
	return InvocationRecord{}, ErrNotFound
}

type InvocationResult struct {
	ID            string
	Name          string
	Output        string
	Error         string
	ExitCode      int
	Truncated     bool
	SpillLocator  string
	SpillSHA256   string
	SpillBytes    int64
	ModifiedFiles []string
}

// ReconcileInvocation records an externally verified result for an unknown
// invocation. It deliberately never executes a tool and only accepts records
// that are already marked unknown, keeping recovery explicit for operations
// whose side effect may have happened before the original acknowledgement was
// lost.
func (m *Manager) ReconcileInvocation(result InvocationResult) (InvocationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := strings.TrimSpace(result.ID)
	for index := range m.current.Invocations {
		if m.current.Invocations[index].ID != id {
			continue
		}
		existing := m.current.Invocations[index]
		if existing.Status != InvocationUnknown {
			return InvocationRecord{}, fmt.Errorf("invocation %s is %s; only unknown invocations can be reconciled", id, existing.Status)
		}
		previous := cloneSession(m.current)
		now := time.Now()
		existing.Status = InvocationCommitted
		existing.Name = firstNonEmpty(strings.TrimSpace(result.Name), existing.Name)
		existing.Output = result.Output
		existing.Error = result.Error
		existing.ExitCode = result.ExitCode
		existing.Truncated = result.Truncated
		existing.SpillLocator = result.SpillLocator
		existing.SpillSHA256 = result.SpillSHA256
		existing.SpillBytes = result.SpillBytes
		existing.CompletedAt = now
		existing.ModifiedFiles = mergeFiles(existing.ModifiedFiles, result.ModifiedFiles)
		m.current.Invocations[index] = existing
		m.current.Metrics.ToolCalls++
		m.current.Metrics.FilesModified = mergeFiles(m.current.Metrics.FilesModified, result.ModifiedFiles)
		m.current.Messages = append(m.current.Messages, Message{
			Role: RoleTool, Name: existing.Name, ToolCallID: id, Content: existing.Output,
			IsError: existing.Error != "", CreatedAt: now,
			SchemaVersion: ConversationMessageSchemaVersion,
		})
		m.current.UpdatedAt = now
		if !m.commitLocked(context.Background(), previous) {
			return InvocationRecord{}, errors.New("persist reconciled invocation")
		}
		return existing, nil
	}
	return InvocationRecord{}, ErrNotFound
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// CommitInvocation durably appends the paired tool result. Committed IDs are
// returned without appending a second result, which makes retry safe.
func (m *Manager) CommitInvocation(result InvocationResult) (InvocationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := strings.TrimSpace(result.ID)
	for index := range m.current.Invocations {
		if m.current.Invocations[index].ID != id {
			continue
		}
		existing := m.current.Invocations[index]
		if existing.Status == InvocationCommitted {
			return existing, nil
		}
		previous := cloneSession(m.current)
		now := time.Now()
		existing.Status = InvocationCommitted
		existing.Output, existing.Error, existing.ExitCode, existing.CompletedAt = result.Output, result.Error, result.ExitCode, now
		existing.Truncated = result.Truncated
		existing.SpillLocator = result.SpillLocator
		existing.SpillSHA256 = result.SpillSHA256
		existing.SpillBytes = result.SpillBytes
		existing.ModifiedFiles = mergeFiles(existing.ModifiedFiles, result.ModifiedFiles)
		m.current.Metrics.ToolCalls++
		m.current.Metrics.FilesModified = mergeFiles(m.current.Metrics.FilesModified, result.ModifiedFiles)
		m.current.Invocations[index] = existing
		m.current.Messages = append(m.current.Messages, Message{
			Role: RoleTool, Name: result.Name, ToolCallID: id, Content: result.Output, IsError: result.Error != "", CreatedAt: now,
			SchemaVersion: ConversationMessageSchemaVersion,
		})
		m.current.UpdatedAt = now
		if !m.commitLocked(context.Background(), previous) {
			return InvocationRecord{}, errors.New("persist committed invocation")
		}
		return existing, nil
	}
	return InvocationRecord{}, ErrNotFound
}

// MarkInvocationUnknown records that execution may have happened but its
// result could not be durably acknowledged. Recovery can reconcile this state
// without blindly repeating a non-idempotent operation.
func (m *Manager) MarkInvocationUnknown(id, reason string) (InvocationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.current.Invocations {
		if m.current.Invocations[index].ID != strings.TrimSpace(id) {
			continue
		}
		if m.current.Invocations[index].Status == InvocationCommitted {
			return InvocationRecord{}, fmt.Errorf("invocation %s is already committed", id)
		}
		if m.current.Invocations[index].Status == InvocationUnknown {
			return m.current.Invocations[index], nil
		}
		previous := cloneSession(m.current)
		m.current.Invocations[index].Status = InvocationUnknown
		m.current.Invocations[index].Error = strings.TrimSpace(reason)
		m.current.UpdatedAt = time.Now()
		if !m.commitLocked(context.Background(), previous) {
			return InvocationRecord{}, errors.New("persist unknown invocation")
		}
		return m.current.Invocations[index], nil
	}
	return InvocationRecord{}, ErrNotFound
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

// SetActor persists the authenticated identity bound to the current session.
func (m *Manager) SetActor(actor identity.Actor) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)
	now := time.Now()
	m.ensureCurrentLocked(now)
	m.current.Actor = actor
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
		Role: RoleTool, Content: formatToolResult(record), CreatedAt: now,
		SchemaVersion: ConversationMessageSchemaVersion,
		Name:          record.Name, ToolCallID: record.ToolCallID,
		IsError: record.ExitCode != 0 || strings.TrimSpace(record.Error) != "",
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
	m.current.PlanTodoRevision++
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
	m.current.PlanTodoRevision++
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous)
	}
	return cloneSession(m.current)
}

// ApplyPlanTodoState atomically replaces the plan and todo snapshots when the
// caller presents the next expected revision. A stale or failed write leaves
// the in-memory projection unchanged and returns ok=false.
func (m *Manager) ApplyPlanTodoState(plan PlanState, todos []TodoItem, revision uint64) (Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)
	now := time.Now()
	m.ensureCurrentLocked(now)
	if revision == 0 || revision != m.current.PlanTodoRevision+1 {
		return previous, false
	}
	m.current.Plan = clonePlan(plan)
	m.current.Todos = cloneTodos(todos)
	m.current.PlanTodoRevision = revision
	m.current.UpdatedAt = now
	if !m.commitLocked(context.Background(), previous) {
		return previous, false
	}
	return cloneSession(m.current), true
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
	record.ProtocolVersion = strings.TrimSpace(record.ProtocolVersion)
	record.RequestID = strings.TrimSpace(record.RequestID)
	record.ParentSessionID = strings.TrimSpace(record.ParentSessionID)
	record.ChildSessionID = strings.TrimSpace(record.ChildSessionID)
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

// AppendWorktreeLifecycle records a bounded, sanitized lifecycle transition
// without keeping terminal directories in the active Worktrees projection.
func (m *Manager) AppendWorktreeLifecycle(event WorktreeLifecycle) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneSession(m.current)
	now := time.Now()
	m.ensureCurrentLocked(now)
	event.RequestID = strings.TrimSpace(event.RequestID)
	event.ParentSessionID = strings.TrimSpace(event.ParentSessionID)
	event.ChildSessionID = strings.TrimSpace(event.ChildSessionID)
	event.LeaseID = strings.TrimSpace(event.LeaseID)
	event.Status = strings.TrimSpace(event.Status)
	event.Reason = strings.TrimSpace(event.Reason)
	if event.CreatedAt.IsZero() {
		event.CreatedAt = now
	}
	if event.RequestID == "" || event.Status == "" {
		return cloneSession(previous)
	}
	m.current.WorktreeEvents = append(m.current.WorktreeEvents, event)
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

// ReplaceMessages installs a caller-provided checkpoint summary and preserves
// the requested recent tail. It is used by the Python orchestrator when its
// token-aware compactor has already selected a safe surface boundary.
func (m *Manager) ReplaceMessages(summary string, keep int) (Session, int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	previous := cloneSession(m.current)
	if keep < 0 {
		keep = 0
	}
	summary = strings.TrimSpace(summary)
	total := len(m.current.Messages)
	if summary == "" || total <= keep {
		return cloneSession(m.current), 0, true
	}
	dropCount := total - keep
	kept := append([]Message(nil), m.current.Messages[dropCount:]...)
	now := time.Now()
	compacted := make([]Message, 0, len(kept)+1)
	compacted = append(compacted, Message{
		Role:      RoleSystem,
		Content:   "[Conversation summary]\n" + summary,
		CreatedAt: now,
	})
	compacted = append(compacted, kept...)
	m.ensureCurrentLocked(now)
	m.ensureMetadataLocked()
	m.current.Messages = compacted
	m.current.UpdatedAt = now
	m.current.Metadata["last_compacted_at"] = now.Format(time.RFC3339)
	m.current.Metadata["last_compacted_removed"] = fmt.Sprint(dropCount)
	m.current.Metadata["last_compacted_keep"] = fmt.Sprint(keep)
	if !m.commitLocked(context.Background(), previous) {
		return cloneSession(previous), 0, false
	}
	return cloneSession(m.current), dropCount, true
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
	// The active session may have been updated after the target session while
	// the wall clock resolution is coarse. A resumed session must still sort
	// ahead of that prior active session, otherwise /sessions and ResumeLatest
	// can immediately select the wrong conversation.
	if !previous.UpdatedAt.IsZero() && !now.After(previous.UpdatedAt) {
		now = previous.UpdatedAt.Add(time.Nanosecond)
	}
	m.current.UpdatedAt = now
	if err := m.store.Save(ctx, m.current); err != nil {
		m.current = previous
		return Session{}, err
	}
	return cloneSession(m.current), nil
}

// Fork copies the current session's durable event prefix into targetID and
// switches the manager to the independent child session.
func (m *Manager) Fork(ctx context.Context, targetID string, targetSeq int64) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current.ID == "" {
		return Session{}, ErrNotFound
	}
	history, ok := m.store.(HistoryStore)
	if !ok {
		return Session{}, ErrHistoryUnsupported
	}
	child, err := history.Fork(ctx, m.current.ID, strings.TrimSpace(targetID), targetSeq)
	if err != nil {
		return Session{}, err
	}
	if child == nil {
		return Session{}, ErrNotFound
	}
	m.current = cloneSession(*child)
	return cloneSession(m.current), nil
}

// Rewind appends a durable rewind marker and switches the manager to the
// state snapshot selected by the target event sequence.
func (m *Manager) Rewind(ctx context.Context, targetSeq int64) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current.ID == "" {
		return Session{}, ErrNotFound
	}
	history, ok := m.store.(HistoryStore)
	if !ok {
		return Session{}, ErrHistoryUnsupported
	}
	rewound, err := history.Rewind(ctx, m.current.ID, targetSeq)
	if err != nil {
		return Session{}, err
	}
	if rewound == nil {
		return Session{}, ErrNotFound
	}
	m.current = cloneSession(*rewound)
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
	out.Actor.Roles = append([]string(nil), session.Actor.Roles...)
	if len(session.Messages) > 0 {
		out.Messages = make([]Message, len(session.Messages))
		for index, message := range session.Messages {
			out.Messages[index] = message
			out.Messages[index].ToolCalls = append([]ToolCall(nil), message.ToolCalls...)
		}
	}
	if len(session.Invocations) > 0 {
		out.Invocations = append([]InvocationRecord(nil), session.Invocations...)
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
	out.WorktreeEvents = cloneWorktreeEvents(session.WorktreeEvents)
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

func cloneWorktreeEvents(in []WorktreeLifecycle) []WorktreeLifecycle {
	if len(in) == 0 {
		return nil
	}
	out := make([]WorktreeLifecycle, len(in))
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
	if strings.TrimSpace(record.SpillLocator) != "" {
		lines = append(lines,
			"spill_locator: "+strings.TrimSpace(record.SpillLocator),
			"spill_sha256: "+strings.TrimSpace(record.SpillSHA256),
			fmt.Sprintf("spill_bytes: %d", record.SpillBytes),
		)
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
	if strings.TrimSpace(record.ProtocolVersion) != "" {
		lines = append(lines, "protocol_version: "+strings.TrimSpace(record.ProtocolVersion))
	}
	if strings.TrimSpace(record.RequestID) != "" {
		lines = append(lines, "request_id: "+strings.TrimSpace(record.RequestID))
	}
	if strings.TrimSpace(record.ParentSessionID) != "" {
		lines = append(lines, "parent_session_id: "+strings.TrimSpace(record.ParentSessionID))
	}
	if strings.TrimSpace(record.ChildSessionID) != "" {
		lines = append(lines, "child_session_id: "+strings.TrimSpace(record.ChildSessionID))
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
