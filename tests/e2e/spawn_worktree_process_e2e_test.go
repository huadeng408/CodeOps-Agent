package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
	"code-agent/internal/worktree"
	_ "modernc.org/sqlite"
)

// TestSpawnAgentWorktreeProcessE2E proves the production Go client and Python
// subprocess agree on one Harness-created checkout before child execution.
func TestSpawnAgentWorktreeProcessE2E(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_SPAWN_WORKTREE_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_SPAWN_WORKTREE_E2E=1 to run the SpawnAgent worktree process E2E")
	}

	repositoryRoot := contextE2ERepositoryRoot(t)
	python := contextE2EPython(t)
	t.Setenv("PYTHONPATH", repositoryRoot)
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	seedSpawnGitRepo(t, projectRoot)

	address := contextE2EAddress(t)
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "spawn_worktree_process_server.py")
	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address:                address,
		AutoStart:              true,
		Command:                python,
		Args:                   []string{fixture},
		ProjectRoot:            projectRoot,
		WorkingDir:             projectRoot,
		MemoryDir:              filepath.Join(projectRoot, ".agent", "memory"),
		StartupTimeout:         20 * time.Second,
		ConversationTimeout:    30 * time.Second,
		RequireHarnessWorktree: true,
	})
	t.Cleanup(manager.Stop)

	client, err := manager.Client(context.Background())
	if err != nil {
		t.Fatalf("start Python orchestrator: %v", err)
	}

	storePath := filepath.Join(projectRoot, ".agent", "sessions.sqlite")
	store := session.NewManager(session.NewSQLiteEventStore(storePath))
	t.Cleanup(func() { _ = store.Close() })
	worktreeManager := worktree.NewManager(projectRoot, "HEAD")
	created := store.NewSession(projectRoot)
	if created.ID == "" {
		t.Fatal("create durable parent session")
	}
	var spawned worktree.Worktree
	client.OnAgentSpawn = func(ctx context.Context, event *codeagentpb.AgentSpawn) error {
		spawned, err = worktreeManager.SpawnAgent(ctx, worktree.AgentSpawnRequest{
			RequestID:       event.GetRequestId(),
			ParentSessionID: event.GetParentSessionId(),
			ChildSessionID:  event.GetChildSessionId(),
			WorktreeName:    event.GetWorktreeName(),
			BaseRef:         event.GetBaseRef(),
		})
		if err != nil {
			return err
		}
		persisted := store.SetWorktrees([]session.WorktreeState{{
			Name:            spawned.Name,
			Path:            spawned.Path,
			BaseRef:         spawned.BaseRef,
			RequestID:       spawned.RequestID,
			ParentSessionID: spawned.ParentSessionID,
			ChildSessionID:  spawned.ChildSessionID,
			LeaseID:         spawned.LeaseID,
			LeaseExpiresAt:  spawned.LeaseExpiresAt,
			Status:          spawned.Status,
			Active:          spawned.Active,
		}})
		if len(persisted.Worktrees) != 1 || persisted.Worktrees[0].LeaseID == "" {
			return fmt.Errorf("persist spawned worktree lease")
		}
		store.AppendWorktreeLifecycle(session.WorktreeLifecycle{
			Name:            spawned.Name,
			Path:            spawned.Path,
			BaseRef:         spawned.BaseRef,
			RequestID:       spawned.RequestID,
			ParentSessionID: spawned.ParentSessionID,
			ChildSessionID:  spawned.ChildSessionID,
			LeaseID:         spawned.LeaseID,
			Status:          spawned.Status,
			Reason:          "spawned",
		})
		return nil
	}
	client.OnAgentLifecycle = func(ctx context.Context, lifecycle *codeagentpb.AgentLifecycle) error {
		tree, ok := worktreeManager.FindAgent(lifecycle.GetRequestId())
		if !ok {
			return nil
		}
		if err := worktreeManager.CleanupAgent(ctx, tree.RequestID, lifecycle.GetStatus() != "completed", lifecycle.GetReason()); err != nil {
			return err
		}
		store.SetWorktrees(nil)
		store.AppendWorktreeLifecycle(session.WorktreeLifecycle{
			RequestID:       tree.RequestID,
			ParentSessionID: tree.ParentSessionID,
			ChildSessionID:  tree.ChildSessionID,
			LeaseID:         tree.LeaseID,
			Status:          lifecycle.GetStatus(),
			Reason:          lifecycle.GetReason(),
		})
		return nil
	}

	response, err := client.ConverseWithHistory(context.Background(), "spawn", created.ID, nil)
	if err != nil {
		t.Fatalf("Go to Python SpawnAgent conversation: %v", err)
	}
	if !strings.Contains(response, "SPAWN_WORKTREE_OK") || !strings.Contains(response, "tracked.txt") {
		t.Fatalf("unexpected child response: %q", response)
	}
	if spawned.Path == "" || spawned.Path == projectRoot {
		t.Fatalf("child was not isolated: %#v", spawned)
	}
	if status := runSpawnGit(t, projectRoot, "status", "--porcelain", "--untracked-files=no"); strings.TrimSpace(status) != "" {
		t.Fatalf("parent worktree changed: %q", status)
	}
	loaded, err := store.Load(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("load persisted parent session: %v", err)
	}
	if len(loaded.Worktrees) != 0 {
		t.Fatalf("terminal lifecycle should clear active worktree projection: %#v", loaded.Worktrees)
	}
	if len(loaded.WorktreeEvents) < 2 || loaded.WorktreeEvents[0].LeaseID == "" || loaded.WorktreeEvents[len(loaded.WorktreeEvents)-1].Status != "completed" {
		t.Fatalf("worktree lifecycle terminal event missing: %#v", loaded.WorktreeEvents)
	}

	if _, err := os.Stat(spawned.Path); !os.IsNotExist(err) {
		t.Fatalf("child worktree remains after lifecycle cleanup, stat err: %v", err)
	}

	receipt := map[string]any{
		"status":            "VERIFIED",
		"git_sha":           contextE2EGitSHA(t, repositoryRoot),
		"run_id":            "spawn-worktree-e2e",
		"go_to_python":      true,
		"parent_unchanged":  true,
		"lease_persisted":   true,
		"lifecycle_cleaned": true,
		"request_id":        spawned.RequestID,
		"lease_id":          spawned.LeaseID,
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "spawn-worktree.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func seedSpawnGitRepo(t *testing.T, repo string) {
	t.Helper()
	runSpawnGit(t, repo, "init")
	runSpawnGit(t, repo, "config", "user.email", "test@example.com")
	runSpawnGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("parent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSpawnGit(t, repo, "add", "tracked.txt")
	runSpawnGit(t, repo, "commit", "-m", "init")
}

func runSpawnGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
