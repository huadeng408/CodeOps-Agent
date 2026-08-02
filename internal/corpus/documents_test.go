package corpus

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func setupStagedSource(t *testing.T) (SourceSpec, string) {
	t.Helper()
	root := t.TempDir()
	spec := SourceSpec{
		SourceID:       "testsrc",
		SourceCommit:   "0123456789abcdef0123456789abcdef01234567",
		IncludePaths:   []string{"Doc/"},
		ExcludePaths:   []string{"Doc/includes"},
		AllowedFormats: []string{"rst", "md"},
	}
	mustWrite(t, filepath.Join(root, "Doc", "library.rst"), "docs content")
	mustWrite(t, filepath.Join(root, "Doc", "guide.md"), "# Guide")
	mustWrite(t, filepath.Join(root, "Doc", "includes", "internal.rst"), "should be excluded")
	mustWrite(t, filepath.Join(root, "README.md"), "outside include")
	mustWrite(t, filepath.Join(root, "Doc", "diagram.pdf"), "not allowed format")
	return spec, root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListDocumentsSelectsByManifestRules(t *testing.T) {
	spec, root := setupStagedSource(t)
	docs, err := ListDocuments(context.Background(), spec, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("documents = %d, want 2 (exclude + format + include filters)", len(docs))
	}
	if docs[0].SourcePath != "Doc/guide.md" || docs[1].SourcePath != "Doc/library.rst" {
		t.Fatalf("unexpected order: %v %v", docs[0].SourcePath, docs[1].SourcePath)
	}
	if docs[0].Checkpoint.SourceID != "testsrc" || docs[0].Checkpoint.Path != "Doc/guide.md" {
		t.Fatalf("checkpoint = %+v", docs[0].Checkpoint)
	}
	if docs[0].Checkpoint.ContentHash == "" {
		t.Fatal("content hash must be computed")
	}
}

func TestListDocumentsDeterministicAcrossRuns(t *testing.T) {
	spec, root := setupStagedSource(t)
	first, err := ListDocuments(context.Background(), spec, root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ListDocuments(context.Background(), spec, root)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if first[i].SourcePath != second[i].SourcePath || first[i].Checkpoint.Key() != second[i].Checkpoint.Key() {
			t.Fatalf("non-deterministic listing: %+v vs %+v", first, second)
		}
	}
}

func TestListDocumentsContentChangeChangesCheckpoint(t *testing.T) {
	spec, root := setupStagedSource(t)
	before, err := ListDocuments(context.Background(), spec, root)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "Doc", "guide.md"), "# Guide v2")
	after, err := ListDocuments(context.Background(), spec, root)
	if err != nil {
		t.Fatal(err)
	}
	// Same path, different content => different checkpoint key.
	if before[0].SourcePath != after[0].SourcePath {
		t.Fatalf("paths changed: %v vs %v", before[0].SourcePath, after[0].SourcePath)
	}
	if before[0].Checkpoint.Key() == after[0].Checkpoint.Key() {
		t.Fatal("content change must produce a new checkpoint")
	}
}

func TestIngestDocumentRejectsMissing(t *testing.T) {
	spec := SourceSpec{SourceID: "testsrc"}
	doc := DocumentEntry{SourcePath: "Doc/nope.rst"}
	if err := IngestDocument(context.Background(), spec, doc, t.TempDir()); err == nil {
		t.Fatal("expected error for missing document")
	}
}

func TestContains(t *testing.T) {
	if !Contains([]string{"a", "b"}, "b") || Contains([]string{"a"}, "z") {
		t.Fatal("Contains() misbehaved")
	}
}
