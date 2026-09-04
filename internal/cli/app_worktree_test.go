package cli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/session"
	"code-agent/internal/worktree"
)

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
		Status:         "completed",
		Reason:         "done",
	}); err != nil {
		t.Fatalf("handle agent lifecycle: %v", err)
	}
	terminal := store.Current()
	if len(manager.List()) != 0 || len(terminal.Worktrees) != 0 || len(terminal.WorktreeEvents) != 2 || terminal.WorktreeEvents[1].Status != "completed" {
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
