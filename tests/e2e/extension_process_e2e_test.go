package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code-agent/internal/extensions"
	"code-agent/internal/orchestrator"
	"code-agent/internal/tools"
)

type processExtensionAdapter struct {
	mu      sync.Mutex
	request extensions.Request
}

func (a *processExtensionAdapter) Execute(_ context.Context, request extensions.Request) (extensions.Result, error) {
	a.mu.Lock()
	a.request = request
	a.mu.Unlock()
	return extensions.Result{Output: "GO_HARNESS_EXTENSION_OK"}, nil
}

func TestProductionExtensionSeamAcrossGoAndPythonProcesses(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_EXTENSION_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_EXTENSION_E2E=1 to run the production extension E2E")
	}
	repositoryRoot := e2ERepositoryRoot(t)
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agent"), 0o755); err != nil {
		t.Fatalf("create project root: %v", err)
	}
	manifestRegistry := extensions.NewRegistry()
	manifestRegistry.SetAuditSink(extensions.AuditSinkFunc(func(context.Context, extensions.AuditRecord) error { return nil }))
	if err := manifestRegistry.Register(extensions.Spec{
		ID: "python-runtime", Kind: extensions.KindCodeRuntime, Version: "v1",
		Description: "Inspect checked-in code", Operations: []string{"inspect"}, MaxPayloadBytes: 1024,
	}, &processExtensionAdapter{}); err != nil {
		t.Fatalf("register manifest extension: %v", err)
	}
	manifestPath := filepath.Join(projectRoot, ".agent", "extensions.json")
	if err := manifestRegistry.WriteManifest(manifestPath); err != nil {
		t.Fatalf("write extension manifest: %v", err)
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read extension manifest: %v", err)
	}
	if strings.Contains(string(manifest), "adapter") {
		t.Fatalf("extension manifest leaked adapter details")
	}

	address := contextE2EAddress(t)
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split address: %v", err)
	}
	python := contextE2EPython(t)
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "extension_process_server.py")
	server := exec.Command(
		python, fixture, "--host", host, "--port", port,
		"--project-root", projectRoot, "--working-dir", projectRoot,
		"--memory-dir", filepath.Join(projectRoot, ".agent", "memory"),
	)
	server.Dir = repositoryRoot
	server.Env = append(os.Environ(), "PYTHONPATH="+repositoryRoot)
	var output strings.Builder
	server.Stdout = &output
	server.Stderr = &output
	if err := server.Start(); err != nil {
		t.Fatalf("start Python orchestrator: %v", err)
	}
	t.Cleanup(func() {
		if server.Process != nil {
			_ = server.Process.Kill()
		}
		_ = server.Wait()
	})
	waitForTCP(t, address, 20*time.Second, &output)

	registry := extensions.NewRegistry()
	var records []extensions.AuditRecord
	registry.SetAuditSink(extensions.AuditSinkFunc(func(_ context.Context, record extensions.AuditRecord) error {
		records = append(records, record)
		return nil
	}))
	adapter := &processExtensionAdapter{}
	if err := registry.Register(extensions.Spec{
		ID: "python-runtime", Kind: extensions.KindCodeRuntime, Version: "v1",
		Description: "Inspect checked-in code", Operations: []string{"inspect"}, MaxPayloadBytes: 1024,
	}, adapter); err != nil {
		t.Fatalf("register runtime adapter: %v", err)
	}
	executor := tools.NewExecutor(projectRoot)
	executor.SetExtensionRegistry(registry)
	t.Cleanup(func() { _ = executor.Close() })
	client, err := orchestrator.NewClient(address)
	if err != nil {
		t.Fatalf("create Go client: %v", err)
	}
	defer client.Close()
	client.SetConversationTimeout(30 * time.Second)
	const sessionID = "extension-process-e2e"
	response, err := client.ConverseWithHistory(
		context.Background(), "RUN_EXTENSION", sessionID, nil,
		func(ctx context.Context, call orchestrator.ToolCall) orchestrator.ToolResult {
			var args map[string]any
			if err := json.Unmarshal([]byte(call.ParametersJSON), &args); err != nil {
				return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "invalid extension arguments", ExitCode: 1}
			}
			result, executeErr := executor.Execute(ctx, tools.ToolRequest{Name: call.Name, Arguments: args, OwnerSessionID: sessionID})
			if executeErr != nil && result.Error == "" {
				result.Error = "extension execution failed"
				result.ExitCode = 1
			}
			return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Output: result.Output, Error: result.Error, ExitCode: int32(result.ExitCode)}
		},
	)
	if err != nil {
		t.Fatalf("cross-process extension conversation: %v; server output=%q", err, output.String())
	}
	if !strings.HasPrefix(response, "EXTENSION_E2E_OK:") {
		t.Fatalf("response = %q", response)
	}
	adapter.mu.Lock()
	seen := adapter.request
	adapter.mu.Unlock()
	if seen.SessionID != sessionID || seen.Operation != "inspect" || seen.Kind != extensions.KindCodeRuntime {
		t.Fatalf("adapter request = %#v", seen)
	}
	if len(records) != 2 || records[0].Status != "started" || records[1].Status != "succeeded" {
		t.Fatalf("audit records = %#v", records)
	}
	digest := sha256.Sum256(manifest)
	receipt := map[string]any{
		"status":                      "VERIFIED",
		"exit_code":                   0,
		"kind":                        "production-extension-seam",
		"git_sha":                     contextE2EGitSHA(t, repositoryRoot),
		"command":                     "CODE_AGENT_RUN_EXTENSION_E2E=1 go test ./tests/e2e -run TestProductionExtensionSeamAcrossGoAndPythonProcesses -count=1",
		"go_client_boundary":          true,
		"python_orchestrator_process": true,
		"extension_kind":              string(extensions.KindCodeRuntime),
		"metadata_only_manifest":      true,
		"manifest_sha256":             fmt.Sprintf("%x", digest),
		"audit_event_count":           len(records),
		"python_pid":                  server.Process.Pid,
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode receipt: %v", err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "extension-process.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatalf("create receipt directory: %v", err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write receipt: %v", err)
	}
}
