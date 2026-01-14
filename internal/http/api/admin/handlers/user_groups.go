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

// UserGroupHandler manages user group endpoints.
type UserGroupHandler struct {
	db *gorm.DB
}

// NewUserGroupHandler constructs a UserGroupHandler.
func NewUserGroupHandler(db *gorm.DB) *UserGroupHandler {
	return &UserGroupHandler{db: db}
}

// createUserGroupRequest defines the request body for user group creation.
type createUserGroupRequest struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	RateLimit int    `json:"rate_limit"`
}

// Create creates a new user group.
func (h *UserGroupHandler) Create(c *gin.Context) {
	var body createUserGroupRequest
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
	group := models.UserGroup{
		Name:      name,
		IsDefault: body.IsDefault,
		RateLimit: body.RateLimit,
		CreatedAt: now,
		UpdatedAt: now,
	}

	errTx := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if body.IsDefault {
			if errClear := tx.Model(&models.UserGroup{}).Where("is_default = ?", true).
				Updates(map[string]any{"is_default": false, "updated_at": now}).Error; errClear != nil {
				return errClear
			}
		}
		return tx.Create(&group).Error
	})
	if errTx != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create user group failed"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":         group.ID,
		"name":       group.Name,
		"is_default": group.IsDefault,
		"created_at": group.CreatedAt,
		"updated_at": group.UpdatedAt,
	})
}

// List returns all user groups.
func (h *UserGroupHandler) List(c *gin.Context) {
	var (
		nameQ = strings.TrimSpace(c.Query("name"))
		idQ   = strings.TrimSpace(c.Query("id"))
	)

	q := h.db.WithContext(c.Request.Context()).Model(&models.UserGroup{})
	if nameQ != "" {
		pattern := dbutil.NormalizeLikePattern(h.db, "%"+nameQ+"%")
		q = q.Where(dbutil.CaseInsensitiveLikeExpr(h.db, "name"), pattern)
	}
	if idQ != "" {
		if id, errParse := strconv.ParseUint(idQ, 10, 64); errParse == nil {
			q = q.Where("id = ?", id)
		}
	}

	var rows []models.UserGroup
	if errFind := q.Order("created_at DESC").Find(&rows).Error; errFind != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list user groups failed"})
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
	c.JSON(http.StatusOK, gin.H{"user_groups": out})
}

// Get returns a user group by ID.
func (h *UserGroupHandler) Get(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var group models.UserGroup
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

// updateUserGroupRequest defines the request body for user group updates.
type updateUserGroupRequest struct {
	Name      *string `json:"name"`
	IsDefault *bool   `json:"is_default"`
	RateLimit *int    `json:"rate_limit"`
}

// Update modifies a user group.
func (h *UserGroupHandler) Update(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body updateUserGroupRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	now := time.Now().UTC()
	errTx := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if body.IsDefault != nil && *body.IsDefault {
			if errClear := tx.Model(&models.UserGroup{}).Where("is_default = ? AND id != ?", true, id).
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

		res := tx.Model(&models.UserGroup{}).Where("id = ?", id).Updates(updates)
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

// Delete removes a user group.
func (h *UserGroupHandler) Delete(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	res := h.db.WithContext(c.Request.Context()).Delete(&models.UserGroup{}, id)
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

// SetDefault marks a user group as default.
func (h *UserGroupHandler) SetDefault(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	now := time.Now().UTC()
	errTx := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if errClear := tx.Model(&models.UserGroup{}).Where("is_default = ?", true).
			Updates(map[string]any{"is_default": false, "updated_at": now}).Error; errClear != nil {
			return errClear
		}

		res := tx.Model(&models.UserGroup{}).Where("id = ?", id).
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
