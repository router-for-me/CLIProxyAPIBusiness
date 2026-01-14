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
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/security"
	"gorm.io/gorm"
)

// UserHandler manages user account endpoints.
type UserHandler struct {
	db *gorm.DB
}

// NewUserHandler constructs a UserHandler.
func NewUserHandler(db *gorm.DB) *UserHandler {
	return &UserHandler{db: db}
}

// createUserRequest defines the request body for user creation.
type createUserRequest struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	RateLimit int    `json:"rate_limit"`
}

// Create creates a new user account.
func (h *UserHandler) Create(c *gin.Context) {
	var body createUserRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing username"})
		return
	}
	password := strings.TrimSpace(body.Password)
	if password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing password"})
		return
	}

	hash, errHash := security.HashPassword(password)
	if errHash != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash password failed"})
		return
	}

	now := time.Now().UTC()
	user := models.User{
		Username:  username,
		Email:     strings.TrimSpace(body.Email),
		Password:  hash,
		RateLimit: body.RateLimit,
		Active:    true,
		Disabled:  false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if errCreate := h.db.WithContext(c.Request.Context()).Create(&user).Error; errCreate != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create user failed"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":         user.ID,
		"username":   user.Username,
		"email":      user.Email,
		"rate_limit": user.RateLimit,
	})
}

// List returns users with optional filters.
func (h *UserHandler) List(c *gin.Context) {
	var (
		usernameQ = strings.TrimSpace(c.Query("username"))
		idQ       = strings.TrimSpace(c.Query("id"))
		emailQ    = strings.TrimSpace(c.Query("email"))
		searchQ   = strings.TrimSpace(c.Query("search"))
	)

	q := h.db.WithContext(c.Request.Context()).Model(&models.User{})
	if usernameQ != "" {
		pattern := dbutil.NormalizeLikePattern(h.db, "%"+usernameQ+"%")
		q = q.Where(dbutil.CaseInsensitiveLikeExpr(h.db, "username"), pattern)
	}
	if idQ != "" {
		if id, errParse := strconv.ParseUint(idQ, 10, 64); errParse == nil {
			q = q.Where("id = ?", id)
		}
	}
	if emailQ != "" {
		pattern := dbutil.NormalizeLikePattern(h.db, "%"+emailQ+"%")
		q = q.Where(dbutil.CaseInsensitiveLikeExpr(h.db, "email"), pattern)
	}
	if searchQ != "" {
		searchPattern := "%" + searchQ + "%"
		ciPattern := dbutil.NormalizeLikePattern(h.db, searchPattern)
		q = q.Where(
			dbutil.CaseInsensitiveLikeExpr(h.db, "username")+" OR "+
				dbutil.CaseInsensitiveLikeExpr(h.db, "email")+" OR CAST(id AS TEXT) LIKE ?",
			ciPattern,
			ciPattern,
			searchPattern,
		)
	}

	var rows []models.User
	if errFind := q.Order("created_at DESC").Find(&rows).Error; errFind != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list users failed"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"id":              row.ID,
			"username":        row.Username,
			"email":           row.Email,
			"user_group_id":   row.UserGroupID,
			"daily_max_usage": row.DailyMaxUsage,
			"rate_limit":      row.RateLimit,
			"active":          row.Active,
			"disabled":        row.Disabled,
			"created_at":      row.CreatedAt,
			"updated_at":      row.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"users": out})
}

// Get returns a user by ID.
func (h *UserHandler) Get(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var user models.User
	if errFind := h.db.WithContext(c.Request.Context()).First(&user, id).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":              user.ID,
		"username":        user.Username,
		"email":           user.Email,
		"user_group_id":   user.UserGroupID,
		"daily_max_usage": user.DailyMaxUsage,
		"rate_limit":      user.RateLimit,
		"active":          user.Active,
		"disabled":        user.Disabled,
		"created_at":      user.CreatedAt,
		"updated_at":      user.UpdatedAt,
	})
}

// updateUserRequest defines the request body for user updates.
type updateUserRequest struct {
	Username      *string  `json:"username"`
	Email         *string  `json:"email"`
	UserGroupID   *uint64  `json:"user_group_id"`
	DailyMaxUsage *float64 `json:"daily_max_usage"`
	RateLimit     *int     `json:"rate_limit"`
	Disabled      *bool    `json:"disabled"`
}

// Update modifies a user account.
func (h *UserHandler) Update(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body updateUserRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	updates := map[string]any{"updated_at": time.Now().UTC()}
	if body.Username != nil {
		username := strings.TrimSpace(*body.Username)
		if username != "" {
			updates["username"] = username
		}
	}
	if body.Email != nil {
		updates["email"] = strings.TrimSpace(*body.Email)
	}
	if body.UserGroupID != nil {
		if *body.UserGroupID == 0 {
			updates["user_group_id"] = nil
		} else {
			updates["user_group_id"] = *body.UserGroupID
		}
	}
	if body.DailyMaxUsage != nil {
		updates["daily_max_usage"] = *body.DailyMaxUsage
	}
	if body.RateLimit != nil {
		updates["rate_limit"] = *body.RateLimit
	}
	if body.Disabled != nil {
		updates["disabled"] = *body.Disabled
	}

	res := h.db.WithContext(c.Request.Context()).Model(&models.User{}).Where("id = ?", id).Updates(updates)
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

// Delete removes a user account and its API keys.
func (h *UserHandler) Delete(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	ctx := c.Request.Context()

	var user models.User
	if errFind := h.db.WithContext(ctx).First(&user, id).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}

	errTx := h.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if errDelKeys := tx.Where("user_id = ?", id).Delete(&models.APIKey{}).Error; errDelKeys != nil {
			return errDelKeys
		}
		if errDelUser := tx.Delete(&models.User{}, id).Error; errDelUser != nil {
			return errDelUser
		}
		return nil
	})
	if errTx != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete failed"})
		return
	}

	c.Status(http.StatusNoContent)
}

// Disable deactivates a user account.
func (h *UserHandler) Disable(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	res := h.db.WithContext(c.Request.Context()).Model(&models.User{}).
		Where("id = ?", id).
		Updates(map[string]any{"disabled": true, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "disable failed"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Enable reactivates a user account.
func (h *UserHandler) Enable(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	res := h.db.WithContext(c.Request.Context()).Model(&models.User{}).
		Where("id = ?", id).
		Updates(map[string]any{"disabled": false, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "enable failed"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// changePasswordRequest defines the request body for password changes.
type changePasswordRequest struct {
	Password string `json:"password"`
}

// ChangePassword updates a user's password.
func (h *UserHandler) ChangePassword(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body changePasswordRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	password := strings.TrimSpace(body.Password)
	if password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing password"})
		return
	}
	hash, errHash := security.HashPassword(password)
	if errHash != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash password failed"})
		return
	}
	res := h.db.WithContext(c.Request.Context()).Model(&models.User{}).
		Where("id = ?", id).
		Updates(map[string]any{"password": hash, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "change password failed"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
