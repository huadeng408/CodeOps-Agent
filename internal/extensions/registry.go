// Package extensions defines the Harness-owned seam for optional attachment,
// code-runtime, and LSP integrations. It deliberately contains no process or
// filesystem implementation: adapters receive an already-authorized request
// and are responsible for using the configured Harness capabilities.
package extensions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Kind string

const (
	KindAttachment  Kind = "attachment"
	KindCodeRuntime Kind = "code_runtime"
	KindLSP         Kind = "lsp"
)

const defaultMaxPayloadBytes = 1 << 20

type Spec struct {
	ID              string   `json:"id"`
	Kind            Kind     `json:"kind"`
	Version         string   `json:"version"`
	Description     string   `json:"description"`
	Operations      []string `json:"operations,omitempty"`
	MaxPayloadBytes int      `json:"max_payload_bytes"`
}

type Request struct {
	ExtensionID string
	Kind        Kind
	Version     string
	SessionID   string
	Operation   string
	Payload     []byte
}

type Result struct {
	ExtensionID string
	Kind        Kind
	Version     string
	Operation   string
	Output      string
	ExitCode    int
	AuditID     string
}

type Adapter interface {
	Execute(context.Context, Request) (Result, error)
}

type AuditRecord struct {
	EventID      string
	SessionID    string
	ExtensionID  string
	Kind         Kind
	Version      string
	Operation    string
	PayloadBytes int
	Status       string
}

type AuditSink interface {
	Record(context.Context, AuditRecord) error
}

type AuditSinkFunc func(context.Context, AuditRecord) error

func (f AuditSinkFunc) Record(ctx context.Context, record AuditRecord) error {
	return f(ctx, record)
}

var (
	ErrInvalidSpec      = errors.New("invalid extension spec")
	ErrInvalidRequest   = errors.New("invalid extension request")
	ErrExtensionMissing = errors.New("extension is not registered")
	ErrAuditUnavailable = errors.New("extension audit sink is not configured")
)

type registered struct {
	spec    Spec
	adapter Adapter
}

type Registry struct {
	mu    sync.RWMutex
	items map[string]registered
	audit AuditSink
}

func NewRegistry() *Registry {
	return &Registry{items: make(map[string]registered)}
}

func (r *Registry) SetAuditSink(sink AuditSink) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.audit = sink
	r.mu.Unlock()
}

func (r *Registry) Register(spec Spec, adapter Adapter) error {
	if r == nil {
		return ErrInvalidSpec
	}
	normalized, err := normalizeSpec(spec)
	if err != nil {
		return err
	}
	if adapter == nil {
		return fmt.Errorf("%w: adapter is required", ErrInvalidSpec)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[normalized.ID]; exists {
		return fmt.Errorf("extension %q is already registered", normalized.ID)
	}
	r.items[normalized.ID] = registered{spec: normalized, adapter: adapter}
	return nil
}

func (r *Registry) List() []Spec {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	items := make([]Spec, 0, len(r.items))
	for _, entry := range r.items {
		items = append(items, cloneSpec(entry.spec))
	}
	r.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

// WriteManifest exports only extension metadata for the Python orchestrator.
// Adapter implementations, payloads, and audit details never cross this file
// boundary.
func (r *Registry) WriteManifest(path string) error {
	if r == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	payload := struct {
		Extensions []Spec `json:"extensions"`
	}{Extensions: r.List()}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode extension manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create extension manifest directory: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write extension manifest: %w", err)
	}
	return nil
}

func (r *Registry) Resolve(id string) (Spec, bool) {
	if r == nil {
		return Spec{}, false
	}
	r.mu.RLock()
	entry, ok := r.items[strings.TrimSpace(id)]
	r.mu.RUnlock()
	if !ok {
		return Spec{}, false
	}
	return cloneSpec(entry.spec), true
}

func (r *Registry) Execute(ctx context.Context, request Request) (Result, error) {
	if r == nil {
		return Result{}, ErrExtensionMissing
	}
	normalized, err := normalizeRequest(request)
	if err != nil {
		return Result{}, err
	}
	r.mu.RLock()
	entry, ok := r.items[normalized.ExtensionID]
	audit := r.audit
	r.mu.RUnlock()
	if !ok || entry.adapter == nil {
		return Result{}, ErrExtensionMissing
	}
	if normalized.Kind != "" && normalized.Kind != entry.spec.Kind {
		return Result{}, fmt.Errorf("%w: extension kind mismatch", ErrInvalidRequest)
	}
	if normalized.Version != "" && normalized.Version != entry.spec.Version {
		return Result{}, fmt.Errorf("%w: extension version mismatch", ErrInvalidRequest)
	}
	if !contains(entry.spec.Operations, normalized.Operation) {
		return Result{}, fmt.Errorf("%w: operation is not supported", ErrInvalidRequest)
	}
	if len(normalized.Payload) > entry.spec.MaxPayloadBytes {
		return Result{}, fmt.Errorf("%w: payload exceeds extension limit", ErrInvalidRequest)
	}
	if audit == nil {
		return Result{}, ErrAuditUnavailable
	}
	auditID := auditID(normalized)
	start := AuditRecord{
		EventID: auditID, SessionID: normalized.SessionID, ExtensionID: entry.spec.ID,
		Kind: entry.spec.Kind, Version: entry.spec.Version, Operation: normalized.Operation,
		PayloadBytes: len(normalized.Payload), Status: "started",
	}
	if err := audit.Record(ctx, start); err != nil {
		return Result{}, errors.New("record extension audit failed")
	}
	result, adapterErr := entry.adapter.Execute(ctx, normalized)
	if adapterErr == nil && result.ExitCode != 0 {
		adapterErr = errors.New("adapter returned non-zero exit code")
	}
	statusValue := "succeeded"
	if adapterErr != nil {
		statusValue = "failed"
	}
	end := start
	end.Status = statusValue
	if err := audit.Record(ctx, end); err != nil && adapterErr == nil {
		return Result{}, errors.New("record extension audit failed")
	}
	if adapterErr != nil {
		// Adapter text may contain source paths, credentials, or provider output.
		// Return only a stable class to the caller and keep detail out of audit.
		return Result{}, fmt.Errorf("adapter execution failed: %T", adapterErr)
	}
	result.ExtensionID = entry.spec.ID
	result.Kind = entry.spec.Kind
	result.Version = entry.spec.Version
	result.Operation = normalized.Operation
	result.AuditID = auditID
	return result, nil
}

func normalizeSpec(spec Spec) (Spec, error) {
	spec.ID = strings.TrimSpace(spec.ID)
	spec.Version = strings.TrimSpace(spec.Version)
	spec.Description = strings.TrimSpace(spec.Description)
	if spec.ID == "" || strings.ContainsAny(spec.ID, "\\/\r\n\t ") || spec.Version == "" || spec.Description == "" || strings.ContainsAny(spec.Description, "\r\n") {
		return Spec{}, ErrInvalidSpec
	}
	switch spec.Kind {
	case KindAttachment, KindCodeRuntime, KindLSP:
	default:
		return Spec{}, fmt.Errorf("%w: unsupported extension kind", ErrInvalidSpec)
	}
	if spec.MaxPayloadBytes <= 0 {
		spec.MaxPayloadBytes = defaultMaxPayloadBytes
	}
	seen := make(map[string]struct{}, len(spec.Operations))
	ops := make([]string, 0, len(spec.Operations))
	for _, operation := range spec.Operations {
		operation = strings.TrimSpace(operation)
		if operation == "" || strings.ContainsAny(operation, "\r\n\t ") {
			return Spec{}, ErrInvalidSpec
		}
		if _, exists := seen[operation]; exists {
			return Spec{}, ErrInvalidSpec
		}
		seen[operation] = struct{}{}
		ops = append(ops, operation)
	}
	if len(ops) == 0 {
		return Spec{}, fmt.Errorf("%w: at least one operation is required", ErrInvalidSpec)
	}
	sort.Strings(ops)
	spec.Operations = ops
	return spec, nil
}

func normalizeRequest(request Request) (Request, error) {
	request.ExtensionID = strings.TrimSpace(request.ExtensionID)
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.Operation = strings.TrimSpace(request.Operation)
	if request.ExtensionID == "" || request.SessionID == "" || request.Operation == "" {
		return Request{}, ErrInvalidRequest
	}
	request.Payload = append([]byte(nil), request.Payload...)
	return request, nil
}

func cloneSpec(spec Spec) Spec {
	spec.Operations = append([]string(nil), spec.Operations...)
	return spec
}

func contains(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

func auditID(request Request) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(request.SessionID))
	_, _ = hash.Write([]byte("\x00"))
	_, _ = hash.Write([]byte(request.ExtensionID))
	_, _ = hash.Write([]byte("\x00"))
	_, _ = hash.Write([]byte(request.Operation))
	_, _ = hash.Write([]byte("\x00"))
	_, _ = hash.Write(request.Payload)
	return "extension-" + hex.EncodeToString(hash.Sum(nil))[:24]
}
