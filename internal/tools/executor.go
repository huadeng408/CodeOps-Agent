package tools

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"code-agent/internal/mcp"
	"code-agent/internal/skills"
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
	// MaxOutputLines 是工具输出的行数上限（先于字节上限施加）。
	MaxOutputLines int
	mu             sync.Mutex
	workingDir     string
	mcp            *mcp.Manager
	skills         *skills.Manager
	// httpAllowPrivate lifts the SSRF private/loopback block for WebFetch/WebSearch.
	// Intended only for tests and trusted local providers; production MUST stay false.
	httpAllowPrivate bool
}

func NewExecutor(root string) *Executor {
	return &Executor{
		Root:           root,
		MaxOutputBytes: 50_000,
		MaxOutputLines: 250,
	}
}

func (e *Executor) SetMCPManager(manager *mcp.Manager) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mcp = manager
}

func (e *Executor) SetSkillsManager(manager *skills.Manager) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.skills = manager
}

// SetHTTPAllowPrivate enables fetching loopback/private addresses in WebFetch/WebSearch.
// Tests use this to target a local mock server; production must leave it disabled.
func (e *Executor) SetHTTPAllowPrivate(allow bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.httpAllowPrivate = allow
}

func (e *Executor) Execute(ctx context.Context, req ToolRequest) (ToolResult, error) {
	switch req.Name {
	case "Read":
		return e.executeRead(ctx, req.Arguments)
	case "Edit":
		return e.executeEdit(ctx, req.Arguments)
	case "NotebookEdit":
		return e.executeNotebookEdit(ctx, req.Arguments)
	case "Write":
		return e.executeWrite(ctx, req.Arguments)
	case "Bash":
		return e.executeBash(ctx, req.Arguments)
	case "Glob":
		return e.executeGlob(ctx, req.Arguments)
	case "Grep":
		return e.executeGrep(ctx, req.Arguments)
	case "Git":
		return e.executeGit(ctx, req.Arguments)
	case "WebFetch":
		return e.executeWebFetch(ctx, req.Arguments)
	case "WebSearch":
		return e.executeWebSearch(ctx, req.Arguments)
	case "Skill":
		return e.executeSkill(ctx, req.Arguments)
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

func (e *Executor) executeSkill(_ context.Context, args map[string]any) (ToolResult, error) {
	e.mu.Lock()
	manager := e.skills
	e.mu.Unlock()
	if manager == nil {
		return ToolResult{Name: "Skill", Error: "skills manager not available", ExitCode: 1}, nil
	}
	name, ok := stringArg(args, "name")
	if !ok || strings.TrimSpace(name) == "" {
		return ToolResult{Name: "Skill", Error: "skill name is required", ExitCode: 1}, nil
	}
	name = strings.TrimSpace(name)
	skill, ok := manager.Get(name)
	if !ok {
		return ToolResult{Name: "Skill", Error: "skill not found: " + name, ExitCode: 1}, nil
	}
	var parts []string
	if strings.TrimSpace(skill.Prompt) != "" {
		parts = append(parts, skill.Prompt)
	}
	if len(skill.Tools) > 0 {
		parts = append(parts, "Preferred tools: "+strings.Join(skill.Tools, ", "))
	}
	userArgs, _ := stringArg(args, "args")
	if strings.TrimSpace(userArgs) != "" {
		parts = append(parts, "User focus: "+userArgs)
	}
	output := strings.Join(parts, "\n\n")
	output, truncated := e.TruncateOutput(output)
	return ToolResult{Name: "Skill", Output: output, Truncated: truncated}, nil
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

// TruncateOutput 按当前 Executor 配置的行数与字节上限截断工具输出，返回截断后的
// 文本以及是否发生过截断。导出方法便于调用方与测试直接复用同一套截断策略。
func (e *Executor) TruncateOutput(output string) (string, bool) {
	return normalizeOutput(output, e.MaxOutputLines, e.MaxOutputBytes)
}

// normalizeOutput 对工具原始输出依次施加行数与字节上限：先按行截断（保留前 maxLines
// 行），再按字节截断（保留前 maxBytes 字节）。任一上限触发都会追加明确的提示信息，
// 并返回 truncated=true。maxLines 或 maxBytes 为 0 表示不施加对应上限。
func normalizeOutput(output string, maxLines, maxBytes int) (string, bool) {
	truncated := false
	// 1) 行截断优先：超过行数上限时保留前 maxLines 行，并提示总行数。
	if maxLines > 0 {
		lines := strings.Split(output, "\n")
		if len(lines) > maxLines {
			output = strings.Join(lines[:maxLines], "\n") +
				fmt.Sprintf("\n\n[Output truncated: %d lines total, showing first %d]", len(lines), maxLines)
			truncated = true
		}
	}
	// 2) 字节截断兜底：行截断后仍超字节上限，则按字节硬截断。
	if maxBytes > 0 && len(output) > maxBytes {
		output = output[:maxBytes] + fmt.Sprintf("\n\n[Output truncated at %s]", byteCapLabel(maxBytes))
		truncated = true
	}
	return output, truncated
}

// byteCapLabel 把字节上限渲染为人类可读的提示后缀（>=1000 用 KB，否则用字节）。
func byteCapLabel(maxBytes int) string {
	if maxBytes >= 1000 {
		return fmt.Sprintf("%dKB", maxBytes/1000)
	}
	return fmt.Sprintf("%d bytes", maxBytes)
}

func workspacePath(root, target string) (string, error) {
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	// Resolve symlinks in the workspace root itself so the containment
	// check uses the real filesystem path.
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("workspace root symlink resolution failed: %w", err)
	}
	if target == "" {
		return realRoot, nil
	}
	if filepath.IsAbs(target) {
		target = filepath.Clean(target)
	} else {
		target = filepath.Join(realRoot, target)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	// Resolve all symlinks in the path to prevent symlink-based path
	// traversal. Falls back to resolving the deepest existing ancestor
	// when the target does not yet exist (e.g., Write creating a file).
	realTarget, err := resolveSymlinks(absTarget)
	if err != nil {
		return "", err
	}
	if realTarget != absTarget {
		log.Printf("[WARN] workspace path resolved through symlink: %s -> %s", absTarget, realTarget)
	}
	rel, err := filepath.Rel(realRoot, realTarget)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", target)
	}
	return realTarget, nil
}

// resolveSymlinks resolves all symlinks in path. If path or any of its
// ancestors do not exist, it walks up to the deepest existing ancestor,
// resolves symlinks there, and appends the non-existent suffix. This
// allows tools like Write to validate paths before the file is created.
func resolveSymlinks(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("symlink resolution failed for %s: %w", path, err)
	}
	// Walk up until we find an existing ancestor whose symlinks we can resolve.
	suffix := filepath.Base(path)
	for current := filepath.Dir(path); ; {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(resolved, suffix), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("symlink resolution failed for %s: %w", current, err)
		}
		base := filepath.Base(current)
		suffix = filepath.Join(base, suffix)
		parent := filepath.Dir(current)
		if parent == current {
			// Reached the filesystem root; no existing ancestor found.
			return path, nil
		}
		current = parent
	}
}
