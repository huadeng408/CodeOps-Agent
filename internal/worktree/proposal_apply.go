package worktree

import (
	"context"
	"errors"
)

// ApplySingleFile preserves the original API as a one-way adapter to the
// common lifecycle application module.
func (m *Manager) ApplySingleFile(ctx context.Context, baseline Baseline, change TaskFileChange, intent func() error) (bool, error) {
	if intent == nil {
		return true, errors.New("application intent is required")
	}
	mode := uint32(0)
	for _, file := range baseline.Files {
		if file.Path == change.Path && file.Exists {
			mode = file.Mode
			break
		}
	}
	if mode == 0 {
		return true, errors.New("application requires an existing baseline file")
	}
	results, err := m.ApplyFiles(ctx, baseline, []FileApplication{{Path: change.Path, Operation: "edit", Before: change.Before, After: change.After, BeforeExists: true, AfterExists: true, BeforeMode: mode, AfterMode: mode}}, func(_ int, result *FileApplicationResult) error {
		if result == nil {
			return intent()
		}
		return nil
	})
	return len(results) == 1 && results[0].State != "unknown", err
}
