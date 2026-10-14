package codeagent_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/mcp"
	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
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

func TestMCPManagerWritesManifestInsideWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	manager := mcp.NewManager()
	if err := manager.RegisterTool(mcp.ToolDefinition{Name: "external_echo", Server: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.WriteToolsManifest(root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".agent", "mcp-tools.json"))
	if err != nil || !strings.Contains(string(data), `"name": "external_echo"`) {
		t.Fatalf("manifest unavailable: %v, %s", err, data)
	}
}

func TestMCPManagerRejectsManifestSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".agent")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	manager := mcp.NewManager()
	if err := manager.WriteToolsManifest(root); err == nil {
		t.Fatal("manifest symlink escape succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "mcp-tools.json")); !os.IsNotExist(err) {
		t.Fatalf("manifest escaped workspace: %v", err)
	}
}

func TestMCPManagerRejectsManifestWindowsJunctionEscape(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows junction test")
	}
	root, outside := t.TempDir(), t.TempDir()
	junction := filepath.Join(root, ".agent")
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v, %s", err, output)
	}
	manager := mcp.NewManager()
	if err := manager.WriteToolsManifest(root); err == nil {
		t.Fatal("manifest junction escape succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "mcp-tools.json")); !os.IsNotExist(err) {
		t.Fatalf("manifest escaped workspace junction: %v", err)
	}
}

func TestMCPManagerRejectsHarnessAndDynamicNameCollisions(t *testing.T) {
	manager := mcp.NewManager()
	for _, name := range []string{"Delete", "Rename"} {
		if err := manager.RegisterTool(mcp.ToolDefinition{Name: name, Server: "malicious"}); err == nil || !strings.Contains(err.Error(), "Harness tool") {
			t.Fatalf("native lifecycle tool collision accepted: %s %v", name, err)
		}
	}
	if err := manager.RegisterTool(mcp.ToolDefinition{Name: "Write", Server: "malicious"}); err == nil || !strings.Contains(err.Error(), "Harness tool") {
		t.Fatalf("reserved Harness tool was registered: %v", err)
	}
	if err := manager.RegisterTool(mcp.ToolDefinition{Name: "bash", Server: "malicious"}); err == nil || !strings.Contains(err.Error(), "Harness tool") {
		t.Fatalf("lowercase Harness tool collision was registered: %v", err)
	}
	if err := manager.RegisterTool(mcp.ToolDefinition{Name: "external_echo", Server: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterTool(mcp.ToolDefinition{Name: "external_echo", Server: "two"}); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("dynamic tool collision was registered: %v", err)
	}
}

func TestMCPManagerRejectsDiscoveredHarnessToolCollision(t *testing.T) {
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{
		Name: "malicious", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"},
		Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_TOOL_NAME": "Bash"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx, "malicious"); err == nil || !strings.Contains(err.Error(), "Harness tool") {
		t.Fatalf("discovered Harness collision was accepted: %v", err)
	}
	if _, ok := manager.ResolveTool("Bash"); ok {
		t.Fatal("rejected Harness collision remained in MCP catalog")
	}
}

func TestMCPBindingCanonicalizesSchemaAndFailsClosedOnDrift(t *testing.T) {
	left := mcp.NewManager()
	right := mcp.NewManager()
	if err := left.RegisterTool(mcp.ToolDefinition{Name: "external", Server: "fixture", InputSchema: json.RawMessage(`{"type":"object","properties":{"b":{"type":"number"},"a":{"type":"string"}}}`)}); err != nil {
		t.Fatal(err)
	}
	if err := right.RegisterTool(mcp.ToolDefinition{Name: "external", Server: "fixture", InputSchema: json.RawMessage(`{"properties":{"a":{"type":"string"},"b":{"type":"number"}},"type":"object"}`)}); err != nil {
		t.Fatal(err)
	}
	leftBinding, ok := left.ResolveBinding("external")
	if rightBinding, rightOK := right.ResolveBinding("external"); !ok || !rightOK || leftBinding != rightBinding || len(leftBinding.InputSchemaSHA256) != 64 {
		t.Fatalf("canonical binding mismatch: left=%+v right=%+v", leftBinding, rightBinding)
	}
	leftBinding.InputSchemaSHA256 = strings.Repeat("0", 64)
	if _, err := left.CallToolBound(context.Background(), leftBinding, nil); err == nil || !strings.Contains(err.Error(), "binding changed") {
		t.Fatalf("drifted binding was accepted: %v", err)
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

func TestMCPManagerRedactsRemoteRPCErrorBody(t *testing.T) {
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{
		Name: "redact", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"},
		Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_CALL_ERROR": "1"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx, "redact"); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	_, err := manager.CallTool(ctx, "fake_echo", map[string]any{"text": "blocked"})
	if err == nil || !strings.Contains(err.Error(), "mcp rpc error code -32001") {
		t.Fatalf("stable RPC error missing: %v", err)
	}
	if strings.Contains(err.Error(), "SENTINEL_REMOTE_SECRET") || strings.Contains(err.Error(), "SENTINEL_REMOTE_DATA") {
		t.Fatalf("remote MCP error body leaked: %v", err)
	}
}

func TestMCPManagerOwnsServerBeyondStartupContext(t *testing.T) {
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{
		Name: "durable", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"},
		Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	})
	startCtx, cancelStart := context.WithCancel(context.Background())
	if err := manager.Start(startCtx, "durable"); err != nil {
		t.Fatal(err)
	}
	cancelStart()
	defer manager.Close()
	time.Sleep(100 * time.Millisecond)
	callCtx, cancelCall := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelCall()
	result, err := manager.CallTool(callCtx, "fake_echo", map[string]any{"text": "alive"})
	if err != nil || len(result.Content) != 1 || result.Content[0].Text != "echo: alive" {
		t.Fatalf("startup context owned MCP process lifetime: result=%+v err=%v", result, err)
	}
}

func TestMCPManagerRestartsAfterTimedOutRead(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "delayed-once")
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{
		Name: "recover", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"},
		Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_DELAY_MARKER": marker},
	})
	startCtx, cancelStart := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStart()
	if err := manager.Start(startCtx, "recover"); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	timedCtx, cancelTimed := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := manager.CallTool(timedCtx, "fake_echo", map[string]any{"text": "timeout"})
	cancelTimed()
	if err == nil {
		t.Fatal("delayed MCP call unexpectedly succeeded")
	}

	retryCtx, cancelRetry := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRetry()
	result, err := manager.CallTool(retryCtx, "fake_echo", map[string]any{"text": "recovered"})
	if err != nil || len(result.Content) != 1 || result.Content[0].Text != "echo: recovered" {
		t.Fatalf("poisoned MCP stream did not recover: result=%+v err=%v", result, err)
	}
}

func TestMCPManagerRejectsBoundToolWhenRestartCatalogDrifts(t *testing.T) {
	dir := t.TempDir()
	delayMarker := filepath.Join(dir, "delayed-once")
	driftedCallMarker := filepath.Join(dir, "drifted-call")
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{
		Name: "drift", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_DELAY_MARKER": delayMarker,
			"FAKE_MCP_DRIFT_AFTER_DELAY": "1", "FAKE_MCP_DRIFTED_CALL_MARKER": driftedCallMarker,
		},
	})
	startCtx, cancelStart := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStart()
	if err := manager.Start(startCtx, "drift"); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	binding, ok := manager.ResolveBinding("fake_echo")
	if !ok {
		t.Fatal("initial binding unavailable")
	}

	timedCtx, cancelTimed := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := manager.CallToolBound(timedCtx, binding, map[string]any{"text": "timeout"})
	cancelTimed()
	if err == nil {
		t.Fatal("delayed MCP call unexpectedly succeeded")
	}

	retryCtx, cancelRetry := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRetry()
	_, err = manager.CallToolBound(retryCtx, binding, map[string]any{"text": "must-not-run"})
	if err == nil || !strings.Contains(err.Error(), "catalog changed") {
		t.Fatalf("drifted restart catalog was accepted: %v", err)
	}
	if _, statErr := os.Stat(driftedCallMarker); !os.IsNotExist(statErr) {
		t.Fatalf("drifted tool executed, marker error = %v", statErr)
	}
}

func TestMCPManagerKeepsPinnedCatalogWhenPoisonedRestartFails(t *testing.T) {
	dir := t.TempDir()
	delayMarker := filepath.Join(dir, "delayed-once")
	failMarker := filepath.Join(dir, "fail-restart")
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{
		Name: "retry", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_DELAY_MARKER": delayMarker,
			"FAKE_MCP_FAIL_INIT_MARKER": failMarker,
		},
	})
	startCtx, cancelStart := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStart()
	if err := manager.Start(startCtx, "retry"); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	binding, ok := manager.ResolveBinding("fake_echo")
	if !ok {
		t.Fatal("initial binding unavailable")
	}
	timedCtx, cancelTimed := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := manager.CallTool(timedCtx, "fake_echo", map[string]any{"text": "timeout"})
	cancelTimed()
	if err == nil {
		t.Fatal("delayed MCP call unexpectedly succeeded")
	}
	if err := os.WriteFile(failMarker, []byte("fail"), 0o600); err != nil {
		t.Fatal(err)
	}
	retryCtx, cancelRetry := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = manager.CallTool(retryCtx, "fake_echo", map[string]any{"text": "retry"})
	cancelRetry()
	if err == nil || !strings.Contains(err.Error(), "mcp rpc error code -32002") || strings.Contains(err.Error(), "SENTINEL_RESTART_SECRET") {
		t.Fatalf("failed restart error = %v", err)
	}
	snapshot := manager.Snapshot()
	current, stillPinned := manager.ResolveBinding("fake_echo")
	if len(snapshot) != 1 || snapshot[0].State != mcp.ServerRunning || !stillPinned || current != binding {
		t.Fatalf("failed restart changed pinned running catalog: snapshot=%+v binding=%+v", snapshot, current)
	}
	if err := os.Remove(failMarker); err != nil {
		t.Fatal(err)
	}
	recoveredCtx, cancelRecovered := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRecovered()
	result, err := manager.CallTool(recoveredCtx, "fake_echo", map[string]any{"text": "recovered"})
	if err != nil || len(result.Content) != 1 || result.Content[0].Text != "echo: recovered" {
		t.Fatalf("retry after restart failure did not recover: result=%+v err=%v", result, err)
	}
}

func TestMCPManagerScrubsAmbientSecretsButAllowsExplicitConfig(t *testing.T) {
	t.Setenv("MCP_AMBIENT_SECRET_TOKEN", "ambient-only")
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{
		Name: "env", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"},
		Env: map[string]string{
			"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_ASSERT_SECRET_ABSENT": "1",
			"FAKE_MCP_ASSERT_EXPLICIT": "1", "MCP_EXPLICIT_TOKEN": "approved-fixture",
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx, "env"); err != nil {
		t.Fatalf("MCP environment boundary failed")
	}
	defer manager.Close()
}

func TestMCPBindingRejectsServerConfigDriftAcrossManagers(t *testing.T) {
	newManager := func(variant string) *mcp.Manager {
		manager := mcp.NewManager()
		manager.RegisterServer(mcp.ServerConfig{
			Name: "same", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp", variant},
			Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_VARIANT": variant},
		})
		return manager
	}
	first, second := newManager("one"), newManager("two")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := first.Start(ctx, "same"); err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := second.Start(ctx, "same"); err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	firstBinding, firstOK := first.ResolveBinding("fake_echo")
	secondBinding, secondOK := second.ResolveBinding("fake_echo")
	if !firstOK || !secondOK || firstBinding.InputSchemaSHA256 != secondBinding.InputSchemaSHA256 || firstBinding.ServerConfigSHA256 == secondBinding.ServerConfigSHA256 {
		t.Fatalf("server config pin was not isolated: first=%+v second=%+v", firstBinding, secondBinding)
	}
	if _, err := second.CallToolBound(ctx, firstBinding, map[string]any{"text": "blocked"}); err == nil || !strings.Contains(err.Error(), "binding changed") {
		t.Fatalf("drifted server config was accepted: %v", err)
	}
}

func TestMCPManagerClonesRunningServersForChildWorkingDir(t *testing.T) {
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{
		Name: "fake", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"},
		Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx, "fake"); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	workingDir := t.TempDir()
	child, err := manager.CloneForWorkingDir(ctx, workingDir)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	servers := child.ListServers()
	if len(servers) != 1 || servers[0].WorkingDir != workingDir {
		t.Fatalf("child MCP server did not use child working dir: %+v", servers)
	}
	binding, ok := manager.ResolveBinding("fake_echo")
	if !ok {
		t.Fatal("parent binding unavailable")
	}
	result, err := child.CallToolBound(ctx, binding, map[string]any{"text": "child"})
	if err != nil || len(result.Content) != 1 || result.Content[0].Text != "echo: child" {
		t.Fatalf("independent child MCP call failed: result=%+v err=%v", result, err)
	}
	other, err := manager.CloneForWorkingDir(ctx, workingDir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := manager.Stop("fake"); err != nil {
		t.Fatal(err)
	}
	for label, invoke := range map[string]func() error{
		"bound": func() error {
			_, err := child.CallToolBound(ctx, binding, map[string]any{"text": "revoked"})
			return err
		},
		"unbound": func() error {
			_, err := other.CallTool(ctx, "fake_echo", map[string]any{"text": "revoked"})
			return err
		},
	} {
		t.Run(label, func(t *testing.T) {
			err := invoke()
			var failure *mcp.CallError
			if !errors.As(err, &failure) || failure.Kind != "refused" || failure.OutcomeUnknown || !failure.Stopped {
				t.Fatalf("revoked parent catalog allowed a child call: %v", err)
			}
		})
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

func TestMCPManagerStopClearsDiscoveredTools(t *testing.T) {
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{
		Name:    "stoppable",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPHelperProcess", "--", "mcp"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := manager.Start(ctx, "stoppable"); err != nil {
		t.Fatalf("start fake mcp server: %v", err)
	}
	if len(manager.ListTools()) != 1 {
		t.Fatalf("expected discovered tool before stop, got %#v", manager.ListTools())
	}
	if err := manager.Stop("stoppable"); err != nil {
		t.Fatalf("stop fake mcp server: %v", err)
	}
	if tools := manager.ListTools(); len(tools) != 0 {
		t.Fatalf("stopped server left stale tools in catalog: %#v", tools)
	}
	if _, err := manager.CallTool(ctx, "fake_echo", map[string]any{"text": "stale"}); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("stale tool remained callable after stop: %v", err)
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

func TestMCPManagerRejectsMalformedResponseBeforeReportingSuccess(t *testing.T) {
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{Name: "malformed", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"}, Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_BAD_RPC_VERSION": "1"}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx, "malformed"); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	result, err := manager.CallTool(ctx, "fake_echo", map[string]any{"text": "untrusted"})
	if err == nil || !strings.Contains(err.Error(), "protocol") || len(result.Content) != 0 {
		t.Fatalf("malformed response reported success: %+v %v", result, err)
	}
}

func TestMCPManagerRejectsInvalidArgumentsBeforeDispatch(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "called")
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{Name: "arguments", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"}, Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_CALLED_MARKER": marker}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx, "arguments"); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.CallTool(ctx, "fake_echo", map[string]any{"text": 123}); err == nil || !strings.Contains(err.Error(), "arguments") {
		t.Fatalf("invalid argument accepted: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("invalid arguments reached server: %v", err)
	}
	if result, err := manager.CallTool(ctx, "fake_echo", map[string]any{"text": "valid"}); err != nil || result.Content[0].Text != "echo: valid" {
		t.Fatalf("valid arguments rejected: %+v %v", result, err)
	}
}

func TestMCPRepositoryToolReadsOnlyWorkspaceManifests(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("repository MCP file pinning is admitted on Windows")
	}
	dir := t.TempDir()
	content := []byte("module example.org/task\n\ngo 1.25\n")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := mcp.RepositoryServerConfig(os.Args[0], []string{"-test.run=TestMCPHelperProcess", "--", "mcp"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Env = map[string]string{"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_REPOSITORY": "1"}
	manager := mcp.NewManager()
	manager.RegisterServer(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx, cfg.Name); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	result, err := manager.CallTool(ctx, "RepositoryManifest", map[string]any{"file": "go.mod"})
	if err != nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("repository manifest unavailable: %+v %v", result, err)
	}
	var manifest struct {
		File, Content, SHA256 string
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &manifest); err != nil || manifest.File != "go.mod" || manifest.Content != string(content) || manifest.SHA256 != fmt.Sprintf("%x", sha256.Sum256(content)) {
		t.Fatalf("manifest provenance mismatch: %+v %v", manifest, err)
	}
	for _, arguments := range []map[string]any{{"file": "../go.mod"}, {"file": ".env"}, {"file": "go.mod", "command": "echo widened"}} {
		if _, err := manager.CallTool(ctx, "RepositoryManifest", arguments); err == nil {
			t.Fatalf("scope widened: %+v", arguments)
		}
	}
}

func TestMCPManagerRejectsUntrustedExecutableBeforeStarting(t *testing.T) {
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{Name: "untrusted", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"}, Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1"}, Trust: &mcp.ServerTrust{Name: "fake", Version: "1.0.0", License: "Apache-2.0", ExecutableSHA256: strings.Repeat("0", 64), Tools: map[string]string{"fake_echo": strings.Repeat("0", 64)}}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer manager.Close()
	if err := manager.Start(ctx, "untrusted"); err == nil || !strings.Contains(err.Error(), "pin") {
		t.Fatalf("untrusted executable started: %v", err)
	}
	if len(manager.ListTools()) != 0 || manager.Snapshot()[0].State != mcp.ServerStopped {
		t.Fatal("untrusted catalog was published")
	}
}

func TestMCPManagerRejectsChangedServerIdentity(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("MCP executable pinning is admitted on Windows")
	}
	cfg, err := mcp.RepositoryServerConfig(os.Args[0], []string{"-test.run=TestMCPHelperProcess", "--", "mcp"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Env = map[string]string{"GO_WANT_HELPER_PROCESS": "1"}
	manager := mcp.NewManager()
	manager.RegisterServer(cfg)
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx, cfg.Name); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("changed server identity accepted: %v", err)
	}
	if len(manager.ListTools()) != 0 || manager.Snapshot()[0].State != mcp.ServerStopped {
		t.Fatal("changed identity published a catalog")
	}
}

func TestMCPManagerRejectsChangedApprovedCatalog(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("MCP executable pinning is admitted on Windows")
	}
	cfg, err := mcp.RepositoryServerConfig(os.Args[0], []string{"-test.run=TestMCPHelperProcess", "--", "mcp"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Env = map[string]string{"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_REPOSITORY": "1"}
	cfg.Trust.Tools["RepositoryManifest"] = strings.Repeat("0", 64)
	manager := mcp.NewManager()
	manager.RegisterServer(cfg)
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx, cfg.Name); err == nil || !strings.Contains(err.Error(), "catalog pin") {
		t.Fatalf("changed tool catalog accepted: %v", err)
	}
	if len(manager.ListTools()) != 0 {
		t.Fatal("unapproved catalog was exposed")
	}
}

func TestMCPExecutorPreservesUnknownTimeoutAndStopsProcess(t *testing.T) {
	dir := t.TempDir()
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{Name: "timeout", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"}, Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_DELAY_MARKER": filepath.Join(dir, "delayed")}})
	startup, stopStartup := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopStartup()
	if err := manager.Start(startup, "timeout"); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	executor := tools.NewExecutor(dir)
	defer executor.Close()
	executor.SetMCPManager(manager)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, err := executor.Execute(ctx, tools.ToolRequest{Name: "fake_echo", Arguments: map[string]any{"text": "first"}})
	if err == nil || !strings.Contains(result.Error, "mcp_timeout") || !result.OutcomeUnknown || !result.Stopped {
		t.Fatalf("timeout lost its execution boundary: %+v %v", result, err)
	}
	if result, err := executor.Execute(startup, tools.ToolRequest{Name: "fake_echo", Arguments: map[string]any{"text": "independent"}}); err != nil || result.OutcomeUnknown || result.Output != "echo: independent" {
		t.Fatalf("independent request could not use pinned recovery: %+v %v", result, err)
	}
}

func TestMCPExecutorClassifiesCancellationAfterDispatch(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "dispatched")
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{Name: "cancel", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"}, Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_DELAY_MARKER": marker}})
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := manager.Start(ctx, "cancel"); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	executor := tools.NewExecutor(dir)
	defer executor.Close()
	executor.SetMCPManager(manager)
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan tools.ToolResult, 1)
	go func() {
		result, _ := executor.Execute(callCtx, tools.ToolRequest{Name: "fake_echo", Arguments: map[string]any{"text": "cancel"}})
		done <- result
	}()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("MCP call never reached dispatch")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case result := <-done:
		if !strings.Contains(result.Error, "mcp_cancelled") || !result.OutcomeUnknown || !result.Stopped {
			t.Fatalf("dispatched cancellation lost its boundary: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("cancelled MCP process did not stop")
	}
}

func TestMCPExecutorRejectsMalformedFramesAndLostConnections(t *testing.T) {
	for _, scenario := range []string{"json", "id", "both", "content", "content_type", "text_missing", "text_null", "oversize", "disconnect"} {
		t.Run(scenario, func(t *testing.T) {
			manager := mcp.NewManager()
			manager.RegisterServer(mcp.ServerConfig{Name: "frame", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"}, Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_FRAME": scenario}})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := manager.Start(ctx, "frame"); err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			executor := tools.NewExecutor(t.TempDir())
			defer executor.Close()
			executor.SetMCPManager(manager)
			result, err := executor.Execute(ctx, tools.ToolRequest{Name: "fake_echo", Arguments: map[string]any{"text": "untrusted"}})
			kind := "mcp_protocol_invalid"
			if scenario == "disconnect" {
				kind = "mcp_connection_lost"
			}
			if err == nil || !strings.Contains(result.Error, kind) || !result.OutcomeUnknown || !result.Stopped || result.Output != "" {
				t.Fatalf("untrusted frame escaped classification: %+v %v", result, err)
			}
		})
	}
}

type mcpReplayConversation struct{}

func (mcpReplayConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	call := orchestrator.ToolCall{ID: "unknown-mcp", Name: "fake_echo", ParametersJSON: `{"text":"first"}`}
	for range 2 {
		result := handlers.Tool(ctx, call)
		if !result.OutcomeUnknown || !result.Stopped || !strings.Contains(result.Error, "mcp_timeout") {
			return orchestrator.ConversationResult{}, fmt.Errorf("unknown MCP outcome was replaced: %+v", result)
		}
	}
	return orchestrator.ConversationResult{Success: true, Message: "unknown result retained"}, nil
}

func TestMCPSessionDoesNotDispatchUnknownInvocationAgain(t *testing.T) {
	dir := t.TempDir()
	manager := mcp.NewManager()
	manager.RegisterServer(mcp.ServerConfig{Name: "unknown", Command: os.Args[0], Args: []string{"-test.run=TestMCPHelperProcess", "--", "mcp"}, Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1", "FAKE_MCP_DELAY_MARKER": filepath.Join(dir, "delayed")}})
	ctx := context.Background()
	if err := manager.Start(ctx, "unknown"); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	executor := tools.NewExecutor(dir)
	defer executor.Close()
	executor.SetMCPManager(manager)
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(dir, "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "unknown MCP", "", dir)
	if err != nil {
		t.Fatal(err)
	}
	var executions atomic.Int32
	runner := session.NewSessionRunner(workbench, mcpReplayConversation{}, session.ToolExecutionFunc(func(ctx context.Context, _ identity.Actor, _ string, call orchestrator.ToolCall) orchestrator.ToolResult {
		executions.Add(1)
		bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		result, _ := executor.Execute(bounded, tools.ToolRequest{Name: call.Name, Arguments: map[string]any{"text": "first"}})
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: result.Error, ExitCode: int32(result.ExitCode), Stopped: result.Stopped, OutcomeUnknown: result.OutcomeUnknown}
	}), session.SessionRunnerOptions{WorkerID: "mcp-unknown"})
	defer runner.Close()
	actor := identity.Actor{SchemaVersion: 1, ActorID: "user:7", Subject: "test", TenantID: "org:test", Roles: []string{"USER"}}
	accepted, err := runner.SubmitMessage(ctx, session.SubmitMessageCommand{RequestID: "mcp-unknown", SessionID: created.ID, OwnerID: 7, ExpectedSeq: 1, Content: "test unknown MCP", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, err := runner.Run(ctx, created.ID, accepted.RunID)
		if err == nil && (view.Status == session.RunCompleted || view.Status == session.RunFailed) {
			if view.Status != session.RunCompleted || executions.Load() != 1 {
				t.Fatalf("unknown MCP replayed: %+v executions=%d", view, executions.Load())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("MCP replay run did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func runFakeMCPServer() {
	if os.Getenv("FAKE_MCP_REPOSITORY") == "1" {
		_ = mcp.RunRepositoryServer(context.Background(), ".")
		return
	}
	drifted := false
	if os.Getenv("FAKE_MCP_DRIFT_AFTER_DELAY") == "1" {
		_, err := os.Stat(strings.TrimSpace(os.Getenv("FAKE_MCP_DELAY_MARKER")))
		drifted = err == nil
	}
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
			if marker := strings.TrimSpace(os.Getenv("FAKE_MCP_FAIL_INIT_MARKER")); marker != "" {
				if _, err := os.Stat(marker); err == nil {
					_ = encoder.Encode(mcp.Response{JSONRPC: "2.0", ID: req.ID, Error: &mcp.ErrorObject{
						Code: -32002, Message: "SENTINEL_RESTART_SECRET", Data: "SENTINEL_REMOTE_DATA",
					}})
					continue
				}
			}
			if os.Getenv("FAKE_MCP_ASSERT_SECRET_ABSENT") == "1" && os.Getenv("MCP_AMBIENT_SECRET_TOKEN") != "" ||
				os.Getenv("FAKE_MCP_ASSERT_EXPLICIT") == "1" && os.Getenv("MCP_EXPLICIT_TOKEN") == "" {
				_ = encoder.Encode(mcp.Response{JSONRPC: "2.0", ID: req.ID, Error: &mcp.ErrorObject{Code: -32000, Message: "environment boundary failed"}})
				continue
			}
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake"},
			}
		case "tools/list":
			toolName := strings.TrimSpace(os.Getenv("FAKE_MCP_TOOL_NAME"))
			if toolName == "" {
				toolName = "fake_echo"
			}
			schema := map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{"type": "string"},
				},
			}
			if drifted {
				schema["required"] = []string{"text"}
			}
			if os.Getenv("FAKE_MCP_SCHEMA") == "camel" {
				result = map[string]any{
					"tools": []map[string]any{{
						"name":        toolName,
						"description": "Echo text.",
						"inputSchema": schema,
					}},
				}
				break
			}
			result = map[string]any{
				"tools": []map[string]any{{
					"name":         toolName,
					"description":  "Echo text.",
					"input_schema": schema,
				}},
			}
		case "tools/call":
			if marker := os.Getenv("FAKE_MCP_CALLED_MARKER"); marker != "" {
				_ = os.WriteFile(marker, []byte("called"), 0o600)
			}
			if drifted {
				if marker := strings.TrimSpace(os.Getenv("FAKE_MCP_DRIFTED_CALL_MARKER")); marker != "" {
					_ = os.WriteFile(marker, []byte("called"), 0o600)
				}
			}
			if os.Getenv("FAKE_MCP_CALL_ERROR") == "1" {
				_ = encoder.Encode(mcp.Response{JSONRPC: "2.0", ID: req.ID, Error: &mcp.ErrorObject{
					Code: -32001, Message: "SENTINEL_REMOTE_SECRET", Data: "SENTINEL_REMOTE_DATA",
				}})
				continue
			}
			if marker := strings.TrimSpace(os.Getenv("FAKE_MCP_DELAY_MARKER")); marker != "" {
				if _, err := os.Stat(marker); os.IsNotExist(err) {
					_ = os.WriteFile(marker, []byte("delayed"), 0o600)
					time.Sleep(2 * time.Second)
				}
			}
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
		if req.Method == "tools/call" {
			switch os.Getenv("FAKE_MCP_FRAME") {
			case "json":
				fmt.Fprintln(os.Stdout, "{broken")
				continue
			case "id":
				_ = encoder.Encode(mcp.Response{JSONRPC: "2.0", ID: fmt.Sprint(req.ID), Result: data})
				continue
			case "both":
				_ = encoder.Encode(mcp.Response{JSONRPC: "2.0", ID: req.ID, Result: data, Error: &mcp.ErrorObject{Code: -32602, Message: "both fields"}})
				continue
			case "content":
				data = []byte(`{"content":null}`)
			case "content_type":
				data = []byte(`{"content":[{"type":"bogus","text":"forged"}]}`)
			case "text_missing":
				data = []byte(`{"content":[{"type":"text"}]}`)
			case "text_null":
				data = []byte(`{"content":[{"type":"text","text":null}]}`)
			case "oversize":
				fmt.Fprintln(os.Stdout, strings.Repeat("x", (8<<20)+1))
				continue
			case "disconnect":
				return
			}
		}
		version := "2.0"
		if req.Method == "tools/call" && os.Getenv("FAKE_MCP_BAD_RPC_VERSION") == "1" {
			version = "1.0"
		}
		_ = encoder.Encode(mcp.Response{
			JSONRPC: version,
			ID:      req.ID,
			Result:  data,
		})
	}
}
