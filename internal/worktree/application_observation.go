package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"code-agent/internal/safety"
)

// ObserveApplication never executes a file transition. Its checksum identifies
// the observed working copy, not a claim of how an interrupted effect happened.
func (m *Manager) ObserveApplication(ctx context.Context, baseline Baseline, changes []FileApplication) (string, []string, error) {
	if len(changes) == 0 || len(changes) > maxBaselineFiles {
		return "", nil, errors.New("invalid application observation")
	}
	copy := baseline
	copy.Checksum = ""
	encoded, err := json.Marshal(copy)
	if err != nil || baselineDigest(encoded) != baseline.Checksum {
		return "", nil, errors.New("invalid original baseline")
	}
	current, err := m.CaptureBaseline(ctx)
	if err != nil {
		return "", nil, err
	}
	root, err := openTaskRoot(m.root)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	parents, files := []string{m.root}, []string{}
	affected := map[string]bool{}
	for _, change := range changes {
		if err := validateFileApplication(change); err != nil {
			return "", nil, err
		}
		for _, name := range []string{change.Path, change.Destination} {
			if name == "" {
				continue
			}
			for prior := range affected {
				if sameBaselinePath(name, prior) || strings.HasPrefix(strings.ToLower(name), strings.ToLower(prior)+"/") || strings.HasPrefix(strings.ToLower(prior), strings.ToLower(name)+"/") {
					return "", nil, errors.New("observation paths alias")
				}
			}
			affected[name] = true
			parent, err := applicationParent(root, name)
			if err != nil {
				return "", nil, err
			}
			parents = append(parents, filepath.Join(m.root, filepath.FromSlash(parent)))
		}
	}
	for _, file := range current.Files {
		if file.Exists {
			files = append(files, filepath.Join(m.root, filepath.FromSlash(file.Path)))
		}
	}
	stopParents, err := safety.PinDirectories(parents...)
	if err != nil {
		return "", nil, err
	}
	defer stopParents()
	stopFiles, err := safety.PinReadOnlyFiles(files...)
	if err != nil {
		return "", nil, err
	}
	defer stopFiles()
	frozen, err := m.CaptureBaseline(ctx)
	if err != nil || frozen.Checksum != current.Checksum {
		return "", nil, errors.New("working copy changed during observation")
	}
	if frozen.RepositoryID != baseline.RepositoryID || frozen.HeadCommit != baseline.HeadCommit || frozen.IndexExists != baseline.IndexExists || frozen.IndexSHA256 != baseline.IndexSHA256 || !slices.Equal(frozen.Excluded, baseline.Excluded) {
		return "", nil, errors.New("repository state changed; reconcile separately")
	}
	before, now := map[string]BaselineFile{}, map[string]BaselineFile{}
	for _, file := range baseline.Files {
		before[file.Path] = file
	}
	for _, file := range frozen.Files {
		now[file.Path] = file
	}
	for name, file := range before {
		if !affected[name] && file != now[name] {
			return "", nil, errors.New("unrelated source changed; reconcile separately")
		}
	}
	for name, file := range now {
		if !affected[name] && file != before[name] {
			return "", nil, errors.New("unrelated source changed; reconcile separately")
		}
	}
	matches := func(name, content string, exists bool, mode uint32) bool {
		info, err := baselineFileInfo(root, name)
		if !exists {
			return errors.Is(err, fs.ErrNotExist)
		}
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		file := now[name]
		return file.Exists && file.SHA256 == baselineDigest([]byte(content)) && file.Mode == mode && file.Size == int64(len(content))
	}
	states := make([]string, len(changes))
	for index, change := range changes {
		target := change.Path
		if change.Destination != "" {
			target = change.Destination
		}
		after := matches(target, change.After, change.AfterExists, change.AfterMode)
		initial := matches(change.Path, change.Before, change.BeforeExists, change.BeforeMode)
		if change.Operation == "rename" {
			after = after && matches(change.Path, "", false, 0)
			initial = initial && matches(target, "", false, 0)
		}
		states[index] = "unknown"
		if after {
			states[index] = "applied"
		} else if initial {
			states[index] = "not_applied"
		}
	}
	return frozen.Checksum, states, nil
}
