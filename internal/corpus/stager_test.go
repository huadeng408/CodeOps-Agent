package corpus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupLocalRepo creates a throwaway git repo with one commit and returns its
// path and commit SHA. Used to exercise the stager without network access.
func setupLocalRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	// LICENSE file with known sha256.
	license := "MIT License placeholder for tests\n"
	licenseHash := HashBytes([]byte(license))
	if err := os.WriteFile(filepath.Join(repo, "LICENSE"), []byte(license), 0o644); err != nil {
		t.Fatal(err)
	}
	docDir := filepath.Join(repo, "Doc")
	if err := os.MkdirAll(docDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docDir, "library.rst"), []byte("docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "LICENSE", "Doc/library.rst")
	run("commit", "--quiet", "-m", "initial")
	commit := run("rev-parse", "HEAD")
	return repo, commit + "|" + licenseHash
}

func TestStageVerifiesPinnedCommitAndLicense(t *testing.T) {
	repo, info := setupLocalRepo(t)
	parts := strings.SplitN(info, "|", 2)
	commit, licenseHash := parts[0], parts[1]

	spec := SourceSpec{
		SourceID:      "testsrc",
		RepositoryURL: "file://" + filepath.ToSlash(repo),
		SourceCommit:  commit,
		LicensePath:   "LICENSE",
		LicenseSHA256: licenseHash,
		LicenseSPDX:   "MIT",
	}
	stager := &Stager{}
	result, err := stager.Stage(context.Background(), spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if result.HeadCommit != commit {
		t.Fatalf("head = %s, want %s", result.HeadCommit, commit)
	}
	if result.LicenseSHA256 != licenseHash {
		t.Fatalf("license hash = %s, want %s", result.LicenseSHA256, licenseHash)
	}
}

func TestStageRejectsLicenseHashMismatch(t *testing.T) {
	repo, info := setupLocalRepo(t)
	commit := strings.SplitN(info, "|", 2)[0]

	spec := SourceSpec{
		SourceID:      "testsrc",
		RepositoryURL: "file://" + filepath.ToSlash(repo),
		SourceCommit:  commit,
		LicensePath:   "LICENSE",
		LicenseSHA256: strings.Repeat("f", 64), // valid shape, wrong hash
		LicenseSPDX:   "MIT",
	}
	stager := &Stager{}
	_, err := stager.Stage(context.Background(), spec, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "license hash mismatch") {
		t.Fatalf("expected license mismatch, got %v", err)
	}
}

func TestStageRejectsWrongCommit(t *testing.T) {
	repo, info := setupLocalRepo(t)
	_ = strings.SplitN(info, "|", 2)[1] // license hash unused here

	spec := SourceSpec{
		SourceID:      "testsrc",
		RepositoryURL: "file://" + filepath.ToSlash(repo),
		SourceCommit:  strings.Repeat("a", 40), // never exists
		LicensePath:   "LICENSE",
		LicenseSHA256: strings.Repeat("b", 64),
		LicenseSPDX:   "MIT",
	}
	stager := &Stager{}
	_, err := stager.Stage(context.Background(), spec, t.TempDir())
	if err == nil {
		t.Fatal("expected checkout failure")
	}
}

func TestStageRejectsFloatingCommitBeforeAnyGitCall(t *testing.T) {
	spec := SourceSpec{
		SourceID:      "testsrc",
		RepositoryURL: "file:///nonexistent",
		SourceCommit:  "main",
		LicensePath:   "LICENSE",
		LicenseSHA256: strings.Repeat("b", 64),
		LicenseSPDX:   "MIT",
	}
	stager := &Stager{}
	_, err := stager.Stage(context.Background(), spec, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "immutable commit") {
		t.Fatalf("expected immutable commit rejection, got %v", err)
	}
}
