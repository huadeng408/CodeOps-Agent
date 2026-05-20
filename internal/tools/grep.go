package tools

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func executeGrep(_ context.Context, root string, args map[string]any) (ToolResult, error) {
	pattern, ok := stringArg(args, "pattern", "query")
	if !ok || pattern == "" {
		return ToolResult{Name: "Grep", Error: "pattern is required"}, fmt.Errorf("pattern is required")
	}
	searchRoot, _ := stringArg(args, "path", "root")
	if searchRoot == "" {
		searchRoot = "."
	}
	absRoot, err := workspacePath(root, searchRoot)
	if err != nil {
		return ToolResult{Name: "Grep", Error: err.Error()}, err
	}

	results := []string{}
	err = filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		file, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer file.Close()

		rel, err := filepath.Rel(absRoot, p)
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(file)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if strings.Contains(line, pattern) {
				results = append(results, fmt.Sprintf("%s:%d:%s", filepath.ToSlash(rel), lineNo, line))
			}
		}
		return scanner.Err()
	})
	if err != nil {
		return ToolResult{Name: "Grep", Error: err.Error()}, err
	}

	sort.Strings(results)
	return ToolResult{Name: "Grep", Output: strings.Join(results, "\n")}, nil
}
