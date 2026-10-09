package localidentity_test

import (
	"bytes"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"code-agent/internal/localidentity"
)

func TestIdentityRefusesLegacyDatabaseWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT); INSERT INTO sessions VALUES ('legacy')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := localidentity.Open(path, "", "")
	if store != nil {
		store.Close()
	}
	if err == nil {
		t.Fatal("identity storage silently migrated an unrelated SQLite database")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("identity refusal rewrote legacy data")
	}
}

func TestIdentityRefusesRedirectedDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	redirect := filepath.Join(root, "redirect")
	var linkErr error
	if runtime.GOOS == "windows" {
		linkErr = exec.Command("cmd", "/c", "mklink", "/J", redirect, target).Run()
	} else {
		linkErr = os.Symlink(target, redirect)
	}
	if err := linkErr; err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	store, err := localidentity.Open(filepath.Join(redirect, "identity.sqlite"), "", "")
	if store != nil {
		store.Close()
	}
	if err == nil {
		t.Fatal("identity storage followed a redirected directory")
	}
	if _, err := os.Stat(filepath.Join(target, "identity.sqlite")); !os.IsNotExist(err) {
		t.Fatal("refusal changed the redirect target")
	}
}
