package worktree

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5"
)

func TestGitFilesystemContractConfinesReadsAndRejectsNewEntries(t *testing.T) {
	root := t.TempDir()
	writeBaselineFile(t, root, "config", "controlled fixture")
	if err := os.Mkdir(filepath.Join(root, "objects"), 0700); err != nil {
		t.Fatal(err)
	}
	opened, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	reader, err := snapshotGitFilesystem(context.Background(), opened)
	if err != nil {
		t.Fatal(err)
	}
	var contract billy.Filesystem = reader
	for _, name := range []string{"../outside", root, `C:\outside`, `\\outside\share`, "config:stream"} {
		if file, err := contract.Open(name); err == nil {
			file.Close()
			t.Fatal("unapproved read accepted")
		}
		if _, err := contract.Chroot(name); err == nil {
			t.Fatal("unapproved chroot accepted")
		}
	}
	writeBaselineFile(t, root, "objects/alternates", "../../outside")
	if _, err := contract.Open("objects/alternates"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("new entry entered captured Git namespace")
	}
	objects, err := contract.Chroot("objects")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := objects.ReadDir(".")
	if err != nil || len(entries) != 0 {
		t.Fatal("new child appeared in captured directory")
	}
	if _, err := contract.OpenFile("config", os.O_WRONLY, 0); !errors.Is(err, billy.ErrReadOnly) {
		t.Fatal("Git reader admitted a write")
	}
	if err := contract.Rename("config", "changed"); !errors.Is(err, billy.ErrReadOnly) {
		t.Fatal("Git reader admitted a rename")
	}
	file, err := contract.Open("config")
	if err != nil {
		t.Fatal(err)
	}
	if file.Name() != "config" {
		t.Fatal("reader leaked a host filename to the Git codec")
	}
	if _, err := file.Write([]byte("changed")); !errors.Is(err, billy.ErrReadOnly) {
		t.Fatal("Git file admitted a write")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGitFilesystemRejectsExpandedVersion4IndexNames(t *testing.T) {
	root := t.TempDir()
	var data bytes.Buffer
	data.WriteString("DIRC")
	binary.Write(&data, binary.BigEndian, uint32(4))
	binary.Write(&data, binary.BigEndian, uint32(200))
	for i := 0; i < 200; i++ {
		data.Write(make([]byte, 62))
		data.WriteByte(0)
		if i == 0 {
			data.WriteString(strings.Repeat("x", 50_000))
		} else {
			data.WriteByte('x')
		}
		data.WriteByte(0)
	}
	data.Write(make([]byte, 20))
	writeBaselineFile(t, root, "index", data.String())
	opened, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	reader, err := snapshotGitFilesystem(context.Background(), opened)
	if err != nil {
		t.Fatal(err)
	}
	if file, err := reader.Open("index"); err == nil {
		file.Close()
		t.Fatal("compressed path expansion reached the index codec")
	}
}

func TestCaptureBaselineAcceptsNativeVersion4Index(t *testing.T) {
	root := taskRepository(t)
	baselineGit(t, root, "update-index", "--index-version", "4")
	if _, err := NewManager(root, "HEAD").CaptureBaseline(context.Background()); err != nil {
		t.Fatal("valid v4 index refused:", err)
	}
}

func TestGitFilesystemContractRejectsReplacementAndCancellation(t *testing.T) {
	root := t.TempDir()
	writeBaselineFile(t, root, "config", "fixture")
	opened, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	contract, err := snapshotGitFilesystem(ctx, opened)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "config"), filepath.Join(root, "original")); err != nil {
		t.Fatal(err)
	}
	writeBaselineFile(t, root, "config", "fixture")
	if file, err := contract.Open("config"); err == nil {
		file.Close()
		t.Fatal("replaced file entered the captured inventory")
	}
	cancel()
	if _, err := contract.ReadDir("."); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled reader continued")
	}
}
