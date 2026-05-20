package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func executeEdit(_ context.Context, root string, args map[string]any) (ToolResult, error) {
	path, ok := stringArg(args, "path", "file")
	if !ok || path == "" {
		return ToolResult{Name: "Edit", Error: "path is required"}, fmt.Errorf("path is required")
	}
	oldText, _ := stringArg(args, "old", "from", "search")
	newText, _ := stringArg(args, "new", "to", "replace")
	if oldText == "" {
		return ToolResult{Name: "Edit", Error: "old text is required"}, fmt.Errorf("old text is required")
	}

	abs, err := workspacePath(root, path)
	if err != nil {
		return ToolResult{Name: "Edit", Error: err.Error()}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return ToolResult{Name: "Edit", Error: err.Error()}, err
	}

	replaced := strings.ReplaceAll(string(data), oldText, newText)
	if err := os.WriteFile(abs, []byte(replaced), 0o644); err != nil {
		return ToolResult{Name: "Edit", Error: err.Error()}, err
	}
	return ToolResult{Name: "Edit", Output: "edited"}, nil
}
