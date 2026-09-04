package codeagent_test

import (
	"bytes"
	"context"
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
)

func TestPlanTodoStateSurvivesPythonProcessRestart(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(filepath.Join(root, "..", ".."))
	project := t.TempDir()
	addr := freePortAddr(t)

	manager := session.NewManager(session.NewSQLiteEventStore(filepath.Join(project, "session.sqlite")))
	created := manager.NewSession(project)
	applyUpdate := func(planUpdate *codeagentpb.PlanUpdate, todoUpdate *codeagentpb.TodoUpdate) error {
		current := manager.Current()
		plan := current.Plan
		todos := append([]session.TodoItem(nil), current.Todos...)
		var revision uint64
		if planUpdate != nil {
			plan = session.PlanState{
				Steps:        append([]string(nil), planUpdate.GetSteps()...),
				CurrentIndex: int(planUpdate.GetCurrentIndex()),
				Mode:         planUpdate.GetMode(),
			}
			revision = planUpdate.GetRevision()
		}
		if todoUpdate != nil {
			todos = make([]session.TodoItem, 0, len(todoUpdate.GetTodos()))
			for _, item := range todoUpdate.GetTodos() {
				if item == nil {
					continue
				}
				todos = append(todos, session.TodoItem{
					Content:    item.GetContent(),
					ActiveForm: item.GetActiveForm(),
					Status:     item.GetStatus(),
				})
			}
			revision = todoUpdate.GetRevision()
		}
		if _, ok := manager.ApplyPlanTodoState(plan, todos, revision); !ok {
			return fmt.Errorf("stale revision %d", revision)
		}
		return nil
	}

	firstCmd, firstOutput := startPlanTodoServer(t, root, project, addr)
	firstClient := waitPlanTodoClient(t, addr, firstOutput)
	firstClient.OnPlanTodoUpdate = applyUpdate
	firstReply, err := firstClient.ConverseWithHistoryAndState(
		context.Background(), "STATE_FIRST", created.ID, nil,
		planTodoSnapshotForTest(manager.Current()), nil, nil,
	)
	if err != nil {
		t.Fatalf("first state conversation failed: %v", err)
	}
	if firstReply != "STATE_FIRST_OK" || manager.Current().PlanTodoRevision != 1 {
		t.Fatalf("first state update was not committed: reply=%q session=%+v", firstReply, manager.Current())
	}
	_ = firstClient.Close()
	_ = firstCmd.Process.Kill()
	_ = firstCmd.Wait()

	secondCmd, secondOutput := startPlanTodoServer(t, root, project, addr)
	secondClient := waitPlanTodoClient(t, addr, secondOutput)
	defer func() {
		_ = secondClient.Close()
		if secondCmd.Process != nil {
			_ = secondCmd.Process.Kill()
			_ = secondCmd.Wait()
		}
		_ = manager.Close()
	}()
	secondClient.OnPlanTodoUpdate = applyUpdate
	secondReply, err := secondClient.ConverseWithHistoryAndState(
		context.Background(), "STATE_RESUME", created.ID, nil,
		planTodoSnapshotForTest(manager.Current()), nil, nil,
	)
	if err != nil {
		t.Fatalf("resumed state conversation failed: %v", err)
	}
	current := manager.Current()
	if secondReply != "STATE_RESUME_OK" || current.PlanTodoRevision != 2 || current.Plan.Mode != "plan" || len(current.Plan.Steps) != 2 {
		t.Fatalf("state was not restored after restart: reply=%q session=%+v", secondReply, current)
	}

	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	reloadedManager := session.NewManager(session.NewSQLiteEventStore(filepath.Join(project, "session.sqlite")))
	reloaded, err := reloadedManager.Resume(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer reloadedManager.Close()
	if reloaded.PlanTodoRevision != 2 || reloaded.Todos[0].Status != "in_progress" {
		t.Fatalf("durable state projection did not survive reload: %+v", reloaded)
	}
}

func startPlanTodoServer(t *testing.T, root, project, addr string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatalf("python is required for cross-process E2E: %v", err)
	}
	port := addr[strings.LastIndex(addr, ":")+1:]
	output := &bytes.Buffer{}
	cmd := exec.Command(
		python,
		filepath.Join(root, "tests", "e2e", "plan_todo_state_server.py"),
		"--host", "127.0.0.1",
		"--port", port,
		"--project-root", project,
		"--working-dir", project,
		"--memory-dir", filepath.Join(project, "memory"),
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PYTHONPATH="+root, "PYTHONUTF8=1")
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start Python state server: %v", err)
	}
	return cmd, output
}

func waitPlanTodoClient(t *testing.T, addr string, output *bytes.Buffer) *orchestrator.Client {
	t.Helper()
	client, err := orchestrator.NewClient(addr)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		response, healthErr := client.Health(context.Background())
		if healthErr == nil && response != nil && response.GetStatus() == "ok" {
			return client
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = client.Close()
	t.Fatalf("Python state server did not become healthy: %s", output.String())
	return nil
}

func planTodoSnapshotForTest(current session.Session) *codeagentpb.PlanTodoSnapshot {
	plan := &codeagentpb.PlanUpdate{
		Steps:        append([]string(nil), current.Plan.Steps...),
		CurrentIndex: int32(current.Plan.CurrentIndex),
		Mode:         current.Plan.Mode,
	}
	todos := make([]*codeagentpb.TodoItem, 0, len(current.Todos))
	for _, item := range current.Todos {
		todos = append(todos, &codeagentpb.TodoItem{Content: item.Content, ActiveForm: item.ActiveForm, Status: item.Status})
	}
	return &codeagentpb.PlanTodoSnapshot{SchemaVersion: 1, Revision: current.PlanTodoRevision, Plan: plan, Todos: todos}
}
