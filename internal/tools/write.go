package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func executeWrite(_ context.Context, root string, args map[string]any) (ToolResult, error) {
	path, ok := stringArg(args, "path", "file")
	if !ok || path == "" {
		return ToolResult{Name: "Write", Error: "path is required"}, fmt.Errorf("path is required")
	}
	content, _ := stringArg(args, "content", "text", "body")

	abs, err := workspacePath(root, path)
	if err != nil {
		return ToolResult{Name: "Write", Error: err.Error()}, err
	}
	beforeBytes, _ := os.ReadFile(abs)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return ToolResult{Name: "Write", Error: err.Error()}, err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return ToolResult{Name: "Write", Error: err.Error()}, err
	}
	return ToolResult{
		Name:   "Write",
		Output: "written",
		Changes: []Change{{
			Path:   filepath.ToSlash(path),
			Before: string(beforeBytes),
			After:  content,
		}},
	}, nil
}
