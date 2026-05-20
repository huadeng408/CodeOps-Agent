package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

type ToolRequest struct {
	Name      string         `json:"name"`
	Arguments  map[string]any `json:"arguments,omitempty"`
}

type ToolResult struct {
	Name      string `json:"name"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated"`
}

type Executor struct {
	Root           string
	MaxOutputBytes int
}

func NewExecutor(root string) *Executor {
	return &Executor{
		Root:           root,
		MaxOutputBytes:  50_000,
	}
}

func (e *Executor) Execute(ctx context.Context, req ToolRequest) (ToolResult, error) {
	switch req.Name {
	case "Read":
		return executeRead(ctx, e.Root, req.Arguments)
	case "Edit":
		return executeEdit(ctx, e.Root, req.Arguments)
	case "Write":
		return executeWrite(ctx, e.Root, req.Arguments)
	case "Bash":
		return executeBash(ctx, e.Root, req.Arguments)
	case "Glob":
		return executeGlob(ctx, e.Root, req.Arguments)
	case "Grep":
		return executeGrep(ctx, e.Root, req.Arguments)
	case "Git":
		return executeGit(ctx, e.Root, req.Arguments)
	case "WebFetch":
		return executeWebFetch(ctx, e.Root, req.Arguments)
	default:
		return ToolResult{Name: req.Name, Error: "unknown tool"}, fmt.Errorf("unknown tool %q", req.Name)
	}
}

func stringArg(args map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		value, ok := args[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case string:
			return v, true
		case fmt.Stringer:
			return v.String(), true
		}
	}
	return "", false
}

func normalizeOutput(output string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(output) <= maxBytes {
		return output, false
	}
	return output[:maxBytes] + "\n\n[Output truncated]", true
}

func workspacePath(root, target string) (string, error) {
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if target == "" {
		return absRoot, nil
	}
	if filepath.IsAbs(target) {
		target = filepath.Clean(target)
	} else {
		target = filepath.Join(absRoot, target)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", target)
	}
	return absTarget, nil
}
