package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"

	"code-agent/internal/identity"
	"code-agent/internal/mcp"
	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
)

func TestContinuationRepositoryMCPProcess(t *testing.T) {
	if os.Getenv("GO_WANT_REPOSITORY_MCP") != "1" {
		return
	}
	if err := mcp.RunRepositoryServer(context.Background(), "."); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

type repositoryMCPConversation struct {
	manager *mcp.Manager
	t       *testing.T
}

func (conversation repositoryMCPConversation) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	if request.MCPCatalog == nil || len(request.MCPCatalog.Tools) != 1 || request.MCPCatalog.Tools[0].Name != "RepositoryManifest" {
		return orchestrator.ConversationResult{}, fmt.Errorf("Go MCP catalog is unavailable to the model")
	}
	result := handlers.Tool(ctx, orchestrator.ToolCall{ID: "manifest", Name: "RepositoryManifest", ParametersJSON: `{"file":"go.mod"}`})
	if result.ExitCode != 0 || result.Error != "" || !strings.Contains(result.Output, "example.org/task") || strings.Contains(result.Output, "example.org/parent") {
		return orchestrator.ConversationResult{}, fmt.Errorf("MCP escaped the task workspace: %+v", result)
	}
	binding, _ := conversation.manager.ResolveBinding("RepositoryManifest")
	if err := conversation.manager.Stop(binding.Server); err != nil {
		return orchestrator.ConversationResult{}, err
	}
	result = handlers.Tool(ctx, orchestrator.ToolCall{ID: "revoked-new-call", Name: binding.Name, ParametersJSON: `{"file":"pyproject.toml"}`})
	if result.ExitCode == 0 || !strings.Contains(result.Error, "mcp_refused") || result.OutcomeUnknown {
		conversation.t.Errorf("revoked task MCP result: %+v", result)
		return orchestrator.ConversationResult{}, fmt.Errorf("revoked task MCP was not refused: %+v", result)
	}
	return orchestrator.ConversationResult{Success: true, Message: "task manifest verified"}, nil
}

func TestContinuationMCPUsesOwnedTaskWorkspace(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first native task workspace pin contract")
	}
	ctx := context.Background()
	repo, storage, parent := t.TempDir(), t.TempDir(), t.TempDir()
	for path, content := range map[string]string{filepath.Join(repo, "go.mod"): "module example.org/task\n\ngo 1.25\n", filepath.Join(parent, "go.mod"): "module example.org/parent\n\ngo 1.25\n"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "fixture"}, {"config", "user.email", "fixture@example.test"}, {"add", "go.mod"}, {"commit", "-qm", "fixture"}} {
		if output, err := exec.Command("git", append([]string{"-c", "core.hooksPath=", "-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("prepare Git fixture: %v %s", err, output)
		}
	}
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "manifest", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	executors := newContinuationToolExecutors(storage)
	defer executors.Close()
	owner := func(context.Context) (uint, error) { return 7, nil }
	tasks := session.NewTaskWorkspaces(workbench, owner, repo, storage).WithBaselineRuntime(session.TaskBaselineRuntime{
		Toolchain: "go", Runner: executors.sandbox, Ready: func(context.Context) error { return nil },
		Acquire: func(context.Context, uint, string) error { return nil }, Finish: func(context.Context, uint, string) error { return nil },
	})
	prepared, err := tasks.Prepare(ctx, 7, created.ID, 1, "prepare-manifest")
	if err != nil || prepared.State != "prepared" {
		t.Fatalf("prepare task: %+v %v", prepared, err)
	}
	if _, err := tasks.ModelWorkspace(ctx, 8, created.ID); err == nil {
		t.Fatal("another owner acquired the task workspace")
	}
	cfg, err := mcp.RepositoryServerConfig(os.Args[0], []string{"-test.run=TestContinuationRepositoryMCPProcess"}, parent)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Env = map[string]string{"GO_WANT_REPOSITORY_MCP": "1"}
	manager := mcp.NewManager()
	manager.RegisterServer(cfg)
	if err := manager.Start(ctx, cfg.Name); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	executors.taskWorkspaces, executors.taskOwner = tasks, owner
	executors.SetMCPManager(manager)
	binding, ok := manager.ResolveBinding("RepositoryManifest")
	if !ok {
		t.Fatal("approved MCP binding missing")
	}
	runner := session.NewSessionRunner(workbench, repositoryMCPConversation{manager: manager, t: t}, executors, session.SessionRunnerOptions{WorkerID: "mcp-test", Workspace: func(ctx context.Context, owner uint, id string) (session.TaskModelWorkspace, error) {
		workspace, err := tasks.ModelWorkspace(ctx, owner, id)
		if err == nil {
			err = executors.PrepareWorkspace(ctx, id, workspace.WorkingDir)
		}
		return workspace, err
	}, MCPCatalog: func() *codeagentpb.MCPToolCatalog {
		tool := manager.ListTools()[0]
		return &codeagentpb.MCPToolCatalog{SchemaVersion: 1, Tools: []*codeagentpb.MCPToolDefinition{{Name: tool.Name, Server: tool.Server, InputSchemaJson: string(tool.InputSchema), InputSchemaSha256: tool.InputSchemaSHA256, ServerConfigSha256: tool.ServerConfigSHA256}}}
	},
		AgentToolBinding: func(name string) (session.AgentToolBinding, bool) {
			resolved, ok := manager.ResolveBinding(name)
			return session.AgentToolBinding{Name: resolved.Name, Server: resolved.Server, InputSchemaSHA256: resolved.InputSchemaSHA256, ServerConfigSHA256: resolved.ServerConfigSHA256}, ok
		}})
	defer runner.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "test", TenantID: "org:test", Roles: []string{"USER"}}
	accepted, err := runner.SubmitMessage(ctx, session.SubmitMessageCommand{RequestID: "manifest", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(prepared.EventCount), Content: "inspect manifest", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		view, err := runner.Run(ctx, created.ID, accepted.RunID)
		if err == nil && (view.Status == session.RunCompleted || view.Status == session.RunFailed) {
			if view.Status != session.RunCompleted {
				t.Fatalf("task MCP failed: %+v", view)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task MCP did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	events, err := ledger.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	results := 0
	calls := 0
	boundCalls := 0
	successfulResults := 0
	for _, event := range events {
		if event.Type == "tool/call" {
			var call struct {
				MCPBinding *mcp.ToolBinding `json:"mcp_binding"`
			}
			if err := json.Unmarshal(event.Payload, &call); err != nil || (call.MCPBinding != nil && *call.MCPBinding != binding) {
				t.Fatalf("canonical call lost its approved MCP binding: %+v %v", call, err)
			}
			if call.MCPBinding != nil {
				boundCalls++
			}
			calls++
		}
		if event.Type != "tool/result" {
			continue
		}
		var result struct {
			ToolName string `json:"tool_name"`
			ExitCode int    `json:"exit_code"`
		}
		if err := json.Unmarshal(event.Payload, &result); err != nil || result.ToolName != "RepositoryManifest" {
			t.Fatalf("canonical MCP result missing: %+v %v", result, err)
		}
		if result.ExitCode == 0 {
			successfulResults++
		}
		results++
	}
	if results != 2 || calls != 2 || successfulResults != 1 || boundCalls != 1 {
		t.Fatalf("expected a bound success and a revoked new call, got calls=%d results=%d successful=%d bound=%d", calls, results, successfulResults, boundCalls)
	}
}
