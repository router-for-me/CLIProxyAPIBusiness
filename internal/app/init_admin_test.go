package app

import (
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPIBusiness/internal/db"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
)

func TestCreateAdminUserWithConn_SetsSuperAdmin(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "cpab-test.db")
	conn, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if errMigrate := db.Migrate(conn); errMigrate != nil {
		t.Fatalf("migrate: %v", errMigrate)
	}

	if errCreate := CreateAdminUserWithConn(conn, "admin", "password", "CLIProxyAPI"); errCreate != nil {
		t.Fatalf("CreateAdminUserWithConn: %v", errCreate)
	}

	var admin models.Admin
	if errFind := conn.First(&admin).Error; errFind != nil {
		t.Fatalf("find admin: %v", errFind)
	}
	if !admin.IsSuperAdmin {
		t.Fatalf("expected first admin to be super admin")
	}
}
