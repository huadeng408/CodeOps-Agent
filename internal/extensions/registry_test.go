package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

type testAdapter struct {
	result Result
	err    error
	seen   Request
}

func (a *testAdapter) Execute(_ context.Context, request Request) (Result, error) {
	a.seen = request
	return a.result, a.err
}

func TestRegistryListsMetadataAndRejectsDuplicateIDs(t *testing.T) {
	registry := NewRegistry()
	spec := Spec{ID: "python", Kind: KindCodeRuntime, Version: "v1", Description: "Run checked-in code", Operations: []string{"test"}, MaxPayloadBytes: 1024}
	if err := registry.Register(spec, &testAdapter{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Register(spec, &testAdapter{}); err == nil {
		t.Fatal("duplicate extension id was accepted")
	}
	items := registry.List()
	if len(items) != 1 || items[0].ID != "python" || items[0].Kind != KindCodeRuntime {
		t.Fatalf("List() = %#v", items)
	}
	manifest := t.TempDir() + "/.agent/extensions.json"
	if err := registry.WriteManifest(manifest); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var payload struct {
		Extensions []map[string]any `json:"extensions"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(payload.Extensions) != 1 || payload.Extensions[0]["id"] != "python" {
		t.Fatalf("manifest = %#v", payload)
	}
	if strings.Contains(string(raw), "adapter") || strings.Contains(string(raw), "prompt") {
		t.Fatalf("manifest contains implementation details: %q", string(raw))
	}
	if err := registry.Register(Spec{
		ID: "unsafe", Kind: KindLSP, Version: "v1", Description: "line1\nline2", Operations: []string{"inspect"},
	}, &testAdapter{}); err == nil {
		t.Fatal("description containing a newline was accepted")
	}
}

func TestRegistryExecuteFailsClosedAndRedactsAdapterErrors(t *testing.T) {
	registry := NewRegistry()
	var audit []AuditRecord
	registry.SetAuditSink(AuditSinkFunc(func(_ context.Context, record AuditRecord) error {
		audit = append(audit, record)
		return nil
	}))
	adapter := &testAdapter{err: errors.New("private marker credential-shaped-value")}
	if err := registry.Register(Spec{ID: "lsp", Kind: KindLSP, Version: "v1", Description: "Language server", Operations: []string{"diagnostics"}, MaxPayloadBytes: 8}, adapter); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := registry.Execute(context.Background(), Request{ExtensionID: "lsp", SessionID: "", Operation: "diagnostics"}); err == nil {
		t.Fatal("empty session was accepted")
	}
	if _, err := registry.Execute(context.Background(), Request{ExtensionID: "unknown", SessionID: "session-1", Operation: "diagnostics"}); err == nil {
		t.Fatal("unknown extension was accepted")
	}
	if _, err := registry.Execute(context.Background(), Request{ExtensionID: "lsp", SessionID: "session-1", Operation: "diagnostics", Payload: []byte("123456789")}); err == nil {
		t.Fatal("oversized payload was accepted")
	}
	_, err := registry.Execute(context.Background(), Request{ExtensionID: "lsp", SessionID: "session-1", Operation: "diagnostics", Payload: []byte("{}")})
	if err == nil || strings.Contains(err.Error(), "private marker") || strings.Contains(err.Error(), "credential-shaped-value") || !strings.Contains(err.Error(), "adapter execution failed") {
		t.Fatalf("adapter error = %v", err)
	}
	if len(audit) != 2 || audit[0].Status != "started" || audit[1].Status != "failed" || audit[0].PayloadBytes != 2 {
		t.Fatalf("audit = %#v", audit)
	}
}

func TestRegistryDoesNotLeakAuditSinkErrors(t *testing.T) {
	registry := NewRegistry()
	registry.SetAuditSink(AuditSinkFunc(func(_ context.Context, _ AuditRecord) error {
		return errors.New("private marker credential-shaped-value")
	}))
	if err := registry.Register(Spec{
		ID: "attachment", Kind: KindAttachment, Version: "v1", Description: "Attachment reader",
		Operations: []string{"inspect"}, MaxPayloadBytes: 32,
	}, &testAdapter{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err := registry.Execute(context.Background(), Request{
		ExtensionID: "attachment", SessionID: "session-1", Operation: "inspect", Payload: []byte("{}"),
	})
	if err == nil || strings.Contains(err.Error(), "private marker") || strings.Contains(err.Error(), "credential-shaped-value") || !strings.Contains(err.Error(), "record extension audit failed") {
		t.Fatalf("audit error = %v", err)
	}
}
