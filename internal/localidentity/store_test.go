package localidentity_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"code-agent/internal/localidentity"
	"code-agent/internal/model"
	"code-agent/internal/service"
	"code-agent/pkg/hash"
	"code-agent/pkg/token"
)

const oldOwnerPassword = "test-only-old-owner-password"

func newLocalOwner(t *testing.T) (*localidentity.Store, string, *model.User) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "identity.sqlite")
	store, err := localidentity.Open(path, "operator@example.test", oldOwnerPassword)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	password, err := hash.HashPassword(oldOwnerPassword)
	if err != nil {
		t.Fatal(err)
	}
	owner := &model.User{Username: "operator@example.test", Password: password}
	if err := store.Create(owner); err != nil {
		t.Fatal(err)
	}
	owner.OrgTags, owner.PrimaryOrg = "FIRST,SECOND", "SECOND"
	if err := store.Update(owner); err != nil {
		t.Fatal(err)
	}
	owner, err = store.FindByUsername(owner.Username)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke("test-only-revoked-token", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return store, path, owner
}

func TestRotateOwnerPasswordPreservesIdentityAcrossReopen(t *testing.T) {
	store, path, before := newLocalOwner(t)
	const newPassword = "test-only-new-owner-password"
	username, err := store.RotateOwnerPassword(context.Background(), before.ID, newPassword)
	if err != nil || username != before.Username {
		t.Fatal("owner password rotation failed")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = localidentity.Open(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after, err := store.FindByUsername(username)
	if err != nil {
		t.Fatal(err)
	}
	if after.Password == before.Password || !hash.CheckPasswordHash(newPassword, after.Password) || hash.CheckPasswordHash(oldOwnerPassword, after.Password) {
		t.Fatal("rotation did not replace the password hash")
	}
	if after.UpdatedAt.Before(before.UpdatedAt) {
		t.Fatal("rotation moved the identity update time backwards")
	}
	preserved := *before
	preserved.Password, preserved.UpdatedAt = after.Password, after.UpdatedAt
	if !reflect.DeepEqual(preserved, *after) {
		t.Fatal("rotation changed another identity fact")
	}
	ownerID, err := store.OwnerID(context.Background())
	if err != nil || ownerID != before.ID {
		t.Fatal("rotation changed the durable owner")
	}
	revoked, err := store.IsRevoked("test-only-revoked-token")
	if err != nil || !revoked {
		t.Fatal("rotation lost the token revocation")
	}
	jwt := token.NewJWTManager("test-only-secret-at-least-32-characters", 1, 1)
	users := service.NewLocalUserService(store, jwt, store)
	if _, _, err := users.Login(username, oldOwnerPassword); err == nil {
		t.Fatal("login accepted the previous password")
	}
	access, refresh, err := users.Login(username, newPassword)
	if err != nil || access == "" || refresh == "" {
		t.Fatal("login rejected the rotated password")
	}
	claims, err := jwt.VerifyToken(access)
	if err != nil || claims.UserID != before.ID || claims.Username != before.Username || claims.Role != before.Role {
		t.Fatal("login changed the original principal")
	}
}

func TestRotateOwnerPasswordRefusalsLeaveIdentityUnchanged(t *testing.T) {
	for _, name := range []string{"wrong owner", "zero owner", "weak password", "bcrypt limit", "canceled context"} {
		t.Run(name, func(t *testing.T) {
			store, _, before := newLocalOwner(t)
			owner, password := before.ID, "test-only-new-owner-password"
			ctx := context.Background()
			switch name {
			case "wrong owner":
				owner++
			case "zero owner":
				owner = 0
			case "weak password":
				password = "short"
			case "bcrypt limit":
				password = string(bytes.Repeat([]byte{'x'}, 73))
			case "canceled context":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			if username, err := store.RotateOwnerPassword(ctx, owner, password); err == nil || username != "" {
				t.Fatal("rotation accepted invalid recovery input")
			}
			after, err := store.FindByUsername(before.Username)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("refused rotation changed an identity fact")
			}
			revoked, err := store.IsRevoked("test-only-revoked-token")
			if err != nil || !revoked {
				t.Fatal("refused rotation lost the token revocation")
			}
		})
	}
}

func TestIdentityUpdateDoesNotRotatePassword(t *testing.T) {
	store, _, owner := newLocalOwner(t)
	before := owner.Password
	owner.Password = "ignored replacement password"
	if err := store.Update(owner); err != nil {
		t.Fatal(err)
	}
	after, err := store.FindByUsername(owner.Username)
	if err != nil || after.Password != before || !hash.CheckPasswordHash(oldOwnerPassword, after.Password) {
		t.Fatal("generic identity update changed the password")
	}
}

func TestRotateOwnerPasswordDoesNotInitializeOwner(t *testing.T) {
	store, err := localidentity.Open(filepath.Join(t.TempDir(), "identity.sqlite"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if username, err := store.RotateOwnerPassword(context.Background(), 123, "test-only-new-owner-password"); err == nil || username != "" {
		t.Fatal("rotation initialized a missing owner")
	}
	if owner, err := store.OwnerID(context.Background()); err == nil || owner != 0 {
		t.Fatal("rotation created an owner")
	}
}

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
