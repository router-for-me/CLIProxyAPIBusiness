package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPIBusiness/internal/db"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
)

func TestHasAdminInitialized(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "cpab-test.db")
	conn, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	initialized, err := HasAdminInitialized(conn)
	if err != nil {
		t.Fatalf("HasAdminInitialized: %v", err)
	}
	if initialized {
		t.Fatalf("expected initialized=false before migrate")
	}

	if errMigrate := db.Migrate(conn); errMigrate != nil {
		t.Fatalf("migrate: %v", errMigrate)
	}

	initialized, err = HasAdminInitialized(conn)
	if err != nil {
		t.Fatalf("HasAdminInitialized after migrate: %v", err)
	}
	if initialized {
		t.Fatalf("expected initialized=false with empty admins table")
	}

	now := time.Now().UTC()
	admin := models.Admin{
		Username:  "admin",
		Password:  "hashed-password",
		Active:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if errCreate := conn.Create(&admin).Error; errCreate != nil {
		t.Fatalf("create admin: %v", errCreate)
	}

	initialized, err = HasAdminInitialized(conn)
	if err != nil {
		t.Fatalf("HasAdminInitialized after seed: %v", err)
	}
	if !initialized {
		t.Fatalf("expected initialized=true after admin created")
	}
}
