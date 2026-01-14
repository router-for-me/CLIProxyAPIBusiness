package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/gorm"
)

// ModelMappingHandler manages admin CRUD endpoints for model mappings.
type ModelMappingHandler struct {
	db *gorm.DB // Database handle for model mapping records.
}

// NewModelMappingHandler constructs a model mapping handler.
func NewModelMappingHandler(db *gorm.DB) *ModelMappingHandler {
	return &ModelMappingHandler{db: db}
}

// createModelMappingRequest captures the payload for creating a model mapping.
type createModelMappingRequest struct {
	Provider     string `json:"provider"`       // Provider identifier.
	ModelName    string `json:"model_name"`     // Source model name.
	NewModelName string `json:"new_model_name"` // Target model name.
	IsEnabled    *bool  `json:"is_enabled"`     // Optional active flag.
	Fork         *bool  `json:"fork"`           // Optional fork flag.
	Selector     *int   `json:"selector"`       // Optional routing selector.
	RateLimit    *int   `json:"rate_limit"`     // Optional rate limit per second.
}

// Create validates input and inserts a new model mapping.
func (h *ModelMappingHandler) Create(c *gin.Context) {
	var body createModelMappingRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	if strings.TrimSpace(body.Provider) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider is required"})
		return
	}
	if strings.TrimSpace(body.ModelName) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model_name is required"})
		return
	}
	if strings.TrimSpace(body.NewModelName) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new_model_name is required"})
		return
	}

	isEnabled := true
	if body.IsEnabled != nil {
		isEnabled = *body.IsEnabled
	}
	fork := false
	if body.Fork != nil {
		fork = *body.Fork
	}
	selector := 0
	if body.Selector != nil {
		selector = *body.Selector
		if selector < 0 || selector > 2 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "selector must be 0, 1, or 2"})
			return
		}
	}
	rateLimit := 0
	if body.RateLimit != nil {
		rateLimit = *body.RateLimit
	}

	now := time.Now().UTC()
	mapping := models.ModelMapping{
		Provider:     strings.TrimSpace(body.Provider),
		ModelName:    strings.TrimSpace(body.ModelName),
		NewModelName: strings.TrimSpace(body.NewModelName),
		Fork:         fork,
		Selector:     selector,
		RateLimit:    rateLimit,
		IsEnabled:    isEnabled,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if errCreate := h.db.WithContext(c.Request.Context()).Create(&mapping).Error; errCreate != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create model mapping failed"})
		return
	}
	c.JSON(http.StatusCreated, h.formatMapping(&mapping))
}

// List returns model mappings filtered by query parameters.
func (h *ModelMappingHandler) List(c *gin.Context) {
	var (
		providerQ  = strings.TrimSpace(c.Query("provider"))
		modelNameQ = strings.TrimSpace(c.Query("model_name"))
		enabledQ   = strings.TrimSpace(c.Query("is_enabled"))
	)

	q := h.db.WithContext(c.Request.Context()).Model(&models.ModelMapping{})
	if providerQ != "" {
		q = q.Where("provider = ?", providerQ)
	}
	if modelNameQ != "" {
		q = q.Where("model_name = ?", modelNameQ)
	}
	if enabledQ != "" {
		if enabledQ == "true" || enabledQ == "1" {
			q = q.Where("is_enabled = ?", true)
		} else if enabledQ == "false" || enabledQ == "0" {
			q = q.Where("is_enabled = ?", false)
		}
	}

	var rows []models.ModelMapping
	if errFind := q.Order("created_at DESC").Find(&rows).Error; errFind != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list model mappings failed"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, h.formatMapping(&row))
	}
	c.JSON(http.StatusOK, gin.H{"model_mappings": out})
}

// Get fetches a model mapping by ID.
func (h *ModelMappingHandler) Get(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var mapping models.ModelMapping
	if errFind := h.db.WithContext(c.Request.Context()).First(&mapping, id).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	c.JSON(http.StatusOK, h.formatMapping(&mapping))
}

// updateModelMappingRequest captures optional fields for mapping updates.
type updateModelMappingRequest struct {
	Provider     *string `json:"provider"`       // Optional provider.
	ModelName    *string `json:"model_name"`     // Optional source model name.
	NewModelName *string `json:"new_model_name"` // Optional target model name.
	IsEnabled    *bool   `json:"is_enabled"`     // Optional active flag.
	Fork         *bool   `json:"fork"`           // Optional fork flag.
	Selector     *int    `json:"selector"`       // Optional routing selector.
	RateLimit    *int    `json:"rate_limit"`     // Optional rate limit per second.
}

// Update validates and applies model mapping field updates.
func (h *ModelMappingHandler) Update(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body updateModelMappingRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	var existing models.ModelMapping
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

	if body.Provider != nil {
		p := strings.TrimSpace(*body.Provider)
		if p == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "provider cannot be empty"})
			return
		}
		updates["provider"] = p
	}
	if body.ModelName != nil {
		m := strings.TrimSpace(*body.ModelName)
		if m == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "model_name cannot be empty"})
			return
		}
		updates["model_name"] = m
	}
	if body.NewModelName != nil {
		n := strings.TrimSpace(*body.NewModelName)
		if n == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "new_model_name cannot be empty"})
			return
		}
		updates["new_model_name"] = n
	}
	if body.IsEnabled != nil {
		updates["is_enabled"] = *body.IsEnabled
	}
	if body.Fork != nil {
		updates["fork"] = *body.Fork
	}
	if body.Selector != nil {
		selector := *body.Selector
		if selector < 0 || selector > 2 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "selector must be 0, 1, or 2"})
			return
		}
		updates["selector"] = selector
	}
	if body.RateLimit != nil {
		updates["rate_limit"] = *body.RateLimit
	}

	res := h.db.WithContext(c.Request.Context()).Model(&models.ModelMapping{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Delete removes a model mapping by ID.
func (h *ModelMappingHandler) Delete(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	res := h.db.WithContext(c.Request.Context()).Delete(&models.ModelMapping{}, id)
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

// Enable marks a model mapping as enabled.
func (h *ModelMappingHandler) Enable(c *gin.Context) {
	h.setEnabled(c, true)
}

// Disable marks a model mapping as disabled.
func (h *ModelMappingHandler) Disable(c *gin.Context) {
	h.setEnabled(c, false)
}

// setEnabled toggles the enabled state for a model mapping.
func (h *ModelMappingHandler) setEnabled(c *gin.Context, enabled bool) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	now := time.Now().UTC()
	res := h.db.WithContext(c.Request.Context()).Model(&models.ModelMapping{}).Where("id = ?", id).
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

// formatMapping converts a model mapping into a response payload.
func (h *ModelMappingHandler) formatMapping(m *models.ModelMapping) gin.H {
	return gin.H{
		"id":             m.ID,
		"provider":       m.Provider,
		"model_name":     m.ModelName,
		"new_model_name": m.NewModelName,
		"fork":           m.Fork,
		"selector":       m.Selector,
		"rate_limit":     m.RateLimit,
		"is_enabled":     m.IsEnabled,
		"created_at":     m.CreatedAt,
		"updated_at":     m.UpdatedAt,
	}
}

// AvailableModels lists mapped or provider-supported models based on query.
func (h *ModelMappingHandler) AvailableModels(c *gin.Context) {
	provider := strings.ToLower(strings.TrimSpace(c.Query("provider")))
	if provider == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider is required"})
		return
	}

	mappedQ := strings.TrimSpace(c.Query("mapped"))
	if mappedQ == "1" || strings.EqualFold(mappedQ, "true") {
		var result []string
		if errFind := h.db.WithContext(c.Request.Context()).
			Model(&models.ModelMapping{}).
			Distinct("new_model_name").
			Where("LOWER(provider) = ? AND is_enabled = ?", provider, true).
			Where("new_model_name <> ''").
			Order("new_model_name ASC").
			Pluck("new_model_name", &result).Error; errFind != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "list mapped models failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"models": result})
		return
	}
	infos := cliproxy.GlobalModelRegistry().GetAvailableModelsByProvider(provider)
	result := make([]string, 0, len(infos))
	for _, info := range infos {
		if info != nil && info.ID != "" {
			result = append(result, info.ID)
		}
	}
	c.JSON(http.StatusOK, gin.H{"models": result})
}
