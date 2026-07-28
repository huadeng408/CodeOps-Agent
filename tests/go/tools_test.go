package codeagent_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"code-agent/internal/tools"
)

func TestExecutorReadWriteAndGlob(t *testing.T) {
	root := t.TempDir()
	executor := tools.NewExecutor(root)

	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Write",
		Arguments: map[string]any{
			"path":    "notes/demo.txt",
			"content": "hello skeleton",
		},
	}); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	writeAgain, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Write",
		Arguments: map[string]any{
			"path":    "notes/demo.txt",
			"content": "hello updated",
		},
	})
	if err != nil {
		t.Fatalf("second write failed: %v", err)
	}
	if len(writeAgain.Changes) != 1 || writeAgain.Changes[0].Before != "hello skeleton" || writeAgain.Changes[0].After != "hello updated" {
		t.Fatalf("unexpected write changes: %#v", writeAgain.Changes)
	}

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Read",
		Arguments: map[string]any{
			"path": "notes/demo.txt",
		},
	})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(result.Output, "1\thello updated") {
		t.Fatalf("unexpected read output: %q", result.Output)
	}

	glob, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Glob",
		Arguments: map[string]any{
			"pattern": "**/*.txt",
		},
	})
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if !strings.Contains(glob.Output, filepath.ToSlash("notes/demo.txt")) {
		t.Fatalf("glob output missing file: %q", glob.Output)
	}
}

func TestExecutorReadSupportsLineRanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("alpha\nbeta\ngamma\ndelta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(root)

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Read",
		Arguments: map[string]any{
			"path":   "sample.txt",
			"offset": 1,
			"limit":  2,
		},
	})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(result.Output, "[Range read]") {
		t.Fatalf("range metadata missing: %q", result.Output)
	}
	if !strings.Contains(result.Output, "2\tbeta") || !strings.Contains(result.Output, "3\tgamma") {
		t.Fatalf("expected ranged lines with line numbers, got %q", result.Output)
	}
	if strings.Contains(result.Output, "1\talpha") || strings.Contains(result.Output, "4\tdelta") {
		t.Fatalf("range included unexpected lines: %q", result.Output)
	}
}

func TestExecutorReadSupportsStartLineAlias(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("alpha\nbeta\ngamma\ndelta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(root)

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Read",
		Arguments: map[string]any{
			"path":  "sample.txt",
			"start": 2,
			"limit": 2,
		},
	})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(result.Output, "2\tbeta") || !strings.Contains(result.Output, "3\tgamma") {
		t.Fatalf("expected ranged lines with line numbers, got %q", result.Output)
	}
	if strings.Contains(result.Output, "1\talpha") || strings.Contains(result.Output, "4\tdelta") {
		t.Fatalf("range included unexpected lines: %q", result.Output)
	}
}

func TestExecutorReadRejectsInvalidArguments(t *testing.T) {
	executor := tools.NewExecutor(t.TempDir())

	if _, err := executor.Execute(context.Background(), tools.ToolRequest{Name: "Read", Arguments: map[string]any{}}); err == nil {
		t.Fatal("expected missing path to fail")
	}

	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Read",
		Arguments: map[string]any{"path": "../outside.txt"},
	}); err == nil || !strings.Contains(err.Error(), "path escapes workspace") {
		t.Fatalf("expected workspace escape failure, got %v", err)
	}

	if err := os.WriteFile(filepath.Join(executor.Root, "sample.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Read",
		Arguments: map[string]any{"path": "sample.txt", "limit": 0},
	}); err == nil || !strings.Contains(err.Error(), "limit must be positive") {
		t.Fatalf("expected invalid limit failure, got %v", err)
	}
}

func TestExecutorGlobLimitsOutput(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("sample-%d.txt", i)), []byte("alpha"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	executor := tools.NewExecutor(root)

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Glob",
		Arguments: map[string]any{
			"pattern":    "**/*.txt",
			"head_limit": 2,
		},
	})
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if !result.Truncated || !strings.Contains(result.Output, "[glob output truncated: 1 more files]") {
		t.Fatalf("expected truncated glob output, got result=%+v", result)
	}
}

func TestExecutorGlobUsesDefaultLimit(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 501; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("sample-%03d.txt", i)), []byte("alpha"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	executor := tools.NewExecutor(root)

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Glob",
		Arguments: map[string]any{"pattern": "**/*.txt"},
	})
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	// 501 个文件同时超过默认的 500 文件计数上限与 250 行输出上限，结果必须被截断。
	if !result.Truncated {
		t.Fatalf("expected default glob truncation, got result=%+v", result)
	}
	// 列表被裁剪：实际显示的文件数必须远少于 501。
	fileCount := strings.Count(result.Output, "sample-")
	if fileCount <= 0 || fileCount >= 501 {
		t.Fatalf("expected glob listing to be capped below 501 files, got %d: %q", fileCount, result.Output)
	}
}

func TestExecutorEditReturnsUndoChange(t *testing.T) {
	root := t.TempDir()
	executor := tools.NewExecutor(root)
	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Write",
		Arguments: map[string]any{
			"path":    "notes/demo.txt",
			"content": "alpha beta",
		},
	}); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Edit",
		Arguments: map[string]any{
			"path": "notes/demo.txt",
			"old":  "beta",
			"new":  "gamma",
		},
	})
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	if len(result.Changes) != 1 || result.Changes[0].Before != "alpha beta" || result.Changes[0].After != "alpha gamma" {
		t.Fatalf("unexpected edit changes: %#v", result.Changes)
	}
}

func TestExecutorEditRequiresUniqueMatchUnlessReplaceAll(t *testing.T) {
	root := t.TempDir()
	executor := tools.NewExecutor(root)
	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Write",
		Arguments: map[string]any{"path": "sample.txt", "content": "alpha beta beta"},
	}); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Edit",
		Arguments: map[string]any{"path": "sample.txt", "old": "beta", "new": "gamma"},
	})
	if err == nil || !strings.Contains(result.Error, "not unique") {
		t.Fatalf("expected non-unique edit failure, got result=%+v err=%v", result, err)
	}

	result, err = executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Edit",
		Arguments: map[string]any{"path": "sample.txt", "old": "beta", "new": "gamma", "replace_all": true},
	})
	if err != nil {
		t.Fatalf("replace_all edit failed: %v", err)
	}
	if !strings.Contains(result.Changes[0].After, "alpha gamma gamma") {
		t.Fatalf("unexpected replace_all content: %#v", result.Changes)
	}
}

func TestExecutorGrepSupportsOutputModesAndContext(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "sample.txt"), []byte("before\nalpha one\nafter\nalpha two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(root)

	content, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Grep",
		Arguments: map[string]any{
			"pattern":     "alpha",
			"path":        "src",
			"output_mode": "content",
			"A":           1,
			"head_limit":  10,
		},
	})
	if err != nil {
		t.Fatalf("grep content failed: %v", err)
	}
	if !strings.Contains(content.Output, "sample.txt:2:alpha one") || !strings.Contains(content.Output, "sample.txt-3-after") {
		t.Fatalf("unexpected grep content output: %q", content.Output)
	}

	count, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Grep",
		Arguments: map[string]any{
			"pattern":     "alpha",
			"path":        "src",
			"output_mode": "count",
		},
	})
	if err != nil {
		t.Fatalf("grep count failed: %v", err)
	}
	if strings.TrimSpace(count.Output) != "src/sample.txt:2" {
		t.Fatalf("unexpected grep count output: %q", count.Output)
	}
}

func TestExecutorGrepHeadLimitMarksResultTruncated(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("alpha one\nalpha two\nalpha three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(root)

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Grep",
		Arguments: map[string]any{
			"pattern":     "alpha",
			"output_mode": "content",
			"head_limit":  1,
		},
	})
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}
	if !result.Truncated || !strings.Contains(result.Output, "[grep output truncated: 2 more lines]") {
		t.Fatalf("expected truncated grep output, got result=%+v", result)
	}
}

func TestExecutorGitBlocksUnsafeArguments(t *testing.T) {
	executor := tools.NewExecutor(t.TempDir())
	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Git",
		Arguments: map[string]any{
			"command": "reset",
			"args":    []any{"--hard", "HEAD"},
		},
	})
	if err == nil || !strings.Contains(result.Error, "blocked git command") {
		t.Fatalf("expected blocked git reset, got result=%+v err=%v", result, err)
	}
}

func TestExecutorReadSupportsImageAndPDFMetadata(t *testing.T) {
	root := t.TempDir()
	pngData := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := os.WriteFile(filepath.Join(root, "image.png"), pngData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "doc.pdf"), []byte("%PDF-1.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(root)

	image, err := executor.Execute(context.Background(), tools.ToolRequest{Name: "Read", Arguments: map[string]any{"path": "image.png"}})
	if err != nil {
		t.Fatalf("image read failed: %v", err)
	}
	if !strings.Contains(image.Output, "[Image image.png: image/png") || !strings.Contains(image.Output, "base64") {
		t.Fatalf("unexpected image output: %q", image.Output)
	}

	pdf, err := executor.Execute(context.Background(), tools.ToolRequest{Name: "Read", Arguments: map[string]any{"path": "doc.pdf", "pages": "1"}})
	if err != nil {
		t.Fatalf("pdf read failed: %v", err)
	}
	if !strings.Contains(pdf.Output, "[PDF doc.pdf") || !strings.Contains(pdf.Output, "pages: 1") {
		t.Fatalf("unexpected pdf output: %q", pdf.Output)
	}
}

func TestExecutorWebSearchWithLocalEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "local agent" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"Heading":"Code Agent","AbstractText":"Local agent result","AbstractURL":"https://example.test/agent","RelatedTopics":[{"Text":"Result one","FirstURL":"https://example.test/one"}]}`))
	}))
	defer server.Close()

	executor := tools.NewExecutor(t.TempDir())
	executor.SetHTTPAllowPrivate(true) // local mock server binds 127.0.0.1
	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "WebSearch",
		Arguments: map[string]any{
			"query":    "local agent",
			"endpoint": server.URL,
		},
	})
	if err != nil {
		t.Fatalf("web search failed: %v", err)
	}
	if !strings.Contains(result.Output, "Code Agent") || !strings.Contains(result.Output, "Result one") {
		t.Fatalf("unexpected web search output: %q", result.Output)
	}
}

func TestExecutorBashRunsSafeCommand(t *testing.T) {
	root := t.TempDir()
	executor := tools.NewExecutor(root)
	command := "printf ok"
	if runtime.GOOS == "windows" {
		command = "Write-Output ok"
	}

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Bash",
		Arguments: map[string]any{
			"command": command,
		},
	})
	if err != nil {
		t.Fatalf("bash failed: %v output=%q", err, result.Output)
	}
	if !strings.Contains(result.Output, "ok") {
		t.Fatalf("unexpected bash output: %q", result.Output)
	}
}

func TestExecutorBashPreservesUTF8OutputOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell UTF-8 output is Windows-specific")
	}
	executor := tools.NewExecutor(t.TempDir())

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Bash",
		Arguments: map[string]any{
			"command": "Write-Output '你好 ┌─┐'",
		},
	})
	if err != nil {
		t.Fatalf("bash failed: %v output=%q", err, result.Output)
	}
	if !strings.Contains(result.Output, "你好 ┌─┐") {
		t.Fatalf("unicode output was not preserved: %q", result.Output)
	}
}

func TestExecutorBashBlocksDangerousCommand(t *testing.T) {
	executor := tools.NewExecutor(t.TempDir())

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Bash",
		Arguments: map[string]any{
			"command": "rm -rf /",
		},
	})
	if err == nil {
		t.Fatal("expected dangerous command to be blocked")
	}
	if !strings.Contains(result.Error, "blocked command") {
		t.Fatalf("unexpected error: %q", result.Error)
	}
}

func TestExecutorBashPersistsWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(root)

	if result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Bash",
		Arguments: map[string]any{"command": "cd nested"},
	}); err != nil {
		t.Fatalf("cd failed: %v output=%q", err, result.Output)
	}
	if got := executor.WorkingDir(); got != filepath.Join(root, "nested") {
		t.Fatalf("unexpected working dir: %s", got)
	}

	command := "pwd"
	if runtime.GOOS == "windows" {
		command = "(Get-Location).Path"
	}
	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Bash",
		Arguments: map[string]any{"command": command},
	})
	if err != nil {
		t.Fatalf("pwd failed: %v output=%q", err, result.Output)
	}
	if !strings.Contains(filepath.Clean(result.Output), filepath.Join(root, "nested")) {
		t.Fatalf("bash did not use persisted working dir: %q", result.Output)
	}
}

func TestExecutorBashRejectsWorkingDirectoryEscape(t *testing.T) {
	executor := tools.NewExecutor(t.TempDir())

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name:      "Bash",
		Arguments: map[string]any{"command": "cd .."},
	})
	if err == nil {
		t.Fatal("expected cd outside workspace to fail")
	}
	if !strings.Contains(result.Error, "path escapes workspace") {
		t.Fatalf("unexpected error: %q", result.Error)
	}
}

// TestExecutorTruncateOutputHonorsLineAndByteLimits 直接驱动 normalizeOutput 策略
// （通过导出的 TruncateOutput 方法），覆盖设计方案 22.4 的三类情形：双上限内不截断、
// 超行数上限按行截断、超字节上限按字节截断。
func TestExecutorTruncateOutputHonorsLineAndByteLimits(t *testing.T) {
	t.Run("under both limits is unchanged", func(t *testing.T) {
		executor := tools.NewExecutor(t.TempDir())
		executor.MaxOutputLines = 250
		executor.MaxOutputBytes = 50_000

		small := "alpha\nbeta\n"
		out, truncated := executor.TruncateOutput(small)
		if truncated {
			t.Fatalf("expected small output to be unchanged, got truncated=true out=%q", out)
		}
		if out != small {
			t.Fatalf("expected identical output, got %q", out)
		}
	})

	t.Run("exceeds line limit is line-capped", func(t *testing.T) {
		executor := tools.NewExecutor(t.TempDir())
		executor.MaxOutputLines = 5
		executor.MaxOutputBytes = 50_000

		many := strings.Repeat("line\n", 20) // 20 "line" 行 + 末尾空串 = 21 个元素
		out, truncated := executor.TruncateOutput(many)
		if !truncated {
			t.Fatalf("expected line-capped output to be truncated, got %q", out)
		}
		if !strings.Contains(out, "[Output truncated: 21 lines total, showing first 5]") {
			t.Fatalf("expected informative line-truncation notice, got %q", out)
		}
		if !strings.HasPrefix(out, "line\nline\nline\nline\nline") {
			t.Fatalf("expected first 5 lines retained, got %q", out)
		}
	})

	t.Run("exceeds byte limit is byte-capped", func(t *testing.T) {
		executor := tools.NewExecutor(t.TempDir())
		executor.MaxOutputLines = 0 // 关闭行截断以单独验证字节截断
		executor.MaxOutputBytes = 100

		big := strings.Repeat("x", 500)
		out, truncated := executor.TruncateOutput(big)
		if !truncated {
			t.Fatalf("expected byte-capped output to be truncated, got %q", out)
		}
		if !strings.HasPrefix(out, strings.Repeat("x", 100)) {
			t.Fatalf("expected first 100 bytes retained, got prefix=%q", out[:min(100, len(out))])
		}
		if !strings.Contains(out, "[Output truncated at 100 bytes]") {
			t.Fatalf("expected byte-truncation notice, got %q", out)
		}
		// 截断后内容 = 100 字节前缀 + 短提示，远小于原始 500 字节。
		if len(out) >= len(big) {
			t.Fatalf("expected byte-capped output to be smaller than input: %d vs %d", len(out), len(big))
		}
	})
}
