package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	dbutil "github.com/router-for-me/CLIProxyAPIBusiness/internal/db"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// QuotaHandler handles admin quota endpoints.
type QuotaHandler struct {
	db *gorm.DB
}

// NewQuotaHandler constructs a QuotaHandler.
func NewQuotaHandler(db *gorm.DB) *QuotaHandler {
	return &QuotaHandler{db: db}
}

// quotaListQuery defines filters for the quota list view.
type quotaListQuery struct {
	Page  int    `form:"page,default=1"`   // Page number.
	Limit int    `form:"limit,default=12"` // Page size.
	Key   string `form:"key"`              // Auth key filter.
	Type  string `form:"type"`             // Auth type filter.
	Group string `form:"auth_group_id"`    // Auth group filter.
}

// quotaListRow defines the query result row for quota list.
type quotaListRow struct {
	ID        uint64         `gorm:"column:id"`
	AuthID    uint64         `gorm:"column:auth_id"`
	Type      string         `gorm:"column:type"`
	Data      datatypes.JSON `gorm:"column:data"`
	UpdatedAt time.Time      `gorm:"column:updated_at"`
	AuthKey   string         `gorm:"column:auth_key"`
}

// List returns quota records with paging and filters.
func (h *QuotaHandler) List(c *gin.Context) {
	var q quotaListQuery
	if errBind := c.ShouldBindQuery(&q); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid query"})
		return
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Limit < 1 || q.Limit > 100 {
		q.Limit = 12
	}

	keyQ := strings.TrimSpace(q.Key)
	typeQ := strings.TrimSpace(q.Type)
	groupQ := strings.TrimSpace(q.Group)
	var groupID uint64
	if groupQ != "" {
		parsed, errParse := strconv.ParseUint(groupQ, 10, 64)
		if errParse != nil || parsed == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid auth_group_id"})
			return
		}
		groupID = parsed
	}

	ctx := c.Request.Context()

	base := h.db.WithContext(ctx).
		Table("quota").
		Joins("JOIN auths ON auths.id = quota.auth_id")
	if keyQ != "" {
		pattern := dbutil.NormalizeLikePattern(h.db, "%"+keyQ+"%")
		base = base.Where(dbutil.CaseInsensitiveLikeExpr(h.db, "auths.key"), pattern)
	}
	if typeQ != "" {
		base = base.Where("quota.type = ?", typeQ)
	}
	if groupID > 0 {
		base = base.Where(dbutil.JSONArrayContainsExpr(h.db, "auths.auth_group_id"), dbutil.JSONArrayContainsValue(h.db, groupID))
	}

	var total int64
	if errCount := base.Count(&total).Error; errCount != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "count quotas failed"})
		return
	}

	typeQuery := h.db.WithContext(ctx).
		Table("quota").
		Joins("JOIN auths ON auths.id = quota.auth_id")
	if keyQ != "" {
		pattern := dbutil.NormalizeLikePattern(h.db, "%"+keyQ+"%")
		typeQuery = typeQuery.Where(dbutil.CaseInsensitiveLikeExpr(h.db, "auths.key"), pattern)
	}
	if groupID > 0 {
		typeQuery = typeQuery.Where(dbutil.JSONArrayContainsExpr(h.db, "auths.auth_group_id"), dbutil.JSONArrayContainsValue(h.db, groupID))
	}
	var types []string
	if errTypes := typeQuery.Distinct("quota.type").Order("quota.type ASC").Pluck("quota.type", &types).Error; errTypes != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list quota types failed"})
		return
	}

	offset := (q.Page - 1) * q.Limit
	var rows []quotaListRow
	if errFind := base.
		Select("quota.id, quota.auth_id, quota.type, quota.data, quota.updated_at, auths.key AS auth_key").
		Order("auths.id ASC, quota.updated_at DESC").
		Offset(offset).
		Limit(q.Limit).
		Scan(&rows).Error; errFind != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list quotas failed"})
		return
	}

	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		payload := row.Data
		if isAntigravityType(row.Type) {
			payload = normalizeAntigravityQuota(row.Data)
		}
		out = append(out, gin.H{
			"id":         row.ID,
			"auth_id":    row.AuthID,
			"auth_key":   row.AuthKey,
			"type":       row.Type,
			"data":       payload,
			"updated_at": row.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"quotas": out,
		"types":  types,
		"total":  total,
		"page":   q.Page,
		"limit":  q.Limit,
	})
}

func isAntigravityType(value string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(value)), "antigravity")
}

func modelName2Alias(modelName string) string {
	switch modelName {
	case "rev19-uic3-1p":
		return "gemini-2.5-computer-use-preview-10-2025"
	case "gemini-3-pro-image":
		return "gemini-3-pro-image-preview"
	case "gemini-3-pro-high":
		return "gemini-3-pro-preview"
	case "gemini-3-flash":
		return "gemini-3-flash-preview"
	case "claude-sonnet-4-5":
		return "gemini-claude-sonnet-4-5"
	case "claude-sonnet-4-5-thinking":
		return "gemini-claude-sonnet-4-5-thinking"
	case "claude-opus-4-5-thinking":
		return "gemini-claude-opus-4-5-thinking"
	case "chat_20706", "chat_23310", "gemini-2.5-flash-thinking", "gemini-3-pro-low", "gemini-2.5-pro":
		return ""
	default:
		return modelName
	}
}

func normalizeAntigravityQuota(data datatypes.JSON) datatypes.JSON {
	if len(data) == 0 {
		return data
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return data
	}
	modelsRaw, ok := payload["models"].(map[string]any)
	if !ok {
		return data
	}

	buckets := make([]map[string]any, 0, len(modelsRaw))
	for key, value := range modelsRaw {
		entry, okEntry := value.(map[string]any)
		if !okEntry {
			continue
		}
		if _, okProvider := entry["modelProvider"]; !okProvider {
			continue
		}
		modelName := strings.TrimSpace(key)
		alias := modelName2Alias(strings.ToLower(modelName))
		if alias == "" {
			continue
		}
		bucket := map[string]any{
			"modelId": alias,
		}
		if quotaInfo, okQuota := entry["quotaInfo"].(map[string]any); okQuota {
			if resetTime, okReset := quotaInfo["resetTime"]; okReset {
				bucket["resetTime"] = resetTime
			}
			if remaining, okRemaining := quotaInfo["remainingFraction"]; okRemaining {
				bucket["remainingFraction"] = remaining
			}
		}
		buckets = append(buckets, bucket)
	}
	if len(buckets) > 1 {
		getModelID := func(bucket map[string]any) string {
			if v, ok := bucket["modelId"].(string); ok {
				return strings.ToLower(strings.TrimSpace(v))
			}
			return ""
		}
		sort.Slice(buckets, func(i, j int) bool {
			return getModelID(buckets[i]) < getModelID(buckets[j])
		})
	}
	updated, errMarshal := json.Marshal(map[string]any{
		"buckets": buckets,
	})
	if errMarshal != nil {
		return data
	}
	return datatypes.JSON(updated)
}
