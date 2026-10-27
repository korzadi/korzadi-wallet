package adminpanel

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	appsecurity "korzadi-wallet/backend/internal/application/security"
)

func (h *Handler) securityFreezes(c *gin.Context) {
	if h.security.Freezes == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "security_unavailable"})
		return
	}
	rows, err := h.security.Freezes.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "freezes_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func (h *Handler) setFreeze(c *gin.Context) {
	if h.security.Freezes == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "security_unavailable"})
		return
	}
	var req struct {
		Operation string `json:"operation"`
		Frozen    bool   `json:"frozen"`
		Reason    string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !validReason(req.Reason) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_required"})
		return
	}
	op := appsecurity.OperationType(strings.ToUpper(strings.TrimSpace(req.Operation)))
	if !op.Valid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_operation"})
		return
	}
	if err := h.security.Freezes.Set(c.Request.Context(), op, req.Frozen, req.Reason, uuid.Nil); err != nil {
		h.audit(c, "security.freeze_failed", "security_operation", nil, req.Reason, "failure", map[string]any{"operation": op, "frozen": req.Frozen})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "freeze_failed"})
		return
	}
	h.audit(c, "security.freeze", "security_operation", nil, req.Reason, "success", map[string]any{"operation": op, "frozen": req.Frozen})
	c.JSON(http.StatusOK, gin.H{"status": "ok", "operation": op, "frozen": req.Frozen})
}

func (h *Handler) securityPolicies(c *gin.Context) {
	if h.security.Policies == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "security_unavailable"})
		return
	}
	rows, err := h.security.Policies.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "policies_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func (h *Handler) upsertPolicy(c *gin.Context) {
	if h.security.Policies == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "security_unavailable"})
		return
	}
	var req struct {
		Operation               string  `json:"operation"`
		AssetCode               string  `json:"asset_code"`
		Network                 string  `json:"network"`
		MinAmount               string  `json:"min_amount"`
		MaxAmount               *string `json:"max_amount"`
		DailyLimit              *string `json:"daily_limit"`
		DailyCountLimit         *int    `json:"daily_count_limit"`
		MonthlyLimit            *string `json:"monthly_limit"`
		MonthlyCountLimit       *int    `json:"monthly_count_limit"`
		StepUpThreshold         *string `json:"step_up_threshold"`
		ManualApprovalThreshold *string `json:"manual_approval_threshold"`
		Enabled                 bool    `json:"enabled"`
		Reason                  string  `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !validReason(req.Reason) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_required"})
		return
	}
	op := appsecurity.OperationType(strings.ToUpper(strings.TrimSpace(req.Operation)))
	if !op.Valid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_operation"})
		return
	}
	minAmount := decimal.Zero
	if strings.TrimSpace(req.MinAmount) != "" {
		v, err := decimal.NewFromString(req.MinAmount)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_min_amount"})
			return
		}
		minAmount = v
	}
	maxAmount, err := optDecimal(req.MaxAmount)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_max_amount"})
		return
	}
	daily, err := optDecimal(req.DailyLimit)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_daily_limit"})
		return
	}
	monthly, err := optDecimal(req.MonthlyLimit)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_monthly_limit"})
		return
	}
	stepUp, err := optDecimal(req.StepUpThreshold)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_step_up"})
		return
	}
	manual, err := optDecimal(req.ManualApprovalThreshold)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_manual_approval"})
		return
	}
	policy := appsecurity.Policy{Operation: op, AssetCode: req.AssetCode, Network: req.Network,
		MinAmount: minAmount, MaxAmount: maxAmount, DailyLimit: daily, DailyCountLimit: req.DailyCountLimit,
		MonthlyLimit: monthly, MonthlyCountLimit: req.MonthlyCountLimit, StepUpThreshold: stepUp,
		ManualApprovalAbove: manual, Enabled: req.Enabled}
	if err := appsecurity.ValidatePolicyShape(policy); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_policy", "message": err.Error()})
		return
	}
	if err := h.security.Policies.Upsert(c.Request.Context(), policy); err != nil {
		h.audit(c, "security.policy_upsert_failed", "security_policy", nil, req.Reason, "failure", map[string]any{"operation": op})
		c.JSON(http.StatusInternalServerError, gin.H{"error": "policy_failed"})
		return
	}
	h.audit(c, "security.policy_upsert", "security_policy", nil, req.Reason, "success",
		map[string]any{"operation": op, "asset_code": req.AssetCode, "network": req.Network, "enabled": req.Enabled})
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func optDecimal(raw *string) (*decimal.Decimal, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	v, err := decimal.NewFromString(strings.TrimSpace(*raw))
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (h *Handler) securityEvents(c *gin.Context) {
	if h.security.EventsRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "security_unavailable"})
		return
	}
	limit, offset := pageParams(c)
	var userID *uuid.UUID
	if raw := c.Query("user_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_user_id"})
			return
		}
		userID = &id
	}
	rows, err := h.security.EventsRepo.List(c.Request.Context(), userID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "events_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}
