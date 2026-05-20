package tools

import (
	"context"
	"fmt"
	"os"
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

	output, truncated := normalizeOutput(string(data), 50_000)
	return ToolResult{Name: "Read", Output: output, Truncated: truncated}, nil
}
