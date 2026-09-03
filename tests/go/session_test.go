package codeagent_test

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"code-agent/internal/session"
)

func TestSessionManagerTracksMessages(t *testing.T) {
	manager := session.NewManager(nil)
	manager.NewSession(t.TempDir())

	current := manager.Append(session.RoleUser, "hello")
	if current.ID == "" {
		t.Fatal("session id should be assigned")
	}

	messages := manager.Messages()
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Content != "hello" {
		t.Fatalf("unexpected message content: %+v", messages[0])
	}
}

func TestSQLiteStorePersistsSessions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")

	store := session.NewSQLiteStore(path)
	manager := session.NewManager(store)
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "nested")
	created := manager.NewSession(workspace)
	manager.SetWorkingDir(nested)
	manager.SetMode("plan")
	manager.Append(session.RoleUser, "persist me")
	manager.Append(session.RoleAssistant, "persisted")
	manager.AddLLMUsage(100, 50, 0.00075)
	manager.AppendToolResult(session.ToolResultRecord{
		Name:          "Write",
		Output:        "written",
		ModifiedFiles: []string{"notes/demo.txt", "notes/demo.txt", "internal/app.go"},
	})
	manager.SetTodos([]session.TodoItem{
		{Content: "Draft plan", ActiveForm: "drafting plan", Status: "in_progress"},
		{Content: "Run tests", ActiveForm: "running tests", Status: "pending"},
	})
	manager.SetPlan(session.PlanState{
		Steps:        []string{"Inspect repository", "Implement persistence", "Run tests"},
		CurrentIndex: 1,
		Mode:         "plan",
	})
	manager.AppendAgentSpawn(session.AgentSpawnRecord{
		Kind:        "general",
		Task:        "inspect repository",
		ContextJSON: `{"scope":"repo"}`,
		Parallel:    true,
	})
	manager.SetUndo([]session.UndoEntry{{
		ID:          "undo-1",
		Description: "Write",
		Changes: []session.UndoChange{{
			Path:   "notes/demo.txt",
			Before: "before",
			After:  "after",
		}},
	}})
	manager.SetApprovedTools([]string{"Write", "Write", "Git"})
	manager.SetWorktrees([]session.WorktreeState{
		{Name: "agent-a", Path: filepath.Join(workspace, ".worktrees", "agent-a"), BaseRef: "main", Active: true},
	})
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reloadedStore := session.NewSQLiteStore(path)
	reloaded := session.NewManager(reloadedStore)
	loaded, err := reloaded.Load(ctx, created.ID)
	if err != nil {
		t.Fatalf("load persisted session: %v", err)
	}
	defer reloadedStore.Close()
	if loaded.ID != created.ID {
		t.Fatalf("loaded wrong session id: %s", loaded.ID)
	}
	if loaded.Mode != "plan" {
		t.Fatalf("unexpected mode: %q", loaded.Mode)
	}
	if loaded.WorkingDir != nested {
		t.Fatalf("unexpected working dir: %q", loaded.WorkingDir)
	}
	if len(loaded.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "persist me" {
		t.Fatalf("unexpected first message: %+v", loaded.Messages[0])
	}
	if loaded.Messages[2].Role != session.RoleTool || loaded.Messages[2].Content == "" {
		t.Fatalf("expected persisted tool message, got %+v", loaded.Messages[2])
	}
	if loaded.Metrics.TotalTokensIn != 100 || loaded.Metrics.TotalTokensOut != 50 {
		t.Fatalf("unexpected token metrics: %+v", loaded.Metrics)
	}
	if loaded.Metrics.TotalCost != 0.00075 {
		t.Fatalf("unexpected cost metric: %+v", loaded.Metrics)
	}
	if loaded.Metrics.ToolCalls != 1 {
		t.Fatalf("unexpected tool call count: %+v", loaded.Metrics)
	}
	if got := loaded.Metrics.FilesModified; len(got) != 2 || got[0] != "notes/demo.txt" || got[1] != "internal/app.go" {
		t.Fatalf("unexpected modified files: %#v", got)
	}
	if got := loaded.Todos; len(got) != 2 || got[0].Content != "Draft plan" || got[0].Status != "in_progress" {
		t.Fatalf("unexpected persisted todos: %#v", got)
	}
	if got := loaded.Plan; len(got.Steps) != 3 || got.Steps[1] != "Implement persistence" || got.CurrentIndex != 1 || got.Mode != "plan" {
		t.Fatalf("unexpected persisted plan: %#v", got)
	}
	if got := loaded.Agents; len(got) != 1 || got[0].Kind != "general" || got[0].Task != "inspect repository" || !got[0].Parallel {
		t.Fatalf("unexpected persisted agents: %#v", got)
	}
	if got := loaded.Undo; len(got) != 1 || got[0].Description != "Write" || got[0].Changes[0].Before != "before" {
		t.Fatalf("unexpected persisted undo stack: %#v", got)
	}
	if got := loaded.ApprovedTools; len(got) != 2 || got[0] != "Write" || got[1] != "Git" {
		t.Fatalf("unexpected persisted approved tools: %#v", got)
	}
	if got := loaded.Worktrees; len(got) != 1 || got[0].Name != "agent-a" || !got[0].Active {
		t.Fatalf("unexpected persisted worktrees: %#v", got)
	}
}

func TestManagerResumeLatest(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	manager := session.NewManager(store)

	first := manager.NewSession("first")
	manager.SetMode("plan")
	manager.AppendAgentSpawn(session.AgentSpawnRecord{Kind: "general", Task: "first agent"})
	manager.Append(session.RoleUser, "first message")
	second := manager.NewSession("second")
	manager.Append(session.RoleUser, "second message")

	if second.ID == first.ID {
		t.Fatal("sessions should have unique ids")
	}

	resumed, ok, err := manager.ResumeLatest(ctx)
	if err != nil {
		t.Fatalf("resume latest: %v", err)
	}
	if !ok {
		t.Fatal("expected previous session")
	}
	if resumed.ID != first.ID {
		t.Fatalf("expected to resume first session, got %s", resumed.ID)
	}
}

func TestManagerResumeByIDAndListRecent(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	manager := session.NewManager(store)

	first := manager.NewSession("first")
	manager.SetMode("plan")
	manager.AppendAgentSpawn(session.AgentSpawnRecord{Kind: "general", Task: "first agent"})
	manager.Append(session.RoleUser, "first message")
	second := manager.NewSession("second")
	manager.Append(session.RoleUser, "second message")

	resumed, err := manager.Resume(ctx, first.ID)
	if err != nil {
		t.Fatalf("resume by id: %v", err)
	}
	if resumed.ID != first.ID || resumed.WorkingDir != "first" {
		t.Fatalf("unexpected resumed session: %+v", resumed)
	}

	recent, err := manager.ListRecent(ctx, 2)
	if err != nil {
		t.Fatalf("list recent: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent sessions, got %d", len(recent))
	}
	if recent[0].ID != first.ID {
		t.Fatalf("expected resumed session to be most recent, got %+v", recent)
	}
	if recent[1].ID != second.ID {
		t.Fatalf("expected second session in recent list, got %+v", recent)
	}
	if recent[0].MessageCount != 2 || recent[0].LastMessage != "user: first message" {
		t.Fatalf("unexpected summary: %+v", recent[0])
	}
	if recent[0].Mode != "plan" || recent[0].AgentCount != 1 {
		t.Fatalf("unexpected session summary state: %+v", recent[0])
	}
}

func TestManagerResumeMakesResumedSessionMostRecentWhenClockLagsCurrent(t *testing.T) {
	ctx := context.Background()
	base := time.Now().UTC()
	first := session.Session{ID: "first", WorkingDir: "first", CreatedAt: base, UpdatedAt: base}
	second := session.Session{ID: "second", WorkingDir: "second", CreatedAt: base, UpdatedAt: base.Add(time.Hour)}
	store := &fixedSessionStore{sessions: map[string]session.Session{
		first.ID:  first,
		second.ID: second,
	}}
	manager := session.NewManager(store)
	if _, err := manager.Load(ctx, second.ID); err != nil {
		t.Fatalf("load current session: %v", err)
	}
	if _, err := manager.Resume(ctx, first.ID); err != nil {
		t.Fatalf("resume older session: %v", err)
	}
	recent, err := manager.ListRecent(ctx, 2)
	if err != nil {
		t.Fatalf("list recent: %v", err)
	}
	if len(recent) != 2 || recent[0].ID != first.ID {
		t.Fatalf("resumed session ordering = %+v, want first before second", recent)
	}
}

type fixedSessionStore struct {
	sessions map[string]session.Session
}

func (s *fixedSessionStore) Save(_ context.Context, current session.Session) error {
	s.sessions[current.ID] = current
	return nil
}

func (s *fixedSessionStore) Load(_ context.Context, id string) (*session.Session, error) {
	current, ok := s.sessions[id]
	if !ok {
		return nil, session.ErrNotFound
	}
	return &current, nil
}

func (s *fixedSessionStore) List(_ context.Context) ([]session.Session, error) {
	out := make([]session.Session, 0, len(s.sessions))
	for _, current := range s.sessions {
		out = append(out, current)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func TestSessionModeNormalizesAndPersistsInMetadata(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	current := manager.NewSession("workspace")
	if current.Mode != "" {
		t.Fatalf("new session should not force mode before explicit set: %q", current.Mode)
	}

	current = manager.SetMode("plan")
	if current.Mode != "plan" || current.Metadata["mode"] != "plan" {
		t.Fatalf("unexpected plan mode state: %+v", current)
	}
	current = manager.SetMode("invalid")
	if current.Mode != "chat" || current.Metadata["mode"] != "chat" {
		t.Fatalf("unexpected chat mode state: %+v", current)
	}
}

func TestSessionMetricsAreCloned(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	manager.NewSession("workspace")
	current := manager.RecordToolCall("a.txt")
	current.Metrics.FilesModified[0] = "mutated.txt"

	again := manager.Current()
	if got := again.Metrics.FilesModified[0]; got != "a.txt" {
		t.Fatalf("session metrics leaked mutable slice, got %q", got)
	}
}

func TestSessionTodosAreCloned(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	manager.NewSession("workspace")
	current := manager.SetTodos([]session.TodoItem{
		{Content: "first", ActiveForm: "doing first", Status: "in_progress"},
	})
	current.Todos[0].Content = "mutated"

	again := manager.Current()
	if got := again.Todos[0].Content; got != "first" {
		t.Fatalf("session todos leaked mutable slice, got %q", got)
	}
}

func TestSessionPlanIsCloned(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	manager.NewSession("workspace")
	current := manager.SetPlan(session.PlanState{
		Steps:        []string{"first"},
		CurrentIndex: 0,
		Mode:         "plan",
	})
	current.Plan.Steps[0] = "mutated"

	again := manager.Current()
	if got := again.Plan.Steps[0]; got != "first" {
		t.Fatalf("session plan leaked mutable slice, got %q", got)
	}
}

func TestSessionAgentsAreCloned(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	manager.NewSession("workspace")
	current := manager.AppendAgentSpawn(session.AgentSpawnRecord{
		Kind: "general",
		Task: "inspect",
	})
	current.Agents[0].Task = "mutated"

	again := manager.Current()
	if got := again.Agents[0].Task; got != "inspect" {
		t.Fatalf("session agents leaked mutable slice, got %q", got)
	}
}

func TestSessionUndoIsCloned(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	manager.NewSession("workspace")
	current := manager.SetUndo([]session.UndoEntry{{
		ID:          "undo-1",
		Description: "Write",
		Changes: []session.UndoChange{{
			Path:   "a.txt",
			Before: "before",
			After:  "after",
		}},
	}})
	current.Undo[0].Changes[0].Before = "mutated"

	again := manager.Current()
	if got := again.Undo[0].Changes[0].Before; got != "before" {
		t.Fatalf("session undo leaked mutable slice, got %q", got)
	}
}

func TestSessionApprovedToolsAreClonedAndDeduped(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	manager.NewSession("workspace")
	current := manager.SetApprovedTools([]string{"Write", "Write", "", "Git"})
	current.ApprovedTools[0] = "mutated"

	again := manager.Current()
	if got := again.ApprovedTools; len(got) != 2 || got[0] != "Write" || got[1] != "Git" {
		t.Fatalf("unexpected approved tools: %#v", got)
	}
}

func TestSessionWorktreesAreCloned(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	manager.NewSession("workspace")
	current := manager.SetWorktrees([]session.WorktreeState{{Name: "agent-a", Path: "path-a", Active: true}})
	current.Worktrees[0].Name = "mutated"

	again := manager.Current()
	if got := again.Worktrees[0].Name; got != "agent-a" {
		t.Fatalf("session worktrees leaked mutable slice, got %q", got)
	}
}

func TestAppendToolResultStoresBoundedToolHistory(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	manager.NewSession("workspace")
	longOutput := strings.Repeat("x", 2500)

	current := manager.AppendToolResult(session.ToolResultRecord{
		Name:          "Edit",
		ExitCode:      0,
		Output:        longOutput,
		ModifiedFiles: []string{"a.txt", "a.txt", "b.txt"},
	})

	if current.Metrics.ToolCalls != 1 {
		t.Fatalf("expected 1 tool call, got %d", current.Metrics.ToolCalls)
	}
	if got := current.Metrics.FilesModified; len(got) != 2 || got[0] != "a.txt" || got[1] != "b.txt" {
		t.Fatalf("unexpected modified files: %#v", got)
	}
	if len(current.Messages) != 1 || current.Messages[0].Role != session.RoleTool {
		t.Fatalf("expected one tool message, got %#v", current.Messages)
	}
	if !strings.Contains(current.Messages[0].Content, "tool: Edit") {
		t.Fatalf("tool message missing name: %q", current.Messages[0].Content)
	}
	if !strings.Contains(current.Messages[0].Content, "[stored tool output truncated]") {
		t.Fatalf("tool message was not truncated: %q", current.Messages[0].Content)
	}
}

func TestAppendToolResultStoresSpillAuditMetadata(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	manager.NewSession(t.TempDir())
	manager.AppendToolResult(session.ToolResultRecord{
		Name: "Read", Output: "preview", Truncated: true,
		SpillLocator: "spill://" + strings.Repeat("a", 64),
		SpillSHA256:  strings.Repeat("a", 64), SpillBytes: 123,
	})
	message := manager.Current().Messages[len(manager.Current().Messages)-1].Content
	for _, want := range []string{"spill_locator: spill://", "spill_sha256: " + strings.Repeat("a", 64), "spill_bytes: 123"} {
		if !strings.Contains(message, want) {
			t.Fatalf("tool audit missing %q: %q", want, message)
		}
	}
}

func TestManagerAutoSaveRequiresCurrentSession(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())

	if err := manager.AutoSave(context.Background()); err != session.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestManagerCompactSummarizesMessages(t *testing.T) {
	manager := session.NewManager(nil)
	manager.NewSession("workspace")
	manager.Append(session.RoleUser, "first message")
	manager.Append(session.RoleAssistant, "second message")
	manager.Append(session.RoleUser, "third message")
	manager.Append(session.RoleAssistant, "fourth message")

	compacted, removed, summary := manager.Compact(2)
	if removed != 2 {
		t.Fatalf("expected to remove 2 messages, got %d", removed)
	}
	if summary == "" {
		t.Fatal("expected a compaction summary")
	}

	messages := compacted.Messages
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages after compaction, got %d", len(messages))
	}
	if messages[0].Role != session.RoleSystem {
		t.Fatalf("expected summary message to be system role, got %s", messages[0].Role)
	}
	if messages[1].Content != "third message" || messages[2].Content != "fourth message" {
		t.Fatalf("unexpected retained messages: %#v", messages)
	}
}

func TestSessionReplaceMessagesUsesProvidedSummaryAndTail(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	manager.NewSession("workspace")
	manager.Append(session.RoleUser, "old request")
	manager.Append(session.RoleAssistant, "old answer")
	manager.Append(session.RoleUser, "recent request")
	manager.Append(session.RoleAssistant, "recent answer")

	replaced, removed, ok := manager.ReplaceMessages("checkpoint summary", 2)

	if !ok {
		t.Fatal("expected replacement to commit")
	}
	if removed != 2 {
		t.Fatalf("removed messages = %d, want 2", removed)
	}
	if len(replaced.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(replaced.Messages))
	}
	if replaced.Messages[0].Role != session.RoleSystem || replaced.Messages[0].Content != "[Conversation summary]\ncheckpoint summary" {
		t.Fatalf("unexpected summary message: %+v", replaced.Messages[0])
	}
	if replaced.Messages[2].Content != "recent answer" {
		t.Fatalf("tail was not preserved: %+v", replaced.Messages)
	}
}
