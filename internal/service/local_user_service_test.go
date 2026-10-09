package service_test

import (
	"path/filepath"
	"testing"
	"time"

	"code-agent/internal/localidentity"
	"code-agent/internal/service"
	"code-agent/pkg/token"
)

type interruptedRevocation struct{ *localidentity.Store }

func (store interruptedRevocation) Revoke(raw string, expiry time.Time) error {
	defer store.Close()
	return store.Store.Revoke(raw, expiry)
}

func TestLocalLogoutReportsIncompleteRevocation(t *testing.T) {
	store, err := localidentity.Open(filepath.Join(t.TempDir(), "identity.sqlite"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	jwt := token.NewJWTManager("test-only-secret-at-least-32-characters", 1, 1)
	access, err := jwt.GenerateToken(9, "operator@example.test", "USER")
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := jwt.GenerateRefreshToken(9, "operator@example.test", "USER")
	if err != nil {
		t.Fatal(err)
	}
	users := service.NewLocalUserService(store, jwt, interruptedRevocation{store})
	if err := users.Logout(access, refresh); err == nil {
		t.Fatal("local logout reported success although refresh-token revocation failed")
	}
}
