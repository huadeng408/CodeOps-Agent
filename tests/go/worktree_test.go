package codeagent_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-agent/internal/worktree"
)

func TestWorktreeDiffLinesIncludesTrackedAndUntrackedChanges(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")

	tracked := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed tracked file: %v", err)
	}
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "init")

	if err := os.WriteFile(tracked, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("modify tracked file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("create untracked file: %v", err)
	}

	manager := worktree.NewManager(repo, "main")
	lines, err := manager.DiffLines(context.Background())
	if err != nil {
		t.Fatalf("diff lines: %v", err)
	}

	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"unstaged changes:",
		"tracked.txt",
		"status:",
		"new.txt",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diff output missing %q: %#v", want, lines)
		}
	}
}

func TestWorktreeDiffLinesCleanRepo(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")

	file := filepath.Join(repo, "clean.txt")
	if err := os.WriteFile(file, []byte("clean\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	runGit(t, repo, "add", "clean.txt")
	runGit(t, repo, "commit", "-m", "init")

	manager := worktree.NewManager(repo, "main")
	lines, err := manager.DiffLines(context.Background())
	if err != nil {
		t.Fatalf("diff lines: %v", err)
	}
	if len(lines) != 1 || lines[0] != "working tree clean" {
		t.Fatalf("unexpected clean output: %#v", lines)
	}
}

func TestWorktreeManagerRestoreNormalizesState(t *testing.T) {
	root := t.TempDir()
	manager := worktree.NewManager(root, "main")
	manager.Restore([]worktree.Worktree{
		{Name: "beta", Active: true},
		{Name: "alpha", BaseRef: "feature", Active: true},
		{Name: ""},
	})

	trees := manager.List()
	if len(trees) != 2 {
		t.Fatalf("expected 2 worktrees, got %#v", trees)
	}
	if trees[0].Name != "alpha" || trees[1].Name != "beta" {
		t.Fatalf("expected sorted worktrees, got %#v", trees)
	}
	if trees[0].Path != filepath.Join(root, ".agent", "worktrees", "alpha") {
		t.Fatalf("unexpected restored path: %s", trees[0].Path)
	}
	active := 0
	for _, tree := range trees {
		if tree.Active {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("expected one active worktree, got %#v", trees)
	}
}

func TestWorktreeManagerCreateSwitchCleanupState(t *testing.T) {
	root := t.TempDir()
	manager := worktree.NewManager(root, "main")

	alpha, err := manager.Create(" alpha ")
	if err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	if alpha.Name != "alpha" || !alpha.Active {
		t.Fatalf("unexpected alpha: %#v", alpha)
	}
	if _, err := manager.Create("alpha"); err == nil {
		t.Fatalf("expected duplicate create to fail")
	}
	if _, err := manager.Create("beta"); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	if _, err := manager.Switch(" beta "); err != nil {
		t.Fatalf("switch beta: %v", err)
	}
	if err := manager.Cleanup("beta"); err != nil {
		t.Fatalf("cleanup beta: %v", err)
	}

	trees := manager.List()
	if len(trees) != 1 {
		t.Fatalf("expected one remaining tree, got %#v", trees)
	}
	if trees[0].Name != "alpha" || !trees[0].Active {
		t.Fatalf("expected alpha to become active, got %#v", trees)
	}
}

func TestWorktreeManagerCreatesAndRemovesGitWorktree(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	file := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(file, []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed tracked file: %v", err)
	}
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "init")

	manager := worktree.NewManager(repo, "HEAD")
	tree, err := manager.CreateContext(context.Background(), "demo")
	if err != nil {
		t.Fatalf("create git worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree.Path, "tracked.txt")); err != nil {
		t.Fatalf("expected git worktree checkout: %v", err)
	}
	worktrees := runGit(t, repo, "worktree", "list")
	if !strings.Contains(worktrees, filepath.ToSlash(filepath.Clean(tree.Path))) || !strings.Contains(worktrees, "agent/demo") {
		t.Fatalf("git worktree list missing demo branch/path: %s", worktrees)
	}

	if err := manager.CleanupContext(context.Background(), "demo"); err != nil {
		t.Fatalf("cleanup git worktree: %v", err)
	}
	if _, err := os.Stat(tree.Path); !os.IsNotExist(err) {
		t.Fatalf("expected worktree directory removed, stat err: %v", err)
	}
	if branches := runGit(t, repo, "branch", "--list", "agent/demo"); strings.TrimSpace(branches) != "" {
		t.Fatalf("expected agent branch removed, got: %s", branches)
	}
}

func TestWorktreeManagerFallsBackToHeadWhenBaseRefMissing(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed tracked file: %v", err)
	}
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "init")

	manager := worktree.NewManager(repo, "missing-base")
	tree, err := manager.CreateContext(context.Background(), "fallback")
	if err != nil {
		t.Fatalf("create git worktree with missing base: %v", err)
	}
	if tree.BaseRef != "HEAD" {
		t.Fatalf("expected missing base ref to fall back to HEAD, got %q", tree.BaseRef)
	}
	if err := manager.CleanupContext(context.Background(), "fallback"); err != nil {
		t.Fatalf("cleanup fallback worktree: %v", err)
	}
}

func TestWorktreeManagerRejectsUnsafeNames(t *testing.T) {
	manager := worktree.NewManager(t.TempDir(), "main")
	for _, name := range []string{"../bad", `bad\name`, "bad:name", "bad name", ".."} {
		if _, err := manager.Create(name); err == nil {
			t.Fatalf("expected unsafe name %q to fail", name)
		}
	}
}

func TestWorktreeManagerRefusesDirtyCleanupWithoutDiscard(t *testing.T) {
	repo := t.TempDir()
	seedGitRepo(t, repo)

	manager := worktree.NewManager(repo, "HEAD")
	tree, err := manager.CreateContext(context.Background(), "dirty")
	if err != nil {
		t.Fatalf("create git worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tree.Path, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = manager.CleanupContext(context.Background(), "dirty")
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("expected dirty cleanup refusal, got %v", err)
	}
	if _, statErr := os.Stat(tree.Path); statErr != nil {
		t.Fatalf("dirty worktree should remain, stat err: %v", statErr)
	}

	if err := manager.CleanupContextDiscard(context.Background(), "dirty"); err != nil {
		t.Fatalf("discard cleanup dirty worktree: %v", err)
	}
	if _, statErr := os.Stat(tree.Path); !os.IsNotExist(statErr) {
		t.Fatalf("expected discard cleanup to remove worktree, stat err: %v", statErr)
	}
}

func TestWorktreeManagerRefusesUnmergedCommitCleanupWithoutDiscard(t *testing.T) {
	repo := t.TempDir()
	seedGitRepo(t, repo)

	manager := worktree.NewManager(repo, "HEAD")
	tree, err := manager.CreateContext(context.Background(), "commit")
	if err != nil {
		t.Fatalf("create git worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tree.Path, "commit.txt"), []byte("commit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, tree.Path, "add", "commit.txt")
	runGit(t, tree.Path, "commit", "-m", "worktree commit")

	err = manager.CleanupContext(context.Background(), "commit")
	if err == nil || !strings.Contains(err.Error(), "commits not merged") {
		t.Fatalf("expected unmerged commit cleanup refusal, got %v", err)
	}

	if err := manager.CleanupContextDiscard(context.Background(), "commit"); err != nil {
		t.Fatalf("discard cleanup unmerged worktree: %v", err)
	}
	if branches := runGit(t, repo, "branch", "--list", "agent/commit"); strings.TrimSpace(branches) != "" {
		t.Fatalf("expected discarded branch removed, got: %s", branches)
	}
}

func TestWorktreeManagerSpawnsIsolatedAgentWithIdempotentLease(t *testing.T) {
	repo := t.TempDir()
	seedGitRepo(t, repo)

	manager := worktree.NewManager(repo, "HEAD")
	request := worktree.AgentSpawnRequest{
		RequestID:       "request-1",
		ParentSessionID: "parent-session",
		ChildSessionID:  "child-session",
		WorktreeName:    "child-request-1",
	}
	first, err := manager.SpawnAgent(context.Background(), request)
	if err != nil {
		t.Fatalf("spawn agent worktree: %v", err)
	}
	if first.Status != worktree.AgentWorktreeActive || first.LeaseID == "" || first.LeaseExpiresAt.IsZero() {
		t.Fatalf("expected active lease metadata, got %#v", first)
	}
	if first.Path == repo || !strings.HasPrefix(filepath.Clean(first.Path), filepath.Join(repo, ".agent", "worktrees")) {
		t.Fatalf("agent worktree escaped controlled root: %#v", first)
	}
	if _, err := os.Stat(filepath.Join(first.Path, "tracked.txt")); err != nil {
		t.Fatalf("expected isolated checkout: %v", err)
	}

	second, err := manager.SpawnAgent(context.Background(), request)
	if err != nil {
		t.Fatalf("idempotent spawn: %v", err)
	}
	if second.LeaseID != first.LeaseID || second.Path != first.Path {
		t.Fatalf("duplicate request created a new lease: first=%#v second=%#v", first, second)
	}
	if trees := manager.List(); len(trees) != 1 {
		t.Fatalf("expected one worktree after duplicate spawn, got %#v", trees)
	}

	if err := manager.CleanupAgent(context.Background(), request.RequestID, true, "completed"); err != nil {
		t.Fatalf("cleanup agent worktree: %v", err)
	}
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatalf("expected isolated worktree removed, stat err: %v", err)
	}
	if err := manager.CleanupAgent(context.Background(), request.RequestID, true, "duplicate-cleanup"); err != nil {
		t.Fatalf("cleanup must be idempotent: %v", err)
	}
}

func TestWorktreeManagerSpawnFailsClosedForNonGitAndUnsafeInputs(t *testing.T) {
	manager := worktree.NewManager(t.TempDir(), "HEAD")
	_, err := manager.SpawnAgent(context.Background(), worktree.AgentSpawnRequest{
		RequestID:       "request-1",
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		WorktreeName:    "child",
	})
	if err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("expected non-git root to fail closed, got %v", err)
	}

	repo := t.TempDir()
	seedGitRepo(t, repo)
	manager = worktree.NewManager(repo, "HEAD")
	for _, request := range []worktree.AgentSpawnRequest{
		{RequestID: "", ParentSessionID: "parent", ChildSessionID: "child", WorktreeName: "child"},
		{RequestID: "request", ParentSessionID: "", ChildSessionID: "child", WorktreeName: "child"},
		{RequestID: "request", ParentSessionID: "parent", ChildSessionID: "child", WorktreeName: "../escape"},
	} {
		if _, err := manager.SpawnAgent(context.Background(), request); err == nil {
			t.Fatalf("expected unsafe spawn request to fail: %#v", request)
		}
	}
}

func TestWorktreeManagerReapsExpiredAgentLeaseWithDiscard(t *testing.T) {
	repo := t.TempDir()
	seedGitRepo(t, repo)
	manager := worktree.NewManager(repo, "HEAD")
	spawned, err := manager.SpawnAgent(context.Background(), worktree.AgentSpawnRequest{
		RequestID:       "request-expired",
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		WorktreeName:    "expired",
	})
	if err != nil {
		t.Fatalf("spawn expired agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(spawned.Path, "crash.txt"), []byte("orphaned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reaped, err := manager.ReapExpired(context.Background(), spawned.LeaseExpiresAt.Add(time.Second))
	if err != nil {
		t.Fatalf("reap expired agent: %v", err)
	}
	if len(reaped) != 1 || reaped[0].Status != worktree.AgentWorktreeReaped {
		t.Fatalf("unexpected reap result: %#v", reaped)
	}
	if _, err := os.Stat(spawned.Path); !os.IsNotExist(err) {
		t.Fatalf("expected expired worktree removed, stat err: %v", err)
	}
}

func TestWorktreeManagerRestoreCheckedRejectsAgentPathEscape(t *testing.T) {
	repo := t.TempDir()
	seedGitRepo(t, repo)
	manager := worktree.NewManager(repo, "HEAD")
	err := manager.RestoreChecked([]worktree.Worktree{{
		Name:            "agent",
		Path:            filepath.Join(repo, "..", "outside"),
		RequestID:       "request-1",
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		LeaseID:         "lease-1",
		Status:          worktree.AgentWorktreeActive,
	}})
	if err == nil || !strings.Contains(err.Error(), "escapes repository root") {
		t.Fatalf("expected restore path escape rejection, got %v", err)
	}
	if len(manager.List()) != 0 {
		t.Fatalf("unsafe restored worktree must not enter manager: %#v", manager.List())
	}
}

func TestWorktreeManagerRestoredLeaseCanBeReapedByNewProcess(t *testing.T) {
	repo := t.TempDir()
	seedGitRepo(t, repo)
	first := worktree.NewManager(repo, "HEAD")
	spawned, err := first.SpawnAgent(context.Background(), worktree.AgentSpawnRequest{
		RequestID:       "request-restart",
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		WorktreeName:    "restart",
	})
	if err != nil {
		t.Fatalf("spawn restart worktree: %v", err)
	}
	second := worktree.NewManager(repo, "HEAD")
	if err := second.RestoreChecked([]worktree.Worktree{spawned}); err != nil {
		t.Fatalf("restore lease after process restart: %v", err)
	}
	reaped, err := second.ReapExpired(context.Background(), spawned.LeaseExpiresAt.Add(time.Second))
	if err != nil {
		t.Fatalf("reap restored lease: %v", err)
	}
	if len(reaped) != 1 || reaped[0].RequestID != spawned.RequestID {
		t.Fatalf("unexpected restored reap: %#v", reaped)
	}
	if _, err := os.Stat(spawned.Path); !os.IsNotExist(err) {
		t.Fatalf("restored expired worktree remains, stat err: %v", err)
	}
}

func TestWorktreeManagerRenewsOnlyActiveLease(t *testing.T) {
	repo := t.TempDir()
	seedGitRepo(t, repo)
	manager := worktree.NewManager(repo, "HEAD")
	spawned, err := manager.SpawnAgent(context.Background(), worktree.AgentSpawnRequest{
		RequestID:       "request-renew",
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		WorktreeName:    "renew",
	})
	if err != nil {
		t.Fatalf("spawn lease: %v", err)
	}
	manager.SetAgentLeaseTTL(time.Hour)
	renewed, err := manager.RenewAgentLease(context.Background(), spawned.LeaseID)
	if err != nil {
		t.Fatalf("renew lease: %v", err)
	}
	if !renewed.LeaseExpiresAt.After(spawned.LeaseExpiresAt) || renewed.Status != worktree.AgentWorktreeActive {
		t.Fatalf("lease was not extended: before=%#v after=%#v", spawned, renewed)
	}
	if err := manager.CleanupAgent(context.Background(), spawned.RequestID, true, "done"); err != nil {
		t.Fatalf("cleanup lease: %v", err)
	}
	if _, err := manager.RenewAgentLease(context.Background(), spawned.LeaseID); err == nil {
		t.Fatal("renewing a released lease must fail")
	}
}

func seedGitRepo(t *testing.T, repo string) {
	t.Helper()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	file := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(file, []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed tracked file: %v", err)
	}
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "init")
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
