package tools

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
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

	data, err := os.ReadFile(abs)
	if err != nil {
		return ToolResult{Name: "Read", Error: err.Error()}, err
	}

	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	offset, hasOffset, err := intArg(args, "offset")
	if err != nil {
		return ToolResult{Name: "Read", Error: err.Error()}, err
	}
	limit, hasLimit, err := intArg(args, "limit")
	if err != nil {
		return ToolResult{Name: "Read", Error: err.Error()}, err
	}
	if hasOffset && offset < 0 {
		return ToolResult{Name: "Read", Error: "offset must be non-negative"}, fmt.Errorf("offset must be non-negative")
	}
	if hasLimit && limit <= 0 {
		return ToolResult{Name: "Read", Error: "limit must be positive"}, fmt.Errorf("limit must be positive")
	}

	start := 0
	if hasOffset {
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
	if hasOffset || hasLimit {
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

func intArg(args map[string]any, key string) (int, bool, error) {
	value, ok := args[key]
	if !ok {
		return 0, false, nil
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
