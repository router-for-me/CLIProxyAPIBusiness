package models

import "time"

// BillPeriodType represents the billing period unit.
type BillPeriodType int

// BillPeriodType constants define billing periods.
const (
	// BillPeriodTypeMonthly charges monthly.
	BillPeriodTypeMonthly BillPeriodType = 1
	// BillPeriodTypeYearly charges yearly.
	BillPeriodTypeYearly BillPeriodType = 2
)

// BillStatus represents the lifecycle state of a bill.
type BillStatus int

// BillStatus constants define bill lifecycle states.
const (
	// BillStatusPending marks a bill awaiting payment.
	BillStatusPending BillStatus = 1
	// BillStatusPaid marks a bill as paid.
	BillStatusPaid BillStatus = 2
	// BillStatusRefundRequested marks a refund request.
	BillStatusRefundRequested BillStatus = 3
	// BillStatusRefunded marks a bill as refunded.
	BillStatusRefunded BillStatus = 4
)

// Bill records a user billing period and quota usage.
type Bill struct {
	ID uint64 `gorm:"primaryKey;autoIncrement"` // Primary key.

	PlanID uint64 `gorm:"not null;index"`    // Related plan ID.
	Plan   Plan   `gorm:"foreignKey:PlanID"` // Related plan record.

	UserID uint64 `gorm:"not null;index"`    // Related user ID.
	User   User   `gorm:"foreignKey:UserID"` // Related user record.

	PeriodType BillPeriodType `gorm:"not null"` // Billing period type.

	Amount float64 `gorm:"type:decimal(10,2);not null;default:0"` // Bill amount.

	PeriodStart time.Time `gorm:"not null"` // Period start time.
	PeriodEnd   time.Time `gorm:"not null"` // Period end time.

	TotalQuota float64 `gorm:"type:decimal(20,10);not null;default:0"` // Total quota for the period.
	DailyQuota float64 `gorm:"type:decimal(20,10);not null;default:0"` // Daily quota limit.
	UsedQuota  float64 `gorm:"type:decimal(20,10);not null;default:0"` // Consumed quota amount.
	LeftQuota  float64 `gorm:"type:decimal(20,10);not null;default:0"` // Remaining quota amount.
	RateLimit  int     `gorm:"not null;default:0"`                     // Rate limit per second.

	UsedCount int `gorm:"not null;default:0"` // Usage count within the period.

	IsEnabled bool       `gorm:"not null;default:true"` // Whether the bill is active.
	Status    BillStatus `gorm:"not null;default:1"`    // Current bill status.

	CreatedAt time.Time `gorm:"not null;autoCreateTime"` // Creation timestamp.
	UpdatedAt time.Time `gorm:"not null;autoUpdateTime"` // Last update timestamp.
}
