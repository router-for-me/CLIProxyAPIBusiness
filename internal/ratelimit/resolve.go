package ratelimit

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPIBusiness/internal/modelmapping"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/gorm"
)

// ResolveLimit resolves the effective rate limit based on priority order.
func ResolveLimit(ctx context.Context, db *gorm.DB, userID uint64, provider, model, authKey string) (Decision, error) {
	if db == nil || userID == 0 {
		return Decision{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().UTC()

	billLimit, errBill := resolveBillRateLimit(ctx, db, userID, now)
	if errBill != nil {
		return Decision{}, errBill
	}
	if billLimit > 0 {
		return Decision{Limit: billLimit, Scope: ScopeUser}, nil
	}

	mappingID, mappingLimit, okMapping := modelmapping.LookupRateLimit(provider, model)
	if okMapping && mappingLimit > 0 && mappingID > 0 {
		return Decision{Limit: mappingLimit, Scope: ScopeModelMapping, MappingID: mappingID}, nil
	}

	userLimit, userGroupID, errUser := loadUserRateLimit(ctx, db, userID)
	if errUser != nil {
		return Decision{}, errUser
	}
	if userLimit > 0 {
		return Decision{Limit: userLimit, Scope: ScopeUser}, nil
	}

	if userGroupID != nil && *userGroupID > 0 {
		groupLimit, errGroup := loadUserGroupRateLimit(ctx, db, *userGroupID)
		if errGroup != nil {
			return Decision{}, errGroup
		}
		if groupLimit > 0 {
			return Decision{Limit: groupLimit, Scope: ScopeUser}, nil
		}
	}

	authLimit, authGroupID, errAuth := loadAuthRateLimit(ctx, db, authKey)
	if errAuth != nil {
		return Decision{}, errAuth
	}
	if authLimit > 0 {
		return Decision{Limit: authLimit, Scope: ScopeUser}, nil
	}

	if authGroupID != nil && *authGroupID > 0 {
		groupLimit, errGroup := loadAuthGroupRateLimit(ctx, db, *authGroupID)
		if errGroup != nil {
			return Decision{}, errGroup
		}
		if groupLimit > 0 {
			return Decision{Limit: groupLimit, Scope: ScopeUser}, nil
		}
	}

	settingsLimit := DefaultSettingsLimit()
	if settingsLimit > 0 {
		return Decision{Limit: settingsLimit, Scope: ScopeUser}, nil
	}
	return Decision{}, nil
}

func resolveBillRateLimit(ctx context.Context, db *gorm.DB, userID uint64, now time.Time) (int, error) {
	var rows []models.Bill
	if errFind := db.WithContext(ctx).
		Model(&models.Bill{}).
		Select("rate_limit").
		Where("user_id = ? AND is_enabled = ? AND status = ? AND left_quota > 0", userID, true, models.BillStatusPaid).
		Where("period_start <= ? AND period_end >= ?", now, now).
		Find(&rows).Error; errFind != nil {
		return 0, errFind
	}
	total := 0
	for _, row := range rows {
		if row.RateLimit > 0 {
			total += row.RateLimit
		}
	}
	return total, nil
}

func loadUserRateLimit(ctx context.Context, db *gorm.DB, userID uint64) (int, *uint64, error) {
	if db == nil || userID == 0 {
		return 0, nil, nil
	}
	type userRow struct {
		RateLimit   int
		UserGroupID *uint64
	}
	var row userRow
	if errFind := db.WithContext(ctx).
		Model(&models.User{}).
		Select("rate_limit", "user_group_id").
		Where("id = ?", userID).
		Take(&row).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			return 0, nil, nil
		}
		return 0, nil, errFind
	}
	return row.RateLimit, row.UserGroupID, nil
}

func loadUserGroupRateLimit(ctx context.Context, db *gorm.DB, groupID uint64) (int, error) {
	if db == nil || groupID == 0 {
		return 0, nil
	}
	var group models.UserGroup
	if errFind := db.WithContext(ctx).
		Model(&models.UserGroup{}).
		Select("rate_limit").
		Where("id = ?", groupID).
		Take(&group).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, errFind
	}
	return group.RateLimit, nil
}

func loadAuthRateLimit(ctx context.Context, db *gorm.DB, authKey string) (int, *uint64, error) {
	authKey = strings.TrimSpace(authKey)
	if db == nil || authKey == "" {
		return 0, nil, nil
	}
	var auth models.Auth
	if errFind := db.WithContext(ctx).
		Model(&models.Auth{}).
		Select("rate_limit", "auth_group_id").
		Where("key = ?", authKey).
		Take(&auth).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			return 0, nil, nil
		}
		return 0, nil, errFind
	}
	groupID := auth.AuthGroupID.Primary()
	return auth.RateLimit, groupID, nil
}

func loadAuthGroupRateLimit(ctx context.Context, db *gorm.DB, groupID uint64) (int, error) {
	if db == nil || groupID == 0 {
		return 0, nil
	}
	var group models.AuthGroup
	if errFind := db.WithContext(ctx).
		Model(&models.AuthGroup{}).
		Select("rate_limit").
		Where("id = ?", groupID).
		Take(&group).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, errFind
	}
	return group.RateLimit, nil
}
