package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func taskRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	baselineGit(t, root, "init")
	baselineGit(t, root, "config", "user.name", "Fixture")
	baselineGit(t, root, "config", "user.email", "fixture@example.test")
	writeBaselineFile(t, root, "main.go", "committed")
	writeBaselineFile(t, root, "gone.py", "delete me")
	writeBaselineFile(t, root, ".env", "private fixture")
	baselineGit(t, root, "add", ".")
	baselineGit(t, root, "commit", "-m", "fixture")
	writeBaselineFile(t, root, "main.go", "staged")
	baselineGit(t, root, "add", "main.go")
	writeBaselineFile(t, root, "main.go", "current dirty content")
	writeBaselineFile(t, root, "new.py", "new source")
	writeBaselineFile(t, root, "empty.py", "")
	if err := os.Remove(filepath.Join(root, "gone.py")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTaskWorkspaceCopiesCurrentBaselineAndRecovers(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first native Git directory pin contract")
	}
	ctx := context.Background()
	root, storage := taskRepository(t), t.TempDir()
	manager := NewManager(root, "HEAD")
	plan, err := manager.PlanTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	status := baselineGit(t, root, "status", "--porcelain=v1", "-z")
	index, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.PrepareTask(ctx, storage, &plan); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(storage, plan.LeaseID)
	for name, want := range map[string]string{"main.go": "current dirty content", "new.py": "new source", "empty.py": ""} {
		data, err := os.ReadFile(filepath.Join(target, name))
		if err != nil || string(data) != want {
			t.Fatalf("task copy mismatch: %s", name)
		}
	}
	for _, name := range []string{".env", "gone.py"} {
		if _, err := os.Lstat(filepath.Join(target, name)); !os.IsNotExist(err) {
			t.Fatalf("excluded/deleted file materialized: %s", name)
		}
	}
	if err := NewManager(root, "HEAD").VerifyTask(ctx, storage, plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(baselineGit(t, root, "worktree", "list", "--porcelain"), target) && !strings.Contains(baselineGit(t, root, "worktree", "list", "--porcelain"), filepath.ToSlash(target)) {
		t.Fatal("task is not a registered Git worktree")
	}
	afterIndex, _ := os.ReadFile(filepath.Join(root, ".git", "index"))
	if string(index) != string(afterIndex) || status != baselineGit(t, root, "status", "--porcelain=v1", "-z") {
		t.Fatal("source/index changed")
	}
	if err := manager.PrepareTask(ctx, storage, &plan); err == nil {
		t.Fatal("existing task was silently overwritten")
	}
	gitfile, err := os.ReadFile(filepath.Join(target, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{strings.TrimPrefix(string(gitfile), "gitdir: "), "gitdir: \t" + strings.TrimPrefix(string(gitfile), "gitdir: "), strings.TrimRight(string(gitfile), "\r\n") + " \n"} {
		writeBaselineFile(t, target, ".git", invalid)
		if err := manager.VerifyTask(ctx, storage, plan); err == nil {
			t.Fatal("malformed Git link reported prepared")
		}
	}
	writeBaselineFile(t, target, ".git", string(gitfile))
	for _, name := range []string{"HEAD", "commondir", "gitdir", "locked"} {
		path := ".git/worktrees/" + plan.LeaseID + "/" + name
		original, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		writeBaselineFile(t, root, path, "\t"+string(original))
		if err := manager.VerifyTask(ctx, storage, plan); err == nil {
			t.Fatal("malformed Git control file reported prepared: " + name)
		}
		writeBaselineFile(t, root, path, string(original))
	}
	writeBaselineFile(t, target, "unrecorded.py", "unexpected file")
	if err := manager.VerifyTask(ctx, storage, plan); err == nil {
		t.Fatal("unrecorded task file reported prepared")
	}
	if err := os.Remove(filepath.Join(target, "unrecorded.py")); err != nil {
		t.Fatal(err)
	}
	writeBaselineFile(t, target, "main.go", "unexpected task change")
	if err := manager.VerifyTask(ctx, storage, plan); err == nil {
		t.Fatal("changed task accepted as prepared")
	}
	writeBaselineFile(t, target, "main.go", "current dirty content")
	if err := manager.VerifyTask(ctx, storage, plan); err != nil {
		t.Fatal(err)
	}
	writeBaselineFile(t, root, ".git/worktrees/"+plan.LeaseID+"/index", "changed index")
	if err := manager.VerifyTask(ctx, storage, plan); err == nil {
		t.Fatal("changed task index reported prepared")
	}
}

func TestTaskWorkspaceRejectsStaleBaselineAndUnsafeStorage(t *testing.T) {
	ctx := context.Background()
	root, storage := taskRepository(t), t.TempDir()
	manager := NewManager(root, "HEAD")
	plan, err := manager.PlanTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writeBaselineFile(t, root, "new.py", "changed after planning")
	if err := manager.PrepareTask(ctx, storage, &plan); err == nil {
		t.Fatal("stale baseline accepted")
	}
	if _, err := os.Lstat(filepath.Join(storage, plan.LeaseID)); !os.IsNotExist(err) {
		t.Fatal("stale preparation touched storage")
	}
	plan, err = manager.PlanTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, filepath.Join(root, "tasks"), `\\unreachable.invalid\share`, storage + ":stream"} {
		if err := manager.PrepareTask(ctx, path, &plan); err == nil {
			t.Fatal("unsafe task storage accepted")
		}
	}
}

func TestTaskWorkspaceNeverRunsCheckoutFilters(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first native Git directory pin contract")
	}
	ctx := context.Background()
	root, storage := taskRepository(t), t.TempDir()
	writeBaselineFile(t, root, ".gitattributes", "*.go filter=unapproved\n")
	baselineGit(t, root, "add", ".gitattributes")
	baselineGit(t, root, "commit", "-m", "fixture attributes")
	baselineGit(t, root, "config", "filter.unapproved.smudge", "codeops-unapproved-filter-does-not-exist")
	baselineGit(t, root, "config", "filter.unapproved.required", "true")
	m := NewManager(root, "HEAD")
	plan, err := m.PlanTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.PrepareTask(ctx, storage, &plan); err != nil {
		t.Fatal("task preparation attempted a checkout filter", err)
	}
}

func TestTaskWorkspaceRefusesCredentialContentAndStorageJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows product boundary")
	}
	ctx := context.Background()
	root, storage := taskRepository(t), t.TempDir()
	writeBaselineFile(t, root, "config.py", "sk-"+strings.Repeat("a", 28))
	m := NewManager(root, "HEAD")
	plan, err := m.PlanTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.PrepareTask(ctx, storage, &plan); err == nil {
		t.Fatal("credential-shaped source copied")
	}
	if entries, err := os.ReadDir(storage); err != nil || len(entries) != 0 {
		t.Fatal("credential refusal touched task storage")
	}
	if err := os.Remove(filepath.Join(root, "config.py")); err != nil {
		t.Fatal(err)
	}
	plan, err = m.PlanTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := exec.Command("cmd", "/c", "mklink", "/J", alias, storage).Run(); err != nil {
		t.Fatal("fixture junction creation failed")
	}
	if err := m.PrepareTask(ctx, alias, &plan); err == nil {
		t.Fatal("junction storage accepted")
	}
	if entries, err := os.ReadDir(storage); err != nil || len(entries) != 0 {
		t.Fatal("junction denial touched target")
	}
}
