package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

func executeRead(_ context.Context, root string, args map[string]any) (ToolResult, error) {
	path, ok := stringArg(args, "path", "file")
	if !ok || path == "" {
		return ToolResult{Name: "Read", Error: "path is required"}, fmt.Errorf("path is required")
	}

	abs, err := workspacePath(root, path)
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
		return readImage(path, data)
	}
	if strings.EqualFold(filepath.Ext(abs), ".pdf") {
		return readPDF(path, data, args)
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

	output, truncated := normalizeOutput(output, 50_000)
	return ToolResult{Name: "Read", Output: output, Truncated: truncated}, nil
}

func readImage(path string, data []byte) (ToolResult, error) {
	mime := imageMime(path)
	encoded := base64.StdEncoding.EncodeToString(data)
	output, truncated := normalizeOutput(fmt.Sprintf("[Image %s: %s, %d bytes]\ndata:%s;base64,%s", path, mime, len(data), mime, encoded), 50_000)
	return ToolResult{Name: "Read", Output: output, Truncated: truncated}, nil
}

func readPDF(path string, data []byte, args map[string]any) (ToolResult, error) {
	pages, _ := stringArg(args, "pages", "page")
	if strings.TrimSpace(pages) == "" {
		pages = "all"
	}
	output, truncated := normalizeOutput(fmt.Sprintf("[PDF %s: %d bytes]\npages: %s\nPDF text extraction is not available in this local harness yet; use an external PDF parser or attach extracted text.", path, len(data), pages), 50_000)
	return ToolResult{Name: "Read", Output: output, Truncated: truncated}, nil
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
