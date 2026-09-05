package tools

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"code-agent/internal/jobs"
	"code-agent/internal/mcp"
	"code-agent/internal/rag"
	"code-agent/internal/sandbox"
	"code-agent/internal/skills"
	"code-agent/internal/telemetry/genai"
)

const maxMultimodalBytes = 20 << 20

type ToolRequest struct {
	Name           string         `json:"name"`
	Arguments      map[string]any `json:"arguments,omitempty"`
	OwnerSessionID string         `json:"owner_session_id,omitempty"`
}

type ToolResult struct {
	Name          string         `json:"name"`
	Output        string         `json:"output,omitempty"`
	Error         string         `json:"error,omitempty"`
	ExitCode      int            `json:"exit_code"`
	Truncated     bool           `json:"truncated"`
	Spill         *SpillRef      `json:"spill,omitempty"`
	Changes       []Change       `json:"changes,omitempty"`
	ContentBlocks []ContentBlock `json:"content_blocks,omitempty"`
	spillContent  string
}

type ContentBlock struct {
	Text      string `json:"text,omitempty"`
	ImageBlob []byte `json:"image_blob,omitempty"`
	MIME      string `json:"mime,omitempty"`
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
	rag            rag.Searcher
	sandbox        sandbox.Runner
	skills         *skills.Manager
	spill          SpillStore
	// httpAllowPrivate lifts the SSRF private/loopback block for WebFetch/WebSearch.
	// Intended only for tests and trusted local providers; production MUST stay false.
	httpAllowPrivate bool
	tracer           genai.Tracer
	jobs             *jobs.Registry
}

func NewExecutor(root string) *Executor {
	spill, _ := NewFileSpillStore(filepath.Join(root, ".runtime", "spill"))
	return &Executor{
		Root:           root,
		MaxOutputBytes: 50_000,
		MaxOutputLines: 250,
		spill:          spill,
		jobs:           jobs.NewRegistry(root, jobs.Config{}),
	}
}

// SetJobsRegistry replaces the process lifecycle registry. It is intended for
// integration tests and Harness composition; production executors get one from
// NewExecutor and keep it as the sole job state owner.
func (e *Executor) SetJobsRegistry(registry *jobs.Registry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.jobs = registry
}

// Jobs returns the configured background-job registry for lifecycle-aware
// callers such as the CLI shutdown path.
func (e *Executor) Jobs() *jobs.Registry {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.jobs
}

// Close releases background process resources owned by this executor.
func (e *Executor) Close() error {
	e.mu.Lock()
	registry := e.jobs
	e.mu.Unlock()
	if registry == nil {
		return nil
	}
	return registry.Close()
}

func (e *Executor) SetMCPManager(manager *mcp.Manager) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mcp = manager
}

// SetRAGSearcher configures the knowledge search backend used by SearchKnowledge.
func (e *Executor) SetRAGSearcher(searcher rag.Searcher) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rag = searcher
}

// SetSandbox routes Bash commands through a constrained backend. Once set, a
// sandbox failure is returned to the caller and never retried on the host.
func (e *Executor) SetSandbox(runner sandbox.Runner) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sandbox = runner
}

func (e *Executor) sandboxRunner() sandbox.Runner {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sandbox
}

// HasSandbox reports whether Bash is constrained by a configured backend.
func (e *Executor) HasSandbox() bool {
	return e.sandboxRunner() != nil
}

// SetTracer injects a genai.Tracer for creating execute_tool and retrieve spans.
func (e *Executor) SetTracer(t genai.Tracer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tracer = t
}

// UsesRAGSearcher reports whether the executor is configured with this RAG client instance.
// The concrete client check avoids relying on interface comparability for arbitrary implementations.
func (e *Executor) UsesRAGSearcher(searcher rag.Searcher) bool {
	wanted, ok := searcher.(*rag.Client)
	if !ok || wanted == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	configured, ok := e.rag.(*rag.Client)
	return ok && configured != nil && configured == wanted
}

func (e *Executor) SetSkillsManager(manager *skills.Manager) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.skills = manager
}

// SetSpillStore configures an optional best-effort backend for complete,
// redacted tool output that exceeds the model preview budget.
func (e *Executor) SetSpillStore(store SpillStore) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spill = store
}

// SetHTTPAllowPrivate enables fetching loopback/private addresses in WebFetch/WebSearch.
// Tests use this to target a local mock server; production must leave it disabled.
func (e *Executor) SetHTTPAllowPrivate(allow bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.httpAllowPrivate = allow
}

func (e *Executor) Execute(ctx context.Context, req ToolRequest) (ToolResult, error) {
	var result ToolResult
	var err error
	switch req.Name {
	case "Read":
		result, err = e.executeRead(ctx, req.Arguments)
	case "Edit":
		result, err = e.executeEdit(ctx, req.Arguments)
	case "NotebookEdit":
		result, err = e.executeNotebookEdit(ctx, req.Arguments)
	case "Write":
		result, err = e.executeWrite(ctx, req.Arguments)
	case "Bash":
		result, err = e.executeBash(ctx, req.Arguments)
	case "JobStart", "job_start":
		result, err = e.executeJobStart(ctx, req)
	case "JobOutput", "job_output":
		result, err = e.executeJobOutput(ctx, req)
	case "JobList", "job_list":
		result, err = e.executeJobList(ctx, req)
	case "JobKill", "job_kill":
		result, err = e.executeJobKill(ctx, req)
	case "JobWrite", "job_write":
		result, err = e.executeJobWrite(ctx, req)
	case "JobWait", "job_wait":
		result, err = e.executeJobWait(ctx, req)
	case "Glob":
		result, err = e.executeGlob(ctx, req.Arguments)
	case "Grep":
		result, err = e.executeGrep(ctx, req.Arguments)
	case "Git":
		result, err = e.executeGit(ctx, req.Arguments)
	case "WebFetch":
		result, err = e.executeWebFetch(ctx, req.Arguments)
	case "WebSearch":
		result, err = e.executeWebSearch(ctx, req.Arguments)
	case "SearchKnowledge":
		result, err = e.executeSearchKnowledge(ctx, req.Arguments)
	case "Skill":
		result, err = e.executeSkill(ctx, req.Arguments)
	case "ReadSpill":
		result, err = e.executeReadSpill(ctx, req.Arguments)
	default:
		var ok bool
		if result, ok, err = e.executeMCPTool(ctx, req); !ok {
			result = ToolResult{Name: req.Name, Error: "unknown tool"}
			err = fmt.Errorf("unknown tool %q", req.Name)
		}
	}
	return e.processResult(ctx, result), err
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
	skill, ok, err := manager.Load(name)
	if err != nil {
		return ToolResult{Name: "Skill", Error: err.Error(), ExitCode: 1}, nil
	}
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
	return ToolResult{Name: "Skill", Output: output}, nil
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

// processResult is the sole model-facing result boundary. It redacts text
// first, persists a complete redacted result when preview truncation occurs,
// and never turns a successfully executed tool into a failure when spill
// storage is absent or unavailable. Changes stay untouched for undo fidelity.
func (e *Executor) processResult(ctx context.Context, result ToolResult) ToolResult {
	result.Output = redactToolText(result.Output)
	result.Error = redactToolText(result.Error)
	for index := range result.ContentBlocks {
		result.ContentBlocks[index].Text = redactToolText(result.ContentBlocks[index].Text)
	}
	if result.Output == "" {
		return result
	}
	complete := result.Output
	if result.spillContent != "" {
		complete = redactToolText(result.spillContent)
	}
	preview, truncated := normalizeOutput(complete, e.MaxOutputLines, e.MaxOutputBytes)
	if result.spillContent == "" {
		result.Output = preview
	}
	result.Truncated = result.Truncated || truncated
	if !result.Truncated {
		return result
	}
	e.mu.Lock()
	store := e.spill
	e.mu.Unlock()
	if store == nil {
		return result
	}
	ref, err := store.Save(ctx, SpillRequest{ToolName: result.Name, Content: complete})
	if err != nil {
		result.Output = strings.TrimSpace(result.Output) + "\n[Complete output was not saved: spill storage failed.]"
		return result
	}
	result.Spill = &ref
	result.Output = strings.TrimSpace(result.Output) + fmt.Sprintf("\n[Complete redacted output: %s. Use ReadSpill with locator to retrieve a bounded range.]", ref.Locator)
	return result
}

func (e *Executor) executeReadSpill(ctx context.Context, args map[string]any) (ToolResult, error) {
	locator, ok := stringArg(args, "locator")
	if !ok || strings.TrimSpace(locator) == "" {
		return ToolResult{Name: "ReadSpill", Error: "spill locator is required", ExitCode: 1}, fmt.Errorf("spill locator is required")
	}
	e.mu.Lock()
	store := e.spill
	e.mu.Unlock()
	if store == nil {
		return ToolResult{Name: "ReadSpill", Error: "spill storage is not configured", ExitCode: 1}, fmt.Errorf("spill storage is not configured")
	}
	content, err := store.Load(ctx, locator)
	if err != nil {
		return ToolResult{Name: "ReadSpill", Error: "spill output is unavailable", ExitCode: 1}, fmt.Errorf("load spill: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	startLine, hasStart, err := intArg(args, "start")
	if err != nil {
		return ToolResult{Name: "ReadSpill", Error: err.Error(), ExitCode: 1}, err
	}
	offset, hasOffset, err := intArg(args, "offset")
	if err != nil {
		return ToolResult{Name: "ReadSpill", Error: err.Error(), ExitCode: 1}, err
	}
	limit, hasLimit, err := intArg(args, "limit")
	if err != nil {
		return ToolResult{Name: "ReadSpill", Error: err.Error(), ExitCode: 1}, err
	}
	if hasStart && startLine <= 0 {
		return ToolResult{Name: "ReadSpill", Error: "start must be positive", ExitCode: 1}, fmt.Errorf("start must be positive")
	}
	if hasOffset && offset < 0 {
		return ToolResult{Name: "ReadSpill", Error: "offset must be non-negative", ExitCode: 1}, fmt.Errorf("offset must be non-negative")
	}
	if hasStart && hasOffset && offset != startLine-1 {
		return ToolResult{Name: "ReadSpill", Error: "start and offset refer to different lines", ExitCode: 1}, fmt.Errorf("start and offset refer to different lines")
	}
	if hasLimit && limit <= 0 {
		return ToolResult{Name: "ReadSpill", Error: "limit must be positive", ExitCode: 1}, fmt.Errorf("limit must be positive")
	}
	if !hasStart && !hasOffset && !hasLimit {
		limit = e.MaxOutputLines
		if limit <= 0 {
			limit = 250
		}
		hasLimit = true
	}
	start := 0
	if hasStart {
		start = startLine - 1
	} else if hasOffset {
		start = offset
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := len(lines)
	if hasLimit && start+limit < end {
		end = start + limit
	}
	return ToolResult{Name: "ReadSpill", Output: strings.Join(lines[start:end], "\n")}, nil
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
		output = validUTF8Prefix(output, maxBytes) + fmt.Sprintf("\n\n[Output truncated at %s]", byteCapLabel(maxBytes))
		truncated = true
	}
	return output, truncated
}

func validUTF8Prefix(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	prefix := value[:maxBytes]
	for len(prefix) > 0 && !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
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
