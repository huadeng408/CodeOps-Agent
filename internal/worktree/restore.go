package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileTransition is the content needed to restore one workspace file. Before
// and After are never accepted blindly: the current file must still match
// After before any file is changed.
type FileTransition struct {
	Path   string
	Before string
	After  string
}

// RestoreFileTransitions restores a set of files only when every transition
// is conflict-free. Preflighting the complete set prevents partial rollback.
func RestoreFileTransitions(ctx context.Context, root string, changes []FileTransition) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	paths := make([]string, len(changes))
	for i, change := range changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := restorePath(rootAbs, change.Path)
		if err != nil {
			return err
		}
		paths[i] = path
		current, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && change.After == "" {
				continue
			}
			return fmt.Errorf("read %s: %w", change.Path, err)
		}
		if string(current) != change.After {
			return fmt.Errorf("workspace conflict for %s: current content hash %s does not match expected after hash %s", change.Path, digest(string(current)), digest(change.After))
		}
	}
	// All reads and conflict checks have passed. Writes happen only now.
	for i := len(changes) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		change := changes[i]
		path := paths[i]
		if change.Before == "" {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove %s: %w", change.Path, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create parent for %s: %w", change.Path, err)
		}
		if err := os.WriteFile(path, []byte(change.Before), 0o644); err != nil {
			return fmt.Errorf("restore %s: %w", change.Path, err)
		}
	}
	return nil
}

func restorePath(root, target string) (string, error) {
	if strings.TrimSpace(target) == "" || filepath.IsAbs(target) {
		return "", fmt.Errorf("restore path must be relative to workspace")
	}
	abs, err := filepath.Abs(filepath.Join(root, target))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("restore path escapes workspace: %s", target)
	}
	rootReal, err := resolvePathWithExistingParent(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	targetReal, err := resolvePathWithExistingParent(abs)
	if err != nil || !pathWithin(rootReal, targetReal, false) {
		return "", fmt.Errorf("restore path escapes workspace: %s", target)
	}
	return abs, nil
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
