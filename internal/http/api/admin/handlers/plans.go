package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// PlanHandler manages admin CRUD endpoints for plans.
type PlanHandler struct {
	db *gorm.DB // Database handle for plan records.
}

// NewPlanHandler constructs a plan handler.
func NewPlanHandler(db *gorm.DB) *PlanHandler {
	return &PlanHandler{db: db}
}

// planSupportModel defines a model entry in the support_models payload.
type planSupportModel struct {
	Provider string `json:"provider"` // Provider identifier.
	Name     string `json:"name"`     // Model name.
}

// normalizePlanSupportModels validates and normalizes the support_models JSON payload.
func normalizePlanSupportModels(raw json.RawMessage) (datatypes.JSON, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return datatypes.JSON([]byte("[]")), nil
	}

	var objectModels []planSupportModel
	if errUnmarshal := json.Unmarshal(raw, &objectModels); errUnmarshal == nil {
		cleaned := make([]planSupportModel, 0, len(objectModels))
		for _, model := range objectModels {
			name := strings.TrimSpace(model.Name)
			if name == "" {
				continue
			}
			cleaned = append(cleaned, planSupportModel{
				Provider: strings.TrimSpace(model.Provider),
				Name:     name,
			})
		}
		rawSupportModels, errMarshal := json.Marshal(cleaned)
		if errMarshal != nil {
			return nil, errMarshal
		}
		return datatypes.JSON(rawSupportModels), nil
	}

	var stringModels []string
	if errUnmarshal := json.Unmarshal(raw, &stringModels); errUnmarshal == nil {
		cleaned := make([]planSupportModel, 0, len(stringModels))
		for _, modelName := range stringModels {
			name := strings.TrimSpace(modelName)
			if name == "" {
				continue
			}
			cleaned = append(cleaned, planSupportModel{Name: name})
		}
		rawSupportModels, errMarshal := json.Marshal(cleaned)
		if errMarshal != nil {
			return nil, errMarshal
		}
		return datatypes.JSON(rawSupportModels), nil
	}

	return nil, errors.New("invalid support_models")
}

// createPlanRequest captures the payload for creating a plan.
type createPlanRequest struct {
	Name          string          `json:"name"`           // Plan name.
	MonthPrice    float64         `json:"month_price"`    // Monthly price.
	Description   string          `json:"description"`    // Plan description.
	SupportModels json.RawMessage `json:"support_models"` // Supported models payload.
	Feature1      string          `json:"feature1"`       // Feature line 1.
	Feature2      string          `json:"feature2"`       // Feature line 2.
	Feature3      string          `json:"feature3"`       // Feature line 3.
	Feature4      string          `json:"feature4"`       // Feature line 4.
	SortOrder     int             `json:"sort_order"`     // Display order.
	TotalQuota    float64         `json:"total_quota"`    // Total quota value.
	DailyQuota    float64         `json:"daily_quota"`    // Daily quota value.
	RateLimit     int             `json:"rate_limit"`     // Rate limit per second.
	IsEnabled     *bool           `json:"is_enabled"`     // Optional active flag.
}

// Create validates input and inserts a new plan.
func (h *PlanHandler) Create(c *gin.Context) {
	var body createPlanRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	if strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	isEnabled := true
	if body.IsEnabled != nil {
		isEnabled = *body.IsEnabled
	}

	supportModels, errSupportModels := normalizePlanSupportModels(body.SupportModels)
	if errSupportModels != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid support_models"})
		return
	}

	now := time.Now().UTC()
	plan := models.Plan{
		Name:          strings.TrimSpace(body.Name),
		MonthPrice:    body.MonthPrice,
		Description:   body.Description,
		SupportModels: supportModels,
		Feature1:      body.Feature1,
		Feature2:      body.Feature2,
		Feature3:      body.Feature3,
		Feature4:      body.Feature4,
		SortOrder:     body.SortOrder,
		TotalQuota:    body.TotalQuota,
		DailyQuota:    body.DailyQuota,
		RateLimit:     body.RateLimit,
		IsEnabled:     isEnabled,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if errCreate := h.db.WithContext(c.Request.Context()).Create(&plan).Error; errCreate != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create plan failed"})
		return
	}
	c.JSON(http.StatusCreated, h.formatPlan(&plan))
}

// List returns all plans, optionally filtered by enabled flag.
func (h *PlanHandler) List(c *gin.Context) {
	enabledQ := strings.TrimSpace(c.Query("is_enabled"))

	q := h.db.WithContext(c.Request.Context()).Model(&models.Plan{})
	if enabledQ != "" {
		if enabledQ == "true" || enabledQ == "1" {
			q = q.Where("is_enabled = ?", true)
		} else if enabledQ == "false" || enabledQ == "0" {
			q = q.Where("is_enabled = ?", false)
		}
	}

	var rows []models.Plan
	if errFind := q.Order("sort_order ASC, created_at DESC").Find(&rows).Error; errFind != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list plans failed"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, h.formatPlan(&row))
	}
	c.JSON(http.StatusOK, gin.H{"plans": out})
}

// Get fetches a plan by ID.
func (h *PlanHandler) Get(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var plan models.Plan
	if errFind := h.db.WithContext(c.Request.Context()).First(&plan, id).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	c.JSON(http.StatusOK, h.formatPlan(&plan))
}

// updatePlanRequest captures optional fields for plan updates.
type updatePlanRequest struct {
	Name          *string          `json:"name"`           // Optional name update.
	MonthPrice    *float64         `json:"month_price"`    // Optional monthly price.
	Description   *string          `json:"description"`    // Optional description.
	SupportModels *json.RawMessage `json:"support_models"` // Optional supported models payload.
	Feature1      *string          `json:"feature1"`       // Optional feature line 1.
	Feature2      *string          `json:"feature2"`       // Optional feature line 2.
	Feature3      *string          `json:"feature3"`       // Optional feature line 3.
	Feature4      *string          `json:"feature4"`       // Optional feature line 4.
	SortOrder     *int             `json:"sort_order"`     // Optional display order.
	TotalQuota    *float64         `json:"total_quota"`    // Optional total quota.
	DailyQuota    *float64         `json:"daily_quota"`    // Optional daily quota.
	RateLimit     *int             `json:"rate_limit"`     // Optional rate limit per second.
	IsEnabled     *bool            `json:"is_enabled"`     // Optional active flag.
}

// Update validates and applies plan field updates.
func (h *PlanHandler) Update(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body updatePlanRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	var existing models.Plan
	if errFind := h.db.WithContext(c.Request.Context()).First(&existing, id).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}

	updates := map[string]any{
		"updated_at": time.Now().UTC(),
	}

	if body.Name != nil {
		n := strings.TrimSpace(*body.Name)
		if n == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name cannot be empty"})
			return
		}
		updates["name"] = n
	}
	if body.MonthPrice != nil {
		updates["month_price"] = *body.MonthPrice
	}
	if body.Description != nil {
		updates["description"] = *body.Description
	}
	if body.SupportModels != nil {
		supportModels, errSupportModels := normalizePlanSupportModels(*body.SupportModels)
		if errSupportModels != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid support_models"})
			return
		}
		updates["support_models"] = supportModels
	}
	if body.Feature1 != nil {
		updates["feature1"] = *body.Feature1
	}
	if body.Feature2 != nil {
		updates["feature2"] = *body.Feature2
	}
	if body.Feature3 != nil {
		updates["feature3"] = *body.Feature3
	}
	if body.Feature4 != nil {
		updates["feature4"] = *body.Feature4
	}
	if body.SortOrder != nil {
		updates["sort_order"] = *body.SortOrder
	}
	if body.TotalQuota != nil {
		updates["total_quota"] = *body.TotalQuota
	}
	if body.DailyQuota != nil {
		updates["daily_quota"] = *body.DailyQuota
	}
	if body.RateLimit != nil {
		updates["rate_limit"] = *body.RateLimit
	}
	if body.IsEnabled != nil {
		updates["is_enabled"] = *body.IsEnabled
	}

	res := h.db.WithContext(c.Request.Context()).Model(&models.Plan{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Delete removes a plan by ID.
func (h *PlanHandler) Delete(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	res := h.db.WithContext(c.Request.Context()).Delete(&models.Plan{}, id)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete failed"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.Status(http.StatusNoContent)
}

// Enable marks a plan as enabled.
func (h *PlanHandler) Enable(c *gin.Context) {
	h.setEnabled(c, true)
}

// Disable marks a plan as disabled.
func (h *PlanHandler) Disable(c *gin.Context) {
	h.setEnabled(c, false)
}

// setEnabled toggles the enabled state for a plan.
func (h *PlanHandler) setEnabled(c *gin.Context, enabled bool) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	now := time.Now().UTC()
	res := h.db.WithContext(c.Request.Context()).Model(&models.Plan{}).Where("id = ?", id).
		Updates(map[string]any{"is_enabled": enabled, "updated_at": now})
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// formatPlan converts a plan model into a response payload.
func (h *PlanHandler) formatPlan(p *models.Plan) gin.H {
	return gin.H{
		"id":             p.ID,
		"name":           p.Name,
		"month_price":    p.MonthPrice,
		"description":    p.Description,
		"support_models": p.SupportModels,
		"feature1":       p.Feature1,
		"feature2":       p.Feature2,
		"feature3":       p.Feature3,
		"feature4":       p.Feature4,
		"sort_order":     p.SortOrder,
		"total_quota":    p.TotalQuota,
		"daily_quota":    p.DailyQuota,
		"rate_limit":     p.RateLimit,
		"is_enabled":     p.IsEnabled,
		"created_at":     p.CreatedAt,
		"updated_at":     p.UpdatedAt,
	}
}
