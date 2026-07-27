package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (e *Executor) executeRead(ctx context.Context, args map[string]any) (ToolResult, error) {
	path, ok := stringArg(args, "path", "file")
	if !ok || path == "" {
		return ToolResult{Name: "Read", Error: "path is required"}, fmt.Errorf("path is required")
	}

	abs, err := workspacePath(e.Root, path)
	if err != nil {
		return ToolResult{Name: "Read", Error: err.Error()}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return ToolResult{Name: "Read", Error: err.Error()}, err
	}
	if info.IsDir() {
		err := fmt.Errorf("cannot read directory: %s", path)
		return ToolResult{Name: "Read", Error: err.Error()}, err
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return ToolResult{Name: "Read", Error: err.Error()}, err
	}
	if len(data) == 0 {
		return ToolResult{Name: "Read", Output: fmt.Sprintf("[Read %s: empty file]", path)}, nil
	}
	if isImagePath(abs) {
		return e.readImage(path, data)
	}
	if strings.EqualFold(filepath.Ext(abs), ".pdf") {
		return e.readPDF(ctx, path, data, args)
	}
	if strings.EqualFold(filepath.Ext(abs), ".ipynb") {
		return e.readNotebook(path, data)
	}
	if !utf8.Valid(data) || looksBinary(data) {
		err := fmt.Errorf("binary file cannot be displayed as text: %s", path)
		return ToolResult{Name: "Read", Error: err.Error(), ExitCode: 1}, err
	}

	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	startLine, hasStart, err := intArg(args, "start")
	if err != nil {
		return ToolResult{Name: "Read", Error: err.Error()}, err
	}
	offset, hasOffset, err := intArg(args, "offset")
	if err != nil {
		return ToolResult{Name: "Read", Error: err.Error()}, err
	}
	limit, hasLimit, err := intArg(args, "limit")
	if err != nil {
		return ToolResult{Name: "Read", Error: err.Error()}, err
	}
	if hasStart && startLine <= 0 {
		return ToolResult{Name: "Read", Error: "start must be positive"}, fmt.Errorf("start must be positive")
	}
	if hasOffset && offset < 0 {
		return ToolResult{Name: "Read", Error: "offset must be non-negative"}, fmt.Errorf("offset must be non-negative")
	}
	if hasStart && hasOffset && offset != startLine-1 {
		return ToolResult{Name: "Read", Error: "start and offset refer to different lines"}, fmt.Errorf("start and offset refer to different lines")
	}
	if hasLimit && limit <= 0 {
		return ToolResult{Name: "Read", Error: "limit must be positive"}, fmt.Errorf("limit must be positive")
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

	output := numberedLines(lines[start:end], start+1)
	metadata := []string{fmt.Sprintf("[Read %s: lines %d-%d of %d]", path, displayStart(start, end), end, len(lines))}
	if hasStart || hasOffset || hasLimit {
		metadata = append(metadata, "[Range read]")
	}
	if output != "" {
		output = strings.Join(metadata, "\n") + "\n" + output
	} else {
		output = strings.Join(metadata, "\n")
	}

	output, truncated := e.TruncateOutput(output)
	return ToolResult{Name: "Read", Output: output, Truncated: truncated}, nil
}

func (e *Executor) readImage(path string, data []byte) (ToolResult, error) {
	mime := imageMime(path)
	encoded := base64.StdEncoding.EncodeToString(data)
	output, truncated := e.TruncateOutput(fmt.Sprintf("[Image %s: %s, %d bytes]\ndata:%s;base64,%s", path, mime, len(data), mime, encoded))
	return ToolResult{Name: "Read", Output: output, Truncated: truncated}, nil
}

// readNotebook 把 .ipynb 渲染成简洁的单元格摘要（而非原始 JSON），保持输出可读、
// 并受 TruncateOutput 约束。
func (e *Executor) readNotebook(path string, data []byte) (ToolResult, error) {
	output, truncated := e.TruncateOutput(renderNotebookSummary(path, data))
	return ToolResult{Name: "Read", Output: output, Truncated: truncated}, nil
}

// readPDF 调用 Poppler 的 pdftotext 抽取文本。pdftotext 不在 PATH 上时优雅回退到
// 明确的提示信息（本项目依 AGENT.md 不引入第三方 Go PDF 依赖）。pages 形如 "1-3"
// 或 "1"，"all"/空表示全部页。
func (e *Executor) readPDF(ctx context.Context, path string, data []byte, args map[string]any) (ToolResult, error) {
	pages, _ := stringArg(args, "pages", "page")
	pages = strings.TrimSpace(pages)
	if pages == "" {
		pages = "all"
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		err := fmt.Errorf("not a valid PDF file: %s", path)
		return ToolResult{Name: "Read", Error: err.Error(), ExitCode: 1}, err
	}

	exe, lookErr := exec.LookPath("pdftotext")
	if lookErr != nil {
		notice := fmt.Sprintf(
			"[PDF %s: %d bytes]\npages: %s\nPDF text extraction requires an external tool. "+
				"Install Poppler (pdftotext) on PATH and retry, or attach pre-extracted text / convert the page to an image.",
			path, len(data), pages,
		)
		output, truncated := e.TruncateOutput(notice)
		return ToolResult{Name: "Read", Output: output, Truncated: truncated}, nil
	}

	cmdArgs := []string{}
	if first, last, ok := parsePDFPageRange(pages); ok {
		cmdArgs = append(cmdArgs, "-f", strconv.Itoa(first), "-l", strconv.Itoa(last))
	}
	cmdArgs = append(cmdArgs, path, "-") // 末尾 "-" 让 pdftotext 输出到 stdout

	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, runErr := exec.CommandContext(runCtx, exe, cmdArgs...).Output()

	header := fmt.Sprintf("[PDF %s: %d bytes, pages: %s]\n", path, len(data), pages)
	if runErr != nil {
		notice := fmt.Sprintf(
			"%sPDF text extraction failed via pdftotext: %v\n"+
				"The file may be corrupted or scanned; install/verify Poppler or attach extracted text.",
			header, runErr,
		)
		output, truncated := e.TruncateOutput(notice)
		return ToolResult{Name: "Read", Output: output, Truncated: truncated}, nil
	}
	text := string(out)
	if strings.TrimSpace(text) == "" {
		text = "(no extractable text; the PDF may be scanned images — try OCR or export the page as an image)"
	}
	output, truncated := e.TruncateOutput(header + text)
	return ToolResult{Name: "Read", Output: output, Truncated: truncated}, nil
}

// parsePDFPageRange 解析 "1" / "1-3" 形式的页码范围，返回 (首页, 末页, 是否有效)。
func parsePDFPageRange(pages string) (int, int, bool) {
	pages = strings.TrimSpace(pages)
	if pages == "" || strings.EqualFold(pages, "all") {
		return 0, 0, false
	}
	if dash := strings.Index(pages, "-"); dash >= 0 {
		first, err1 := strconv.Atoi(strings.TrimSpace(pages[:dash]))
		last, err2 := strconv.Atoi(strings.TrimSpace(pages[dash+1:]))
		if err1 == nil && err2 == nil && first > 0 && last >= first {
			return first, last, true
		}
		return 0, 0, false
	}
	n, err := strconv.Atoi(pages)
	if err == nil && n > 0 {
		return n, n, true
	}
	return 0, 0, false
}

func isImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg":
		return true
	default:
		return false
	}
}

func imageMime(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

func looksBinary(data []byte) bool {
	limit := len(data)
	if limit > 8192 {
		limit = 8192
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return true
		}
	}
	return false
}

func intArg(args map[string]any, keys ...string) (int, bool, error) {
	for _, key := range keys {
		value, ok := args[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case int:
			return v, true, nil
		case int32:
			return int(v), true, nil
		case int64:
			return int(v), true, nil
		case float64:
			if v != float64(int(v)) {
				return 0, true, fmt.Errorf("%s must be an integer", key)
			}
			return int(v), true, nil
		case string:
			parsed, err := strconv.Atoi(v)
			if err != nil {
				return 0, true, fmt.Errorf("%s must be an integer", key)
			}
			return parsed, true, nil
		default:
			return 0, true, fmt.Errorf("%s must be an integer", key)
		}
	}
	return 0, false, nil
}

func numberedLines(lines []string, startLine int) string {
	if len(lines) == 0 {
		return ""
	}
	var builder strings.Builder
	for i, line := range lines {
		if i > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(strconv.Itoa(startLine + i))
		builder.WriteByte('\t')
		builder.WriteString(line)
	}
	return builder.String()
}

func displayStart(start, end int) int {
	if end == 0 {
		return 0
	}
	return start + 1
}
