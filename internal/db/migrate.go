package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	internalsettings "github.com/router-for-me/CLIProxyAPIBusiness/internal/settings"
	"gorm.io/gorm"
)

// Migrate runs database migrations for the current dialect.
func Migrate(conn *gorm.DB) error {
	if conn == nil {
		return fmt.Errorf("db: nil connection")
	}
	switch DialectName(conn) {
	case DialectSQLite:
		return migrateSQLite(conn)
	case DialectPostgres, "":
		return migratePostgres(conn)
	default:
		return fmt.Errorf("db: unsupported dialect: %s", DialectName(conn))
	}
}

// migratePostgres applies PostgreSQL-specific schema updates and indexes.
func migratePostgres(conn *gorm.DB) error {
	if errRename := conn.Exec(`
		DO $$
		BEGIN
			IF to_regclass('public.recharge_cards') IS NOT NULL AND to_regclass('public.prepaid_cards') IS NULL THEN
				ALTER TABLE recharge_cards RENAME TO prepaid_cards;
			END IF;
		END $$;
	`).Error; errRename != nil {
		return fmt.Errorf("db: rename recharge_cards: %w", errRename)
	}

	if errAutoMigrate := conn.AutoMigrate(
		&models.Admin{},
		&models.Plan{},
		&models.UserGroup{},
		&models.AuthGroup{},
		&models.User{},
		&models.Auth{},
		&models.Quota{},
		&models.APIKey{},
		&models.Usage{},
		&models.Bill{},
		&models.BillingRule{},
		&models.ModelMapping{},
		&models.UserModelAuthBinding{},
		&models.ModelPayloadRule{},
		&models.ProviderAPIKey{},
		&models.Proxy{},
		&models.PrepaidCard{},
		&models.Setting{},
	); errAutoMigrate != nil {
		return fmt.Errorf("db: migrate: %w", errAutoMigrate)
	}
	if errSeed := ensureDefaultGroups(conn); errSeed != nil {
		return errSeed
	}
	if errSeed := ensureOnlyMappedModelsSetting(conn); errSeed != nil {
		return errSeed
	}
	if errSeed := ensureQuotaPollSettings(conn); errSeed != nil {
		return errSeed
	}
	if errSeed := ensureAutoAssignProxySetting(conn); errSeed != nil {
		return errSeed
	}
	if errSeed := ensureRateLimitSetting(conn); errSeed != nil {
		return errSeed
	}
	if errAuthGroup := migrateAuthGroupIDsPostgres(conn); errAuthGroup != nil {
		return errAuthGroup
	}

	if errDropRuleType := conn.Exec(`
		ALTER TABLE model_payload_rules
		DROP COLUMN IF EXISTS rule_type
	`).Error; errDropRuleType != nil {
		return fmt.Errorf("db: drop payload rule_type: %w", errDropRuleType)
	}
	if errDropPayloadIndex := conn.Exec(`
		DROP INDEX IF EXISTS idx_model_payload_rules_enabled
	`).Error; errDropPayloadIndex != nil {
		return fmt.Errorf("db: drop payload rules index: %w", errDropPayloadIndex)
	}
	if errDropPayloadPriority := conn.Exec(`
		ALTER TABLE model_payload_rules
		DROP COLUMN IF EXISTS priority
	`).Error; errDropPayloadPriority != nil {
		return fmt.Errorf("db: drop payload priority: %w", errDropPayloadPriority)
	}
	if errPayloadUnique := conn.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_model_payload_rules_mapping
		ON model_payload_rules (model_mapping_id)
	`).Error; errPayloadUnique != nil {
		return fmt.Errorf("db: create payload mapping index: %w", errPayloadUnique)
	}

	if errBalanceAdd := conn.Exec(`
		ALTER TABLE prepaid_cards
		ADD COLUMN IF NOT EXISTS balance decimal(20,10) NOT NULL DEFAULT 0
	`).Error; errBalanceAdd != nil {
		return fmt.Errorf("db: add prepaid balance: %w", errBalanceAdd)
	}
	if errBalanceBackfill := conn.Exec(`
		UPDATE prepaid_cards
		SET balance = amount
		WHERE balance = 0 AND amount > 0
	`).Error; errBalanceBackfill != nil {
		return fmt.Errorf("db: backfill prepaid balance: %w", errBalanceBackfill)
	}
	if errValidDaysAdd := conn.Exec(`
		ALTER TABLE prepaid_cards
		ADD COLUMN IF NOT EXISTS valid_days integer NOT NULL DEFAULT 0
	`).Error; errValidDaysAdd != nil {
		return fmt.Errorf("db: add prepaid valid days: %w", errValidDaysAdd)
	}
	if errExpiresAdd := conn.Exec(`
		ALTER TABLE prepaid_cards
		ADD COLUMN IF NOT EXISTS expires_at timestamptz
	`).Error; errExpiresAdd != nil {
		return fmt.Errorf("db: add prepaid expires_at: %w", errExpiresAdd)
	}
	if errExpireIdx := conn.Exec(`
		CREATE INDEX IF NOT EXISTS idx_prepaid_cards_redeemed_expiry ON prepaid_cards (redeemed_user_id, expires_at)
	`).Error; errExpireIdx != nil {
		return fmt.Errorf("db: add prepaid expiry index: %w", errExpireIdx)
	}
	if errDropUserBalance := conn.Exec(`
		ALTER TABLE users
		DROP COLUMN IF EXISTS balance
	`).Error; errDropUserBalance != nil {
		return fmt.Errorf("db: drop user balance: %w", errDropUserBalance)
	}

	if errAdminPermAdd := conn.Exec(`
		ALTER TABLE admins
		ADD COLUMN IF NOT EXISTS permissions jsonb DEFAULT '[]'::jsonb
	`).Error; errAdminPermAdd != nil {
		return fmt.Errorf("db: add admin permissions: %w", errAdminPermAdd)
	}
	if errAdminPermUpdate := conn.Exec(`
		UPDATE admins
		SET permissions = '[]'::jsonb
		WHERE permissions IS NULL
	`).Error; errAdminPermUpdate != nil {
		return fmt.Errorf("db: backfill admin permissions: %w", errAdminPermUpdate)
	}
	if errAdminPermDefault := conn.Exec(`
		ALTER TABLE admins
		ALTER COLUMN permissions SET DEFAULT '[]'::jsonb
	`).Error; errAdminPermDefault != nil {
		return fmt.Errorf("db: default admin permissions: %w", errAdminPermDefault)
	}
	if errAdminPermNotNull := conn.Exec(`
		ALTER TABLE admins
		ALTER COLUMN permissions SET NOT NULL
	`).Error; errAdminPermNotNull != nil {
		return fmt.Errorf("db: enforce admin permissions not null: %w", errAdminPermNotNull)
	}

	if errAdminSuperAdd := conn.Exec(`
		ALTER TABLE admins
		ADD COLUMN IF NOT EXISTS is_super_admin boolean DEFAULT false
	`).Error; errAdminSuperAdd != nil {
		return fmt.Errorf("db: add admin super flag: %w", errAdminSuperAdd)
	}
	if errAdminSuperUpdate := conn.Exec(`
		UPDATE admins
		SET is_super_admin = false
		WHERE is_super_admin IS NULL
	`).Error; errAdminSuperUpdate != nil {
		return fmt.Errorf("db: backfill admin super flag: %w", errAdminSuperUpdate)
	}
	if errAdminSuperDefault := conn.Exec(`
		ALTER TABLE admins
		ALTER COLUMN is_super_admin SET DEFAULT false
	`).Error; errAdminSuperDefault != nil {
		return fmt.Errorf("db: default admin super flag: %w", errAdminSuperDefault)
	}
	if errAdminSuperNotNull := conn.Exec(`
		ALTER TABLE admins
		ALTER COLUMN is_super_admin SET NOT NULL
	`).Error; errAdminSuperNotNull != nil {
		return fmt.Errorf("db: enforce admin super flag not null: %w", errAdminSuperNotNull)
	}
	if errAdminSuperSeed := conn.Exec(`
		UPDATE admins
		SET is_super_admin = true
		WHERE id = (
			SELECT id FROM admins ORDER BY created_at ASC, id ASC LIMIT 1
		)
		AND NOT EXISTS (SELECT 1 FROM admins WHERE is_super_admin = true)
	`).Error; errAdminSuperSeed != nil {
		return fmt.Errorf("db: seed admin super flag: %w", errAdminSuperSeed)
	}

	if errAdminTotpAdd := conn.Exec(`
		ALTER TABLE admins
		ADD COLUMN IF NOT EXISTS totp_secret text
	`).Error; errAdminTotpAdd != nil {
		return fmt.Errorf("db: add admin totp secret: %w", errAdminTotpAdd)
	}
	if errAdminPasskeyIDAdd := conn.Exec(`
		ALTER TABLE admins
		ADD COLUMN IF NOT EXISTS passkey_id bytea
	`).Error; errAdminPasskeyIDAdd != nil {
		return fmt.Errorf("db: add admin passkey id: %w", errAdminPasskeyIDAdd)
	}
	if errAdminPasskeyKeyAdd := conn.Exec(`
		ALTER TABLE admins
		ADD COLUMN IF NOT EXISTS passkey_public_key bytea
	`).Error; errAdminPasskeyKeyAdd != nil {
		return fmt.Errorf("db: add admin passkey public key: %w", errAdminPasskeyKeyAdd)
	}
	if errAdminPasskeyCountAdd := conn.Exec(`
		ALTER TABLE admins
		ADD COLUMN IF NOT EXISTS passkey_sign_count bigint
	`).Error; errAdminPasskeyCountAdd != nil {
		return fmt.Errorf("db: add admin passkey sign count: %w", errAdminPasskeyCountAdd)
	}
	if errAdminPasskeyBackupEligibleAdd := conn.Exec(`
		ALTER TABLE admins
		ADD COLUMN IF NOT EXISTS passkey_backup_eligible boolean
	`).Error; errAdminPasskeyBackupEligibleAdd != nil {
		return fmt.Errorf("db: add admin passkey backup eligible: %w", errAdminPasskeyBackupEligibleAdd)
	}
	if errAdminPasskeyBackupStateAdd := conn.Exec(`
		ALTER TABLE admins
		ADD COLUMN IF NOT EXISTS passkey_backup_state boolean
	`).Error; errAdminPasskeyBackupStateAdd != nil {
		return fmt.Errorf("db: add admin passkey backup state: %w", errAdminPasskeyBackupStateAdd)
	}

	if errUserTotpAdd := conn.Exec(`
		ALTER TABLE users
		ADD COLUMN IF NOT EXISTS totp_secret text
	`).Error; errUserTotpAdd != nil {
		return fmt.Errorf("db: add user totp secret: %w", errUserTotpAdd)
	}
	if errUserPasskeyIDAdd := conn.Exec(`
		ALTER TABLE users
		ADD COLUMN IF NOT EXISTS passkey_id bytea
	`).Error; errUserPasskeyIDAdd != nil {
		return fmt.Errorf("db: add user passkey id: %w", errUserPasskeyIDAdd)
	}
	if errUserPasskeyKeyAdd := conn.Exec(`
		ALTER TABLE users
		ADD COLUMN IF NOT EXISTS passkey_public_key bytea
	`).Error; errUserPasskeyKeyAdd != nil {
		return fmt.Errorf("db: add user passkey public key: %w", errUserPasskeyKeyAdd)
	}
	if errUserPasskeyCountAdd := conn.Exec(`
		ALTER TABLE users
		ADD COLUMN IF NOT EXISTS passkey_sign_count bigint
	`).Error; errUserPasskeyCountAdd != nil {
		return fmt.Errorf("db: add user passkey sign count: %w", errUserPasskeyCountAdd)
	}
	if errUserPasskeyBackupEligibleAdd := conn.Exec(`
		ALTER TABLE users
		ADD COLUMN IF NOT EXISTS passkey_backup_eligible boolean
	`).Error; errUserPasskeyBackupEligibleAdd != nil {
		return fmt.Errorf("db: add user passkey backup eligible: %w", errUserPasskeyBackupEligibleAdd)
	}
	if errUserPasskeyBackupStateAdd := conn.Exec(`
		ALTER TABLE users
		ADD COLUMN IF NOT EXISTS passkey_backup_state boolean
	`).Error; errUserPasskeyBackupStateAdd != nil {
		return fmt.Errorf("db: add user passkey backup state: %w", errUserPasskeyBackupStateAdd)
	}

	_ = conn.Exec(`CREATE EXTENSION IF NOT EXISTS pg_trgm`).Error

	// ddl defines an index or DDL statement to apply.
	type ddl struct {
		name string // Human-readable name for error reporting.
		sql  string // SQL to execute.
	}
	ddls := []ddl{
		{
			name: "idx_auths_content_type",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_auths_content_type
				ON auths ((content->>'type'))
			`,
		},
		{
			name: "idx_auths_updated_at_id",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_auths_updated_at_id
				ON auths (updated_at DESC, id DESC)
			`,
		},
		{
			name: "idx_auths_auth_group_id",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_auths_auth_group_id
				ON auths (auth_group_id)
			`,
		},
		{
			name: "idx_auths_auth_group_id",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_auths_auth_group_id
				ON auths USING gin (auth_group_id)
			`,
		},
		{
			name: "idx_settings_updated_at_key",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_settings_updated_at_key
				ON settings (updated_at DESC, key DESC)
			`,
		},
		{
			name: "idx_auths_available_id",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_auths_available_id
				ON auths (id)
				WHERE is_available = true
			`,
		},
		{
			name: "idx_user_groups_default_true",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_user_groups_default_true
				ON user_groups (id)
				WHERE is_default = true
			`,
		},
		{
			name: "idx_auth_groups_default_true",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_auth_groups_default_true
				ON auth_groups (id)
				WHERE is_default = true
			`,
		},
		{
			name: "idx_plans_sort_order_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_plans_sort_order_created_at
				ON plans (sort_order ASC, created_at DESC)
			`,
		},
		{
			name: "idx_plans_is_enabled_sort_order_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_plans_is_enabled_sort_order_created_at
				ON plans (is_enabled, sort_order ASC, created_at DESC)
			`,
		},
		{
			name: "idx_bills_user_id_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_bills_user_id_created_at
				ON bills (user_id, created_at DESC)
			`,
		},
		{
			name: "idx_bills_plan_id_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_bills_plan_id_created_at
				ON bills (plan_id, created_at DESC)
			`,
		},
		{
			name: "idx_bills_status_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_bills_status_created_at
				ON bills (status, created_at DESC)
			`,
		},
		{
			name: "idx_bills_is_enabled_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_bills_is_enabled_created_at
				ON bills (is_enabled, created_at DESC)
			`,
		},
		{
			name: "idx_billing_rules_match",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_billing_rules_match
				ON billing_rules (auth_group_id, user_group_id, is_enabled, provider, model)
			`,
		},
		{
			name: "idx_model_mappings_provider_model_name_is_enabled",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_model_mappings_provider_model_name_is_enabled
				ON model_mappings (provider, model_name, is_enabled)
			`,
		},
		{
			name: "idx_model_mappings_provider_new_model_name_is_enabled",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_model_mappings_provider_new_model_name_is_enabled
				ON model_mappings (provider, new_model_name, is_enabled)
			`,
		},
		{
			name: "idx_prepaid_cards_redeemed_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_prepaid_cards_redeemed_at
				ON prepaid_cards (redeemed_at)
			`,
		},
		{
			name: "idx_prepaid_cards_redeemed_expiry",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_prepaid_cards_redeemed_expiry
				ON prepaid_cards (redeemed_user_id, expires_at)
			`,
		},
		{
			name: "idx_api_keys_user_id_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_api_keys_user_id_created_at
				ON api_keys (user_id, created_at DESC)
			`,
		},
		{
			name: "idx_api_keys_user_id_active_not_revoked",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_api_keys_user_id_active_not_revoked
				ON api_keys (user_id)
				WHERE active = true AND revoked_at IS NULL
			`,
		},
		{
			name: "idx_api_keys_user_id_expires_at_active",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_api_keys_user_id_expires_at_active
				ON api_keys (user_id, expires_at)
				WHERE active = true AND revoked_at IS NULL AND expires_at IS NOT NULL
			`,
		},
		{
			name: "idx_api_keys_user_id_revoked_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_api_keys_user_id_revoked_at
				ON api_keys (user_id, revoked_at)
				WHERE revoked_at IS NOT NULL
			`,
		},
		{
			name: "idx_usages_source",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_usages_source
				ON usages (source)
			`,
		},
		{
			name: "idx_usages_user_id_requested_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_usages_user_id_requested_at
				ON usages (user_id, requested_at DESC)
			`,
		},
		{
			name: "idx_usages_api_key_id_requested_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_usages_api_key_id_requested_at
				ON usages (api_key_id, requested_at DESC)
			`,
		},
		{
			name: "idx_usages_user_id_model",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_usages_user_id_model
				ON usages (user_id, model)
			`,
		},
		{
			name: "idx_usages_user_id_provider_model",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_usages_user_id_provider_model
				ON usages (user_id, provider, model)
			`,
		},
		{
			name: "idx_usages_user_id_source",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_usages_user_id_source
				ON usages (user_id, source)
			`,
		},
		{
			name: "idx_user_model_auth_bindings_user_model",
			sql: `
				CREATE UNIQUE INDEX IF NOT EXISTS idx_user_model_auth_bindings_user_model
				ON user_model_auth_bindings (user_id, model_mapping_id)
			`,
		},
	}
	for _, item := range ddls {
		if errDDL := conn.Exec(item.sql).Error; errDDL != nil {
			return fmt.Errorf("db: create index %s: %w", item.name, errDDL)
		}
	}

	// trgmIndex defines trigram and fallback index statements.
	type trgmIndex struct {
		name     string // Logical index name.
		trgmSQL  string // Trigram index SQL.
		lowerSQL string // Lowercase fallback index SQL.
	}
	trgmIndexes := []trgmIndex{
		{
			name: "idx_auths_key",
			trgmSQL: `
				CREATE INDEX IF NOT EXISTS idx_auths_key_trgm
				ON auths USING gin (key gin_trgm_ops)
			`,
			lowerSQL: `
				CREATE INDEX IF NOT EXISTS idx_auths_key_lower
				ON auths (LOWER(key))
			`,
		},
		{
			name: "idx_users_username",
			trgmSQL: `
				CREATE INDEX IF NOT EXISTS idx_users_username_trgm
				ON users USING gin (username gin_trgm_ops)
			`,
			lowerSQL: `
				CREATE INDEX IF NOT EXISTS idx_users_username_lower
				ON users (LOWER(username))
			`,
		},
		{
			name: "idx_users_email",
			trgmSQL: `
				CREATE INDEX IF NOT EXISTS idx_users_email_trgm
				ON users USING gin (email gin_trgm_ops)
			`,
			lowerSQL: `
				CREATE INDEX IF NOT EXISTS idx_users_email_lower
				ON users (LOWER(email))
			`,
		},
		{
			name: "idx_admins_username",
			trgmSQL: `
				CREATE INDEX IF NOT EXISTS idx_admins_username_trgm
				ON admins USING gin (username gin_trgm_ops)
			`,
			lowerSQL: `
				CREATE INDEX IF NOT EXISTS idx_admins_username_lower
				ON admins (LOWER(username))
			`,
		},
		{
			name: "idx_user_groups_name",
			trgmSQL: `
				CREATE INDEX IF NOT EXISTS idx_user_groups_name_trgm
				ON user_groups USING gin (name gin_trgm_ops)
			`,
			lowerSQL: `
				CREATE INDEX IF NOT EXISTS idx_user_groups_name_lower
				ON user_groups (LOWER(name))
			`,
		},
		{
			name: "idx_auth_groups_name",
			trgmSQL: `
				CREATE INDEX IF NOT EXISTS idx_auth_groups_name_trgm
				ON auth_groups USING gin (name gin_trgm_ops)
			`,
			lowerSQL: `
				CREATE INDEX IF NOT EXISTS idx_auth_groups_name_lower
				ON auth_groups (LOWER(name))
			`,
		},
		{
			name: "idx_prepaid_cards_name",
			trgmSQL: `
				CREATE INDEX IF NOT EXISTS idx_prepaid_cards_name_trgm
				ON prepaid_cards USING gin (name gin_trgm_ops)
			`,
			lowerSQL: `
				CREATE INDEX IF NOT EXISTS idx_prepaid_cards_name_lower
				ON prepaid_cards (LOWER(name))
			`,
		},
		{
			name: "idx_prepaid_cards_card_sn",
			trgmSQL: `
				CREATE INDEX IF NOT EXISTS idx_prepaid_cards_card_sn_trgm
				ON prepaid_cards USING gin (card_sn gin_trgm_ops)
			`,
			lowerSQL: `
				CREATE INDEX IF NOT EXISTS idx_prepaid_cards_card_sn_lower
				ON prepaid_cards (LOWER(card_sn))
			`,
		},
		{
			name: "idx_api_keys_name",
			trgmSQL: `
				CREATE INDEX IF NOT EXISTS idx_api_keys_name_trgm
				ON api_keys USING gin (LOWER(name) gin_trgm_ops)
			`,
			lowerSQL: `
				CREATE INDEX IF NOT EXISTS idx_api_keys_name_lower
				ON api_keys (LOWER(name))
			`,
		},
		{
			name: "idx_api_keys_api_key",
			trgmSQL: `
				CREATE INDEX IF NOT EXISTS idx_api_keys_api_key_trgm
				ON api_keys USING gin (LOWER(api_key) gin_trgm_ops)
			`,
			lowerSQL: `
				CREATE INDEX IF NOT EXISTS idx_api_keys_api_key_lower
				ON api_keys (LOWER(api_key))
			`,
		},
		{
			name: "idx_provider_api_keys_name",
			trgmSQL: `
				CREATE INDEX IF NOT EXISTS idx_provider_api_keys_name_trgm
				ON provider_api_keys USING gin (LOWER(name) gin_trgm_ops)
			`,
			lowerSQL: `
				CREATE INDEX IF NOT EXISTS idx_provider_api_keys_name_lower
				ON provider_api_keys (LOWER(name))
			`,
		},
		{
			name: "idx_provider_api_keys_api_key",
			trgmSQL: `
				CREATE INDEX IF NOT EXISTS idx_provider_api_keys_api_key_trgm
				ON provider_api_keys USING gin (LOWER(api_key) gin_trgm_ops)
			`,
			lowerSQL: `
				CREATE INDEX IF NOT EXISTS idx_provider_api_keys_api_key_lower
				ON provider_api_keys (LOWER(api_key))
			`,
		},
		{
			name: "idx_proxies_proxy_url",
			trgmSQL: `
				CREATE INDEX IF NOT EXISTS idx_proxies_proxy_url_trgm
				ON proxies USING gin (LOWER(proxy_url) gin_trgm_ops)
			`,
			lowerSQL: `
				CREATE INDEX IF NOT EXISTS idx_proxies_proxy_url_lower
				ON proxies (LOWER(proxy_url))
			`,
		},
	}
	for _, item := range trgmIndexes {
		if errIdx := conn.Exec(item.trgmSQL).Error; errIdx != nil {
			if errLower := conn.Exec(item.lowerSQL).Error; errLower != nil {
				return fmt.Errorf("db: create index %s: %w", item.name, errLower)
			}
		}
	}

	return nil
}

// migrateSQLite applies SQLite-specific schema updates and indexes.
func migrateSQLite(conn *gorm.DB) error {
	if errFix := fixSQLiteTimestampColumns(conn); errFix != nil {
		return errFix
	}

	if errRename := renameTableIfNeeded(conn, "recharge_cards", "prepaid_cards"); errRename != nil {
		return fmt.Errorf("db: rename recharge_cards: %w", errRename)
	}

	if errAutoMigrate := conn.AutoMigrate(
		&models.Admin{},
		&models.Plan{},
		&models.UserGroup{},
		&models.AuthGroup{},
		&models.User{},
		&models.Auth{},
		&models.Quota{},
		&models.APIKey{},
		&models.Usage{},
		&models.Bill{},
		&models.BillingRule{},
		&models.ModelMapping{},
		&models.UserModelAuthBinding{},
		&models.ModelPayloadRule{},
		&models.ProviderAPIKey{},
		&models.Proxy{},
		&models.PrepaidCard{},
		&models.Setting{},
	); errAutoMigrate != nil {
		return fmt.Errorf("db: migrate: %w", errAutoMigrate)
	}
	if errSeed := ensureDefaultGroups(conn); errSeed != nil {
		return errSeed
	}
	if errSeed := ensureOnlyMappedModelsSetting(conn); errSeed != nil {
		return errSeed
	}
	if errSeed := ensureQuotaPollSettings(conn); errSeed != nil {
		return errSeed
	}
	if errSeed := ensureAutoAssignProxySetting(conn); errSeed != nil {
		return errSeed
	}
	if errSeed := ensureRateLimitSetting(conn); errSeed != nil {
		return errSeed
	}

	if errDropPayloadIndex := conn.Exec(`
		DROP INDEX IF EXISTS idx_model_payload_rules_enabled
	`).Error; errDropPayloadIndex != nil {
		return fmt.Errorf("db: drop payload rules index: %w", errDropPayloadIndex)
	}
	if errPayloadUnique := conn.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_model_payload_rules_mapping
		ON model_payload_rules (model_mapping_id)
	`).Error; errPayloadUnique != nil {
		return fmt.Errorf("db: create payload mapping index: %w", errPayloadUnique)
	}

	if errBalanceBackfill := conn.Exec(`
		UPDATE prepaid_cards
		SET balance = amount
		WHERE balance = 0 AND amount > 0
	`).Error; errBalanceBackfill != nil {
		return fmt.Errorf("db: backfill prepaid balance: %w", errBalanceBackfill)
	}

	if errAdminPermUpdate := conn.Exec(`
		UPDATE admins
		SET permissions = '[]'
		WHERE permissions IS NULL
	`).Error; errAdminPermUpdate != nil {
		return fmt.Errorf("db: backfill admin permissions: %w", errAdminPermUpdate)
	}
	if errAdminSuperUpdate := conn.Exec(`
		UPDATE admins
		SET is_super_admin = false
		WHERE is_super_admin IS NULL
	`).Error; errAdminSuperUpdate != nil {
		return fmt.Errorf("db: backfill admin super flag: %w", errAdminSuperUpdate)
	}
	if errAdminSuperSeed := conn.Exec(`
		UPDATE admins
		SET is_super_admin = true
		WHERE id = (
			SELECT id FROM admins ORDER BY created_at ASC, id ASC LIMIT 1
		)
		AND NOT EXISTS (SELECT 1 FROM admins WHERE is_super_admin = true)
	`).Error; errAdminSuperSeed != nil {
		return fmt.Errorf("db: seed admin super flag: %w", errAdminSuperSeed)
	}

	// ddl defines an index or DDL statement to apply.
	type ddl struct {
		name string // Human-readable name for error reporting.
		sql  string // SQL to execute.
	}
	ddls := []ddl{
		{
			name: "idx_auths_updated_at_id",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_auths_updated_at_id
				ON auths (updated_at DESC, id DESC)
			`,
		},
		{
			name: "idx_settings_updated_at_key",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_settings_updated_at_key
				ON settings (updated_at DESC, key DESC)
			`,
		},
		{
			name: "idx_auths_available_id",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_auths_available_id
				ON auths (id)
				WHERE is_available = true
			`,
		},
		{
			name: "idx_user_groups_default_true",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_user_groups_default_true
				ON user_groups (id)
				WHERE is_default = true
			`,
		},
		{
			name: "idx_auth_groups_default_true",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_auth_groups_default_true
				ON auth_groups (id)
				WHERE is_default = true
			`,
		},
		{
			name: "idx_plans_sort_order_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_plans_sort_order_created_at
				ON plans (sort_order ASC, created_at DESC)
			`,
		},
		{
			name: "idx_plans_is_enabled_sort_order_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_plans_is_enabled_sort_order_created_at
				ON plans (is_enabled, sort_order ASC, created_at DESC)
			`,
		},
		{
			name: "idx_bills_user_id_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_bills_user_id_created_at
				ON bills (user_id, created_at DESC)
			`,
		},
		{
			name: "idx_bills_plan_id_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_bills_plan_id_created_at
				ON bills (plan_id, created_at DESC)
			`,
		},
		{
			name: "idx_bills_status_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_bills_status_created_at
				ON bills (status, created_at DESC)
			`,
		},
		{
			name: "idx_bills_is_enabled_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_bills_is_enabled_created_at
				ON bills (is_enabled, created_at DESC)
			`,
		},
		{
			name: "idx_billing_rules_match",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_billing_rules_match
				ON billing_rules (auth_group_id, user_group_id, is_enabled, provider, model)
			`,
		},
		{
			name: "idx_proxies_proxy_url",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_proxies_proxy_url
				ON proxies (proxy_url)
			`,
		},
		{
			name: "idx_model_mappings_provider_model_name_is_enabled",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_model_mappings_provider_model_name_is_enabled
				ON model_mappings (provider, model_name, is_enabled)
			`,
		},
		{
			name: "idx_model_mappings_provider_new_model_name_is_enabled",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_model_mappings_provider_new_model_name_is_enabled
				ON model_mappings (provider, new_model_name, is_enabled)
			`,
		},
		{
			name: "idx_prepaid_cards_redeemed_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_prepaid_cards_redeemed_at
				ON prepaid_cards (redeemed_at)
			`,
		},
		{
			name: "idx_api_keys_user_id_created_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_api_keys_user_id_created_at
				ON api_keys (user_id, created_at DESC)
			`,
		},
		{
			name: "idx_api_keys_user_id_active_not_revoked",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_api_keys_user_id_active_not_revoked
				ON api_keys (user_id)
				WHERE active = true AND revoked_at IS NULL
			`,
		},
		{
			name: "idx_api_keys_user_id_expires_at_active",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_api_keys_user_id_expires_at_active
				ON api_keys (user_id, expires_at)
				WHERE active = true AND revoked_at IS NULL AND expires_at IS NOT NULL
			`,
		},
		{
			name: "idx_api_keys_user_id_revoked_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_api_keys_user_id_revoked_at
				ON api_keys (user_id, revoked_at)
				WHERE revoked_at IS NOT NULL
			`,
		},
		{
			name: "idx_usages_source",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_usages_source
				ON usages (source)
			`,
		},
		{
			name: "idx_usages_user_id_requested_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_usages_user_id_requested_at
				ON usages (user_id, requested_at DESC)
			`,
		},
		{
			name: "idx_usages_api_key_id_requested_at",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_usages_api_key_id_requested_at
				ON usages (api_key_id, requested_at DESC)
			`,
		},
		{
			name: "idx_usages_user_id_model",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_usages_user_id_model
				ON usages (user_id, model)
			`,
		},
		{
			name: "idx_usages_user_id_provider_model",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_usages_user_id_provider_model
				ON usages (user_id, provider, model)
			`,
		},
		{
			name: "idx_usages_user_id_source",
			sql: `
				CREATE INDEX IF NOT EXISTS idx_usages_user_id_source
				ON usages (user_id, source)
			`,
		},
		{
			name: "idx_user_model_auth_bindings_user_model",
			sql: `
				CREATE UNIQUE INDEX IF NOT EXISTS idx_user_model_auth_bindings_user_model
				ON user_model_auth_bindings (user_id, model_mapping_id)
			`,
		},
	}
	for _, item := range ddls {
		if errDDL := conn.Exec(item.sql).Error; errDDL != nil {
			return fmt.Errorf("db: create index %s: %w", item.name, errDDL)
		}
	}

	return nil
}

func migrateAuthGroupIDsPostgres(conn *gorm.DB) error {
	if conn == nil {
		return fmt.Errorf("db: migrate auth group ids: nil connection")
	}
	if errAlter := conn.Exec(`
		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1
				FROM information_schema.columns
				WHERE table_name = 'auths'
				AND column_name = 'auth_group_id'
				AND data_type <> 'jsonb'
			) THEN
				ALTER TABLE auths
					ALTER COLUMN auth_group_id TYPE jsonb
					USING CASE
						WHEN auth_group_id IS NULL THEN '[]'::jsonb
						ELSE to_jsonb(ARRAY[auth_group_id])
					END;
			END IF;
		END $$;
	`).Error; errAlter != nil {
		return fmt.Errorf("db: migrate auth group ids: %w", errAlter)
	}
	if errNormalize := conn.Exec(`
		UPDATE auths
		SET auth_group_id = to_jsonb(ARRAY[auth_group_id::bigint])
		WHERE jsonb_typeof(auth_group_id) = 'number'
	`).Error; errNormalize != nil {
		return fmt.Errorf("db: normalize auth group ids: %w", errNormalize)
	}
	if errBackfill := conn.Exec(`
		UPDATE auths
		SET auth_group_id = '[]'::jsonb
		WHERE auth_group_id IS NULL
	`).Error; errBackfill != nil {
		return fmt.Errorf("db: backfill auth group ids: %w", errBackfill)
	}
	if errDefault := conn.Exec(`
		ALTER TABLE auths
		ALTER COLUMN auth_group_id SET DEFAULT '[]'::jsonb
	`).Error; errDefault != nil {
		return fmt.Errorf("db: default auth group ids: %w", errDefault)
	}
	if errNotNull := conn.Exec(`
		ALTER TABLE auths
		ALTER COLUMN auth_group_id SET NOT NULL
	`).Error; errNotNull != nil {
		return fmt.Errorf("db: enforce auth group ids not null: %w", errNotNull)
	}
	return nil
}

func migrateAuthGroupIDsSQLite(conn *gorm.DB) error {
	if conn == nil {
		return fmt.Errorf("db: migrate auth group ids: nil connection")
	}
	if errUpdate := conn.Exec(`
		UPDATE auths
		SET auth_group_id = printf('[%d]', auth_group_id)
		WHERE auth_group_id IS NOT NULL
		AND typeof(auth_group_id) IN ('integer', 'real')
	`).Error; errUpdate != nil {
		return fmt.Errorf("db: convert auth group ids: %w", errUpdate)
	}
	if errUpdate := conn.Exec(`
		UPDATE auths
		SET auth_group_id = '[' || auth_group_id || ']'
		WHERE auth_group_id IS NOT NULL
		AND typeof(auth_group_id) = 'text'
		AND auth_group_id NOT LIKE '[%'
	`).Error; errUpdate != nil {
		return fmt.Errorf("db: normalize auth group ids: %w", errUpdate)
	}
	if errUpdate := conn.Exec(`
		UPDATE auths
		SET auth_group_id = '[]'
		WHERE auth_group_id IS NULL
	`).Error; errUpdate != nil {
		return fmt.Errorf("db: backfill auth group ids: %w", errUpdate)
	}
	return nil
}

// renameTableIfNeeded renames a table when the source exists and target is absent.
func renameTableIfNeeded(conn *gorm.DB, from, to string) error {
	migrator := conn.Migrator()
	if migrator == nil {
		return fmt.Errorf("db: nil migrator")
	}
	hasFrom := migrator.HasTable(from)
	hasTo := migrator.HasTable(to)
	if !hasFrom || hasTo {
		return nil
	}
	return migrator.RenameTable(from, to)
}

// ensureDefaultGroups seeds default auth and user groups.
func ensureDefaultGroups(conn *gorm.DB) error {
	if errAuth := ensureDefaultAuthGroup(conn); errAuth != nil {
		return errAuth
	}
	if errUser := ensureDefaultUserGroup(conn); errUser != nil {
		return errUser
	}
	return nil
}

// ensureOnlyMappedModelsSetting ensures ONLY_MAPPED_MODELS exists and defaults to true.
func ensureOnlyMappedModelsSetting(conn *gorm.DB) error {
	var existing models.Setting
	if errFind := conn.Where("key = ?", "ONLY_MAPPED_MODELS").First(&existing).Error; errFind == nil {
		if len(existing.Value) == 0 || string(existing.Value) == "null" {
			if errUpdate := conn.Model(&existing).Updates(map[string]any{
				"value":      json.RawMessage("true"),
				"updated_at": time.Now().UTC(),
			}).Error; errUpdate != nil {
				return fmt.Errorf("db: update ONLY_MAPPED_MODELS setting: %w", errUpdate)
			}
		}
		return nil
	} else if !errors.Is(errFind, gorm.ErrRecordNotFound) {
		return fmt.Errorf("db: query ONLY_MAPPED_MODELS setting: %w", errFind)
	}

	now := time.Now().UTC()
	setting := models.Setting{
		Key:       "ONLY_MAPPED_MODELS",
		Value:     json.RawMessage("true"),
		UpdatedAt: now,
	}
	if errCreate := conn.Create(&setting).Error; errCreate != nil {
		return fmt.Errorf("db: create ONLY_MAPPED_MODELS setting: %w", errCreate)
	}
	return nil
}

// ensureQuotaPollSettings ensures quota polling settings exist with defaults.
func ensureQuotaPollSettings(conn *gorm.DB) error {
	if errEnsure := ensureIntSetting(
		conn,
		internalsettings.QuotaPollIntervalSecondsKey,
		internalsettings.DefaultQuotaPollIntervalSeconds,
	); errEnsure != nil {
		return errEnsure
	}
	if errEnsure := ensureIntSetting(
		conn,
		internalsettings.QuotaPollMaxConcurrencyKey,
		internalsettings.DefaultQuotaPollMaxConcurrency,
	); errEnsure != nil {
		return errEnsure
	}
	return nil
}

// ensureAutoAssignProxySetting ensures AUTO_ASSIGN_PROXY exists with defaults.
func ensureAutoAssignProxySetting(conn *gorm.DB) error {
	return ensureBoolSetting(
		conn,
		internalsettings.AutoAssignProxyKey,
		internalsettings.DefaultAutoAssignProxy,
	)
}

// ensureRateLimitSetting ensures RATE_LIMIT exists with defaults.
func ensureRateLimitSetting(conn *gorm.DB) error {
	return ensureIntSetting(conn, internalsettings.RateLimitKey, internalsettings.DefaultRateLimit)
}

// ensureIntSetting ensures an integer setting exists and defaults when empty.
func ensureIntSetting(conn *gorm.DB, key string, value int) error {
	payload, errMarshal := json.Marshal(value)
	if errMarshal != nil {
		return fmt.Errorf("db: marshal %s setting: %w", key, errMarshal)
	}
	rawValue := json.RawMessage(payload)

	var existing models.Setting
	if errFind := conn.Where("key = ?", key).First(&existing).Error; errFind == nil {
		trimmed := strings.TrimSpace(string(existing.Value))
		if len(existing.Value) == 0 || trimmed == "" || trimmed == "null" {
			if errUpdate := conn.Model(&existing).Updates(map[string]any{
				"value":      rawValue,
				"updated_at": time.Now().UTC(),
			}).Error; errUpdate != nil {
				return fmt.Errorf("db: update %s setting: %w", key, errUpdate)
			}
		}
		return nil
	} else if !errors.Is(errFind, gorm.ErrRecordNotFound) {
		return fmt.Errorf("db: query %s setting: %w", key, errFind)
	}

	now := time.Now().UTC()
	setting := models.Setting{
		Key:       key,
		Value:     rawValue,
		UpdatedAt: now,
	}
	if errCreate := conn.Create(&setting).Error; errCreate != nil {
		return fmt.Errorf("db: create %s setting: %w", key, errCreate)
	}
	return nil
}

// ensureBoolSetting ensures a boolean setting exists and defaults when empty.
func ensureBoolSetting(conn *gorm.DB, key string, value bool) error {
	payload, errMarshal := json.Marshal(value)
	if errMarshal != nil {
		return fmt.Errorf("db: marshal %s setting: %w", key, errMarshal)
	}
	rawValue := json.RawMessage(payload)

	var existing models.Setting
	if errFind := conn.Where("key = ?", key).First(&existing).Error; errFind == nil {
		trimmed := strings.TrimSpace(string(existing.Value))
		if len(existing.Value) == 0 || trimmed == "" || trimmed == "null" {
			if errUpdate := conn.Model(&existing).Updates(map[string]any{
				"value":      rawValue,
				"updated_at": time.Now().UTC(),
			}).Error; errUpdate != nil {
				return fmt.Errorf("db: update %s setting: %w", key, errUpdate)
			}
		}
		return nil
	} else if !errors.Is(errFind, gorm.ErrRecordNotFound) {
		return fmt.Errorf("db: query %s setting: %w", key, errFind)
	}

	now := time.Now().UTC()
	setting := models.Setting{
		Key:       key,
		Value:     rawValue,
		UpdatedAt: now,
	}
	if errCreate := conn.Create(&setting).Error; errCreate != nil {
		return fmt.Errorf("db: create %s setting: %w", key, errCreate)
	}
	return nil
}

// ensureDefaultAuthGroup ensures a default auth group exists and is marked default.
func ensureDefaultAuthGroup(conn *gorm.DB) error {
	var count int64
	if errCount := conn.Model(&models.AuthGroup{}).Where("is_default = ?", true).Count(&count).Error; errCount != nil {
		return fmt.Errorf("db: check default auth group: %w", errCount)
	}
	if count > 0 {
		return nil
	}

	var existing models.AuthGroup
	if errFind := conn.Where("name = ?", "Default").First(&existing).Error; errFind == nil {
		if errUpdate := conn.Model(&existing).Update("is_default", true).Error; errUpdate != nil {
			return fmt.Errorf("db: set default auth group: %w", errUpdate)
		}
		return nil
	} else if !errors.Is(errFind, gorm.ErrRecordNotFound) {
		return fmt.Errorf("db: query auth group: %w", errFind)
	}

	now := time.Now().UTC()
	group := models.AuthGroup{
		Name:      "Default",
		IsDefault: true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if errCreate := conn.Create(&group).Error; errCreate != nil {
		return fmt.Errorf("db: create default auth group: %w", errCreate)
	}
	return nil
}

// ensureDefaultUserGroup ensures a default user group exists and is marked default.
func ensureDefaultUserGroup(conn *gorm.DB) error {
	var count int64
	if errCount := conn.Model(&models.UserGroup{}).Where("is_default = ?", true).Count(&count).Error; errCount != nil {
		return fmt.Errorf("db: check default user group: %w", errCount)
	}
	if count > 0 {
		return nil
	}

	var existing models.UserGroup
	if errFind := conn.Where("name = ?", "Default").First(&existing).Error; errFind == nil {
		if errUpdate := conn.Model(&existing).Update("is_default", true).Error; errUpdate != nil {
			return fmt.Errorf("db: set default user group: %w", errUpdate)
		}
		return nil
	} else if !errors.Is(errFind, gorm.ErrRecordNotFound) {
		return fmt.Errorf("db: query user group: %w", errFind)
	}

	now := time.Now().UTC()
	group := models.UserGroup{
		Name:      "Default",
		IsDefault: true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if errCreate := conn.Create(&group).Error; errCreate != nil {
		return fmt.Errorf("db: create default user group: %w", errCreate)
	}
	return nil
}

// sqliteTableInfo mirrors PRAGMA table_info output.
type sqliteTableInfo struct {
	Cid          int            `gorm:"column:cid"`        // Column index.
	Name         string         `gorm:"column:name"`       // Column name.
	Type         string         `gorm:"column:type"`       // Column type.
	NotNull      int            `gorm:"column:notnull"`    // Not-null flag.
	DefaultValue sql.NullString `gorm:"column:dflt_value"` // Default value string.
	PK           int            `gorm:"column:pk"`         // Primary key flag.
}

// fixSQLiteTimestampColumns rebuilds tables with incompatible timestamptz types.
func fixSQLiteTimestampColumns(conn *gorm.DB) error {
	if conn == nil {
		return fmt.Errorf("db: nil connection")
	}

	if errDisable := conn.Exec("PRAGMA foreign_keys=OFF").Error; errDisable != nil {
		return fmt.Errorf("db: disable foreign keys: %w", errDisable)
	}
	defer func() {
		_ = conn.Exec("PRAGMA foreign_keys=ON").Error
	}()

	modelsToCheck := []any{
		&models.Admin{},
		&models.Plan{},
		&models.UserGroup{},
		&models.AuthGroup{},
		&models.User{},
		&models.Auth{},
		&models.Quota{},
		&models.APIKey{},
		&models.Usage{},
		&models.Bill{},
		&models.BillingRule{},
		&models.ModelMapping{},
		&models.ModelPayloadRule{},
		&models.ProviderAPIKey{},
		&models.PrepaidCard{},
		&models.Setting{},
	}

	for _, model := range modelsToCheck {
		if errFix := rebuildSQLiteTableIfNeeded(conn, model); errFix != nil {
			return errFix
		}
	}

	return nil
}

// rebuildSQLiteTableIfNeeded recreates a SQLite table when legacy types are detected.
func rebuildSQLiteTableIfNeeded(conn *gorm.DB, model any) error {
	tableName, err := tableNameForModel(conn, model)
	if err != nil {
		return err
	}
	migrator := conn.Migrator()
	if migrator == nil || !migrator.HasTable(tableName) {
		return nil
	}

	var info []sqliteTableInfo
	pragmaSQL := fmt.Sprintf("PRAGMA table_info(%s)", quoteSQLiteIdentifier(tableName))
	if errQuery := conn.Raw(pragmaSQL).Scan(&info).Error; errQuery != nil {
		return fmt.Errorf("db: read sqlite table info %s: %w", tableName, errQuery)
	}

	needsRebuild := false
	oldColumns := make([]string, 0, len(info))
	for _, col := range info {
		if col.Name == "" {
			continue
		}
		oldColumns = append(oldColumns, col.Name)
		if strings.Contains(strings.ToLower(col.Type), "timestamptz") {
			needsRebuild = true
		}
	}
	if !needsRebuild {
		return nil
	}

	legacyName := uniqueSQLiteLegacyName(migrator, tableName)
	if errRename := migrator.RenameTable(tableName, legacyName); errRename != nil {
		return fmt.Errorf("db: rename sqlite table %s: %w", tableName, errRename)
	}

	if errCreate := conn.Table(tableName).AutoMigrate(model); errCreate != nil {
		return fmt.Errorf("db: recreate sqlite table %s: %w", tableName, errCreate)
	}

	newColumns := map[string]struct{}{}
	if colTypes, errCols := migrator.ColumnTypes(tableName); errCols == nil {
		for _, col := range colTypes {
			if col == nil {
				continue
			}
			if name := col.Name(); name != "" {
				newColumns[name] = struct{}{}
			}
		}
	}

	insertColumns := make([]string, 0, len(oldColumns))
	for _, col := range oldColumns {
		if _, ok := newColumns[col]; ok {
			insertColumns = append(insertColumns, col)
		}
	}
	if len(insertColumns) == 0 {
		if errDrop := migrator.DropTable(legacyName); errDrop != nil {
			return fmt.Errorf("db: drop sqlite legacy table %s: %w", legacyName, errDrop)
		}
		return nil
	}

	quotedColumns := make([]string, 0, len(insertColumns))
	for _, col := range insertColumns {
		quotedColumns = append(quotedColumns, quoteSQLiteIdentifier(col))
	}
	columnList := strings.Join(quotedColumns, ", ")
	copySQL := fmt.Sprintf(
		"INSERT INTO %s (%s) SELECT %s FROM %s",
		quoteSQLiteIdentifier(tableName),
		columnList,
		columnList,
		quoteSQLiteIdentifier(legacyName),
	)
	if errCopy := conn.Exec(copySQL).Error; errCopy != nil {
		return fmt.Errorf("db: copy sqlite data for %s: %w", tableName, errCopy)
	}
	if errDrop := migrator.DropTable(legacyName); errDrop != nil {
		return fmt.Errorf("db: drop sqlite legacy table %s: %w", legacyName, errDrop)
	}

	return nil
}

// tableNameForModel resolves the table name for the provided model.
func tableNameForModel(conn *gorm.DB, model any) (string, error) {
	stmt := &gorm.Statement{DB: conn}
	if err := stmt.Parse(model); err != nil {
		return "", fmt.Errorf("db: parse model: %w", err)
	}
	if stmt.Schema == nil || stmt.Schema.Table == "" {
		return "", fmt.Errorf("db: resolve table name")
	}
	return stmt.Schema.Table, nil
}

// uniqueSQLiteLegacyName builds a non-conflicting legacy table name.
func uniqueSQLiteLegacyName(migrator gorm.Migrator, tableName string) string {
	base := tableName + "_legacy_tz"
	if !migrator.HasTable(base) {
		return base
	}
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s_%d", base, i)
		if !migrator.HasTable(candidate) {
			return candidate
		}
	}
}

// quoteSQLiteIdentifier quotes a SQLite identifier safely.
func quoteSQLiteIdentifier(name string) string {
	if name == "" {
		return "\"\""
	}
	return "\"" + strings.ReplaceAll(name, "\"", "\"\"") + "\""
}
