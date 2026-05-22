package codeagent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-agent/internal/mcp"
	"code-agent/internal/tools"
)

func TestMCPManagerLoadsConfigFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(configPath, []byte(`{
		"servers": {
			"search": {
				"command": "agent-search",
				"args": ["--stdio"],
				"working_dir": "tools"
			}
		}
	}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	manager := mcp.NewManager()
	if err := manager.LoadConfigFile(configPath); err != nil {
		t.Fatalf("load config: %v", err)
	}

	servers := manager.ListServers()
	if len(servers) != 1 {
		t.Fatalf("expected one server, got %#v", servers)
	}
	if servers[0].Name != "search" {
		t.Fatalf("expected inferred server name, got %q", servers[0].Name)
	}
	if servers[0].Command != "agent-search" {
		t.Fatalf("unexpected command: %q", servers[0].Command)
	}
	if want := filepath.Join(dir, "tools"); servers[0].WorkingDir != want {
		t.Fatalf("expected working dir %q, got %q", want, servers[0].WorkingDir)
	}
}

func TestMCPManagerStartAllDegradesOnInvalidServer(t *testing.T) {
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{Name: "broken"})

	errs := manager.StartAll(context.Background())
	if len(errs) != 1 {
		t.Fatalf("expected one start error, got %#v", errs)
	}
	if !strings.Contains(errs[0].Error(), "broken") {
		t.Fatalf("expected server name in error, got %v", errs[0])
	}

	snapshot := manager.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected one snapshot item, got %#v", snapshot)
	}
	if snapshot[0].State != mcp.ServerStopped {
		t.Fatalf("expected stopped state, got %s", snapshot[0].State)
	}
	if snapshot[0].LastError == "" {
		t.Fatal("expected last error to be recorded")
	}
}

func TestMCPManagerMissingConfigIsNoop(t *testing.T) {
	manager := mcp.NewManager()

	if err := manager.LoadConfigFile(filepath.Join(t.TempDir(), "missing.json")); err != nil {
		t.Fatalf("missing config should be ignored: %v", err)
	}
	if len(manager.ListServers()) != 0 {
		t.Fatalf("expected no servers, got %#v", manager.ListServers())
	}
}

func TestMCPManagerDiscoversAndCallsStdioTool(t *testing.T) {
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{
		Name:    "fake",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPHelperProcess", "--", "mcp"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := manager.Start(ctx, "fake"); err != nil {
		t.Fatalf("start fake mcp server: %v", err)
	}
	defer manager.Stop("fake")

	tools := manager.ListTools()
	if len(tools) != 1 {
		t.Fatalf("expected one discovered tool, got %#v", tools)
	}
	if tools[0].Name != "fake_echo" || tools[0].Server != "fake" {
		t.Fatalf("unexpected discovered tool: %#v", tools[0])
	}

	result, err := manager.CallTool(ctx, "fake_echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("call fake tool: %v", err)
	}
	if result.IsError || len(result.Content) != 1 || result.Content[0].Text != "echo: hello" {
		t.Fatalf("unexpected tool result: %#v", result)
	}
}

func TestMCPManagerSupportsCamelCaseInputSchema(t *testing.T) {
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{
		Name:    "fake-camel",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPHelperProcess", "--", "mcp"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"FAKE_MCP_SCHEMA":        "camel",
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := manager.Start(ctx, "fake-camel"); err != nil {
		t.Fatalf("start fake mcp server: %v", err)
	}
	defer manager.Stop("fake-camel")

	tools := manager.ListTools()
	if len(tools) != 1 {
		t.Fatalf("expected one discovered tool, got %#v", tools)
	}
	if !strings.Contains(string(tools[0].InputSchema), "text") {
		t.Fatalf("expected input schema to be captured, got %s", string(tools[0].InputSchema))
	}
}

func TestExecutorCallsDiscoveredMCPTool(t *testing.T) {
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{
		Name:    "fake",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPHelperProcess", "--", "mcp"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := manager.Start(ctx, "fake"); err != nil {
		t.Fatalf("start fake mcp server: %v", err)
	}
	defer manager.Stop("fake")

	executor := tools.NewExecutor(t.TempDir())
	executor.SetMCPManager(manager)
	result, err := executor.Execute(ctx, tools.ToolRequest{
		Name:      "fake_echo",
		Arguments: map[string]any{"text": "from executor"},
	})
	if err != nil {
		t.Fatalf("execute mcp tool: %v", err)
	}
	if result.ExitCode != 0 || result.Output != "echo: from executor" {
		t.Fatalf("unexpected mcp execute result: %#v", result)
	}
}

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	runFakeMCPServer()
	os.Exit(0)
}

func runFakeMCPServer() {
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var req mcp.Request
		if err := decoder.Decode(&req); err != nil {
			return
		}
		if req.ID == nil {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake"},
			}
		case "tools/list":
			schema := map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{"type": "string"},
				},
			}
			if os.Getenv("FAKE_MCP_SCHEMA") == "camel" {
				result = map[string]any{
					"tools": []map[string]any{{
						"name":        "fake_echo",
						"description": "Echo text.",
						"inputSchema": schema,
					}},
				}
				break
			}
			result = map[string]any{
				"tools": []map[string]any{{
					"name":         "fake_echo",
					"description":  "Echo text.",
					"input_schema": schema,
				}},
			}
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			result = map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": fmt.Sprintf("echo: %s", params.Arguments["text"]),
				}},
			}
		default:
			_ = encoder.Encode(mcp.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &mcp.ErrorObject{Code: -32601, Message: "method not found"},
			})
			continue
		}
		data, _ := json.Marshal(result)
		_ = encoder.Encode(mcp.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  data,
		})
	}
}
