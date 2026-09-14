package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"code-agent/internal/extensions"
	"code-agent/internal/skills"
)

type extensionTestAdapter struct {
	request extensions.Request
}

func (a *extensionTestAdapter) Execute(_ context.Context, request extensions.Request) (extensions.Result, error) {
	a.request = request
	return extensions.Result{Output: "EXTENSION_OK"}, nil
}

type failingExtensionAdapter struct{}

func (failingExtensionAdapter) Execute(context.Context, extensions.Request) (extensions.Result, error) {
	return extensions.Result{}, errors.New("private marker credential-shaped-value")
}

func TestExecutorRoutesExtensionThroughAuditedRegistry(t *testing.T) {
	registry := extensions.NewRegistry()
	var records []extensions.AuditRecord
	registry.SetAuditSink(extensions.AuditSinkFunc(func(_ context.Context, record extensions.AuditRecord) error {
		records = append(records, record)
		return nil
	}))
	adapter := &extensionTestAdapter{}
	if err := registry.Register(extensions.Spec{
		ID: "python", Kind: extensions.KindCodeRuntime, Version: "v1",
		Description: "Run checked-in code", Operations: []string{"test"}, MaxPayloadBytes: 32,
	}, adapter); err != nil {
		t.Fatalf("Register: %v", err)
	}
	executor := NewExecutor(t.TempDir())
	executor.SetExtensionRegistry(registry)
	result, err := executor.Execute(context.Background(), ToolRequest{
		Name: "Extension", OwnerSessionID: "session-1", Arguments: map[string]any{
			"extension_id": "python", "kind": "code_runtime", "version": "v1",
			"operation": "test", "payload": map[string]any{"command": "go test"},
		},
	})
	if err != nil || result.Output != "EXTENSION_OK" || result.ExitCode != 0 {
		t.Fatalf("extension result = %#v, err = %v", result, err)
	}
	if adapter.request.SessionID != "session-1" || adapter.request.Kind != extensions.KindCodeRuntime || string(adapter.request.Payload) != `{"command":"go test"}` {
		t.Fatalf("adapter request = %#v", adapter.request)
	}
	if len(records) != 2 || records[0].Status != "started" || records[1].Status != "succeeded" {
		t.Fatalf("audit records = %#v", records)
	}
}

func TestExecutorDoesNotLeakExtensionAdapterErrors(t *testing.T) {
	registry := extensions.NewRegistry()
	registry.SetAuditSink(extensions.AuditSinkFunc(func(context.Context, extensions.AuditRecord) error { return nil }))
	if err := registry.Register(extensions.Spec{
		ID: "lsp", Kind: extensions.KindLSP, Version: "v1", Description: "Language server",
		Operations: []string{"diagnostics"}, MaxPayloadBytes: 32,
	}, failingExtensionAdapter{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	executor := NewExecutor(t.TempDir())
	executor.SetExtensionRegistry(registry)
	result, err := executor.Execute(context.Background(), ToolRequest{
		Name: "LSP", OwnerSessionID: "session-1", Arguments: map[string]any{
			"extension_id": "lsp", "operation": "diagnostics", "payload": "{}",
		},
	})
	if err == nil || result.ExitCode != 1 || strings.Contains(result.Error, "private marker") || strings.Contains(result.Error, "credential-shaped-value") {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestExecutorSkillToolRejectsNonModelInvocableSkill(t *testing.T) {
	manager := skills.NewManager()
	manager.Register(skills.Skill{
		Name: "user-only", Description: "user skill", Prompt: "private",
		Invocation: skills.InvocationPolicy{ModelInvocable: false, UserInvocable: true, Configured: true},
	})
	executor := NewExecutor(t.TempDir())
	executor.SetSkillsManager(manager)
	result, err := executor.Execute(context.Background(), ToolRequest{
		Name: "Skill", Arguments: map[string]any{"name": "user-only"},
	})
	if err != nil || result.ExitCode == 0 || !strings.Contains(result.Error, "not model-invocable") {
		t.Fatalf("result=%+v err=%v, want model policy denial", result, err)
	}
}
