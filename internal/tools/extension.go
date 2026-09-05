package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"code-agent/internal/extensions"
)

func (e *Executor) executeExtension(ctx context.Context, req ToolRequest) (ToolResult, error) {
	e.mu.Lock()
	registry := e.extensions
	e.mu.Unlock()
	if registry == nil {
		return ToolResult{Name: req.Name, Error: "extension registry is not configured", ExitCode: 1}, errors.New("extension registry is not configured")
	}
	args := req.Arguments
	id, ok := stringArg(args, "extension_id", "id", "name")
	if !ok || strings.TrimSpace(id) == "" {
		return ToolResult{Name: req.Name, Error: "extension_id is required", ExitCode: 1}, errors.New("extension_id is required")
	}
	operation, ok := stringArg(args, "operation")
	if !ok || strings.TrimSpace(operation) == "" {
		return ToolResult{Name: req.Name, Error: "operation is required", ExitCode: 1}, errors.New("operation is required")
	}
	payload, err := extensionPayload(args)
	if err != nil {
		return ToolResult{Name: req.Name, Error: "invalid extension payload", ExitCode: 1}, err
	}
	kindText, _ := stringArg(args, "kind")
	version, _ := stringArg(args, "version")
	result, err := registry.Execute(ctx, extensions.Request{
		ExtensionID: strings.TrimSpace(id),
		Kind:        extensions.Kind(strings.TrimSpace(kindText)),
		Version:     strings.TrimSpace(version),
		SessionID:   strings.TrimSpace(req.OwnerSessionID),
		Operation:   strings.TrimSpace(operation),
		Payload:     payload,
	})
	if err != nil {
		return ToolResult{Name: req.Name, Error: err.Error(), ExitCode: 1}, err
	}
	return ToolResult{Name: req.Name, Output: result.Output, ExitCode: result.ExitCode}, nil
}

func extensionPayload(args map[string]any) ([]byte, error) {
	if raw, ok := args["payload"]; ok {
		switch value := raw.(type) {
		case string:
			return []byte(value), nil
		case []byte:
			return append([]byte(nil), value...), nil
		default:
			data, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("encode extension payload: %w", err)
			}
			return data, nil
		}
	}
	if raw, ok := args["payload_json"]; ok {
		value, ok := raw.(string)
		if !ok {
			return nil, errors.New("payload_json must be a string")
		}
		return []byte(value), nil
	}
	return []byte("{}"), nil
}
