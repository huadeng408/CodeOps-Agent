package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreFileTransitionsPreflightsAllChangesBeforeWriting(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.txt")
	second := filepath.Join(root, "second.txt")
	if err := os.WriteFile(first, []byte("after-first"), 0o644); err != nil { t.Fatal(err) }
	if err := os.WriteFile(second, []byte("unexpected"), 0o644); err != nil { t.Fatal(err) }
	changes := []FileTransition{
		{Path: "first.txt", Before: "before-first", After: "after-first"},
		{Path: "second.txt", Before: "before-second", After: "after-second"},
	}
	if err := RestoreFileTransitions(context.Background(), root, changes); err == nil {
		t.Fatal("expected conflict")
	}
	got, err := os.ReadFile(first)
	if err != nil { t.Fatal(err) }
	if string(got) != "after-first" {
		t.Fatalf("first file changed after conflict: %q", got)
	}
}

func TestRestoreFileTransitionsWritesPreviousContentsAfterSuccessfulPreflight(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "file.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil { t.Fatal(err) }
	if err := RestoreFileTransitions(context.Background(), root, []FileTransition{{Path: "nested/file.txt", Before: "before", After: "after"}}); err != nil { t.Fatal(err) }
	got, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	if string(got) != "before" { t.Fatalf("restored content = %q, want before", got) }
}

func TestRestoreFileTransitionsRejectsEscapingPathWithoutWriting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil { t.Fatal(err) }
	if err := RestoreFileTransitions(context.Background(), root, []FileTransition{{Path: "../outside", Before: "before", After: "after"}}); err == nil { t.Fatal("expected escaping path rejection") }
	got, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	if string(got) != "after" { t.Fatalf("file changed after path rejection: %q", got) }
}

func TestRestoreFileTransitionsRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "escape.txt"), []byte("after"), 0o644); err != nil { t.Fatal(err) }
	if err := RestoreFileTransitions(context.Background(), root, []FileTransition{{Path: "linked/escape.txt", Before: "before", After: "after"}}); err == nil {
		t.Fatal("expected symlink escape rejection")
	}
	got, err := os.ReadFile(filepath.Join(outside, "escape.txt"))
	if err != nil { t.Fatal(err) }
	if string(got) != "after" { t.Fatalf("outside file changed after rejection: %q", got) }
}
