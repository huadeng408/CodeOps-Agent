package e2e_test

import (
	"context"
	"crypto/sha256"
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

const (
	worktreeRestartRunEnv     = "CODE_AGENT_RUN_WORKTREE_RESTART_E2E"
	worktreeRestartModeEnv    = "CODE_AGENT_WORKTREE_RESTART_MODE"
	worktreeRestartRootEnv    = "CODE_AGENT_WORKTREE_RESTART_ROOT"
	worktreeRestartDBEnv      = "CODE_AGENT_WORKTREE_RESTART_DB"
	worktreeRestartSessionEnv = "CODE_AGENT_WORKTREE_RESTART_SESSION"
	worktreeRestartStatusEnv  = "CODE_AGENT_WORKTREE_RESTART_STATUS"
)

// TestManagedWorktreeRestoresAcrossProcessRestart exercises the durable
// boundary with two separate Go processes: process one creates and persists a
// lease, process two reopens SQLite, restores the manager and verifies the
// checkout before reaping an explicitly expired lease.
func TestManagedWorktreeRestoresAcrossProcessRestart(t *testing.T) {
	if mode := os.Getenv(worktreeRestartModeEnv); mode != "" {
		runManagedWorktreeRestartHelper(t, mode)
		return
	}
	if os.Getenv(worktreeRestartRunEnv) != "1" {
		t.Skip("set CODE_AGENT_RUN_WORKTREE_RESTART_E2E=1 to run the managed worktree restart E2E")
	}

	repositoryRoot := contextE2ERepositoryRoot(t)
	runBase := filepath.Join(repositoryRoot, ".runtime", "e2e", "managed-worktree-runs")
	if err := os.MkdirAll(runBase, 0o700); err != nil {
		t.Fatal(err)
	}
	runRoot, err := os.MkdirTemp(runBase, "run-")
	if err != nil {
		t.Fatal(err)
	}
	projectRoot := filepath.Join(runRoot, "project")
	databasePath := filepath.Join(projectRoot, ".agent", "sessions.sqlite")
	statusPath := filepath.Join(projectRoot, ".agent", "restart-status")
	checks := map[string]bool{
		"sqlite_reopened": false, "checkout_restored": false,
		"child_commit_restored": false, "lease_reaped": false, "parent_unchanged": false,
	}
	receipt := map[string]any{
		"status": "IMPLEMENTED", "kind": "managed-worktree-process-restart",
		"evidence_type": "go-manager-process-integration",
		"run_id":        filepath.Base(runRoot), "trace_id": nil, "checks": checks,
		"command": "CODE_AGENT_RUN_WORKTREE_RESTART_E2E=1 go test ./tests/e2e -run TestManagedWorktreeRestoresAcrossProcessRestart -count=1",
	}
	defer func() {
		passed := 0
		for _, ok := range checks {
			if ok {
				passed++
			}
		}
		receipt["passed"], receipt["total"] = passed, len(checks)
		sourceStatus, sourceErr := exec.Command("git", "-C", repositoryRoot, "status", "--porcelain").Output()
		receipt["source_dirty"] = sourceErr != nil || len(strings.TrimSpace(string(sourceStatus))) > 0
		var artifacts []map[string]string
		for _, path := range []string{filepath.Join(runRoot, "seed.log"), filepath.Join(runRoot, "restore.log"), databasePath, statusPath} {
			body, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Errorf("read restart artifact: %v", err)
				continue
			}
			digest := sha256.Sum256(body)
			artifacts = append(artifacts, map[string]string{"path": path, "sha256": fmt.Sprintf("%x", digest)})
		}
		receipt["artifact_manifest"] = artifacts
		receipt["exit_code"] = 0
		if t.Failed() {
			receipt["exit_code"] = 1
		}
		body, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			t.Errorf("marshal restart receipt: %v", err)
			return
		}
		for _, path := range []string{filepath.Join(runRoot, "receipt.json"), filepath.Join(repositoryRoot, ".runtime", "e2e", "managed-worktree-process-restart.json")} {
			if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
				t.Errorf("write restart receipt: %v", err)
			}
		}
	}()
	receipt["git_sha"] = contextE2EGitSHA(t, repositoryRoot)
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	seedSpawnGitRepo(t, projectRoot)
	parentHead := runSpawnGit(t, projectRoot, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		t.Fatal(err)
	}

	runHelper := func(mode string) *exec.Cmd {
		command := exec.Command(os.Args[0], "-test.run", "^TestManagedWorktreeRestoresAcrossProcessRestart$", "-test.v")
		command.Dir = repositoryRoot
		command.Env = append(os.Environ(),
			worktreeRestartModeEnv+"="+mode,
			worktreeRestartRootEnv+"="+projectRoot,
			worktreeRestartDBEnv+"="+databasePath,
			worktreeRestartStatusEnv+"="+statusPath,
		)
		return command
	}

	first := runHelper("seed")
	firstOutput, firstErr := first.CombinedOutput()
	if first.ProcessState != nil {
		receipt["first_process_exit"], receipt["first_pid"] = first.ProcessState.ExitCode(), first.ProcessState.Pid()
	}
	if err := os.WriteFile(filepath.Join(runRoot, "seed.log"), firstOutput, 0o600); err != nil {
		t.Fatal(err)
	}
	if firstErr != nil {
		t.Fatalf("seed process failed: %v\n%s", firstErr, firstOutput)
	}
	firstSession, err := os.ReadFile(filepath.Join(projectRoot, ".agent", "session-id"))
	if err != nil {
		t.Fatalf("read persisted session id: %v", err)
	}
	receipt["session_id"] = strings.TrimSpace(string(firstSession))
	second := runHelper("restore")
	second.Env = append(second.Env, worktreeRestartSessionEnv+"="+strings.TrimSpace(string(firstSession)))
	secondOutput, secondErr := second.CombinedOutput()
	if second.ProcessState != nil {
		receipt["second_process_exit"], receipt["second_pid"] = second.ProcessState.ExitCode(), second.ProcessState.Pid()
	}
	if err := os.WriteFile(filepath.Join(runRoot, "restore.log"), secondOutput, 0o600); err != nil {
		t.Fatal(err)
	}
	if secondErr != nil {
		t.Fatalf("restore process failed: %v\n%s", secondErr, secondOutput)
	}
	status, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read restart status: %v", err)
	}
	if got := strings.TrimSpace(string(status)); got != "restored-and-reaped" {
		t.Fatalf("restart status = %q, want restored-and-reaped", got)
	}
	for _, name := range []string{"sqlite_reopened", "checkout_restored", "child_commit_restored", "lease_reaped"} {
		checks[name] = true
	}
	if trees := runSpawnGit(t, projectRoot, "worktree", "list", "--porcelain"); strings.Count(trees, "worktree ") != 1 {
		t.Fatalf("expired managed worktree remained after restart recovery: %s", trees)
	}
	if runSpawnGit(t, projectRoot, "rev-parse", "HEAD") != parentHead || strings.TrimSpace(runSpawnGit(t, projectRoot, "status", "--porcelain", "--untracked-files=no")) != "" {
		t.Fatal("parent worktree changed across child restart")
	}
	if first.ProcessState.Pid() == second.ProcessState.Pid() {
		t.Fatal("restart must use distinct Go processes")
	}
	checks["parent_unchanged"] = true
}

func runManagedWorktreeRestartHelper(t *testing.T, mode string) {
	root := os.Getenv(worktreeRestartRootEnv)
	databasePath := os.Getenv(worktreeRestartDBEnv)
	statusPath := os.Getenv(worktreeRestartStatusEnv)
	if root == "" || databasePath == "" || statusPath == "" {
		t.Fatal("worktree restart helper requires root, database and status paths")
	}
	store := session.NewManager(session.NewSQLiteEventStore(databasePath))
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	ctx := context.Background()
	switch mode {
	case "seed":
		created := store.NewSession(root)
		if created.ID == "" {
			t.Fatal("create durable session")
		}
		manager := worktree.NewManager(root, "HEAD")
		manager.SetAgentLeaseTTL(time.Hour)
		tree, err := manager.SpawnAgent(ctx, worktree.AgentSpawnRequest{
			RequestID: "request-process-restart", ParentSessionID: created.ID,
			ChildSessionID: created.ID + "/child", WorktreeName: "restart",
		})
		if err != nil {
			t.Fatalf("spawn managed worktree: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tree.Path, "tracked.txt"), []byte("child commit\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		runSpawnGit(t, tree.Path, "add", "tracked.txt")
		runSpawnGit(t, tree.Path, "commit", "-m", "child work before restart")
		if err := os.WriteFile(filepath.Join(root, ".agent", "child-head"), []byte(runSpawnGit(t, tree.Path, "rev-parse", "HEAD")), 0o600); err != nil {
			t.Fatal(err)
		}
		persisted := store.SetWorktrees([]session.WorktreeState{{
			Name: tree.Name, Path: tree.Path, BaseRef: tree.BaseRef, Active: tree.Active,
			RequestID: tree.RequestID, ParentSessionID: tree.ParentSessionID,
			ChildSessionID: tree.ChildSessionID, LeaseID: tree.LeaseID,
			LeaseExpiresAt: tree.LeaseExpiresAt, Status: tree.Status,
		}})
		if len(persisted.Worktrees) != 1 || persisted.Worktrees[0].LeaseID != tree.LeaseID {
			t.Fatalf("persisted worktree lease missing: %#v", persisted.Worktrees)
		}
		if err := os.WriteFile(filepath.Join(root, ".agent", "session-id"), []byte(created.ID), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	case "restore":
		sessionID := strings.TrimSpace(os.Getenv(worktreeRestartSessionEnv))
		if sessionID == "" {
			t.Fatal("restore helper requires session id")
		}
		loaded, err := store.Load(ctx, sessionID)
		if err != nil {
			t.Fatalf("reopen persisted session: %v", err)
		}
		if loaded.ID == "" || len(loaded.Worktrees) != 1 {
			t.Fatalf("restored session worktrees = %#v", loaded)
		}
		treeState := loaded.Worktrees[0]
		manager := worktree.NewManager(root, "HEAD")
		if err := manager.RestoreChecked([]worktree.Worktree{{
			Name: treeState.Name, Path: treeState.Path, BaseRef: treeState.BaseRef, Active: treeState.Active,
			RequestID: treeState.RequestID, ParentSessionID: treeState.ParentSessionID,
			ChildSessionID: treeState.ChildSessionID, LeaseID: treeState.LeaseID,
			LeaseExpiresAt: treeState.LeaseExpiresAt, Status: treeState.Status,
		}}); err != nil {
			t.Fatalf("restore worktree manager: %v", err)
		}
		if _, err := os.Stat(treeState.Path); err != nil {
			t.Fatalf("restored checkout missing: %v", err)
		}
		if got := strings.TrimSpace(runSpawnGit(t, treeState.Path, "rev-parse", "--show-toplevel")); !strings.EqualFold(filepath.Clean(got), filepath.Clean(treeState.Path)) {
			t.Fatalf("restored checkout root = %q, want %q", got, treeState.Path)
		}
		childHead, err := os.ReadFile(filepath.Join(root, ".agent", "child-head"))
		if err != nil || runSpawnGit(t, treeState.Path, "rev-parse", "HEAD") != string(childHead) {
			t.Fatalf("child commit was not restored: %v", err)
		}
		content, err := os.ReadFile(filepath.Join(treeState.Path, "tracked.txt"))
		if err != nil || string(content) != "child commit\n" {
			t.Fatalf("child source was not restored: %v", err)
		}
		reaped, err := manager.ReapExpired(ctx, treeState.LeaseExpiresAt.Add(time.Second))
		if err != nil || len(reaped) != 1 || reaped[0].LeaseID != treeState.LeaseID {
			t.Fatalf("reap restored lease = %#v, err=%v", reaped, err)
		}
		if _, err := os.Stat(treeState.Path); !os.IsNotExist(err) {
			t.Fatalf("reaped checkout still exists: %v", err)
		}
		if _, err := store.ApplyWorktreeTransition(ctx, sessionID, treeState, true, session.WorktreeLifecycle{
			Name: treeState.Name, Path: treeState.Path, BaseRef: treeState.BaseRef,
			RequestID: treeState.RequestID, ParentSessionID: treeState.ParentSessionID,
			ChildSessionID: treeState.ChildSessionID, LeaseID: treeState.LeaseID,
			Status: worktree.AgentWorktreeReaped, Reason: "lease-expired",
		}); err != nil {
			t.Fatalf("persist reaped worktree: %v", err)
		}
		loaded, err = store.Load(ctx, sessionID)
		if err != nil {
			t.Fatalf("session ledger unavailable after restore: %v", err)
		}
		if len(loaded.Worktrees) != 0 || len(loaded.WorktreeEvents) != 1 || loaded.WorktreeEvents[0].Status != worktree.AgentWorktreeReaped || loaded.WorktreeEvents[0].LeaseID != treeState.LeaseID {
			t.Fatalf("reaped lease was not durably cleared: %+v", loaded)
		}
		if err := os.WriteFile(statusPath, []byte("restored-and-reaped\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	default:
		t.Fatalf("unknown worktree restart helper mode %q", mode)
	}
}

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
