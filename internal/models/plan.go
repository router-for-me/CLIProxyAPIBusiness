package models

import (
	"time"

	"gorm.io/datatypes"
)

// Plan represents a subscription plan configuration.
type Plan struct {
	ID uint64 `gorm:"primaryKey;autoIncrement"` // Primary key.

	Name          string         `gorm:"type:varchar(255);not null"`            // Plan name.
	MonthPrice    float64        `gorm:"type:decimal(10,2);not null;default:0"` // Monthly price.
	Description   string         `gorm:"type:text"`                             // Plan description.
	SupportModels datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'"`      // Supported model list.

	Feature1 string `gorm:"type:varchar(255)"` // Feature description slot 1.
	Feature2 string `gorm:"type:varchar(255)"` // Feature description slot 2.
	Feature3 string `gorm:"type:varchar(255)"` // Feature description slot 3.
	Feature4 string `gorm:"type:varchar(255)"` // Feature description slot 4.

	SortOrder int `gorm:"not null;default:0"` // Display ordering weight.

	TotalQuota float64 `gorm:"type:decimal(20,10);not null;default:0"` // Total quota allocation.
	DailyQuota float64 `gorm:"type:decimal(20,10);not null;default:0"` // Daily quota allocation.
	RateLimit  int     `gorm:"not null;default:0"`                     // Rate limit per second.

	IsEnabled bool `gorm:"not null;default:true"` // Whether the plan is active.

	CreatedAt time.Time `gorm:"not null;autoCreateTime"` // Creation timestamp.
	UpdatedAt time.Time `gorm:"not null;autoUpdateTime"` // Last update timestamp.
}
