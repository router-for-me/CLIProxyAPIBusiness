package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	internalsettings "github.com/router-for-me/CLIProxyAPIBusiness/internal/settings"
	"gorm.io/gorm"
)

// SettingHandler manages admin CRUD for settings values.
type SettingHandler struct {
	db *gorm.DB // Database handle for settings.
}

// NewSettingHandler constructs a settings handler.
func NewSettingHandler(db *gorm.DB) *SettingHandler {
	return &SettingHandler{db: db}
}

// createSettingRequest captures the payload for creating a setting.
type createSettingRequest struct {
	Key   string          `json:"key"`   // Setting key.
	Value json.RawMessage `json:"value"` // JSON value payload.
}

var positiveIntSettingKeys = map[string]struct{}{
	internalsettings.QuotaPollIntervalSecondsKey: {},
	internalsettings.QuotaPollMaxConcurrencyKey:  {},
}

var nonNegativeIntSettingKeys = map[string]struct{}{
	internalsettings.RateLimitKey:        {},
	internalsettings.RateLimitRedisDBKey: {},
}

var errPositiveIntegerValue = errors.New("value must be a positive integer")
var errNonNegativeIntegerValue = errors.New("value must be a non-negative integer")

// Create validates and inserts a setting, then refreshes the snapshot.
func (h *SettingHandler) Create(c *gin.Context) {
	var body createSettingRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	key := strings.TrimSpace(body.Key)
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	if errValidate := validateSettingValue(key, body.Value); errValidate != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errValidate.Error()})
		return
	}

	var existing models.Setting
	if errFind := h.db.WithContext(c.Request.Context()).Where("key = ?", key).First(&existing).Error; errFind == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "key already exists"})
		return
	}

	setting := models.Setting{
		Key:   key,
		Value: body.Value,
	}

	if errCreate := h.db.WithContext(c.Request.Context()).Create(&setting).Error; errCreate != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create setting failed"})
		return
	}
	if errRefresh := h.refreshDBConfigSnapshot(c.Request.Context()); errRefresh != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "refresh settings snapshot failed"})
		return
	}
	c.JSON(http.StatusCreated, h.formatSetting(&setting))
}

// List returns all settings sorted by key.
func (h *SettingHandler) List(c *gin.Context) {
	var rows []models.Setting
	if errFind := h.db.WithContext(c.Request.Context()).Order("key ASC").Find(&rows).Error; errFind != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list settings failed"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, h.formatSetting(&row))
	}
	c.JSON(http.StatusOK, gin.H{"settings": out})
}

// Get returns a setting by key.
func (h *SettingHandler) Get(c *gin.Context) {
	key := strings.TrimSpace(c.Param("key"))
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key"})
		return
	}
	var setting models.Setting
	if errFind := h.db.WithContext(c.Request.Context()).Where("key = ?", key).First(&setting).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	c.JSON(http.StatusOK, h.formatSetting(&setting))
}

// updateSettingRequest captures the payload for updating a setting.
type updateSettingRequest struct {
	Value json.RawMessage `json:"value"` // New JSON value.
}

// Update updates a setting value and refreshes the snapshot.
func (h *SettingHandler) Update(c *gin.Context) {
	key := strings.TrimSpace(c.Param("key"))
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key"})
		return
	}
	var body updateSettingRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	if errValidate := validateSettingValue(key, body.Value); errValidate != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errValidate.Error()})
		return
	}

	var existing models.Setting
	if errFind := h.db.WithContext(c.Request.Context()).Where("key = ?", key).First(&existing).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}

	res := h.db.WithContext(c.Request.Context()).Model(&models.Setting{}).Where("key = ?", key).
		Update("value", body.Value)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
		return
	}
	if errRefresh := h.refreshDBConfigSnapshot(c.Request.Context()); errRefresh != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "refresh settings snapshot failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Delete removes a setting and refreshes the snapshot.
func (h *SettingHandler) Delete(c *gin.Context) {
	key := strings.TrimSpace(c.Param("key"))
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key"})
		return
	}
	res := h.db.WithContext(c.Request.Context()).Where("key = ?", key).Delete(&models.Setting{})
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete failed"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if errRefresh := h.refreshDBConfigSnapshot(c.Request.Context()); errRefresh != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "refresh settings snapshot failed"})
		return
	}
	c.Status(http.StatusNoContent)
}

// refreshDBConfigSnapshot rebuilds the in-memory settings snapshot from the DB.
func (h *SettingHandler) refreshDBConfigSnapshot(ctx context.Context) error {
	var rows []models.Setting
	if errFind := h.db.WithContext(ctx).
		Select("key", "value", "updated_at").
		Order("key ASC").
		Find(&rows).Error; errFind != nil {
		return errFind
	}

	values := make(map[string]json.RawMessage, len(rows))
	maxUpdatedAt := time.Time{}
	maxUpdatedKey := ""
	for _, row := range rows {
		key := strings.TrimSpace(row.Key)
		if key == "" {
			continue
		}
		values[key] = row.Value
		rowUpdatedAt := row.UpdatedAt.UTC()
		if rowUpdatedAt.After(maxUpdatedAt) || (rowUpdatedAt.Equal(maxUpdatedAt) && key > maxUpdatedKey) {
			maxUpdatedAt = rowUpdatedAt
			maxUpdatedKey = key
		}
	}

	internalsettings.StoreDBConfig(maxUpdatedAt, values)
	return nil
}

func validateSettingValue(key string, value json.RawMessage) error {
	if _, ok := positiveIntSettingKeys[key]; !ok {
		if _, okNonNegative := nonNegativeIntSettingKeys[key]; !okNonNegative {
			return nil
		}
		if _, ok := parseNonNegativeInt(value); !ok {
			return errNonNegativeIntegerValue
		}
		return nil
	}
	if _, ok := parsePositiveInt(value); !ok {
		return errPositiveIntegerValue
	}
	return nil
}

func parsePositiveInt(raw json.RawMessage) (int, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0, false
	}
	var parsedInt int
	if errUnmarshalInt := json.Unmarshal(raw, &parsedInt); errUnmarshalInt == nil {
		return parsedInt, parsedInt > 0
	}
	var parsedString string
	if errUnmarshalString := json.Unmarshal(raw, &parsedString); errUnmarshalString == nil {
		parsed, errParse := strconv.Atoi(strings.TrimSpace(parsedString))
		if errParse != nil {
			return 0, false
		}
		return parsed, parsed > 0
	}
	var parsedFloat float64
	if errUnmarshalFloat := json.Unmarshal(raw, &parsedFloat); errUnmarshalFloat == nil {
		if math.IsNaN(parsedFloat) || math.IsInf(parsedFloat, 0) {
			return 0, false
		}
		if parsedFloat <= 0 || parsedFloat != math.Trunc(parsedFloat) {
			return 0, false
		}
		return int(parsedFloat), true
	}
	return 0, false
}

func parseNonNegativeInt(raw json.RawMessage) (int, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0, false
	}
	var parsedInt int
	if errUnmarshalInt := json.Unmarshal(raw, &parsedInt); errUnmarshalInt == nil {
		return parsedInt, parsedInt >= 0
	}
	var parsedString string
	if errUnmarshalString := json.Unmarshal(raw, &parsedString); errUnmarshalString == nil {
		parsed, errParse := strconv.Atoi(strings.TrimSpace(parsedString))
		if errParse != nil {
			return 0, false
		}
		return parsed, parsed >= 0
	}
	var parsedFloat float64
	if errUnmarshalFloat := json.Unmarshal(raw, &parsedFloat); errUnmarshalFloat == nil {
		if math.IsNaN(parsedFloat) || math.IsInf(parsedFloat, 0) {
			return 0, false
		}
		if parsedFloat < 0 || parsedFloat != math.Trunc(parsedFloat) {
			return 0, false
		}
		return int(parsedFloat), true
	}
	return 0, false
}

// formatSetting formats a setting row into response JSON.
func (h *SettingHandler) formatSetting(s *models.Setting) gin.H {
	return gin.H{
		"key":   s.Key,
		"value": s.Value,
	}
}
