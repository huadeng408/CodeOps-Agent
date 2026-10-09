package serverconfig

import (
	"path/filepath"
	"testing"
)

func TestLocalCoreRejectsUnsafeConfiguration(t *testing.T) {
	valid := Config{
		Server:  ServerConfig{Profile: "local-core", Port: "8081", AllowedOrigins: "http://127.0.0.1:8081"},
		Harness: HarnessConfig{SessionLedgerPath: filepath.Join(t.TempDir(), "ledger.sqlite"), IdentityPath: filepath.Join(t.TempDir(), "identity.sqlite")},
		JWT:     JWTConfig{Secret: "test-only-secret-at-least-32-characters", AccessTokenExpireHours: 1, RefreshTokenExpireDays: 1},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"network", "same-database", "legacy"} {
		cfg := valid
		switch variant {
		case "network":
			cfg.Server.Host = "0.0.0.0"
		case "same-database":
			cfg.Harness.IdentityPath = cfg.Harness.SessionLedgerPath
		case "legacy":
			cfg.Server.Profile = "full-stack"
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("unsafe %s profile configuration was accepted", variant)
		}
	}
}

func TestCorpusDefaults(t *testing.T) {
	corpus := DefaultCorpusConfig()
	if corpus.Generation != "techdocs-2026-07-30-v1" {
		t.Fatalf("generation = %q", corpus.Generation)
	}
	if corpus.TextIndex != "knowledge_base_v2_bge_m3" {
		t.Fatalf("text index = %q", corpus.TextIndex)
	}
	if corpus.ReadAlias != "knowledge_base_current" {
		t.Fatalf("read alias = %q", corpus.ReadAlias)
	}
	if corpus.VisualAlias != "knowledge_page_visual_current" {
		t.Fatalf("visual alias = %q", corpus.VisualAlias)
	}
	if corpus.AllowAliasSwitch {
		t.Fatal("alias switching must default to false")
	}
}
