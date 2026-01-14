package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPIBusiness/internal/models"
	"gorm.io/gorm"
)

// BillHandler manages admin CRUD endpoints for bills.
type BillHandler struct {
	db *gorm.DB // Database handle for bill records.
}

// NewBillHandler constructs a bill handler.
func NewBillHandler(db *gorm.DB) *BillHandler {
	return &BillHandler{db: db}
}

// createBillRequest captures the payload for creating a bill.
type createBillRequest struct {
	PlanID      uint64  `json:"plan_id"`      // Plan ID.
	UserID      uint64  `json:"user_id"`      // User ID.
	PeriodType  int     `json:"period_type"`  // Billing period type.
	Amount      float64 `json:"amount"`       // Billing amount.
	PeriodStart string  `json:"period_start"` // RFC3339 period start.
	PeriodEnd   string  `json:"period_end"`   // RFC3339 period end.
	TotalQuota  float64 `json:"total_quota"`  // Total quota.
	DailyQuota  float64 `json:"daily_quota"`  // Daily quota.
	UsedQuota   float64 `json:"used_quota"`   // Used quota.
	LeftQuota   float64 `json:"left_quota"`   // Remaining quota.
	UsedCount   int     `json:"used_count"`   // Usage count.
	RateLimit   *int    `json:"rate_limit"`   // Optional rate limit per second.
	IsEnabled   *bool   `json:"is_enabled"`   // Optional active flag.
	Status      int     `json:"status"`       // Bill status.
}

// Create validates input and inserts a bill record.
func (h *BillHandler) Create(c *gin.Context) {
	var body createBillRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	if body.PlanID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plan_id is required"})
		return
	}
	if body.UserID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	periodType := models.BillPeriodType(body.PeriodType)
	if periodType != models.BillPeriodTypeMonthly && periodType != models.BillPeriodTypeYearly {
		c.JSON(http.StatusBadRequest, gin.H{"error": "period_type must be 1 (monthly) or 2 (yearly)"})
		return
	}

	status := models.BillStatus(body.Status)
	if status < models.BillStatusPending || status > models.BillStatusRefunded {
		c.JSON(http.StatusBadRequest, gin.H{"error": "status must be 1-4"})
		return
	}

	periodStart, errParseStart := time.Parse(time.RFC3339, body.PeriodStart)
	if errParseStart != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid period_start format, use RFC3339"})
		return
	}
	periodEnd, errParseEnd := time.Parse(time.RFC3339, body.PeriodEnd)
	if errParseEnd != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid period_end format, use RFC3339"})
		return
	}

	var plan models.Plan
	if errFindPlan := h.db.WithContext(c.Request.Context()).First(&plan, body.PlanID).Error; errFindPlan != nil {
		if errors.Is(errFindPlan, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query plan failed"})
		return
	}

	isEnabled := true
	if body.IsEnabled != nil {
		isEnabled = *body.IsEnabled
	}
	rateLimit := plan.RateLimit
	if body.RateLimit != nil {
		rateLimit = *body.RateLimit
	}

	now := time.Now().UTC()
	bill := models.Bill{
		PlanID:      body.PlanID,
		UserID:      body.UserID,
		PeriodType:  periodType,
		Amount:      body.Amount,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		TotalQuota:  body.TotalQuota,
		DailyQuota:  body.DailyQuota,
		UsedQuota:   body.UsedQuota,
		LeftQuota:   body.LeftQuota,
		UsedCount:   body.UsedCount,
		RateLimit:   rateLimit,
		IsEnabled:   isEnabled,
		Status:      status,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if errCreate := h.db.WithContext(c.Request.Context()).Create(&bill).Error; errCreate != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create bill failed"})
		return
	}
	c.JSON(http.StatusCreated, h.formatBill(&bill))
}

// List returns bills filtered by query parameters.
func (h *BillHandler) List(c *gin.Context) {
	var (
		planIDQ  = strings.TrimSpace(c.Query("plan_id"))
		userIDQ  = strings.TrimSpace(c.Query("user_id"))
		statusQ  = strings.TrimSpace(c.Query("status"))
		enabledQ = strings.TrimSpace(c.Query("is_enabled"))
	)

	q := h.db.WithContext(c.Request.Context()).Model(&models.Bill{})
	if planIDQ != "" {
		if id, errParse := strconv.ParseUint(planIDQ, 10, 64); errParse == nil {
			q = q.Where("plan_id = ?", id)
		}
	}
	if userIDQ != "" {
		if id, errParse := strconv.ParseUint(userIDQ, 10, 64); errParse == nil {
			q = q.Where("user_id = ?", id)
		}
	}
	if statusQ != "" {
		if s, errParse := strconv.Atoi(statusQ); errParse == nil {
			q = q.Where("status = ?", s)
		}
	}
	if enabledQ != "" {
		if enabledQ == "true" || enabledQ == "1" {
			q = q.Where("is_enabled = ?", true)
		} else if enabledQ == "false" || enabledQ == "0" {
			q = q.Where("is_enabled = ?", false)
		}
	}

	var rows []models.Bill
	if errFind := q.Order("created_at DESC").Find(&rows).Error; errFind != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list bills failed"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, h.formatBill(&row))
	}
	c.JSON(http.StatusOK, gin.H{"bills": out})
}

// Get returns a bill by ID.
func (h *BillHandler) Get(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var bill models.Bill
	if errFind := h.db.WithContext(c.Request.Context()).First(&bill, id).Error; errFind != nil {
		if errors.Is(errFind, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	c.JSON(http.StatusOK, h.formatBill(&bill))
}

// updateBillRequest captures optional fields for bill updates.
type updateBillRequest struct {
	PlanID      *uint64  `json:"plan_id"`      // Optional plan ID.
	UserID      *uint64  `json:"user_id"`      // Optional user ID.
	PeriodType  *int     `json:"period_type"`  // Optional period type.
	Amount      *float64 `json:"amount"`       // Optional amount.
	PeriodStart *string  `json:"period_start"` // Optional RFC3339 period start.
	PeriodEnd   *string  `json:"period_end"`   // Optional RFC3339 period end.
	TotalQuota  *float64 `json:"total_quota"`  // Optional total quota.
	DailyQuota  *float64 `json:"daily_quota"`  // Optional daily quota.
	UsedQuota   *float64 `json:"used_quota"`   // Optional used quota.
	LeftQuota   *float64 `json:"left_quota"`   // Optional remaining quota.
	UsedCount   *int     `json:"used_count"`   // Optional usage count.
	RateLimit   *int     `json:"rate_limit"`   // Optional rate limit per second.
	IsEnabled   *bool    `json:"is_enabled"`   // Optional active flag.
	Status      *int     `json:"status"`       // Optional bill status.
}

// Update validates and applies bill field updates.
func (h *BillHandler) Update(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body updateBillRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	var existing models.Bill
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

	if body.PlanID != nil {
		if *body.PlanID == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "plan_id cannot be 0"})
			return
		}
		updates["plan_id"] = *body.PlanID
	}
	if body.UserID != nil {
		if *body.UserID == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user_id cannot be 0"})
			return
		}
		updates["user_id"] = *body.UserID
	}
	if body.PeriodType != nil {
		pt := models.BillPeriodType(*body.PeriodType)
		if pt != models.BillPeriodTypeMonthly && pt != models.BillPeriodTypeYearly {
			c.JSON(http.StatusBadRequest, gin.H{"error": "period_type must be 1 (monthly) or 2 (yearly)"})
			return
		}
		updates["period_type"] = pt
	}
	if body.Amount != nil {
		updates["amount"] = *body.Amount
	}
	if body.PeriodStart != nil {
		t, errParseTime := time.Parse(time.RFC3339, *body.PeriodStart)
		if errParseTime != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid period_start format"})
			return
		}
		updates["period_start"] = t
	}
	if body.PeriodEnd != nil {
		t, errParseTime := time.Parse(time.RFC3339, *body.PeriodEnd)
		if errParseTime != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid period_end format"})
			return
		}
		updates["period_end"] = t
	}
	if body.TotalQuota != nil {
		updates["total_quota"] = *body.TotalQuota
	}
	if body.DailyQuota != nil {
		updates["daily_quota"] = *body.DailyQuota
	}
	if body.UsedQuota != nil {
		updates["used_quota"] = *body.UsedQuota
	}
	if body.LeftQuota != nil {
		updates["left_quota"] = *body.LeftQuota
	}
	if body.UsedCount != nil {
		updates["used_count"] = *body.UsedCount
	}
	if body.RateLimit != nil {
		updates["rate_limit"] = *body.RateLimit
	}
	if body.IsEnabled != nil {
		updates["is_enabled"] = *body.IsEnabled
	}
	if body.Status != nil {
		s := models.BillStatus(*body.Status)
		if s < models.BillStatusPending || s > models.BillStatusRefunded {
			c.JSON(http.StatusBadRequest, gin.H{"error": "status must be 1-4"})
			return
		}
		updates["status"] = s
	}

	res := h.db.WithContext(c.Request.Context()).Model(&models.Bill{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Delete removes a bill by ID.
func (h *BillHandler) Delete(c *gin.Context) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	res := h.db.WithContext(c.Request.Context()).Delete(&models.Bill{}, id)
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

// Enable marks a bill as enabled.
func (h *BillHandler) Enable(c *gin.Context) {
	h.setEnabled(c, true)
}

// Disable marks a bill as disabled.
func (h *BillHandler) Disable(c *gin.Context) {
	h.setEnabled(c, false)
}

// setEnabled toggles the enabled state for a bill.
func (h *BillHandler) setEnabled(c *gin.Context, enabled bool) {
	id, errParse := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	now := time.Now().UTC()
	res := h.db.WithContext(c.Request.Context()).Model(&models.Bill{}).Where("id = ?", id).
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

// formatBill converts a bill model into a response payload.
func (h *BillHandler) formatBill(bill *models.Bill) gin.H {
	return gin.H{
		"id":           bill.ID,
		"plan_id":      bill.PlanID,
		"user_id":      bill.UserID,
		"period_type":  bill.PeriodType,
		"amount":       bill.Amount,
		"period_start": bill.PeriodStart,
		"period_end":   bill.PeriodEnd,
		"total_quota":  bill.TotalQuota,
		"daily_quota":  bill.DailyQuota,
		"used_quota":   bill.UsedQuota,
		"left_quota":   bill.LeftQuota,
		"used_count":   bill.UsedCount,
		"rate_limit":   bill.RateLimit,
		"is_enabled":   bill.IsEnabled,
		"status":       bill.Status,
		"created_at":   bill.CreatedAt,
		"updated_at":   bill.UpdatedAt,
	}
}
