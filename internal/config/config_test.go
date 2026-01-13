package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDatabaseDSN_EnvOverride(t *testing.T) {
	t.Setenv("DB_CONNECTION", "postgres://cpab:pass@localhost:5432/cpab?sslmode=disable")

	missingPath := filepath.Join(t.TempDir(), "missing.yaml")
	dsn, err := LoadDatabaseDSN(missingPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if dsn != os.Getenv("DB_CONNECTION") {
		t.Fatalf("expected dsn=%q, got %q", os.Getenv("DB_CONNECTION"), dsn)
	}
}

func TestLoadJWTConfig_EnvOverride(t *testing.T) {
	t.Setenv("JWT_SECRET", "env-secret")
	t.Setenv("JWT_EXPIRY", "2h")

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("jwt:\n  secret: file-secret\n  expiry: 1h\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadJWTConfig(configPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.Secret != "env-secret" {
		t.Fatalf("expected secret=%q, got %q", "env-secret", cfg.Secret)
	}
	if cfg.Expiry != 2*time.Hour {
		t.Fatalf("expected expiry=%s, got %s", (2 * time.Hour).String(), cfg.Expiry.String())
	}
}
