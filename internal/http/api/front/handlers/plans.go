package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/gorm"
)

// PlanFrontHandler serves plan-related front endpoints.
type PlanFrontHandler struct {
	db *gorm.DB
}

// NewPlanFrontHandler constructs a PlanFrontHandler.
func NewPlanFrontHandler(db *gorm.DB) *PlanFrontHandler {
	return &PlanFrontHandler{db: db}
}

// List returns enabled plans for the current user.
func (h *PlanFrontHandler) List(c *gin.Context) {
	var plans []models.Plan
	if errFind := h.db.WithContext(c.Request.Context()).
		Where("is_enabled = ?", true).
		Order("sort_order ASC, created_at DESC").
		Find(&plans).Error; errFind != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list plans failed"})
		return
	}

	out := make([]gin.H, 0, len(plans))
	for _, plan := range plans {
		out = append(out, gin.H{
			"id":             plan.ID,
			"name":           plan.Name,
			"month_price":    plan.MonthPrice,
			"description":    plan.Description,
			"support_models": plan.SupportModels,
			"feature1":       plan.Feature1,
			"feature2":       plan.Feature2,
			"feature3":       plan.Feature3,
			"feature4":       plan.Feature4,
			"sort_order":     plan.SortOrder,
			"total_quota":    plan.TotalQuota,
			"daily_quota":    plan.DailyQuota,
			"rate_limit":     plan.RateLimit,
			"is_enabled":     plan.IsEnabled,
			"created_at":     plan.CreatedAt,
			"updated_at":     plan.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"plans": out})
}
