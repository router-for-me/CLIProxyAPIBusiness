package models

import "time"

// ModelMapping maps provider model names to exposed names.
type ModelMapping struct {
	ID uint64 `gorm:"primaryKey;autoIncrement"` // Primary key.

	Provider     string `gorm:"type:varchar(255);not null;index"` // Provider name.
	ModelName    string `gorm:"type:varchar(255);not null;index"` // Original model name.
	NewModelName string `gorm:"type:varchar(255);not null"`       // Exposed model name.
	Fork         bool   `gorm:"not null;default:false"`           // Whether to fork metadata.

	// Selector indicates the auth routing strategy:
	// 0 = RoundRobin, 1 = FillFirst, 2 = Stick.
	Selector  int `gorm:"not null;default:0"` // Routing selector.
	RateLimit int `gorm:"not null;default:0"` // Rate limit per second.

	IsEnabled bool `gorm:"not null;default:true"` // Whether mapping is active.

	CreatedAt time.Time `gorm:"not null;autoCreateTime"` // Creation timestamp.
	UpdatedAt time.Time `gorm:"not null;autoUpdateTime"` // Last update timestamp.
}
