package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (e *Executor) executeEdit(_ context.Context, args map[string]any) (ToolResult, error) {
	path, ok := stringArg(args, "path", "file")
	if !ok || path == "" {
		return ToolResult{Name: "Edit", Error: "path is required"}, fmt.Errorf("path is required")
	}
	oldText, _ := stringArg(args, "old", "old_string", "from", "search")
	newText, _ := stringArg(args, "new", "new_string", "to", "replace")
	if oldText == "" {
		return ToolResult{Name: "Edit", Error: "old text is required"}, fmt.Errorf("old text is required")
	}

	abs, err := e.workingFilePath(path)
	if err != nil {
		return ToolResult{Name: "Edit", Error: err.Error()}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return ToolResult{Name: "Edit", Error: err.Error()}, err
	}

	before := string(data)
	replaceAll := boolArg(args, "replace_all", "replaceAll", "all")
	count := strings.Count(before, oldText)
	if count == 0 {
		err := fmt.Errorf("old text not found in %s", path)
		return ToolResult{Name: "Edit", Error: err.Error(), ExitCode: 1}, err
	}
	if !replaceAll && count > 1 {
		err := fmt.Errorf("old text is not unique in %s; set replace_all to true to replace all %d occurrences", path, count)
		return ToolResult{Name: "Edit", Error: err.Error(), ExitCode: 1}, err
	}

	replaced := strings.Replace(before, oldText, newText, 1)
	if replaceAll {
		replaced = strings.ReplaceAll(before, oldText, newText)
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(abs); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := atomicWriteFile(abs, []byte(replaced), mode); err != nil {
		return ToolResult{Name: "Edit", Error: err.Error()}, err
	}
	return ToolResult{
		Name:   "Edit",
		Output: fmt.Sprintf("edited %s (%d replacement%s)", filepath.ToSlash(path), countForOutput(count, replaceAll), pluralSuffix(countForOutput(count, replaceAll))),
		Changes: []Change{{
			Path:   filepath.ToSlash(path),
			Before: before,
			After:  replaced,
		}},
	}, nil
}

func countForOutput(count int, replaceAll bool) int {
	if replaceAll {
		return count
	}
	return 1
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
