package modelreference

import (
	"context"
	"fmt"
	"time"

	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// StoreReferences upserts model references and prunes stale rows.
func StoreReferences(ctx context.Context, db *gorm.DB, refs []models.ModelReference, syncTime time.Time) error {
	if db == nil {
		return fmt.Errorf("store model references: nil db")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if syncTime.IsZero() {
		syncTime = time.Now().UTC()
	}
	syncTime = syncTime.UTC()

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(refs) > 0 {
			for i := range refs {
				refs[i].LastSeenAt = syncTime
				refs[i].UpdatedAt = syncTime
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "provider_name"}, {Name: "model_name"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"context_limit",
					"output_limit",
					"input_price",
					"output_price",
					"cache_read_price",
					"cache_write_price",
					"context_over_200k_input_price",
					"context_over_200k_output_price",
					"context_over_200k_cache_read_price",
					"context_over_200k_cache_write_price",
					"extra",
					"last_seen_at",
					"updated_at",
				}),
			}).Create(&refs).Error; err != nil {
				return fmt.Errorf("store model references: upsert: %w", err)
			}
		}

		if len(refs) == 0 {
			return nil
		}

		if err := tx.Where("last_seen_at < ?", syncTime).Delete(&models.ModelReference{}).Error; err != nil {
			return fmt.Errorf("store model references: prune: %w", err)
		}

		return nil
	})
}
