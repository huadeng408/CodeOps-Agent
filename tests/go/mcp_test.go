package codeagent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/mcp"
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
