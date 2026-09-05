package codeagent_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
	t.Cleanup(func() { _ = manager.Close() })
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

func TestSelectPlanTodoPythonSkipsRuntimeWithoutRequiredPackages(t *testing.T) {
	candidates := []planTodoPythonRuntime{
		{executable: "path-python"},
		{executable: "healthy-python", prefixArgs: []string{"-3"}},
	}
	probed := []string{}

	selected, err := selectPlanTodoPython(candidates, func(candidate planTodoPythonRuntime) bool {
		probed = append(probed, candidate.executable)
		return candidate.executable == "healthy-python"
	})

	if err != nil {
		t.Fatalf("select Python runtime: %v", err)
	}
	if selected.executable != "healthy-python" || len(selected.prefixArgs) != 1 || selected.prefixArgs[0] != "-3" {
		t.Fatalf("selected runtime = %+v, want healthy launcher", selected)
	}
	if strings.Join(probed, ",") != "path-python,healthy-python" {
		t.Fatalf("probed runtimes = %v, want ordered capability probes", probed)
	}
}

func TestProbePlanTodoPythonRuntimeHasHardTimeout(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test source")
	}
	sourceBytes, err := os.ReadFile(currentFile)
	if err != nil {
		t.Fatalf("read current test source: %v", err)
	}
	source := string(sourceBytes)
	if !strings.Contains(source, "context.WithTimeout(context.Background(), timeout)") {
		t.Fatal("Python capability probe must use a hard timeout")
	}
	if !strings.Contains(source, "exec.CommandContext(ctx, candidate.executable, args...)") {
		t.Fatal("Python capability probe must be cancellable")
	}
	if !strings.Contains(source, "taskkill") {
		t.Fatal("Windows Python capability probe must reap the process tree after timeout")
	}
}

type planTodoPythonRuntime struct {
	executable string
	prefixArgs []string
}

func selectPlanTodoPython(candidates []planTodoPythonRuntime, probe func(planTodoPythonRuntime) bool) (planTodoPythonRuntime, error) {
	for _, candidate := range candidates {
		if probe(candidate) {
			return candidate, nil
		}
	}
	return planTodoPythonRuntime{}, fmt.Errorf("no Python runtime provides the required E2E packages")
}

func discoverPlanTodoPythonRuntimes(root string) []planTodoPythonRuntime {
	candidates := make([]planTodoPythonRuntime, 0, 4)
	if configured := strings.TrimSpace(os.Getenv("PYTHON_EXECUTABLE")); configured != "" {
		// setup-python publishes an absolute interpreter path. Keep it first,
		// but normalize a relative override against the repository so Go's
		// package working directory cannot change what the child executes.
		if !filepath.IsAbs(configured) {
			if absolute, err := filepath.Abs(filepath.Join(root, configured)); err == nil {
				configured = absolute
			}
		}
		candidates = append(candidates, planTodoPythonRuntime{executable: configured})
	}
	if runtime.GOOS == "windows" {
		projectPython := filepath.Join(root, ".venv", "Scripts", "python.exe")
		if _, err := os.Stat(projectPython); err == nil {
			if absolute, err := filepath.Abs(projectPython); err == nil {
				candidates = append(candidates, planTodoPythonRuntime{executable: absolute})
			}
		}
	}
	if runtime.GOOS == "windows" {
		if launcher, err := exec.LookPath("py"); err == nil {
			candidates = append(candidates, planTodoPythonRuntime{executable: launcher, prefixArgs: []string{"-3"}})
		}
	}
	for _, name := range []string{"python", "python3"} {
		if executable, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, planTodoPythonRuntime{executable: executable})
		}
	}
	return candidates
}

func probePlanTodoPythonRuntime(candidate planTodoPythonRuntime, root string) bool {
	// Importing LangGraph can be cold on a freshly provisioned hosted runner.
	// Keep a hard bound, but allow enough time for one uncached capability
	// probe before falling back to another interpreter.
	return probePlanTodoPythonRuntimeWithTimeout(candidate, root, 30*time.Second)
}

func probePlanTodoPythonRuntimeWithTimeout(candidate planTodoPythonRuntime, root string, timeout time.Duration) bool {
	args := append([]string{}, candidate.prefixArgs...)
	args = append(args, "-c", "import grpc, langchain_core, langgraph, orchestrator.server")
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, candidate.executable, args...)
	cmd.Dir = root
	cmd.Env = planTodoPythonEnv(root)
	err := cmd.Run()
	if err == nil {
		return true
	}
	if ctx.Err() != nil && runtime.GOOS == "windows" && cmd.Process != nil {
		killCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(killCtx, "taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
	}
	return false
}

func planTodoPythonEnv(root string) []string {
	result := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok && (strings.EqualFold(key, "PYTHONPATH") || strings.EqualFold(key, "PYTHONNOUSERSITE") || strings.EqualFold(key, "PYTHONUTF8")) {
			continue
		}
		result = append(result, entry)
	}
	// Preserve the setup-python site-packages while making the checkout itself
	// importable. Replacing PYTHONPATH (rather than appending an empty value)
	// avoids stale developer paths, and PYTHONNOUSERSITE keeps user-level
	// packages from changing the probe result.
	result = append(result, "PYTHONPATH="+root, "PYTHONNOUSERSITE=1", "PYTHONUTF8=1")
	return result
}

func startPlanTodoServer(t *testing.T, root, project, addr string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	candidates := discoverPlanTodoPythonRuntimes(root)
	python, err := selectPlanTodoPython(candidates, func(candidate planTodoPythonRuntime) bool {
		return probePlanTodoPythonRuntime(candidate, root)
	})
	if err != nil {
		t.Fatalf("Python runtime is required for cross-process E2E: %v", err)
	}
	port := addr[strings.LastIndex(addr, ":")+1:]
	output := &bytes.Buffer{}
	args := append([]string{}, python.prefixArgs...)
	args = append(args,
		filepath.Join(root, "tests", "e2e", "plan_todo_state_server.py"),
		"--host", "127.0.0.1",
		"--port", port,
		"--project-root", project,
		"--working-dir", project,
		"--memory-dir", filepath.Join(project, "memory"),
	)
	cmd := exec.Command(python.executable, args...)
	cmd.Dir = root
	cmd.Env = planTodoPythonEnv(root)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start Python state server: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState != nil || cmd.Process == nil {
			return
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
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
