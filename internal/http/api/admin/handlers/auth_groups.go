package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	dbutil "github.com/router-for-me/CLIProxyAPIBusiness/internal/db"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/gorm"
)

// AuthGroupHandler manages auth group endpoints.
type AuthGroupHandler struct {
	db *gorm.DB
}

// NewAuthGroupHandler constructs an AuthGroupHandler.
func NewAuthGroupHandler(db *gorm.DB) *AuthGroupHandler {
	return &AuthGroupHandler{db: db}
}

// createAuthGroupRequest defines the request body for auth group creation.
type createAuthGroupRequest struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	RateLimit int    `json:"rate_limit"`
}

// Create creates a new auth group.
func (h *AuthGroupHandler) Create(c *gin.Context) {
	var body createAuthGroupRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing name"})
		return
	}

	now := time.Now().UTC()
	group := models.AuthGroup{
		Name:      name,
		IsDefault: body.IsDefault,
		RateLimit: body.RateLimit,
		CreatedAt: now,
		UpdatedAt: now,
	}

	errTx := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if body.IsDefault {
			if errClear := tx.Model(&models.AuthGroup{}).Where("is_default = ?", true).
				Updates(map[string]any{"is_default": false, "updated_at": now}).Error; errClear != nil {
				return errClear
			}
		}
		return tx.Create(&group).Error
	})
	if errTx != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create auth group failed"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":         group.ID,
		"name":       group.Name,
		"is_default": group.IsDefault,
		"rate_limit": group.RateLimit,
		"created_at": group.CreatedAt,
		"updated_at": group.UpdatedAt,
	})
}

// List returns all auth groups.
func (h *AuthGroupHandler) List(c *gin.Context) {
	var (
		nameQ = strings.TrimSpace(c.Query("name"))
		idQ   = strings.TrimSpace(c.Query("id"))
	)

	q := h.db.WithContext(c.Request.Context()).Model(&models.AuthGroup{})
	if nameQ != "" {
		pattern := dbutil.NormalizeLikePattern(h.db, "%"+nameQ+"%")
		q = q.Where(dbutil.CaseInsensitiveLikeExpr(h.db, "name"), pattern)
	}
	if idQ != "" {
		if id, errParse := strconv.ParseUint(idQ, 10, 64); errParse == nil {
			q = q.Where("id = ?", id)
		}
	}

	var rows []models.AuthGroup
	if errFind := q.Order("created_at DESC").Find(&rows).Error; errFind != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list auth groups failed"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"id":         row.ID,
			"name":       row.Name,
			"is_default": row.IsDefault,
			"rate_limit": row.RateLimit,
			"created_at": row.CreatedAt,
			"updated_at": row.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"auth_groups": out})
}

// Get returns an auth group by ID.
func (h *AuthGroupHandler) Get(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var group models.AuthGroup
	if errFind := h.db.WithContext(c.Request.Context()).First(&group, id).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":         group.ID,
		"name":       group.Name,
		"is_default": group.IsDefault,
		"rate_limit": group.RateLimit,
		"created_at": group.CreatedAt,
		"updated_at": group.UpdatedAt,
	})
}

// updateAuthGroupRequest defines the request body for auth group updates.
type updateAuthGroupRequest struct {
	Name      *string `json:"name"`
	IsDefault *bool   `json:"is_default"`
	RateLimit *int    `json:"rate_limit"`
}

// Update modifies an auth group.
func (h *AuthGroupHandler) Update(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body updateAuthGroupRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	now := time.Now().UTC()
	errTx := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if body.IsDefault != nil && *body.IsDefault {
			if errClear := tx.Model(&models.AuthGroup{}).Where("is_default = ? AND id != ?", true, id).
				Updates(map[string]any{"is_default": false, "updated_at": now}).Error; errClear != nil {
				return errClear
			}
		}

		updates := map[string]any{"updated_at": now}
		if body.Name != nil {
			updates["name"] = strings.TrimSpace(*body.Name)
		}
		if body.IsDefault != nil {
			updates["is_default"] = *body.IsDefault
		}
		if body.RateLimit != nil {
			updates["rate_limit"] = *body.RateLimit
		}

		res := tx.Model(&models.AuthGroup{}).Where("id = ?", id).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if errTx != nil {
		if errors.Is(errTx, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Delete removes an auth group.
func (h *AuthGroupHandler) Delete(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	res := h.db.WithContext(c.Request.Context()).Delete(&models.AuthGroup{}, id)
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

// SetDefault marks an auth group as default.
func (h *AuthGroupHandler) SetDefault(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	now := time.Now().UTC()
	errTx := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if errClear := tx.Model(&models.AuthGroup{}).Where("is_default = ?", true).
			Updates(map[string]any{"is_default": false, "updated_at": now}).Error; errClear != nil {
			return errClear
		}

		res := tx.Model(&models.AuthGroup{}).Where("id = ?", id).
			Updates(map[string]any{"is_default": true, "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if errTx != nil {
		if errors.Is(errTx, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "set default failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
