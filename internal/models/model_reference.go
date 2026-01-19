package models

import (
	"time"

	"gorm.io/datatypes"
)

// ModelReference stores provider model pricing references.
type ModelReference struct {
	ProviderName string `gorm:"type:varchar(255);not null;primaryKey;index"` // Provider display name.
	ModelName    string `gorm:"type:varchar(255);not null;primaryKey;index"` // Model display name.

	ContextLimit int `gorm:"not null;default:0"` // Max context length.
	OutputLimit  int `gorm:"not null;default:0"` // Max output tokens.

	InputPrice      *float64 `gorm:"type:decimal(20,10)"` // Input token price.
	OutputPrice     *float64 `gorm:"type:decimal(20,10)"` // Output token price.
	CacheReadPrice  *float64 `gorm:"type:decimal(20,10)"` // Cached read price.
	CacheWritePrice *float64 `gorm:"type:decimal(20,10)"` // Cached write price.

	ContextOver200kInputPrice      *float64 `gorm:"column:context_over_200k_input_price;type:decimal(20,10)"`       // Input price beyond 200k context.
	ContextOver200kOutputPrice     *float64 `gorm:"column:context_over_200k_output_price;type:decimal(20,10)"`      // Output price beyond 200k context.
	ContextOver200kCacheReadPrice  *float64 `gorm:"column:context_over_200k_cache_read_price;type:decimal(20,10)"`  // Cache read price beyond 200k context.
	ContextOver200kCacheWritePrice *float64 `gorm:"column:context_over_200k_cache_write_price;type:decimal(20,10)"` // Cache write price beyond 200k context.

	Extra      datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'"` // Extra payload fields.
	LastSeenAt time.Time      `gorm:"not null;index"`                   // Last sync timestamp.
	CreatedAt  time.Time      `gorm:"not null;autoCreateTime"`          // Creation timestamp.
	UpdatedAt  time.Time      `gorm:"not null;autoUpdateTime"`          // Update timestamp.
}

// TableName overrides the default table name.
func (ModelReference) TableName() string {
	return "models"
}
