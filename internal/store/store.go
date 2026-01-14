package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GormAuthStore persists CLIProxyAPI auth JSON blobs to PostgreSQL via GORM.
type GormAuthStore struct {
	db *gorm.DB

	mu      sync.Mutex
	dirLock sync.RWMutex
}

// NewGormAuthStore constructs a GormAuthStore.
func NewGormAuthStore(db *gorm.DB) *GormAuthStore {
	return &GormAuthStore{db: db}
}

// Save upserts an auth record into the database.
func (s *GormAuthStore) Save(ctx context.Context, auth *cliproxyauth.Auth) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("gorm auth store: not initialized")
	}
	if auth == nil {
		return "", fmt.Errorf("gorm auth store: auth is nil")
	}

	id := strings.TrimSpace(auth.ID)
	if id == "" {
		return "", fmt.Errorf("gorm auth store: missing id")
	}

	if auth.Disabled {
		return "", nil
	}

	provider := strings.TrimSpace(auth.Provider)
	if provider == "" {
		provider = inferProviderFromID(id)
	}

	var payload []byte
	var errMarshal error

	switch {
	case auth.Storage != nil:
		payload, errMarshal = json.Marshal(auth.Storage)
		if errMarshal != nil {
			return "", fmt.Errorf("gorm auth store: marshal storage failed: %w", errMarshal)
		}
	case auth.Metadata != nil:
		payload, errMarshal = json.Marshal(auth.Metadata)
		if errMarshal != nil {
			return "", fmt.Errorf("gorm auth store: marshal metadata failed: %w", errMarshal)
		}
	default:
		return "", fmt.Errorf("gorm auth store: nothing to persist for %s", auth.ID)
	}

	if len(payload) == 0 {
		return "", nil
	}

	payload = ensureJSONType(payload, provider)

	s.mu.Lock()
	defer s.mu.Unlock()

	var existing models.Auth
	errFind := s.db.WithContext(ctx).Where("key = ?", id).First(&existing).Error
	if errFind == nil {
		if jsonEqual(existing.Content, payload) {
			return id, nil
		}
	}

	now := time.Now().UTC()
	record := models.Auth{
		Key:       id,
		Content:   datatypes.JSON(payload),
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"content", "updated_at"}),
	}).Create(&record).Error; err != nil {
		return "", fmt.Errorf("gorm auth store: upsert: %w", err)
	}

	return id, nil
}

// List loads auth records from the database and converts them to SDK auths.
func (s *GormAuthStore) List(ctx context.Context) ([]*cliproxyauth.Auth, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("gorm auth store: not initialized")
	}

	var rows []models.Auth
	if errFind := s.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; errFind != nil {
		return nil, fmt.Errorf("gorm auth store: list: %w", errFind)
	}

	auths := make([]*cliproxyauth.Auth, 0, len(rows))
	for _, row := range rows {
		if len(row.Content) == 0 {
			continue
		}
		metadata := make(map[string]any)
		if errUnmarshal := json.Unmarshal(row.Content, &metadata); errUnmarshal != nil {
			continue
		}
		provider, _ := metadata["type"].(string)
		if provider == "" {
			provider = "unknown"
		}
		attr := map[string]string{}
		if email, ok := metadata["email"].(string); ok && strings.TrimSpace(email) != "" {
			attr["email"] = strings.TrimSpace(email)
		}
		if row.Priority != 0 {
			attr["priority"] = strconv.Itoa(row.Priority)
		}
		auths = append(auths, &cliproxyauth.Auth{
			ID:               row.Key,
			Provider:         provider,
			FileName:         row.Key,
			ProxyURL:         row.ProxyURL,
			Label:            labelFor(metadata),
			Status:           cliproxyauth.StatusActive,
			Attributes:       attr,
			Metadata:         metadata,
			CreatedAt:        row.CreatedAt,
			UpdatedAt:        row.UpdatedAt,
			LastRefreshedAt:  time.Time{},
			NextRefreshAfter: time.Time{},
		})
	}
	return auths, nil
}

// Delete removes an auth record by ID.
func (s *GormAuthStore) Delete(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("gorm auth store: not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("gorm auth store: id is empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if errDelete := s.db.WithContext(ctx).Where("key = ?", id).Delete(&models.Auth{}).Error; errDelete != nil {
		return fmt.Errorf("gorm auth store: delete db row: %w", errDelete)
	}
	return nil
}

// labelFor returns a display label for the auth metadata.
func labelFor(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	if v, ok := metadata["label"].(string); ok && v != "" {
		return v
	}
	if v, ok := metadata["email"].(string); ok && v != "" {
		return v
	}
	if project, ok := metadata["project_id"].(string); ok && project != "" {
		return project
	}
	return ""
}

// jsonEqual compares two JSON objects for deep equality.
func jsonEqual(a, b []byte) bool {
	var objA, objB map[string]any
	if errA := json.Unmarshal(a, &objA); errA != nil {
		return false
	}
	if errB := json.Unmarshal(b, &objB); errB != nil {
		return false
	}
	if len(objA) != len(objB) {
		return false
	}
	for k, va := range objA {
		vb, ok := objB[k]
		if !ok {
			return false
		}
		jsonA, _ := json.Marshal(va)
		jsonB, _ := json.Marshal(vb)
		if string(jsonA) != string(jsonB) {
			return false
		}
	}
	return true
}

// ensureJSONType injects the provider type into JSON metadata when missing.
func ensureJSONType(payload []byte, provider string) []byte {
	provider = strings.TrimSpace(provider)
	if provider == "" || len(payload) == 0 {
		return payload
	}

	var metadata map[string]any
	if errUnmarshal := json.Unmarshal(payload, &metadata); errUnmarshal != nil {
		return payload
	}

	existing, _ := metadata["type"].(string)
	if strings.TrimSpace(existing) != "" {
		return payload
	}

	metadata["type"] = provider
	updated, errMarshal := json.Marshal(metadata)
	if errMarshal != nil || len(updated) == 0 {
		return payload
	}
	return updated
}

// inferProviderFromID derives a provider name from an auth ID prefix.
func inferProviderFromID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	parts := strings.SplitN(id, "-", 2)
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}
