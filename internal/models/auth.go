package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/datatypes"
)

// AuthGroupIDs stores auth group identifiers as a JSON array.
type AuthGroupIDs []*uint64

// Value implements driver.Valuer for database serialization.
func (ids AuthGroupIDs) Value() (driver.Value, error) {
	cleaned := ids.Clean()
	values := make([]uint64, 0, len(cleaned))
	for _, id := range cleaned {
		if id == nil {
			continue
		}
		values = append(values, *id)
	}
	data, errMarshal := json.Marshal(values)
	if errMarshal != nil {
		return nil, fmt.Errorf("auth group ids marshal: %w", errMarshal)
	}
	return data, nil
}

// Scan implements sql.Scanner for database deserialization.
func (ids *AuthGroupIDs) Scan(value any) error {
	if ids == nil {
		return fmt.Errorf("auth group ids scan: nil receiver")
	}
	if value == nil {
		*ids = AuthGroupIDs{}
		return nil
	}

	switch typed := value.(type) {
	case []byte:
		return parseAuthGroupIDsFromBytes(ids, typed)
	case string:
		return parseAuthGroupIDsFromBytes(ids, []byte(typed))
	case int64:
		idValue := uint64(typed)
		*ids = AuthGroupIDs{&idValue}
		return nil
	case uint64:
		idValue := typed
		*ids = AuthGroupIDs{&idValue}
		return nil
	case int:
		if typed < 0 {
			*ids = AuthGroupIDs{}
			return nil
		}
		idValue := uint64(typed)
		*ids = AuthGroupIDs{&idValue}
		return nil
	default:
		return fmt.Errorf("auth group ids scan: unsupported type %T", value)
	}
}

func parseAuthGroupIDsFromBytes(target *AuthGroupIDs, data []byte) error {
	if target == nil {
		return fmt.Errorf("auth group ids scan: nil target")
	}
	if len(data) == 0 {
		*target = AuthGroupIDs{}
		return nil
	}

	var list []uint64
	if errList := json.Unmarshal(data, &list); errList == nil {
		out := make(AuthGroupIDs, 0, len(list))
		for _, id := range list {
			idCopy := id
			out = append(out, &idCopy)
		}
		*target = out
		return nil
	}

	var listPtr []*uint64
	if errListPtr := json.Unmarshal(data, &listPtr); errListPtr == nil {
		*target = AuthGroupIDs(listPtr).Clean()
		return nil
	}

	var single uint64
	if errSingle := json.Unmarshal(data, &single); errSingle == nil {
		idCopy := single
		*target = AuthGroupIDs{&idCopy}
		return nil
	}

	return fmt.Errorf("auth group ids scan: invalid json")
}

// Clean normalizes auth group ids by removing nil/zero values and duplicates.
func (ids AuthGroupIDs) Clean() AuthGroupIDs {
	if len(ids) == 0 {
		return AuthGroupIDs{}
	}
	seen := make(map[uint64]struct{}, len(ids))
	cleaned := make(AuthGroupIDs, 0, len(ids))
	for _, id := range ids {
		if id == nil || *id == 0 {
			continue
		}
		if _, ok := seen[*id]; ok {
			continue
		}
		seen[*id] = struct{}{}
		idCopy := *id
		cleaned = append(cleaned, &idCopy)
	}
	if len(cleaned) == 0 {
		return AuthGroupIDs{}
	}
	return cleaned
}

// Primary returns the first non-zero auth group id if any.
func (ids AuthGroupIDs) Primary() *uint64 {
	for _, id := range ids {
		if id != nil && *id != 0 {
			return id
		}
	}
	return nil
}

// Values returns unique non-zero auth group ids in order.
func (ids AuthGroupIDs) Values() []uint64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[uint64]struct{}, len(ids))
	values := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if id == nil || *id == 0 {
			continue
		}
		if _, ok := seen[*id]; ok {
			continue
		}
		seen[*id] = struct{}{}
		values = append(values, *id)
	}
	return values
}

// Auth stores an authentication entry and its content for relay usage.
type Auth struct {
	ID  uint64 `gorm:"primaryKey;autoIncrement"`       // Primary key.
	Key string `gorm:"type:text;not null;uniqueIndex"` // Unique auth key.

	ProxyURL string `gorm:"type:text"` // Optional proxy override.

	AuthGroupID AuthGroupIDs `gorm:"type:jsonb;not null;default:'[]'"` // Owning auth group IDs.
	AuthGroup   []*AuthGroup `gorm:"-"`                                // Owning auth groups.

	Content datatypes.JSON `gorm:"type:jsonb;not null"` // Auth payload content.

	IsAvailable bool `gorm:"type:boolean;not null;default:true"` // Availability flag.

	CreatedAt time.Time `gorm:"not null;autoCreateTime"` // Creation timestamp.
	UpdatedAt time.Time `gorm:"not null;autoUpdateTime"` // Last update timestamp.
}
