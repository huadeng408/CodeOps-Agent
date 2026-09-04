package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// secureFilePath validates a file-tool target without following symlinks in
// the workspace. Missing leaf and descendant components are allowed so Write
// can create new files, but every existing component must be a real directory
// or the final regular file.
func secureFilePath(root, target string) (string, error) {
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("workspace root symlink resolution failed: %w", err)
	}
	if target == "" {
		return "", fmt.Errorf("path is required")
	}
	absTarget := target
	if !filepath.IsAbs(absTarget) {
		absTarget = filepath.Join(realRoot, target)
	}
	absTarget, err = filepath.Abs(absTarget)
	if err != nil {
		return "", err
	}
	absTarget = filepath.Clean(absTarget)
	rel, err := filepath.Rel(realRoot, absTarget)
	if err != nil {
		return "", err
	}
	if rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
		return "", fmt.Errorf("path escapes workspace: %s", target)
	}

	current := realRoot
	for _, part := range splitPath(rel) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			// No later component exists either, so there cannot be an existing
			// symlink to follow. The caller revalidates after creating parents.
			break
		}
		if statErr != nil {
			return "", fmt.Errorf("inspect workspace path %s: %w", target, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink path is not allowed: %s", target)
		}
		// EvalSymlinks also catches Windows junctions/reparse points, which
		// may not always be reported as ModeSymlink by Lstat.
		if resolved, evalErr := filepath.EvalSymlinks(current); evalErr == nil && !samePath(current, resolved) {
			return "", fmt.Errorf("symlink path is not allowed: %s", target)
		}
		if !samePath(current, absTarget) && !info.IsDir() {
			return "", fmt.Errorf("path component is not a directory: %s", target)
		}
	}
	return absTarget, nil
}

func samePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func splitPath(rel string) []string {
	if rel == "." || rel == "" {
		return nil
	}
	parts := make([]string, 0, 4)
	for rel != "." && rel != "" {
		base := filepath.Base(rel)
		parts = append([]string{base}, parts...)
		next := filepath.Dir(rel)
		if next == rel {
			break
		}
		rel = next
	}
	return parts
}
