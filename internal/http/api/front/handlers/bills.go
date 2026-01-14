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
	"gorm.io/gorm/clause"
)

// BillFrontHandler handles billing endpoints for users.
type BillFrontHandler struct {
	db *gorm.DB
}

// NewBillFrontHandler constructs a BillFrontHandler.
func NewBillFrontHandler(db *gorm.DB) *BillFrontHandler {
	return &BillFrontHandler{db: db}
}

// createBillFrontRequest defines the request body for creating bills.
type createBillFrontRequest struct {
	PlanID uint64 `json:"plan_id"`
}

// Create purchases a plan using prepaid balance and creates a bill.
func (h *BillFrontHandler) Create(c *gin.Context) {
	const balanceEpsilon = 0.01

	userID := getUserID(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var body createBillFrontRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if body.PlanID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plan_id is required"})
		return
	}

	var plan models.Plan
	if errFindPlan := h.db.WithContext(c.Request.Context()).
		Where("id = ? AND is_enabled = ?", body.PlanID, true).
		First(&plan).Error; errFindPlan != nil {
		if errors.Is(errFindPlan, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query plan failed"})
		return
	}

	requiredAmount := plan.MonthPrice
	insufficientErr := errors.New("insufficient prepaid card balance")
	var created models.Bill
	errTx := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		var cards []models.PrepaidCard
		if errCards := tx.WithContext(c.Request.Context()).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("redeemed_user_id = ? AND is_enabled = ? AND balance > 0 AND redeemed_at IS NOT NULL", userID, true).
			Where("(expires_at IS NULL OR expires_at >= ?)", now).
			Order("expires_at ASC NULLS LAST, redeemed_at ASC NULLS LAST, id ASC").
			Find(&cards).Error; errCards != nil {
			return errCards
		}

		totalBalance := 0.0
		for _, card := range cards {
			totalBalance += card.Balance
		}
		if totalBalance+balanceEpsilon < requiredAmount {
			return insufficientErr
		}

		remaining := requiredAmount
		for _, card := range cards {
			if remaining <= 0 {
				break
			}
			if card.Balance <= 0 {
				continue
			}
			deduct := card.Balance
			if deduct > remaining {
				deduct = remaining
			}
			res := tx.WithContext(c.Request.Context()).
				Model(&models.PrepaidCard{}).
				Where("id = ?", card.ID).
				Update("balance", gorm.Expr("balance - ?", deduct))
			if res.Error != nil {
				return res.Error
			}
			remaining -= deduct
		}

		periodEnd := now.AddDate(0, 1, 0)
		bill := models.Bill{
			PlanID:      plan.ID,
			UserID:      userID,
			PeriodType:  models.BillPeriodTypeMonthly,
			Amount:      requiredAmount,
			PeriodStart: now,
			PeriodEnd:   periodEnd,
			TotalQuota:  plan.TotalQuota,
			DailyQuota:  plan.DailyQuota,
			UsedQuota:   0,
			LeftQuota:   plan.TotalQuota,
			UsedCount:   0,
			RateLimit:   plan.RateLimit,
			IsEnabled:   true,
			Status:      models.BillStatusPaid,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if errCreateBill := tx.WithContext(c.Request.Context()).Create(&bill).Error; errCreateBill != nil {
			return errCreateBill
		}
		created = bill
		return nil
	})

	if errTx != nil {
		if errors.Is(errTx, insufficientErr) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "insufficient prepaid card balance to complete subscription"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create bill failed"})
		return
	}

	c.JSON(http.StatusCreated, h.formatBill(&created))
}

// List returns bills for the authenticated user with filters.
func (h *BillFrontHandler) List(c *gin.Context) {
	userID := getUserID(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	planIDQ := strings.TrimSpace(c.Query("plan_id"))
	statusQ := strings.TrimSpace(c.Query("status"))
	enabledQ := strings.TrimSpace(c.Query("is_enabled"))
	activeQ := strings.TrimSpace(c.Query("active"))

	q := h.db.WithContext(c.Request.Context()).
		Model(&models.Bill{}).
		Where("user_id = ?", userID)

	if planIDQ != "" {
		if id, errParse := strconv.ParseUint(planIDQ, 10, 64); errParse == nil {
			q = q.Where("plan_id = ?", id)
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
	if activeQ == "true" || activeQ == "1" {
		now := time.Now().UTC()
		q = q.Where("is_enabled = ?", true).
			Where("status = ?", models.BillStatusPaid).
			Where("period_start <= ? AND period_end >= ?", now, now)
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

// formatBill converts a bill model to a response payload.
func (h *BillFrontHandler) formatBill(bill *models.Bill) gin.H {
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
