// Package corpus implements the official technical corpus loader: staged
// checkout, license gate, pilot sampling and idempotent checkpointing.
package corpus

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DocumentEntry is one document selected from a staged source checkout.
type DocumentEntry struct {
	SourceID   string
	SourcePath string // repo-relative, slash-separated
	Checkpoint Checkpoint
}

// ListDocuments walks a staged checkout and returns the documents selected by
// the manifest include/exclude rules, in deterministic order. Documents whose
// extension is not in the manifest allowed_formats are skipped.
func ListDocuments(_ context.Context, spec SourceSpec, repoDir string) ([]DocumentEntry, error) {
	var entries []DocumentEntry
	err := filepath.WalkDir(repoDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Skip the git metadata directory.
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(repoDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !spec.Includes(rel) {
			return nil
		}
		if !spec.Allowed(filepath.Ext(path)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		entries = append(entries, DocumentEntry{
			SourceID:   spec.SourceID,
			SourcePath: rel,
			Checkpoint: Checkpoint{
				SourceID:     spec.SourceID,
				SourceCommit: spec.SourceCommit,
				Path:         rel,
				ContentHash:  HashBytes(data),
			},
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Deterministic order regardless of filesystem walk order.
	sortEntries(entries)
	return entries, nil
}

func sortEntries(entries []DocumentEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j-1].SourcePath > entries[j].SourcePath; j-- {
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}
}

// Contains reports whether needle is in haystack.
func Contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

// IngestDocument hands one staged document to the RAG pipeline. The current
// implementation is a stub that verifies the file exists and is non-empty;
// the actual hand-off (upload service -> Kafka pipeline) is wired by the
// server process via the existing upload API. Returns an error for missing
// or empty files so the loader never marks a failed document as imported.
func IngestDocument(ctx context.Context, spec SourceSpec, doc DocumentEntry, stagingDir string) error {
	path := filepath.Join(stagingDir, spec.SourceID, filepath.FromSlash(doc.SourcePath))
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("ingest source document %s: %w", doc.SourcePath, err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("ingest source document %s: empty file", doc.SourcePath)
	}
	return nil
}
