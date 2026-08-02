package corpus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileCheckpointStoreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.txt")
	store, err := NewFileCheckpointStore(path)
	if err != nil {
		t.Fatal(err)
	}

	key := "python@0123456789abcdef0123456789abcdef01234567:Doc/library.rst#ab12"
	seen, err := store.Seen(key)
	if err != nil || seen {
		t.Fatalf("fresh store Seen() = %v, %v; want false", seen, err)
	}
	if err := store.Mark(key); err != nil {
		t.Fatal(err)
	}
	// Marking twice must be a no-op.
	if err := store.Mark(key); err != nil {
		t.Fatal(err)
	}
	seen, err = store.Seen(key)
	if err != nil || !seen {
		t.Fatalf("marked store Seen() = %v, %v; want true", seen, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Re-open: the key must survive a restart.
	reopened, err := NewFileCheckpointStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	seen, err = reopened.Seen(key)
	if err != nil || !seen {
		t.Fatalf("reopened store Seen() = %v, %v; want true", seen, err)
	}
}

func TestFileCheckpointStorePersistsOnlyDirty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.txt")
	store, err := NewFileCheckpointStore(path)
	if err != nil {
		t.Fatal(err)
	}
	// No marks: Close must not create the file.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("clean store created checkpoint file: %v", err)
	}
}

func TestPilotSelectorDeterministicSubset(t *testing.T) {
	keys := []string{
		"python@c1:Doc/a.rst#1",
		"python@c1:Doc/b.rst#2",
		"python@c1:Doc/c.rst#3",
		"python@c1:Doc/d.rst#4",
		"python@c1:Doc/e.rst#5",
	}
	selector := PilotSelector{PerSource: 2}
	first := selector.Select(keys)
	if len(first) != 2 {
		t.Fatalf("pilot size = %d, want 2", len(first))
	}
	second := selector.Select(keys)
	if len(first) != len(second) {
		t.Fatalf("pilot not deterministic: %v vs %v", first, second)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("pilot not deterministic: %v vs %v", first, second)
		}
	}
}

func TestPilotSelectorRespectsPerSourceCap(t *testing.T) {
	keys := []string{"a#1", "b#2", "c#3", "d#4"}
	selector := PilotSelector{PerSource: 10}
	if got := selector.Select(keys); len(got) != len(keys) {
		t.Fatalf("pilot size = %d, want %d when per-source exceeds count", len(got), len(keys))
	}
	selector.PerSource = 0
	if got := selector.Select(keys); got != nil {
		t.Fatalf("pilot with PerSource=0 = %v, want nil", got)
	}
}
