package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func (e *Executor) executeWrite(_ context.Context, args map[string]any) (ToolResult, error) {
	path, ok := stringArg(args, "path", "file")
	if !ok || path == "" {
		return ToolResult{Name: "Write", Error: "path is required"}, fmt.Errorf("path is required")
	}
	content, _ := stringArg(args, "content", "text", "body")

	abs, err := e.workingFilePath(path)
	if err != nil {
		return ToolResult{Name: "Write", Error: err.Error()}, err
	}
	beforeBytes, _ := os.ReadFile(abs)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return ToolResult{Name: "Write", Error: err.Error()}, err
	}
	// Validate again after parent creation to close the common symlink-swap
	// window before the temporary file is opened.
	abs, err = e.workingFilePath(path)
	if err != nil {
		return ToolResult{Name: "Write", Error: err.Error()}, err
	}
	if err := atomicWriteFile(abs, []byte(content), 0o644); err != nil {
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
