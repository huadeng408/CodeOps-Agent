package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/mcp"
	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
	"code-agent/internal/tools"
)

type absoluteEditConversation struct {
	path string
}

func (a absoluteEditConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	result := handlers.Tool(ctx, orchestrator.ToolCall{
		ID: "absolute-edit-call", Name: "Edit",
		ParametersJSON: fmt.Sprintf(`{"path":%q,"old":"before","new":"after"}`, a.path),
	})
	if result.Error != "" || result.ExitCode != 0 {
		return orchestrator.ConversationResult{}, fmt.Errorf("Edit failed: %s", result.Error)
	}
	return orchestrator.ConversationResult{Success: true, Message: "edited"}, nil
}

func TestContinuationEditAbsolutePathPersistsCodeModification(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "src", "example.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "edit", "goal", root)
	if err != nil {
		t.Fatal(err)
	}
	executors := newContinuationToolExecutors(root)
	t.Cleanup(func() { _ = executors.Close() })
	runner := session.NewSessionRunner(workbench, absoluteEditConversation{path: path}, executors, session.SessionRunnerOptions{WorkerID: "server-test"})
	t.Cleanup(func() { _ = runner.Close() })
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "test", TenantID: "org:test", Roles: []string{"USER"}}
	run, err := runner.SubmitMessage(ctx, session.SubmitMessageCommand{
		RequestID: "absolute-edit-request", SessionID: created.ID, OwnerID: 7,
		ExpectedSeq: int64(created.EventCount), Content: "edit the file", Actor: actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, viewErr := runner.Run(ctx, created.ID, run.RunID)
		if viewErr == nil && (view.Status == session.RunCompleted || view.Status == session.RunFailed) {
			if view.Status != session.RunCompleted {
				t.Fatalf("run failed: %+v", view)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %+v, err=%v", view, viewErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "after" {
		t.Fatalf("edited file = %q, err=%v", string(data), err)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found map[string]any
	for _, event := range events {
		if event.Type == "code/modified" {
			if err := json.Unmarshal(event.Payload, &found); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if found == nil {
		t.Fatalf("code/modified event missing; event types=%v", eventTypes(events))
	}
	if got, _ := found["path"].(string); got != "src/example.txt" {
		t.Fatalf("code/modified path = %q, want workspace-relative path", found["path"])
	}
}

func eventTypes(events []session.Event) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func TestConfigureContinuationSkillsLoadsProjectSkill(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".agent", "skills", "repo-explorer")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: repo-explorer\ndescription: Explore the repository.\ntools: [Read, Glob, Grep]\n---\ninspect the repository\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	executor := tools.NewExecutor(root)
	defer executor.Close()
	if err := configureContinuationSkills(executor, root); err != nil {
		t.Fatalf("skill discovery: %v", err)
	}
	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Skill", Arguments: map[string]any{"name": "repo-explorer"},
	})
	if err != nil {
		t.Fatalf("execute Skill: %v", err)
	}
	if result.Error != "" || !strings.Contains(result.Output, "inspect the repository") {
		t.Fatalf("unexpected Skill result: %+v", result)
	}
}

func TestConfigureContinuationSkillsUsesDeepSeekProjectPrecedence(t *testing.T) {
	root := t.TempDir()
	for _, item := range []struct {
		dir  string
		body string
	}{
		{filepath.Join(root, ".agents", "skills"), "agents body"},
		{filepath.Join(root, ".dsh", "skills"), "dsh body"},
		{filepath.Join(root, ".agent", "skills"), "legacy body"},
	} {
		dir := filepath.Join(item.dir, "same")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		contents := "---\nname: same\ndescription: " + item.body + "\ntools: [Read]\n---\n" + item.body + "\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	executor := tools.NewExecutor(root)
	defer executor.Close()
	if err := configureContinuationSkills(executor, root); err != nil {
		t.Fatalf("skill discovery: %v", err)
	}
	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Skill", Arguments: map[string]any{"name": "same"},
	})
	if err != nil {
		t.Fatalf("execute Skill: %v", err)
	}
	if result.Error != "" || !strings.Contains(result.Output, "dsh body") {
		t.Fatalf("project .dsh Skill should win, got %+v", result)
	}
}

func TestContinuationToolExecutorsIsolateConcurrentWorkingDirs(t *testing.T) {
	root := t.TempDir()
	left := filepath.Join(root, "left")
	right := filepath.Join(root, "right")
	for dir, marker := range map[string]string{left: "LEFT_MARKER", right: "RIGHT_MARKER"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(marker), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager := newContinuationToolExecutors(root)
	defer manager.Close()

	type result struct {
		output string
		err    string
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, tc := range []struct {
		sessionID  string
		workingDir string
		marker     string
	}{
		{sessionID: "session-left", workingDir: left, marker: "LEFT_MARKER"},
		{sessionID: "session-right", workingDir: right, marker: "RIGHT_MARKER"},
	} {
		tc := tc
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := manager.ExecuteInWorkingDir(context.Background(), identity.Actor{}, tc.sessionID, tc.workingDir, orchestrator.ToolCall{
				ID: tc.sessionID, Name: "Read", ParametersJSON: `{"path":"README.md"}`,
			})
			results <- result{output: got.Output, err: got.Error}
			if !strings.Contains(got.Output, tc.marker) {
				t.Errorf("session %s read %q, want %s", tc.sessionID, got.Output, tc.marker)
			}
		}()
	}
	wg.Wait()
	close(results)
	for got := range results {
		if got.err != "" {
			t.Fatalf("isolated read failed: %s", got.err)
		}
	}
}

func TestConfigureContinuationSkillsKeepsBuiltinsWhenOptionalDirectoryMissing(t *testing.T) {
	root := t.TempDir()
	executor := tools.NewExecutor(root)
	defer executor.Close()
	if err := configureContinuationSkills(executor, root); err != nil {
		t.Fatalf("missing optional directory should not fail discovery: %v", err)
	}
	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Skill", Arguments: map[string]any{"name": "commit"},
	})
	if err != nil || result.Error != "" || result.Output == "" {
		t.Fatalf("built-in Skill should remain available: err=%v result=%+v", err, result)
	}
}

func TestContinuationChildDoesNotReuseHarnessMCPProcess(t *testing.T) {
	root := t.TempDir()
	mcpManager := mcp.NewManager()
	mcpManager.RegisterTool(mcp.ToolDefinition{Name: "e2e_echo", Server: "fixture"})
	manager := newContinuationToolExecutors(root)
	manager.SetMCPManager(mcpManager)
	defer manager.Close()

	result := manager.ExecuteInWorkingDir(context.Background(), identity.Actor{}, "agent-child", root, orchestrator.ToolCall{
		ID: "mcp-call", Name: "e2e_echo", ParametersJSON: `{"text":"hello"}`,
	})
	if !strings.Contains(result.Error, "unknown tool") {
		t.Fatalf("child executor reused the parent MCP catalog: %+v", result)
	}
}

func TestContinuationToolExecutorsReleaseOneSession(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"child", "child-other"} {
		path := filepath.Join(root, dir)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "README.md"), []byte(dir), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager := newContinuationToolExecutors(root)
	defer manager.Close()
	for _, tc := range []struct{ sessionID, dir string }{{"agent-child", "child"}, {"agent-child-other", "child-other"}} {
		result := manager.ExecuteInWorkingDir(context.Background(), identity.Actor{}, tc.sessionID, filepath.Join(root, tc.dir), orchestrator.ToolCall{
			ID: tc.sessionID, Name: "Read", ParametersJSON: `{"path":"README.md"}`,
		})
		if result.Error != "" {
			t.Fatalf("create executor for %s: %s", tc.sessionID, result.Error)
		}
	}
	if err := manager.ReleaseSession("agent-child"); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.items) != 1 {
		t.Fatalf("executor count after release = %d, want 1", len(manager.items))
	}
	for key := range manager.items {
		if !strings.HasPrefix(key, "agent-child-other\x00") {
			t.Fatalf("wrong executor survived release: %q", key)
		}
	}
}
