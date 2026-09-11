package serverconfig

import (
	"strings"
	"testing"
)

func TestConfigValidateRejectsMissingProductionCredentials(t *testing.T) {
	cfg := Config{}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("missing production credentials were accepted")
	}
	message := err.Error()
	for _, want := range []string{"jwt.secret", "database.mysql.dsn", "database.redis.addr", "harness.session_ledger_path"} {
		if !strings.Contains(message, want) {
			t.Fatalf("validation error %q does not mention %q", message, want)
		}
	}
}

func TestConfigValidateAcceptsCompleteConfiguration(t *testing.T) {
	cfg := Config{
		Server:  ServerConfig{Port: "8081", AllowedOrigins: "http://localhost:5173"},
		Harness: HarnessConfig{SessionLedgerPath: ".agent/sessions/sessions.sqlite"},
		Database: DatabaseConfig{
			MySQL: MySQLConfig{DSN: "user:password@tcp(127.0.0.1:3306)/codeagent"},
			Redis: RedisConfig{Addr: "127.0.0.1:6379"},
		},
		JWT: JWTConfig{Secret: strings.Repeat("x", 32), AccessTokenExpireHours: 1, RefreshTokenExpireDays: 1},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("complete configuration rejected: %v", err)
	}
}

func TestConfigValidateRejectsWildcardCorsAndPlaceholderSecret(t *testing.T) {
	cfg := Config{
		Server:   ServerConfig{Port: "8081", AllowedOrigins: "*"},
		Harness:  HarnessConfig{SessionLedgerPath: ".agent/sessions/sessions.sqlite"},
		Database: DatabaseConfig{MySQL: MySQLConfig{DSN: "mysql"}, Redis: RedisConfig{Addr: "redis:6379"}},
		JWT:      JWTConfig{Secret: "dev-secret-key-change-in-production", AccessTokenExpireHours: 1, RefreshTokenExpireDays: 1},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "allowed_origins") || !strings.Contains(err.Error(), "jwt.secret") {
		t.Fatalf("unsafe configuration was accepted or error was incomplete: %v", err)
	}
}

func TestConfigValidateErrorExplainsExternalSecretInjection(t *testing.T) {
	err := (Config{}).Validate()
	if err == nil {
		t.Fatal("empty configuration unexpectedly validated")
	}
	if !strings.Contains(err.Error(), "jwt.secret") || !strings.Contains(err.Error(), "database.mysql.dsn") {
		t.Fatalf("validation error omitted credential fields: %v", err)
	}
}
