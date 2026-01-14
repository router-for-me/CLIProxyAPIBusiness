package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	dbutil "github.com/router-for-me/CLIProxyAPIBusiness/internal/db"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AuthFileHandler manages auth file endpoints.
type AuthFileHandler struct {
	db *gorm.DB
}

// NewAuthFileHandler constructs an AuthFileHandler.
func NewAuthFileHandler(db *gorm.DB) *AuthFileHandler {
	return &AuthFileHandler{db: db}
}

// createAuthFileRequest defines the request body for auth file creation.
type createAuthFileRequest struct {
	Key         string              `json:"key"`
	AuthGroupID models.AuthGroupIDs `json:"auth_group_id"`
	ProxyURL    *string             `json:"proxy_url"`
	Content     map[string]any      `json:"content"`
	IsAvailable *bool               `json:"is_available"`
	RateLimit   int                 `json:"rate_limit"`
	Priority    int                 `json:"priority"`
}

type importAuthFilesFailure struct {
	File  string `json:"file"`
	Error string `json:"error"`
}

type importAuthFilesResponse struct {
	Imported int                      `json:"imported"`
	Failed   []importAuthFilesFailure `json:"failed"`
}

// Create creates a new auth file entry.
func (h *AuthFileHandler) Create(c *gin.Context) {
	var body createAuthFileRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	key := strings.TrimSpace(body.Key)
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing key"})
		return
	}

	isAvailable := true
	if body.IsAvailable != nil {
		isAvailable = *body.IsAvailable
	}

	proxyURL := ""
	if body.ProxyURL != nil {
		proxyURL = strings.TrimSpace(*body.ProxyURL)
	}
	if proxyURL == "" && autoAssignProxyEnabled() {
		assignedProxyURL, errAssignProxy := pickRandomProxyURL(c.Request.Context(), h.db)
		if errAssignProxy != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "auto assign proxy failed"})
			return
		}
		if assignedProxyURL != "" {
			proxyURL = assignedProxyURL
		}
	}

	contentJSON := datatypes.JSON("{}")
	if body.Content != nil {
		contentBytes, errMarshal := json.Marshal(body.Content)
		if errMarshal == nil {
			contentJSON = datatypes.JSON(contentBytes)
		}
	}

	now := time.Now().UTC()
	authGroupIDs := body.AuthGroupID.Clean()
	if body.AuthGroupID == nil {
		var defaultGroup models.AuthGroup
		if errFind := h.db.WithContext(c.Request.Context()).
			Where("is_default = ?", true).
			First(&defaultGroup).Error; errFind == nil {
			defaultGroupID := defaultGroup.ID
			authGroupIDs = models.AuthGroupIDs{&defaultGroupID}
		} else if !errors.Is(errFind, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "query default auth group failed"})
			return
		}
	}
	auth := models.Auth{
		Key:         key,
		AuthGroupID: authGroupIDs,
		ProxyURL:    proxyURL,
		Content:     contentJSON,
		IsAvailable: isAvailable,
		RateLimit:   body.RateLimit,
		Priority:    body.Priority,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if errCreate := h.db.WithContext(c.Request.Context()).Create(&auth).Error; errCreate != nil {
		if strings.Contains(errCreate.Error(), "duplicate") || strings.Contains(errCreate.Error(), "unique") {
			c.JSON(http.StatusConflict, gin.H{"error": "key already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create auth file failed"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":            auth.ID,
		"key":           auth.Key,
		"auth_group_id": auth.AuthGroupID.Clean(),
		"proxy_url":     auth.ProxyURL,
		"content":       auth.Content,
		"is_available":  auth.IsAvailable,
		"rate_limit":    auth.RateLimit,
		"priority":      auth.Priority,
		"created_at":    auth.CreatedAt,
		"updated_at":    auth.UpdatedAt,
	})
}

// Import uploads multiple auth json files and persists them into the auth table.
func (h *AuthFileHandler) Import(c *gin.Context) {
	form, errForm := c.MultipartForm()
	if errForm != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid multipart form"})
		return
	}

	files := form.File["files"]
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no files provided"})
		return
	}

	var authGroupIDs models.AuthGroupIDs
	groupValue := strings.TrimSpace(c.PostForm("auth_group_id"))
	groupProvided := groupValue != ""
	if groupProvided {
		parsedIDs, errParse := parseAuthGroupIDsInput(groupValue)
		if errParse != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid auth group id"})
			return
		}
		authGroupIDs = parsedIDs.Clean()
	}

	if !groupProvided {
		var defaultGroup models.AuthGroup
		if errFind := h.db.WithContext(c.Request.Context()).
			Where("is_default = ?", true).
			First(&defaultGroup).Error; errFind == nil {
			defaultGroupID := defaultGroup.ID
			authGroupIDs = models.AuthGroupIDs{&defaultGroupID}
		} else if !errors.Is(errFind, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "query default auth group failed"})
			return
		}
	}

	now := time.Now().UTC()
	imported := 0
	failures := make([]importAuthFilesFailure, 0)

	for _, file := range files {
		if file == nil {
			continue
		}
		if !strings.EqualFold(filepath.Ext(file.Filename), ".json") {
			failures = append(failures, importAuthFilesFailure{
				File:  file.Filename,
				Error: "file must be json",
			})
			continue
		}
		reader, errOpen := file.Open()
		if errOpen != nil {
			failures = append(failures, importAuthFilesFailure{
				File:  file.Filename,
				Error: "open file failed",
			})
			continue
		}
		data, errRead := io.ReadAll(reader)
		_ = reader.Close()
		if errRead != nil {
			failures = append(failures, importAuthFilesFailure{
				File:  file.Filename,
				Error: "read file failed",
			})
			continue
		}
		if len(data) == 0 {
			failures = append(failures, importAuthFilesFailure{
				File:  file.Filename,
				Error: "empty json file",
			})
			continue
		}

		var payload map[string]any
		if errUnmarshal := json.Unmarshal(data, &payload); errUnmarshal != nil {
			failures = append(failures, importAuthFilesFailure{
				File:  file.Filename,
				Error: "invalid json",
			})
			continue
		}

		key := ""
		if idValue, okID := payload["id"].(string); okID {
			key = strings.TrimSpace(idValue)
		}
		if key == "" {
			if keyValue, okKey := payload["key"].(string); okKey {
				key = strings.TrimSpace(keyValue)
			}
		}
		if key == "" {
			key = strings.TrimSpace(file.Filename)
		}
		if key == "" {
			failures = append(failures, importAuthFilesFailure{
				File:  file.Filename,
				Error: "missing key",
			})
			continue
		}

		if _, okType := payload["type"]; !okType {
			if provider, okProvider := payload["provider"].(string); okProvider && strings.TrimSpace(provider) != "" {
				payload["type"] = strings.TrimSpace(provider)
			} else if metadataValue, okMetadata := payload["metadata"].(map[string]any); okMetadata {
				if typeValue, okType := metadataValue["type"].(string); okType && strings.TrimSpace(typeValue) != "" {
					payload["type"] = strings.TrimSpace(typeValue)
				}
			}
		}

		proxyURL := ""
		if proxyValue, okProxy := payload["proxy_url"].(string); okProxy {
			proxyURL = strings.TrimSpace(proxyValue)
		}
		if proxyURL == "" && autoAssignProxyEnabled() {
			assignedProxyURL, errAssignProxy := pickRandomProxyURL(c.Request.Context(), h.db)
			if errAssignProxy != nil {
				failures = append(failures, importAuthFilesFailure{
					File:  file.Filename,
					Error: "auto assign proxy failed",
				})
				continue
			}
			if assignedProxyURL != "" {
				proxyURL = assignedProxyURL
			}
		}

		contentBytes, errMarshal := json.Marshal(payload)
		if errMarshal != nil {
			failures = append(failures, importAuthFilesFailure{
				File:  file.Filename,
				Error: "marshal json failed",
			})
			continue
		}

		auth := models.Auth{
			Key:         key,
			AuthGroupID: authGroupIDs,
			ProxyURL:    proxyURL,
			Content:     datatypes.JSON(contentBytes),
			IsAvailable: true,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		errCreate := h.db.WithContext(c.Request.Context()).Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "key"}},
			DoUpdates: clause.Assignments(map[string]any{
				"content":    auth.Content,
				"updated_at": now,
			}),
		}).Create(&auth).Error
		if errCreate != nil {
			failures = append(failures, importAuthFilesFailure{
				File:  file.Filename,
				Error: "import auth file failed",
			})
			continue
		}
		imported++
	}

	c.JSON(http.StatusOK, importAuthFilesResponse{
		Imported: imported,
		Failed:   failures,
	})
}

// List returns auth files with optional filters.
func (h *AuthFileHandler) List(c *gin.Context) {
	var (
		keyQ         = strings.TrimSpace(c.Query("key"))
		authGroupIDQ = strings.TrimSpace(c.Query("auth_group_id"))
		typeQ        = strings.TrimSpace(c.Query("type"))
	)

	q := h.db.WithContext(c.Request.Context()).Model(&models.Auth{})
	if keyQ != "" {
		pattern := dbutil.NormalizeLikePattern(h.db, "%"+keyQ+"%")
		q = q.Where(dbutil.CaseInsensitiveLikeExpr(h.db, "key"), pattern)
	}
	if authGroupIDQ != "" {
		if id, errParse := strconv.ParseUint(authGroupIDQ, 10, 64); errParse == nil {
			q = q.Where(dbutil.JSONArrayContainsExpr(h.db, "auth_group_id"), dbutil.JSONArrayContainsValue(h.db, id))
		}
	}
	if typeQ != "" {
		typeExpr := dbutil.JSONExtractTextExpr(h.db, "content", "type")
		q = q.Where(typeExpr+" = ?", typeQ)
	}

	var rows []models.Auth
	if errFind := q.Order("created_at DESC").Find(&rows).Error; errFind != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list auth files failed"})
		return
	}

	groupMap, errGroups := loadAuthGroupMap(c.Request.Context(), h.db, rows)
	if errGroups != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load auth groups failed"})
		return
	}

	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		authGroupIDs := row.AuthGroupID.Clean()
		item := gin.H{
			"id":            row.ID,
			"key":           row.Key,
			"auth_group_id": authGroupIDs,
			"proxy_url":     row.ProxyURL,
			"content":       row.Content,
			"is_available":  row.IsAvailable,
			"rate_limit":    row.RateLimit,
			"priority":      row.Priority,
			"created_at":    row.CreatedAt,
			"updated_at":    row.UpdatedAt,
		}
		item["auth_group"] = buildAuthGroupSummaries(authGroupIDs, groupMap)
		out = append(out, item)
	}
	c.JSON(http.StatusOK, gin.H{"auth_files": out})
}

// Get returns a single auth file by ID.
func (h *AuthFileHandler) Get(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var auth models.Auth
	if errFind := h.db.WithContext(c.Request.Context()).First(&auth, id).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}

	authGroupIDs := auth.AuthGroupID.Clean()
	groupMap, errGroups := loadAuthGroupMap(c.Request.Context(), h.db, []models.Auth{auth})
	if errGroups != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load auth groups failed"})
		return
	}
	item := gin.H{
		"id":            auth.ID,
		"key":           auth.Key,
		"auth_group_id": authGroupIDs,
		"proxy_url":     auth.ProxyURL,
		"content":       auth.Content,
		"is_available":  auth.IsAvailable,
		"rate_limit":    auth.RateLimit,
		"priority":      auth.Priority,
		"created_at":    auth.CreatedAt,
		"updated_at":    auth.UpdatedAt,
	}
	item["auth_group"] = buildAuthGroupSummaries(authGroupIDs, groupMap)
	c.JSON(http.StatusOK, item)
}

// updateAuthFileRequest defines the request body for auth file updates.
type updateAuthFileRequest struct {
	Key         *string              `json:"key"`
	AuthGroupID *models.AuthGroupIDs `json:"auth_group_id"`
	ProxyURL    *string              `json:"proxy_url"`
	Content     map[string]any       `json:"content"`
	IsAvailable *bool                `json:"is_available"`
	RateLimit   *int                 `json:"rate_limit"`
	Priority    *int                 `json:"priority"`
}

// Update modifies an auth file entry.
func (h *AuthFileHandler) Update(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var body updateAuthFileRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	now := time.Now().UTC()
	updates := map[string]any{"updated_at": now}

	if body.Key != nil {
		updates["key"] = strings.TrimSpace(*body.Key)
	}
	if body.AuthGroupID != nil {
		updates["auth_group_id"] = body.AuthGroupID.Clean()
	}
	if body.ProxyURL != nil {
		updates["proxy_url"] = strings.TrimSpace(*body.ProxyURL)
	}
	if body.Content != nil {
		contentBytes, errMarshal := json.Marshal(body.Content)
		if errMarshal == nil {
			updates["content"] = datatypes.JSON(contentBytes)
		}
	}
	if body.IsAvailable != nil {
		updates["is_available"] = *body.IsAvailable
	}
	if body.RateLimit != nil {
		updates["rate_limit"] = *body.RateLimit
	}
	if body.Priority != nil {
		updates["priority"] = *body.Priority
	}

	res := h.db.WithContext(c.Request.Context()).Model(&models.Auth{}).Where("id = ?", id).Updates(updates)
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

// Delete removes an auth file entry.
func (h *AuthFileHandler) Delete(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	res := h.db.WithContext(c.Request.Context()).Delete(&models.Auth{}, id)
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

// SetAvailable marks an auth file as available.
func (h *AuthFileHandler) SetAvailable(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	now := time.Now().UTC()
	res := h.db.WithContext(c.Request.Context()).Model(&models.Auth{}).Where("id = ?", id).
		Updates(map[string]any{"is_available": true, "updated_at": now})
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

// SetUnavailable marks an auth file as unavailable.
func (h *AuthFileHandler) SetUnavailable(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	now := time.Now().UTC()
	res := h.db.WithContext(c.Request.Context()).Model(&models.Auth{}).Where("id = ?", id).
		Updates(map[string]any{"is_available": false, "updated_at": now})
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

// ListTypes returns distinct auth file types.
func (h *AuthFileHandler) ListTypes(c *gin.Context) {
	var types []string
	typeExpr := dbutil.JSONExtractTextExpr(h.db, "content", "type")
	if errQuery := h.db.WithContext(c.Request.Context()).
		Model(&models.Auth{}).
		Select(fmt.Sprintf("DISTINCT %s AS content_type", typeExpr)).
		Where(fmt.Sprintf("%s IS NOT NULL AND %s != ''", typeExpr, typeExpr)).
		Order("content_type").
		Pluck("content_type", &types).Error; errQuery != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list types failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"types": types})
}

func parseAuthGroupIDsInput(value string) (models.AuthGroupIDs, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var list []uint64
		if errUnmarshal := json.Unmarshal([]byte(trimmed), &list); errUnmarshal != nil {
			return nil, errUnmarshal
		}
		return authGroupIDsFromValues(list), nil
	}
	parsed, errParse := strconv.ParseUint(trimmed, 10, 64)
	if errParse != nil {
		return nil, errParse
	}
	return authGroupIDsFromValues([]uint64{parsed}), nil
}

func authGroupIDsFromValues(values []uint64) models.AuthGroupIDs {
	if len(values) == 0 {
		return models.AuthGroupIDs{}
	}
	out := make(models.AuthGroupIDs, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		idCopy := value
		out = append(out, &idCopy)
	}
	if len(out) == 0 {
		return models.AuthGroupIDs{}
	}
	return out
}

func loadAuthGroupMap(ctx context.Context, db *gorm.DB, rows []models.Auth) (map[uint64]models.AuthGroup, error) {
	groupIDs := make([]uint64, 0)
	seen := make(map[uint64]struct{})
	for _, row := range rows {
		for _, id := range row.AuthGroupID.Values() {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			groupIDs = append(groupIDs, id)
		}
	}
	if len(groupIDs) == 0 {
		return map[uint64]models.AuthGroup{}, nil
	}
	var groups []models.AuthGroup
	if errFind := db.WithContext(ctx).Where("id IN ?", groupIDs).Find(&groups).Error; errFind != nil {
		return nil, errFind
	}
	groupMap := make(map[uint64]models.AuthGroup, len(groups))
	for _, group := range groups {
		groupMap[group.ID] = group
	}
	return groupMap, nil
}

func buildAuthGroupSummaries(ids models.AuthGroupIDs, groupMap map[uint64]models.AuthGroup) []gin.H {
	values := ids.Values()
	if len(values) == 0 {
		return []gin.H{}
	}
	out := make([]gin.H, 0, len(values))
	for _, id := range values {
		group, ok := groupMap[id]
		if !ok {
			continue
		}
		out = append(out, gin.H{
			"id":   group.ID,
			"name": group.Name,
		})
	}
	return out
}
