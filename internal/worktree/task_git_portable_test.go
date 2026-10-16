package worktree

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"code-agent/internal/safety"
)

func TestTaskWorkspaceGitSurvivesUnavailableOriginalMetadata(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first task preparation contract")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	root, storage := taskRepository(t), t.TempDir()
	manager := NewManager(root, "HEAD")
	plan, err := manager.PlanTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original := make(map[string][]byte)
	for _, name := range []string{"main.go", "new.py", ".env", ".git/index"} {
		original[name], err = os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.PrepareTask(ctx, storage, &plan); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(storage, plan.LeaseID)
	// The Linux mount cannot resolve the original host metadata either.
	offline := root + "-temporarily-unavailable"
	for _, name := range []string{root, offline} {
		relative, err := filepath.Rel(filepath.Dir(root), name)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatal("fixture rename escaped its test-owned parent")
		}
	}
	if err := os.Rename(root, offline); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Rename(offline, root); err != nil {
			t.Error("restore test fixture:", err)
		}
	}()
	command := exec.CommandContext(ctx, "git", safety.HardenedGitArgs(target, "status", []string{"--porcelain=v1"})...)
	command.Env = safety.ScrubGitEnvironment(os.Environ())
	output, gitErr := command.CombinedOutput()
	for name, before := range original {
		after, err := os.ReadFile(filepath.Join(offline, name))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("prepared copy or Git changed original %s: %v", name, err)
		}
	}
	if gitErr != nil || !strings.Contains(string(output), "main.go") || !strings.Contains(string(output), "new.py") {
		t.Fatalf("isolated Git must read prepared changes without original metadata: %v\n%s", gitErr, output)
	}
	for _, check := range []struct {
		args []string
		want string
	}{
		{[]string{"diff", "--", "main.go"}, "current dirty content"},
		{[]string{"show", "HEAD:main.go"}, "committed"},
		{[]string{"log", "--format=%s"}, "fixture"},
		{[]string{"diff", "--cached", "--exit-code", "HEAD", "--"}, ""},
		{[]string{"diff", "--stat"}, "main.go"},
	} {
		command := exec.CommandContext(ctx, "git", safety.HardenedGitArgs(target, check.args[0], check.args[1:])...)
		command.Env = safety.ScrubGitEnvironment(os.Environ())
		output, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(output), check.want) {
			t.Fatalf("isolated Git %v: %v\n%s", check.args, err, output)
		}
		if strings.Contains(string(output), ".env") {
			t.Fatal("excluded credentials appear in task diff")
		}
	}
	command = exec.CommandContext(ctx, "git", safety.HardenedGitArgs(target, "show", []string{"HEAD:.env"})...)
	command.Env = safety.ScrubGitEnvironment(os.Environ())
	if err := command.Run(); err == nil {
		t.Fatal("excluded credential blob is readable from task Git")
	}
}

func TestTaskWorkspaceRejectsGitSnapshotTampering(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first task preparation contract")
	}
	ctx := context.Background()
	root, storage := taskRepository(t), t.TempDir()
	manager := NewManager(root, "HEAD")
	plan, err := manager.PlanTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.PrepareTask(ctx, storage, &plan); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(storage, plan.LeaseID)
	blob := strings.TrimSpace(baselineGit(t, root, "rev-parse", "HEAD:main.go"))
	for _, name := range []string{".git/HEAD", ".git/index", ".git/config", ".git/shallow", ".git/objects/" + blob[:2] + "/" + blob[2:]} {
		before, err := os.ReadFile(filepath.Join(target, name))
		if err != nil {
			t.Fatal(err)
		}
		writeBaselineFile(t, target, name, "changed metadata")
		if err := NewManager(root, "HEAD").VerifyTask(ctx, storage, plan); err == nil {
			t.Fatal("changed metadata accepted:", name)
		}
		writeBaselineFile(t, target, name, string(before))
	}
	if err := manager.VerifyTask(ctx, storage, plan); err != nil {
		t.Fatal("unchanged snapshot cannot reopen:", err)
	}
	legacy := plan
	legacy.GitMetadataSHA256 = ""
	if err := manager.VerifyTask(ctx, storage, legacy); err == nil {
		t.Fatal("portable directory silently downgraded to legacy link")
	}
	writeBaselineFile(t, target, ".git/hooks/unapproved", "unexpected")
	if err := manager.VerifyTask(ctx, storage, plan); err == nil {
		t.Fatal("unrecorded Git metadata accepted")
	}
}

func TestTaskWorkspaceRefusesCredentialInAdmittedHeadBlob(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first task preparation contract")
	}
	ctx := context.Background()
	root, storage := taskRepository(t), t.TempDir()
	writeBaselineFile(t, root, "config.go", "sk-"+strings.Repeat("a", 28))
	baselineGit(t, root, "add", "config.go")
	baselineGit(t, root, "commit", "-m", "unsafe historical fixture")
	writeBaselineFile(t, root, "config.go", "safe current bytes")
	manager := NewManager(root, "HEAD")
	plan, err := manager.PlanTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(root, ".git/index"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.PrepareTask(ctx, storage, &plan); err == nil {
		t.Fatal("credential in admitted HEAD was exported")
	}
	if entries, err := os.ReadDir(storage); err != nil || len(entries) != 0 {
		t.Fatal("credential refusal touched task storage")
	}
	after, err := os.ReadFile(filepath.Join(root, ".git/index"))
	if err != nil || !bytes.Equal(index, after) {
		t.Fatal("credential refusal changed original index")
	}
}

func TestTaskWorkspacePreservesApprovedFixtureWithGitLineEndings(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first task preparation contract")
	}
	ctx := context.Background()
	root, storage := taskRepository(t), t.TempDir()
	baselineGit(t, root, "config", "core.autocrlf", "false")
	blob := "-----BEGIN" + " PRIVATE KEY-----\npublic fixture only\n"
	writeBaselineFile(t, root, "testdata/key.pem", blob)
	baselineGit(t, root, "add", "testdata/key.pem")
	baselineGit(t, root, "commit", "-m", "LF public fixture")
	working := strings.ReplaceAll(blob, "\n", "\r\n")
	writeBaselineFile(t, root, "testdata/key.pem", working)
	manager := NewManager(root, "HEAD")
	head := strings.TrimSpace(baselineGit(t, root, "rev-parse", "HEAD"))
	if err := manager.SetPublicTestFixtures(head, map[string]string{"testdata/key.pem": baselineDigest([]byte(working))}); err != nil {
		t.Fatal(err)
	}
	plan, err := manager.PlanTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.PrepareTask(ctx, storage, &plan); err != nil {
		t.Fatal("approved fixture's Git line endings blocked preparation:", err)
	}
	data, err := os.ReadFile(filepath.Join(storage, plan.LeaseID, "testdata/key.pem"))
	if err != nil || string(data) != working {
		t.Fatal("preparation changed the approved working bytes")
	}
}

func TestTaskWorkspaceGitIgnoresCheckoutLineEndingNoise(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first task preparation contract")
	}
	for _, fixture := range []struct{ name, committed string }{{"LF", "unchanged\n"}, {"CRLF", "unchanged\r\n"}} {
		t.Run(fixture.name, func(t *testing.T) {
			root, storage := taskRepository(t), t.TempDir()
			baselineGit(t, root, "config", "core.autocrlf", "false")
			writeBaselineFile(t, root, "unchanged.go", fixture.committed)
			baselineGit(t, root, "add", "unchanged.go")
			baselineGit(t, root, "commit", "-m", "line ending fixture")
			writeBaselineFile(t, root, "unchanged.go", "unchanged\r\n")
			manager := NewManager(root, "HEAD")
			plan, err := manager.PlanTask(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.PrepareTask(context.Background(), storage, &plan); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(storage, plan.LeaseID)
			command := exec.Command("git", safety.HardenedGitArgs(target, "status", []string{"--short"})...)
			command.Env = safety.ScrubGitEnvironment(os.Environ())
			output, err := command.CombinedOutput()
			if err != nil || strings.Contains(string(output), "unchanged.go") || !strings.Contains(string(output), "main.go") {
				t.Fatalf("Git must show real content changes without checkout noise: %v\n%s", err, output)
			}
			if err := manager.VerifyTask(context.Background(), storage, plan); err != nil {
				t.Fatal("Git read changed prepared bytes or metadata:", err)
			}
		})
	}
}
