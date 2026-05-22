package codeagent_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
