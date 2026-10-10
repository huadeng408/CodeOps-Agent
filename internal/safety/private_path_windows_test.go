package safety

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPinDirectoriesBlocksReplacementAndAllowsChildFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "pinned")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	release, err := PinDirectories(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := os.Rename(root, root+"-moved"); err == nil {
		t.Fatal("pinned directory could be replaced")
	}
	if err := os.WriteFile(filepath.Join(root, "child.txt"), []byte("bounded fixture"), 0600); err != nil {
		t.Fatal("pin blocked safe child creation")
	}
	if err := os.Rename(filepath.Join(root, "child.txt"), filepath.Join(root, "renamed.txt")); err != nil {
		t.Fatal("pin blocked safe child file rename")
	}
}

func TestPinReadOnlyFilesRejectsMutationAndReplacement(t *testing.T) {
	name := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(name, []byte("controlled fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	release, err := PinReadOnlyFiles(name)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := os.WriteFile(name, []byte("changed"), 0600); err == nil {
		t.Fatal("pinned metadata file was overwritten")
	}
	if err := os.Rename(name, name+"-moved"); err == nil {
		t.Fatal("pinned metadata file was replaced")
	}
}
