package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"code-agent/internal/mcp"
)

type ToolRequest struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type ToolResult struct {
	Name      string   `json:"name"`
	Output    string   `json:"output,omitempty"`
	Error     string   `json:"error,omitempty"`
	ExitCode  int      `json:"exit_code"`
	Truncated bool     `json:"truncated"`
	Changes   []Change `json:"changes,omitempty"`
}

type Change struct {
	Path   string `json:"path"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type Executor struct {
	Root           string
	MaxOutputBytes int
	mu             sync.Mutex
	workingDir     string
	mcp            *mcp.Manager
}

func NewExecutor(root string) *Executor {
	return &Executor{
		Root:           root,
		MaxOutputBytes: 50_000,
	}
}

func (e *Executor) SetMCPManager(manager *mcp.Manager) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mcp = manager
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
		return e.executeBash(ctx, req.Arguments)
	case "Glob":
		return executeGlob(ctx, e.Root, req.Arguments)
	case "Grep":
		return executeGrep(ctx, e.Root, req.Arguments)
	case "Git":
		return executeGit(ctx, e.Root, req.Arguments)
	case "WebFetch":
		return executeWebFetch(ctx, e.Root, req.Arguments)
	case "WebSearch":
		return executeWebSearch(ctx, e.Root, req.Arguments)
	default:
		if result, ok, err := e.executeMCPTool(ctx, req); ok {
			return result, err
		}
		return ToolResult{Name: req.Name, Error: "unknown tool"}, fmt.Errorf("unknown tool %q", req.Name)
	}
}

func (e *Executor) executeMCPTool(ctx context.Context, req ToolRequest) (ToolResult, bool, error) {
	e.mu.Lock()
	manager := e.mcp
	e.mu.Unlock()
	if manager == nil {
		return ToolResult{}, false, nil
	}
	if _, ok := manager.ResolveTool(req.Name); !ok {
		return ToolResult{}, false, nil
	}
	result, err := manager.CallTool(ctx, req.Name, req.Arguments)
	output := mcpToolOutput(result)
	if err != nil {
		return ToolResult{Name: req.Name, Output: output, Error: err.Error(), ExitCode: 1}, true, err
	}
	exitCode := 0
	errorText := ""
	if result.IsError {
		exitCode = 1
		errorText = output
	}
	return ToolResult{Name: req.Name, Output: output, Error: errorText, ExitCode: exitCode}, true, nil
}

func mcpToolOutput(result mcp.ToolCallResult) string {
	parts := make([]string, 0, len(result.Content))
	for _, item := range result.Content {
		if strings.TrimSpace(item.Text) != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (e *Executor) WorkingDir() string {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.workingDir == "" {
		abs, err := workspacePath(e.Root, "")
		if err != nil {
			return e.Root
		}
		return abs
	}
	return e.workingDir
}

func (e *Executor) SetWorkingDir(path string) error {
	abs, err := workspacePath(e.Root, path)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.workingDir = abs
	return nil
}

func (e *Executor) SetWorkingDirFrom(base, target string) error {
	if target == "" {
		target = base
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	return e.SetWorkingDir(target)
}

func (e *Executor) currentWorkingDir() (string, error) {
	e.mu.Lock()
	current := e.workingDir
	e.mu.Unlock()
	if current == "" {
		return workspacePath(e.Root, "")
	}
	return workspacePath(e.Root, current)
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

func boolArg(args map[string]any, keys ...string) bool {
	for _, key := range keys {
		value, ok := args[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case bool:
			return v
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "1", "true", "yes", "y", "on":
				return true
			}
		case int:
			return v != 0
		case int64:
			return v != 0
		case float64:
			return v != 0
		}
	}
	return false
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
