package models

import "time"

// User represents an end-user account stored in the database.
type User struct {
	ID uint64 `gorm:"primaryKey;autoIncrement"` // Primary key.

	Username string `gorm:"type:text;not null;uniqueIndex"` // Unique login name.
	Name     string `gorm:"type:text"`                      // Display name.
	Email    string `gorm:"type:text;uniqueIndex"`          // Email address.
	Password string `gorm:"type:text;not null"`             // Hashed password.

	UserGroupID *uint64    `gorm:"index"`                  // Assigned user group ID.
	UserGroup   *UserGroup `gorm:"foreignKey:UserGroupID"` // Assigned user group.

	PlanID *uint64 `gorm:"index"`             // Active plan ID.
	Plan   *Plan   `gorm:"foreignKey:PlanID"` // Active plan.

	DailyMaxUsage float64 `gorm:"type:decimal(20,10);not null;default:0"` // Daily usage cap.
	RateLimit     int     `gorm:"not null;default:0"`                     // Rate limit per second.

	Active   bool `gorm:"not null;default:true"`  // Whether the user can sign in.
	Disabled bool `gorm:"not null;default:false"` // Explicit disable flag.

	TOTPSecret            string  `gorm:"type:text"`    // TOTP secret for MFA.
	PasskeyID             []byte  `gorm:"type:bytea"`   // WebAuthn credential ID.
	PasskeyPublicKey      []byte  `gorm:"type:bytea"`   // WebAuthn public key bytes.
	PasskeySignCount      *uint32 `gorm:"type:bigint"`  // WebAuthn signature counter.
	PasskeyBackupEligible *bool   `gorm:"type:boolean"` // WebAuthn backup eligibility flag.
	PasskeyBackupState    *bool   `gorm:"type:boolean"` // WebAuthn backup state flag.

	APIKeys []APIKey `gorm:"foreignKey:UserID"` // Related API keys.

	CreatedAt time.Time `gorm:"not null;autoCreateTime"` // Creation timestamp.
	UpdatedAt time.Time `gorm:"not null;autoUpdateTime"` // Last update timestamp.
}
