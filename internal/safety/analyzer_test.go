package safety

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestShellASTBlocksObfuscatedAndChainedCommands(t *testing.T) {
	analyzer := NewAnalyzer()

	blockedCases := []struct {
		name    string
		command string
		reason  string
	}{
		{
			name:    "quote-obfuscated recursive delete",
			command: "r'm' -rf /",
			reason:  "recursive delete blocked",
		},
		{
			name:    "chained destructive git operation",
			command: "git status && git push --force origin main",
			reason:  "destructive or shared git operation blocked",
		},
		{
			name:    "command substitution destructive command",
			command: "echo $(rm -rf /)",
			reason:  "recursive delete blocked",
		},
		{
			name:    "backtick substitution git hard reset",
			command: "echo `git reset --hard HEAD~1`",
			reason:  "destructive or shared git operation blocked",
		},
		{
			name:    "subshell ownership change",
			command: "(chmod -R 777 /)",
			reason:  "broad permission or ownership change blocked",
		},
		{
			name:    "dynamic eval execution",
			command: `eval "rm -rf /tmp/data"`,
			reason:  "dynamic shell eval blocked",
		},
		{
			name:    "unclosed quote fails closed",
			command: `echo "unterminated`,
			reason:  "malformed shell syntax blocked",
		},
	}

	for _, tc := range blockedCases {
		t.Run(tc.name, func(t *testing.T) {
			res := analyzer.AnalyzeCommand(tc.command)
			if res.Allowed {
				t.Fatalf("expected command %q to be blocked, got Allowed=true", tc.command)
			}
			if res.Reason != tc.reason {
				t.Fatalf("AnalyzeCommand(%q) reason = %q, want %q", tc.command, res.Reason, tc.reason)
			}
		})
	}
}

func TestShellASTAllowsSafePipelinesAndGitInspection(t *testing.T) {
	analyzer := NewAnalyzer()
	safeCommands := []string{
		"git status --short && git diff",
		"Write-Output first; Start-Sleep -Milliseconds 10; Write-Output second",
		"go test ./internal/safety/...",
	}
	for _, cmd := range safeCommands {
		res := analyzer.AnalyzeCommand(cmd)
		if !res.Allowed {
			t.Fatalf("expected safe command %q to be allowed, got reason=%q", cmd, res.Reason)
		}
	}
}

func TestPathTrieEnforcesWorkspaceBoundaryAndSensitivePrefixes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	analyzer := NewWorkspaceAnalyzer(root, ".env")

	if ok, reason := analyzer.boundary.CheckPath("internal/safety/analyzer.go"); !ok {
		t.Fatalf("expected in-workspace path to be allowed, got reason=%q", reason)
	}
	if ok, _ := analyzer.boundary.CheckPath("../../etc/passwd"); ok {
		t.Fatal("expected parent traversal outside workspace to be blocked by PathTrie")
	}
	if ok, _ := analyzer.boundary.CheckPath(".git/hooks/pre-commit"); ok {
		t.Fatal("expected .git/hooks path to be blocked by PathTrie deny prefix")
	}

	escapeAnalysis := analyzer.AnalyzeCommand("cat ../../etc/passwd")
	if escapeAnalysis.Allowed {
		t.Fatal("expected AnalyzeCommand with path traversal to be blocked by workspace PathTrie")
	}
	hookAnalysis := analyzer.AnalyzeCommand("chmod +x .git/hooks/pre-commit")
	if hookAnalysis.Allowed {
		t.Fatal("expected AnalyzeCommand targeting .git/hooks to be blocked by workspace PathTrie")
	}
}

func TestPathTrieBlocksSymlinkEscapeIncludingNonExistentChild(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideSecret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideSecret, []byte("top-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(workspace, "escaped_link")
	if err := os.Symlink(outside, linkDir); err != nil {
		if runtime.GOOS == "windows" {
			if jErr := exec.Command("cmd", "/c", "mklink", "/J", linkDir, outside).Run(); jErr != nil {
				t.Skipf("symlink/junction creation not permitted: %v / %v", err, jErr)
			}
		} else {
			t.Skipf("symlink creation not permitted in this environment: %v", err)
		}
	}

	analyzer := NewWorkspaceAnalyzer(workspace, ".env")
	// Existing file through symlink must be blocked
	if ok, _ := analyzer.CheckWorkspacePath(filepath.Join(linkDir, "secret.txt")); ok {
		t.Fatal("expected existing file through symlink outside workspace to be blocked")
	}
	// Non-existent file under symlinked ancestor directory must also be blocked
	if ok, _ := analyzer.CheckWorkspacePath(filepath.Join(linkDir, "new_payload.sh")); ok {
		t.Fatal("expected non-existent child under escaped symlink directory to be blocked")
	}
	// Command referencing escaped symlink inside workspace must be blocked
	if res := analyzer.AnalyzeCommand("cat escaped_link/secret.txt"); res.Allowed {
		t.Fatal("expected command accessing escaped symlink path to be blocked")
	}
}
