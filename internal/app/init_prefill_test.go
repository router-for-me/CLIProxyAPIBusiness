package app

import "testing"

func TestInitPrefillFromDSN_Postgres(t *testing.T) {
	prefill, err := initPrefillFromDSN("postgres://user:pass@localhost:5432/cpab?sslmode=require")
	if err != nil {
		t.Fatalf("initPrefillFromDSN: %v", err)
	}
	if prefill.DatabaseType != "postgres" {
		t.Fatalf("expected database_type=postgres, got %q", prefill.DatabaseType)
	}
	if prefill.DatabaseHost != "localhost" {
		t.Fatalf("expected database_host=localhost, got %q", prefill.DatabaseHost)
	}
	if prefill.DatabasePort != 5432 {
		t.Fatalf("expected database_port=5432, got %d", prefill.DatabasePort)
	}
	if prefill.DatabaseUser != "user" {
		t.Fatalf("expected database_user=user, got %q", prefill.DatabaseUser)
	}
	if prefill.DatabaseName != "cpab" {
		t.Fatalf("expected database_name=cpab, got %q", prefill.DatabaseName)
	}
	if prefill.DatabaseSSLMode != "require" {
		t.Fatalf("expected database_ssl_mode=require, got %q", prefill.DatabaseSSLMode)
	}
	if !prefill.DatabasePasswordSet {
		t.Fatalf("expected database_password_set=true")
	}
}

func TestInitPrefillFromDSN_SQLite(t *testing.T) {
	prefill, err := initPrefillFromDSN("file:cpab.db?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		t.Fatalf("initPrefillFromDSN: %v", err)
	}
	if prefill.DatabaseType != "sqlite" {
		t.Fatalf("expected database_type=sqlite, got %q", prefill.DatabaseType)
	}
	if prefill.DatabasePath != "cpab.db" {
		t.Fatalf("expected database_path=cpab.db, got %q", prefill.DatabasePath)
	}
	if prefill.DatabasePasswordSet {
		t.Fatalf("expected database_password_set=false")
	}
}
