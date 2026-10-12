package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"code-agent/internal/session"
	"code-agent/internal/worktree"
)

func TestTaskWorkspaceRemainsPreparedAfterPythonGitContext(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first native Task Workspace preparation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository := t.TempDir()
	seedSpawnGitRepo(t, repository)
	manager := worktree.NewManager(repository, "HEAD")
	before, err := manager.CaptureBaseline(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "fixture", "read-only context", "", repository)
	if err != nil {
		t.Fatal(err)
	}
	storage := t.TempDir()
	tasks := session.NewTaskWorkspaces(workbench, func(context.Context) (uint, error) { return 7, nil }, repository, storage)
	prepared, err := tasks.Prepare(ctx, 7, created.ID, int64(created.EventCount), "context-read")
	if err != nil || prepared.State != "prepared" {
		t.Fatalf("prepare: state=%s reason=%s err=%v", prepared.State, prepared.Reason, err)
	}
	taskPath := filepath.Join(storage, prepared.WorkspaceID)
	command := exec.CommandContext(ctx, "python", "-c", "import sys; from orchestrator.context import load_git_diff_context; print(load_git_diff_context(sys.argv[1], sys.argv[1]))", taskPath)
	command.Dir = e2ERepositoryRoot(t)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		name = strings.ToUpper(name)
		if strings.Contains(name, "API_KEY") || strings.Contains(name, "AUTH_TOKEN") || strings.Contains(name, "SECRET") || strings.HasPrefix(name, "CODE_AGENT_") || strings.HasPrefix(name, "OTEL_") {
			continue
		}
		command.Env = append(command.Env, entry)
	}
	command.Env = append(command.Env, "OTEL_SDK_DISABLED=true", "CODE_AGENT_MODEL_ADMISSION=required")
	if _, err := command.Output(); err != nil {
		t.Fatal("actual Python context read failed; raw output suppressed")
	}
	inspected, err := tasks.Inspect(ctx, 7, created.ID)
	if err != nil || inspected.State != "prepared" {
		t.Fatalf("read-only Python context invalidated prepared task: state=%s reason=%s err=%v", inspected.State, inspected.Reason, err)
	}
	after, err := manager.CaptureBaseline(ctx)
	if err != nil || after.Checksum != before.Checksum {
		t.Fatal("context read changed original repository")
	}
}
