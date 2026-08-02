// Package corpus implements the official technical corpus loader: staged
// checkout, license gate, pilot sampling and idempotent checkpointing.
package corpus

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// CheckpointStore records which documents have already been processed so a
// loader re-run skips them (idempotency) and only new/changed content is
// re-ingested.
type CheckpointStore interface {
	// Seen reports whether a checkpoint key was already recorded.
	Seen(key string) (bool, error)
	// Mark records a checkpoint key as processed.
	Mark(key string) error
	// Close flushes any buffered state.
	Close() error
}

// FileCheckpointStore persists checkpoints as one key per line in a file.
// A re-run loads the set once and appends new keys, so re-runs are idempotent
// without duplicating entries.
type FileCheckpointStore struct {
	mu     sync.Mutex
	path   string
	seen   map[string]struct{}
	dirty  bool
}

// NewFileCheckpointStore creates a store backed by path, loading existing keys.
func NewFileCheckpointStore(path string) (*FileCheckpointStore, error) {
	store := &FileCheckpointStore{path: path, seen: make(map[string]struct{})}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileCheckpointStore) load() error {
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			s.seen[line] = struct{}{}
		}
	}
	return scanner.Err()
}

// Seen reports whether a checkpoint key was already recorded.
func (s *FileCheckpointStore) Seen(key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.seen[key]
	return ok, nil
}

// Mark records a checkpoint key as processed.
func (s *FileCheckpointStore) Mark(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[key]; ok {
		return nil
	}
	s.seen[key] = struct{}{}
	s.dirty = true
	return nil
}

// Close flushes the checkpoint set to disk if anything changed.
func (s *FileCheckpointStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(s.seen))
	for key := range s.seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return os.WriteFile(s.path, []byte(strings.Join(keys, "\n")+"\n"), 0o644)
}

// PilotSelector samples a deterministic subset of documents for a pilot run.
// The sample is stable across re-runs (same seed => same subset) so pilot
// results are comparable.
type PilotSelector struct {
	PerSource int
}

// Select returns the pilot subset: up to PerSource documents per source,
// deterministically chosen by hashing the document key.
func (p PilotSelector) Select(keys []string) []string {
	if p.PerSource <= 0 {
		return nil
	}
	type scored struct {
		key   string
		score uint32
	}
	scoredKeys := make([]scored, 0, len(keys))
	for _, key := range keys {
		scoredKeys = append(scoredKeys, scored{key: key, score: fnv32(key)})
	}
	sort.Slice(scoredKeys, func(i, j int) bool {
		if scoredKeys[i].score != scoredKeys[j].score {
			return scoredKeys[i].score < scoredKeys[j].score
		}
		return scoredKeys[i].key < scoredKeys[j].key
	})
	if len(scoredKeys) > p.PerSource {
		scoredKeys = scoredKeys[:p.PerSource]
	}
	out := make([]string, 0, len(scoredKeys))
	for _, item := range scoredKeys {
		out = append(out, item.key)
	}
	sort.Strings(out)
	return out
}

// fnv32 is a small deterministic hash used by PilotSelector (kept local to
// avoid pulling in a dependency).
func fnv32(s string) uint32 {
	const (
		offset = 2166136261
		prime  = 16777619
	)
	h := uint32(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime
	}
	return h
}
