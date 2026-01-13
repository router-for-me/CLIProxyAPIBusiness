package app

import (
	"fmt"

	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/gorm"
)

// HasAdminInitialized reports whether the system has at least one admin account.
func HasAdminInitialized(conn *gorm.DB) (bool, error) {
	if conn == nil {
		return false, fmt.Errorf("nil db")
	}
	if !conn.Migrator().HasTable(&models.Admin{}) {
		return false, nil
	}
	var count int64
	if errCount := conn.Model(&models.Admin{}).Count(&count).Error; errCount != nil {
		return false, errCount
	}
	return count > 0, nil
}
