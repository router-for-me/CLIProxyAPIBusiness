package models

import (
	"time"

	"gorm.io/datatypes"
)

// ProviderAPIKey stores upstream provider credentials for CLIProxyAPI.
type ProviderAPIKey struct {
	ID uint64 `gorm:"primaryKey;autoIncrement"` // Primary key.

	Provider string `gorm:"type:varchar(64);not null;index"` // Provider name.
	Priority int    `gorm:"not null;default:0;index"`        // Selection priority (higher wins).
	Name     string `gorm:"type:text"`                       // Display name.
	APIKey   string `gorm:"type:text"`                       // Provider API key.
	Prefix   string `gorm:"type:text"`                       // Key prefix to apply.
	BaseURL  string `gorm:"type:text"`                       // Base URL override.
	ProxyURL string `gorm:"type:text"`                       // Proxy URL override.

	Headers        datatypes.JSON `gorm:"type:jsonb"` // Extra request headers.
	Models         datatypes.JSON `gorm:"type:jsonb"` // Allowed models list.
	ExcludedModels datatypes.JSON `gorm:"type:jsonb"` // Excluded models list.
	APIKeyEntries  datatypes.JSON `gorm:"type:jsonb"` // Nested API key entries.

	CreatedAt time.Time `gorm:"not null;autoCreateTime"` // Creation timestamp.
	UpdatedAt time.Time `gorm:"not null;autoUpdateTime"` // Last update timestamp.
}
