package tools

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

func executeGlob(_ context.Context, root string, args map[string]any) (ToolResult, error) {
	pattern, ok := stringArg(args, "pattern")
	if !ok || pattern == "" {
		return ToolResult{Name: "Glob", Error: "pattern is required"}, fmt.Errorf("pattern is required")
	}

	matches := []string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		ok, err := matchGlob(pattern, rel)
		if err != nil {
			return err
		}
		if ok {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		return ToolResult{Name: "Glob", Error: err.Error()}, err
	}

	sort.Strings(matches)
	limit, hasLimit, err := intArg(args, "head_limit", "limit")
	if err != nil {
		return ToolResult{Name: "Glob", Error: err.Error(), ExitCode: 1}, err
	}
	if hasLimit && limit <= 0 {
		return ToolResult{Name: "Glob", Error: "head_limit must be positive", ExitCode: 1}, fmt.Errorf("head_limit must be positive")
	}
	if !hasLimit {
		limit = 500
	}
	truncated := false
	if limit > 0 && len(matches) > limit {
		remaining := len(matches) - limit
		matches = append(matches[:limit], fmt.Sprintf("[glob output truncated: %d more files]", remaining))
		truncated = true
	}
	return ToolResult{Name: "Glob", Output: strings.Join(matches, "\n"), Truncated: truncated}, nil
}

func matchGlob(pattern, candidate string) (bool, error) {
	pattern = filepath.ToSlash(pattern)
	pSegs := strings.Split(pattern, "/")
	cSegs := strings.Split(candidate, "/")
	return matchGlobSegments(pSegs, cSegs)
}

func matchGlobSegments(pattern, candidate []string) (bool, error) {
	if len(pattern) == 0 {
		return len(candidate) == 0, nil
	}
	if pattern[0] == "**" {
		if len(pattern) == 1 {
			return true, nil
		}
		for i := 0; i <= len(candidate); i++ {
			ok, err := matchGlobSegments(pattern[1:], candidate[i:])
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	if len(candidate) == 0 {
		return false, nil
	}
	ok, err := path.Match(pattern[0], candidate[0])
	if err != nil || !ok {
		return false, err
	}
	return matchGlobSegments(pattern[1:], candidate[1:])
}
