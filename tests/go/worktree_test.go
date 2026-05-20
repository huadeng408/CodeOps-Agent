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
