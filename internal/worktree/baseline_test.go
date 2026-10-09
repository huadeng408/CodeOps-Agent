package worktree

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"code-agent/internal/safety"
)

func TestCaptureBaselineIncludesWorkingCopyAndPreservesRepository(t *testing.T) {
	root := t.TempDir()
	baselineGit(t, root, "init")
	baselineGit(t, root, "config", "user.email", "fixture@example.test")
	baselineGit(t, root, "config", "user.name", "Fixture")
	writeBaselineFile(t, root, "main.go", "committed")
	writeBaselineFile(t, root, "gone.go", "delete me")
	writeBaselineFile(t, root, "staged-gone.go", "delete me too")
	writeBaselineFile(t, root, ".env", "fixture-private-value")
	writeBaselineFile(t, root, "dist/generated.js", "generated")
	baselineGit(t, root, "add", ".")
	baselineGit(t, root, "commit", "-m", "fixture")
	writeBaselineFile(t, root, "main.go", "staged")
	baselineGit(t, root, "add", "main.go")
	writeBaselineFile(t, root, "main.go", "current unstaged contents")
	writeBaselineFile(t, root, "new_test.go", "new source")
	writeBaselineFile(t, root, "empty.py", "")
	writeBaselineFile(t, root, "node_modules/dep/index.js", "dependency")
	if err := os.Remove(filepath.Join(root, "gone.go")); err != nil {
		t.Fatal(err)
	}
	baselineGit(t, root, "rm", "staged-gone.go")
	indexBefore, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	statusBefore := baselineGit(t, root, "status", "--porcelain=v1", "-z")
	headBefore := baselineGit(t, root, "rev-parse", "HEAD")
	manager := NewManager(root, "HEAD")
	baseline, err := manager.CaptureBaseline(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]BaselineFile{}
	for _, file := range baseline.Files {
		files[file.Path] = file
	}
	for name, contents := range map[string]string{"main.go": "current unstaged contents", "new_test.go": "new source", "empty.py": ""} {
		file := files[name]
		if !file.Exists || file.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(contents))) {
			t.Errorf("working-copy state missing or incorrect: %s", name)
		}
	}
	for _, name := range []string{"gone.go", "staged-gone.go"} {
		file, exists := files[name]
		if !exists || file.Exists || file.SHA256 != "" {
			t.Errorf("deletion confused with an empty file: %s", name)
		}
	}
	for _, name := range []string{".env", "dist/generated.js", "node_modules/dep/index.js"} {
		if _, exists := files[name]; exists {
			t.Errorf("excluded file entered baseline: %s", name)
		}
	}
	if len(baseline.Excluded) != 3 || len(baseline.Checksum) != sha256.Size*2 || len(baseline.RepositoryID) != sha256.Size*2 {
		t.Fatal("baseline identity or exclusions incomplete")
	}
	indexAfter, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if string(indexBefore) != string(indexAfter) || statusBefore != baselineGit(t, root, "status", "--porcelain=v1", "-z") || headBefore != baselineGit(t, root, "rev-parse", "HEAD") {
		t.Fatal("capture changed the original repository or index")
	}
	again, err := manager.CaptureBaseline(context.Background())
	if err != nil || again.Checksum != baseline.Checksum {
		t.Fatal("unchanged working copy has an unstable baseline")
	}
	writeBaselineFile(t, root, "empty.py", "changed")
	changed, err := manager.CaptureBaseline(context.Background())
	if err != nil || changed.Checksum == baseline.Checksum {
		t.Fatal("working-copy change did not change the baseline")
	}
}

func TestCaptureBaselineRejectsMissingRepositoryAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name string
		ctx  context.Context
		root string
	}{
		{"canceled", ctx, t.TempDir()},
		{"non-repository", context.Background(), t.TempDir()},
		{"missing", context.Background(), filepath.Join(t.TempDir(), "missing")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewManager(test.root, "HEAD").CaptureBaseline(test.ctx); err == nil {
				t.Fatal("invalid baseline was accepted")
			}
		})
	}
}

func TestCaptureBaselineRejectsUnsafeRepositoryRootsBeforeFilesystemAccess(t *testing.T) {
	for _, root := range []string{`\\unreachable.invalid\share`, `//unreachable.invalid/share`, `C:relative`, filepath.Join(t.TempDir(), "repo") + ":stream"} {
		t.Run(root, func(t *testing.T) {
			if runtime.GOOS != "windows" && strings.HasPrefix(root, "//") {
				t.Skip("Windows UNC parsing; POSIX normalizes this to an absolute local path")
			}
			_, err := NewManager(root, "HEAD").CaptureBaseline(context.Background())
			if err == nil || !strings.Contains(err.Error(), "unsafe repository root") {
				t.Fatal("unsafe root was not rejected before filesystem access")
			}
		})
	}
}

func TestCaptureBaselineRejectsJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows reparse boundary")
	}
	root, outside := t.TempDir(), t.TempDir()
	baselineGit(t, root, "init")
	baselineGit(t, root, "config", "user.email", "fixture@example.test")
	baselineGit(t, root, "config", "user.name", "Fixture")
	writeBaselineFile(t, root, "src/main.go", "approved")
	baselineGit(t, root, "add", ".")
	baselineGit(t, root, "commit", "-m", "fixture")
	writeBaselineFile(t, outside, "main.go", "outside content")
	if err := os.Rename(filepath.Join(root, "src"), filepath.Join(root, "original-src")); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "src"), outside).CombinedOutput(); err != nil {
		t.Fatalf("junction fixture unavailable: %v, %s", err, output)
	}
	if _, err := NewManager(root, "HEAD").CaptureBaseline(context.Background()); err == nil {
		t.Fatal("junction content entered the baseline")
	}
	data, err := os.ReadFile(filepath.Join(outside, "main.go"))
	if err != nil || string(data) != "outside content" {
		t.Fatal("outside content changed")
	}
}

func TestCaptureBaselineRequiresApprovedGitMetadataBeforeRunningGit(t *testing.T) {
	repo := t.TempDir()
	baselineGit(t, repo, "init")
	baselineGit(t, repo, "config", "user.email", "fixture@example.test")
	baselineGit(t, repo, "config", "user.name", "Fixture")
	writeBaselineFile(t, repo, "main.go", "source")
	baselineGit(t, repo, "add", ".")
	baselineGit(t, repo, "commit", "-m", "fixture")
	child := filepath.Join(t.TempDir(), "child")
	baselineGit(t, repo, "worktree", "add", "-b", "fixture-child", child)
	writeBaselineFile(t, repo, ".git/commondir", "../../unapproved-metadata")
	t.Setenv("PATH", t.TempDir())
	for _, root := range []string{child, repo} {
		_, err := NewManager(root, "HEAD").CaptureBaseline(context.Background())
		if err == nil || !strings.Contains(err.Error(), "Git metadata") {
			t.Fatal("unapproved linked/common Git metadata reached the Git subprocess")
		}
	}
}

func TestCaptureBaselineDoesNotCountGeneratedInventoryAsSourceFiles(t *testing.T) {
	repo := t.TempDir()
	baselineGit(t, repo, "init")
	baselineGit(t, repo, "config", "user.email", "fixture@example.test")
	baselineGit(t, repo, "config", "user.name", "Fixture")
	writeBaselineFile(t, repo, "main.go", "source")
	baselineGit(t, repo, "add", ".")
	baselineGit(t, repo, "commit", "-m", "fixture")
	blob := strings.TrimSpace(baselineGit(t, repo, "rev-parse", "HEAD:main.go"))
	var entries strings.Builder
	for i := 0; i < 20_001; i++ {
		fmt.Fprintf(&entries, "100644 %s\t.venv/package/file-%05d.py\n", blob, i)
	}
	command := exec.Command("git", safety.HardenedGitArgs(repo, "update-index", []string{"--index-info"})...)
	command.Env, command.Stdin = scrubGitEnvironment(), strings.NewReader(entries.String())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("inventory fixture: %v, %s", err, output)
	}
	baseline, err := NewManager(repo, "HEAD").CaptureBaseline(context.Background())
	if err != nil {
		t.Fatal("generated inventory blocked the source baseline:", err)
	}
	if len(baseline.Files) != 1 || baseline.Files[0].Path != "main.go" || len(baseline.Excluded) != 1 || baseline.Excluded[0].Path != ".venv/" {
		t.Fatal("generated inventory entered the admitted file denominator or was not aggregated")
	}
}

func TestCaptureBaselineExcludesCommonCredentialLocations(t *testing.T) {
	repo := t.TempDir()
	baselineGit(t, repo, "init")
	baselineGit(t, repo, "config", "user.email", "fixture@example.test")
	baselineGit(t, repo, "config", "user.name", "Fixture")
	writeBaselineFile(t, repo, "main.go", "source")
	baselineGit(t, repo, "add", ".")
	baselineGit(t, repo, "commit", "-m", "fixture")
	credentials := []string{".npmrc", ".pypirc", ".netrc", ".codex/auth.json", ".ssh/id_ecdsa", ".kube/config", ".config/gcloud/application_default_credentials.json"}
	for _, name := range credentials {
		writeBaselineFile(t, repo, name, "fixture-private-value")
	}
	baseline, err := NewManager(repo, "HEAD").CaptureBaseline(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Files) != 1 || baseline.Files[0].Path != "main.go" || len(baseline.Excluded) != len(credentials) {
		t.Fatal("common credential locations entered the admitted source baseline")
	}
}

func TestCaptureBaselineBoundsSourceCountAndGitOutput(t *testing.T) {
	for _, test := range []struct{ name, prefix string }{
		{"source-count", "source/"},
		{"output-bytes", ".venv/" + strings.Repeat("part/", 85)},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			baselineGit(t, repo, "init")
			baselineGit(t, repo, "config", "user.email", "fixture@example.test")
			baselineGit(t, repo, "config", "user.name", "Fixture")
			writeBaselineFile(t, repo, "main.go", "source")
			baselineGit(t, repo, "add", ".")
			baselineGit(t, repo, "commit", "-m", "fixture")
			blob := strings.TrimSpace(baselineGit(t, repo, "rev-parse", "HEAD:main.go"))
			var entries strings.Builder
			for i := 0; i < 20_001; i++ {
				fmt.Fprintf(&entries, "100644 %s\t%sfile-%05d.py\n", blob, test.prefix, i)
			}
			command := exec.Command("git", safety.HardenedGitArgs(repo, "update-index", []string{"--index-info"})...)
			command.Env, command.Stdin = scrubGitEnvironment(), strings.NewReader(entries.String())
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("inventory fixture: %v, %s", err, output)
			}
			before, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err = NewManager(repo, "HEAD").CaptureBaseline(ctx)
			if err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("oversized inventory was not refused within its runtime bound")
			}
			after, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
			if err != nil || string(before) != string(after) {
				t.Fatal("refusal changed the source index")
			}
		})
	}
}

func TestCaptureBaselineRejectsMetadataIncludesBeforeRunningGit(t *testing.T) {
	for _, section := range []string{"include", "includeIf \"gitdir:*\""} {
		t.Run(section, func(t *testing.T) {
			repo := t.TempDir()
			baselineGit(t, repo, "init")
			baselineGit(t, repo, "config", "user.email", "fixture@example.test")
			baselineGit(t, repo, "config", "user.name", "Fixture")
			writeBaselineFile(t, repo, "main.go", "source")
			baselineGit(t, repo, "add", ".")
			baselineGit(t, repo, "commit", "-m", "fixture")
			file, err := os.OpenFile(filepath.Join(repo, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(file, "\n[%s]\npath = ../../unapproved-config\n", section)
			file.Close()
			t.Setenv("PATH", t.TempDir())
			_, err = NewManager(repo, "HEAD").CaptureBaseline(context.Background())
			if err == nil || !strings.Contains(err.Error(), "Git metadata") {
				t.Fatal("configuration include was not rejected before Git")
			}
		})
	}
}

func TestCaptureBaselineRejectsNestedMetadataAliasBeforeRunningGit(t *testing.T) {
	repo := t.TempDir()
	baselineGit(t, repo, "init")
	baselineGit(t, repo, "config", "user.email", "fixture@example.test")
	baselineGit(t, repo, "config", "user.name", "Fixture")
	writeBaselineFile(t, repo, "main.go", "source")
	baselineGit(t, repo, "add", ".")
	baselineGit(t, repo, "commit", "-m", "fixture")
	outside := t.TempDir()
	if err := os.Rename(filepath.Join(repo, ".git", "refs"), filepath.Join(repo, ".git", "original-refs")); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(repo, ".git", "refs")
	if runtime.GOOS == "windows" {
		if output, err := exec.Command("cmd", "/c", "mklink", "/J", alias, outside).CombinedOutput(); err != nil {
			t.Fatalf("junction fixture: %v, %s", err, output)
		}
	} else if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	_, err := NewManager(repo, "HEAD").CaptureBaseline(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Git metadata") {
		t.Fatal("nested metadata alias was not rejected before Git")
	}
}

func TestCaptureBaselineIgnoresExternalExcludesConfiguration(t *testing.T) {
	repo := t.TempDir()
	baselineGit(t, repo, "init")
	baselineGit(t, repo, "config", "user.email", "fixture@example.test")
	baselineGit(t, repo, "config", "user.name", "Fixture")
	writeBaselineFile(t, repo, "main.go", "source")
	baselineGit(t, repo, "add", ".")
	baselineGit(t, repo, "commit", "-m", "fixture")
	writeBaselineFile(t, repo, "new_test.go", "new source")
	outside := filepath.Join(t.TempDir(), "external-ignore")
	if err := os.WriteFile(outside, []byte("new_test.go\n"), 0600); err != nil {
		t.Fatal(err)
	}
	baselineGit(t, repo, "config", "core.excludesFile", outside)
	baseline, err := NewManager(repo, "HEAD").CaptureBaseline(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Files) != 2 || baseline.Files[1].Path != "new_test.go" {
		t.Fatal("external ignore configuration influenced the approved source baseline")
	}
}

func baselineGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	output, err := gitOutput(context.Background(), root, args...)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func writeBaselineFile(t *testing.T, root, name, contents string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}
