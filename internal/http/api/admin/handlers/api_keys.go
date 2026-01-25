package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/security"
	"gorm.io/gorm"
)

// APIKeyHandler manages admin API key endpoints.
type APIKeyHandler struct {
	db *gorm.DB
}

// NewAPIKeyHandler constructs an APIKeyHandler.
func NewAPIKeyHandler(db *gorm.DB) *APIKeyHandler {
	return &APIKeyHandler{db: db}
}

// Create issues a new API key.
func (h *APIKeyHandler) Create(c *gin.Context) {
	// body holds the create request payload.
	var body struct {
		Name  string `json:"name"`
		Admin bool   `json:"admin"`
	}
	if errBindJSON := c.ShouldBindJSON(&body); errBindJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing name"})
		return
	}
	token, errGenerate := security.GenerateAPIKey()
	if errGenerate != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "generate api key failed"})
		return
	}
	now := time.Now().UTC()
	row := models.APIKey{
		Name:      name,
		APIKey:    token,
		IsAdmin:   body.Admin,
		Active:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if errCreate := h.db.WithContext(c.Request.Context()).Create(&row).Error; errCreate != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create api key failed"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":    row.ID,
		"name":  row.Name,
		"admin": row.IsAdmin,
		"token": token,
	})
}

// CreateForUser creates an API key for a specific user.
func (h *APIKeyHandler) CreateForUser(c *gin.Context) {
	userID, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing name"})
		return
	}

	token, errGenerate := security.GenerateAPIKey()
	if errGenerate != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "generate api key failed"})
		return
	}

	now := time.Now().UTC()
	row := models.APIKey{
		UserID:    &userID,
		Name:      name,
		APIKey:    token,
		IsAdmin:   false,
		Active:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if errCreate := h.db.WithContext(c.Request.Context()).Create(&row).Error; errCreate != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create api key failed"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":    row.ID,
		"name":  row.Name,
		"token": token,
	})
}

// ListByUser returns API keys for a specific user.
func (h *APIKeyHandler) ListByUser(c *gin.Context) {
	userID, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	var rows []models.APIKey
	if errFind := h.db.WithContext(c.Request.Context()).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&rows).Error; errFind != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list api keys failed"})
		return
	}

	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		prefix := ""
		if len(row.APIKey) >= 8 {
			prefix = row.APIKey[:8] + "········" + row.APIKey[len(row.APIKey)-4:]
		}
		out = append(out, gin.H{
			"id":           row.ID,
			"name":         row.Name,
			"key":          row.APIKey,
			"key_prefix":   prefix,
			"active":       row.Active,
			"expires_at":   row.ExpiresAt,
			"revoked_at":   row.RevokedAt,
			"last_used_at": row.LastUsedAt,
			"created_at":   row.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"api_keys": out})
}

// List returns all API keys.
func (h *APIKeyHandler) List(c *gin.Context) {
	var rows []models.APIKey
	if errFind := h.db.WithContext(c.Request.Context()).Order("created_at DESC").Find(&rows).Error; errFind != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list api keys failed"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"id":           row.ID,
			"name":         row.Name,
			"admin":        row.IsAdmin,
			"active":       row.Active,
			"revoked_at":   row.RevokedAt,
			"last_used_at": row.LastUsedAt,
			"created_at":   row.CreatedAt,
			"updated_at":   row.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"api_keys": out})
}

// Revoke revokes an API key by ID.
func (h *APIKeyHandler) Revoke(c *gin.Context) {
	id, errParseUint := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParseUint != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	now := time.Now().UTC()
	res := h.db.WithContext(c.Request.Context()).Model(&models.APIKey{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Updates(map[string]any{
			"active":     false,
			"revoked_at": &now,
			"updated_at": now,
		})
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "revoke failed"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.Status(http.StatusNoContent)
}
