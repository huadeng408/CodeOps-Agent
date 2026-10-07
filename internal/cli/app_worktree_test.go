package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/session"
	"code-agent/internal/worktree"
)

type failOnceSessionStore struct {
	*session.MemoryStore
	mu       sync.Mutex
	failNext bool
}

func (s *failOnceSessionStore) Save(ctx context.Context, value session.Session) error {
	s.mu.Lock()
	if s.failNext {
		s.failNext = false
		s.mu.Unlock()
		return errors.New("fixture session persistence failure")
	}
	s.mu.Unlock()
	return s.MemoryStore.Save(ctx, value)
}

func (s *failOnceSessionStore) fail() {
	s.mu.Lock()
	s.failNext = true
	s.mu.Unlock()
}

func TestAppAgentWorktreeCallbacksPersistAndCleanLifecycle(t *testing.T) {
	repo := t.TempDir()
	seedAppWorktreeRepo(t, repo)
	store := session.NewManager(session.NewMemoryStore())
	parent := store.NewSession(repo)
	manager := worktree.NewManager(repo, "HEAD")
	app := &App{
		session:  store,
		worktree: manager,
		renderer: NewStreamRenderer(io.Discard),
	}
	spawn := &codeagentpb.AgentSpawn{
		RequestId:       "request-app",
		ParentSessionId: parent.ID,
		ChildSessionId:  "child-app",
		WorktreeName:    "agent-app",
		Isolation:       "worktree",
	}
	if err := app.handleAgentSpawn(context.Background(), spawn); err != nil {
		t.Fatalf("handle agent spawn: %v", err)
	}
	active := store.Current()
	if len(active.Worktrees) != 1 || len(active.WorktreeEvents) != 1 || active.WorktreeEvents[0].Status != worktree.AgentWorktreeActive {
		t.Fatalf("spawn lease was not persisted: %#v", active)
	}
	if err := app.handleAgentLifecycle(context.Background(), &codeagentpb.AgentLifecycle{
		RequestId:      "request-app",
		ChildSessionId: "child-app",
		Status:         "ok",
		Reason:         "done",
	}); err != nil {
		t.Fatalf("handle agent lifecycle: %v", err)
	}
	terminal := store.Current()
	if len(manager.List()) != 0 || len(terminal.Worktrees) != 0 || len(terminal.WorktreeEvents) != 2 || terminal.WorktreeEvents[1].Status != worktree.AgentWorktreeReleased {
		t.Fatalf("terminal cleanup was not persisted: trees=%#v events=%#v", manager.List(), terminal.WorktreeEvents)
	}
}

func TestAppAgentWorktreeCallbackRejectsWrongParentAndIsolation(t *testing.T) {
	repo := t.TempDir()
	seedAppWorktreeRepo(t, repo)
	store := session.NewManager(session.NewMemoryStore())
	parent := store.NewSession(repo)
	app := &App{
		session:  store,
		worktree: worktree.NewManager(repo, "HEAD"),
		renderer: NewStreamRenderer(io.Discard),
	}
	for _, spawn := range []*codeagentpb.AgentSpawn{
		{RequestId: "request-isolation", ParentSessionId: parent.ID, ChildSessionId: "child", WorktreeName: "agent", Isolation: "parent"},
		{RequestId: "request-parent", ParentSessionId: "other", ChildSessionId: "child", WorktreeName: "agent", Isolation: "worktree"},
	} {
		if err := app.handleAgentSpawn(context.Background(), spawn); err == nil {
			t.Fatalf("expected spawn rejection: %#v", spawn)
		}
	}
	if len(app.worktree.List()) != 0 {
		t.Fatalf("rejected spawns must not create worktrees: %#v", app.worktree.List())
	}
}

func TestAppAgentWorktreeLifecycleRetriesAtomicPersistence(t *testing.T) {
	repo := t.TempDir()
	seedAppWorktreeRepo(t, repo)
	backend := &failOnceSessionStore{MemoryStore: session.NewMemoryStore()}
	store := session.NewManager(backend)
	parent := store.NewSession(repo)
	app := &App{session: store, worktree: worktree.NewManager(repo, "HEAD"), renderer: NewStreamRenderer(io.Discard)}
	spawn := &codeagentpb.AgentSpawn{RequestId: "request-retry", ParentSessionId: parent.ID, ChildSessionId: "child-retry", WorktreeName: "agent-retry", Isolation: "worktree"}
	if err := app.handleAgentSpawn(context.Background(), spawn); err != nil {
		t.Fatal(err)
	}
	backend.fail()
	lifecycle := &codeagentpb.AgentLifecycle{RequestId: spawn.RequestId, ChildSessionId: spawn.ChildSessionId, Status: "cancelled", Reason: "cancel"}
	if err := app.handleAgentLifecycle(context.Background(), lifecycle); err == nil {
		t.Fatal("lifecycle persistence failure was hidden")
	}
	failed := store.Current()
	if len(app.worktree.List()) != 0 || !containsAgentWorktreeRequest(failed.Worktrees, spawn.RequestId) || len(failed.WorktreeEvents) != 1 {
		t.Fatalf("failed transition was not recoverable: trees=%#v session=%#v", app.worktree.List(), failed)
	}
	if err := app.handleAgentLifecycle(context.Background(), lifecycle); err != nil {
		t.Fatalf("retry lifecycle: %v", err)
	}
	final := store.Current()
	if containsAgentWorktreeRequest(final.Worktrees, spawn.RequestId) || len(final.WorktreeEvents) != 2 || final.WorktreeEvents[1].Status != "cancelled" {
		t.Fatalf("retry did not atomically persist terminal state: %#v", final)
	}
}

func TestAppAgentWorktreeLifecycleRemovesOnlyTargetRequest(t *testing.T) {
	repo := t.TempDir()
	seedAppWorktreeRepo(t, repo)
	store := session.NewManager(session.NewMemoryStore())
	parent := store.NewSession(repo)
	app := &App{session: store, worktree: worktree.NewManager(repo, "HEAD"), renderer: NewStreamRenderer(io.Discard)}
	for _, spawn := range []*codeagentpb.AgentSpawn{
		{RequestId: "request-one", ParentSessionId: parent.ID, ChildSessionId: "child-one", WorktreeName: "agent-one", Isolation: "worktree"},
		{RequestId: "request-two", ParentSessionId: parent.ID, ChildSessionId: "child-two", WorktreeName: "agent-two", Isolation: "worktree"},
	} {
		if err := app.handleAgentSpawn(context.Background(), spawn); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.handleAgentLifecycle(context.Background(), &codeagentpb.AgentLifecycle{RequestId: "request-one", ChildSessionId: "child-one", Status: "cancelled"}); err != nil {
		t.Fatal(err)
	}
	current := store.Current()
	if containsAgentWorktreeRequest(current.Worktrees, "request-one") || !containsAgentWorktreeRequest(current.Worktrees, "request-two") {
		t.Fatalf("cleanup changed the wrong worktree projection: %#v", current.Worktrees)
	}
	if err := app.handleAgentLifecycle(context.Background(), &codeagentpb.AgentLifecycle{RequestId: "request-two", ChildSessionId: "child-two", Status: "cancelled"}); err != nil {
		t.Fatal(err)
	}
}

func TestAppAgentWorktreeLifecyclePersistsToOriginalParent(t *testing.T) {
	repo := t.TempDir()
	seedAppWorktreeRepo(t, repo)
	store := session.NewManager(session.NewMemoryStore())
	parent := store.NewSession(repo)
	app := &App{session: store, worktree: worktree.NewManager(repo, "HEAD"), renderer: NewStreamRenderer(io.Discard)}
	spawn := &codeagentpb.AgentSpawn{RequestId: "request-parent", ParentSessionId: parent.ID, ChildSessionId: "child-parent", WorktreeName: "agent-parent", Isolation: "worktree"}
	if err := app.handleAgentSpawn(context.Background(), spawn); err != nil {
		t.Fatal(err)
	}
	other := store.NewSession(repo)
	if err := app.handleAgentLifecycle(context.Background(), &codeagentpb.AgentLifecycle{RequestId: spawn.RequestId, ChildSessionId: spawn.ChildSessionId, Status: "cancelled"}); err != nil {
		t.Fatal(err)
	}
	if current := store.Current(); current.ID != other.ID || len(current.Worktrees) != 0 || len(current.WorktreeEvents) != 0 {
		t.Fatalf("lifecycle contaminated current session: %#v", current)
	}
	original, err := store.Load(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(original.Worktrees) != 0 || len(original.WorktreeEvents) != 2 || original.WorktreeEvents[1].Status != "cancelled" {
		t.Fatalf("original parent lifecycle missing: %#v", original)
	}
}

func TestAppAgentWorktreeLifecycleRejectsUnknownOrMismatchedIdentity(t *testing.T) {
	repo := t.TempDir()
	seedAppWorktreeRepo(t, repo)
	store := session.NewManager(session.NewMemoryStore())
	parent := store.NewSession(repo)
	app := &App{session: store, worktree: worktree.NewManager(repo, "HEAD"), renderer: NewStreamRenderer(io.Discard)}
	spawn := &codeagentpb.AgentSpawn{RequestId: "request-identity", ParentSessionId: parent.ID, ChildSessionId: "child-identity", WorktreeName: "agent-identity", Isolation: "worktree"}
	if err := app.handleAgentSpawn(context.Background(), spawn); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		msg  *codeagentpb.AgentLifecycle
	}{
		{name: "unknown request", msg: &codeagentpb.AgentLifecycle{RequestId: "missing", ChildSessionId: "child-identity", Status: "cancelled"}},
		{name: "wrong child", msg: &codeagentpb.AgentLifecycle{RequestId: spawn.RequestId, ChildSessionId: "other-child", Status: "cancelled"}},
		{name: "wrong lease", msg: &codeagentpb.AgentLifecycle{RequestId: spawn.RequestId, ChildSessionId: spawn.ChildSessionId, LeaseId: "wrong-lease", Status: "cancelled"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := app.handleAgentLifecycle(context.Background(), tc.msg); err == nil {
				t.Fatal("mismatched lifecycle unexpectedly succeeded")
			}
		})
	}
	if got := len(app.worktree.List()); got != 1 {
		t.Fatalf("rejected lifecycle changed managed worktrees: %d", got)
	}
}

func seedAppWorktreeRepo(t *testing.T, repo string) {
	t.Helper()
	runAppGit(t, repo, "init")
	runAppGit(t, repo, "config", "user.email", "test@example.com")
	runAppGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("parent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runAppGit(t, repo, "add", "tracked.txt")
	runAppGit(t, repo, "commit", "-m", "init")
}

func runAppGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repo
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
