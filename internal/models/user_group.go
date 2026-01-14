package models

import "time"

// UserGroup groups users for access and billing rules.
type UserGroup struct {
	ID uint64 `gorm:"primaryKey;autoIncrement"` // Primary key.

	Name      string `gorm:"type:text;not null;uniqueIndex"` // Display name.
	IsDefault bool   `gorm:"not null;default:false"`         // Marks the default group.
	RateLimit int    `gorm:"not null;default:0"`             // Rate limit per second.

	Users []User `gorm:"foreignKey:UserGroupID"` // Related users.

	CreatedAt time.Time `gorm:"not null;autoCreateTime"` // Creation timestamp.
	UpdatedAt time.Time `gorm:"not null;autoUpdateTime"` // Last update timestamp.
}
