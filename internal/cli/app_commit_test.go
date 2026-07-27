package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/skills"
)

// TestCommitSlashCommandWithNoChanges verifies that /commit prints the
// no-changes message when the working tree is clean.
func TestCommitSlashCommandWithNoChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := newGitTestRepo(t)
	app, out := newPermissionTestApp(root, "")
	app.skills = skills.NewManager()

	if !app.handleSlashCommand(context.Background(), "/commit") {
		t.Fatal("/commit should be handled")
	}
	if !strings.Contains(out.String(), "No uncommitted changes to summarize.") {
		t.Fatalf("expected no-changes message, got: %q", out.String())
	}
}

// TestCommitSlashCommandWithChangesProducesSuggestion verifies that /commit
// falls back to a heuristic suggestion when the orchestrator is unavailable.
func TestCommitSlashCommandWithChangesProducesSuggestion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := newGitTestRepo(t)
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app, out := newPermissionTestApp(root, "")
	app.skills = skills.NewManager()
	// orchestrator is nil on the test app -> heuristic fallback path

	if !app.handleSlashCommand(context.Background(), "/commit") {
		t.Fatal("/commit should be handled")
	}
	rendered := out.String()
	if !strings.Contains(rendered, "commit message suggestion") {
		t.Fatalf("expected suggestion framing, got: %q", rendered)
	}
	if !strings.Contains(rendered, "chore: update 1 file") {
		t.Fatalf("expected heuristic commit suggestion for one file, got: %q", rendered)
	}
}

// TestCommitSlashCommandWithoutGitRepoReportsFailure ensures /commit degrades
// gracefully (prints a failure line) when run outside a git repository.
func TestCommitSlashCommandWithoutGitRepoReportsFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir() // not a git repo
	app, out := newPermissionTestApp(root, "")
	app.skills = skills.NewManager()

	app.handleSlashCommand(context.Background(), "/commit")
	if !strings.Contains(out.String(), "commit diff failed") {
		t.Fatalf("expected diff failure line outside a repo, got: %q", out.String())
	}
}

// newGitTestRepo creates an empty initialized git repository with one initial
// commit and returns its root path. It skips the test if git is unusable.
func newGitTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "-m", "init", "--allow-empty"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git setup failed (%v): %s", err, strings.TrimSpace(string(out)))
		}
	}
	return root
}
