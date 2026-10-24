package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/application/security"
)

// SecurityHandler exposes Bloque 8's admin-facing endpoints: emergency
// freeze, security policy management and the security-events log. It is
// mounted under /api/admin/security in router.go, behind the SAME
// middleware.RequireRole("admin","super_admin") the rest of /admin
// already uses — no new privilege is introduced (spec §11: "no crear
// privilegios administrativos implícitos").
type SecurityHandler struct {
	svc *security.Service
}

func NewSecurityHandler(svc *security.Service) *SecurityHandler {
	return &SecurityHandler{svc: svc}
}

func (h *SecurityHandler) unavailable(c *gin.Context) bool {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "controles de seguridad no configurados"})
		return true
	}
	return false
}

// ---- Emergency freeze (spec §21) ----

func (h *SecurityHandler) ListFreezes(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	if h.svc.Freezes == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "freeze no configurado"})
		return
	}
	statuses, err := h.svc.Freezes.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo listar el estado de freeze"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"freezes": statuses})
}

type setFreezeRequest struct {
	Operation string `json:"operation" binding:"required"`
	Frozen    bool   `json:"frozen"`
	Reason    string `json:"reason"`
}

// SetFreeze toggles the emergency freeze for one operation. It never
// touches balances, the Ledger or in-flight reservations (spec §21: "no
// debe destruir fondos ni alterar balances") — it only flips a flag that
// FreezeGuard.CheckEnabled consults before a NEW operation of that type
// is allowed to start.
func (h *SecurityHandler) SetFreeze(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var req setFreezeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cuerpo inválido"})
		return
	}
	op := security.OperationType(req.Operation)
	if !op.Valid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "operación desconocida"})
		return
	}
	if h.svc.Freezes == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "freeze no configurado"})
		return
	}
	actorID := currentUserID(c)
	if err := h.svc.Freezes.Set(c.Request.Context(), op, req.Frozen, req.Reason, actorID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo actualizar el freeze"})
		return
	}
	if h.svc.Events != nil {
		eventType := security.EventKillSwitchActivated
		metadata := map[string]any{"frozen": req.Frozen, "reason": req.Reason}
		_ = h.svc.Events.Log(c.Request.Context(), security.Event{
			Type: eventType, Severity: "critical", UserID: &actorID, ActorRole: "admin",
			Operation: op, IP: c.ClientIP(), Metadata: metadata,
		})
	}
	c.JSON(http.StatusOK, gin.H{"message": "actualizado", "operation": op, "frozen": req.Frozen})
}

// ---- Security policies (spec §3/§4) ----

func (h *SecurityHandler) ListPolicies(c *gin.Context) {
	if h.unavailable(c) || h.svc.Policies == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "políticas no configuradas"})
		return
	}
	policies, err := h.svc.Policies.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudieron listar las políticas"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"policies": policies})
}

type upsertPolicyRequest struct {
	Operation               string  `json:"operation" binding:"required"`
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
}

// UpsertPolicy lets a (super_)admin change limits (spec §4: "los límites
// deben poder asociarse a user/operation/asset/network... no hardcodear
// límites críticos"). Every amount arrives as a string and is parsed with
// shopspring/decimal — never float64 — matching how the rest of the
// codebase handles money.
func (h *SecurityHandler) UpsertPolicy(c *gin.Context) {
	if h.unavailable(c) || h.svc.Policies == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "políticas no configuradas"})
		return
	}
	var req upsertPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cuerpo inválido"})
		return
	}
	op := security.OperationType(req.Operation)
	if !op.Valid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "operación desconocida"})
		return
	}
	minAmount := decimal.Zero
	if req.MinAmount != "" {
		v, err := decimal.NewFromString(req.MinAmount)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "min_amount inválido"})
			return
		}
		minAmount = v
	}
	maxAmount, err := parseOptionalDecimal(req.MaxAmount)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max_amount inválido"})
		return
	}
	dailyLimit, err := parseOptionalDecimal(req.DailyLimit)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "daily_limit inválido"})
		return
	}
	monthlyLimit, err := parseOptionalDecimal(req.MonthlyLimit)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "monthly_limit inválido"})
		return
	}
	stepUp, err := parseOptionalDecimal(req.StepUpThreshold)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "step_up_threshold inválido"})
		return
	}
	manualApproval, err := parseOptionalDecimal(req.ManualApprovalThreshold)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "manual_approval_threshold inválido"})
		return
	}

	policy := security.Policy{
		Operation: op, AssetCode: req.AssetCode, Network: req.Network, MinAmount: minAmount,
		MaxAmount: maxAmount, DailyLimit: dailyLimit, DailyCountLimit: req.DailyCountLimit,
		MonthlyLimit: monthlyLimit, MonthlyCountLimit: req.MonthlyCountLimit,
		StepUpThreshold: stepUp, ManualApprovalAbove: manualApproval, Enabled: req.Enabled,
	}
	if err := security.ValidatePolicyShape(policy); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.Policies.Upsert(c.Request.Context(), policy); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo guardar la política"})
		return
	}
	actorID := currentUserID(c)
	if h.svc.Events != nil {
		_ = h.svc.Events.Log(c.Request.Context(), security.Event{
			Type: security.EventLimitChanged, Severity: "warning", UserID: &actorID, ActorRole: "admin",
			Operation: op, IP: c.ClientIP(),
			Metadata: map[string]any{"asset_code": req.AssetCode, "network": req.Network, "enabled": req.Enabled},
		})
	}
	c.JSON(http.StatusOK, gin.H{"message": "política actualizada"})
}

func parseOptionalDecimal(raw *string) (*decimal.Decimal, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	v, err := decimal.NewFromString(*raw)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// ---- Security events (spec §16/§17) ----

func (h *SecurityHandler) ListEvents(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	if h.svc.EventsRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "eventos de seguridad no configurados"})
		return
	}
	limit, offset := parsePagination(c)
	var userIDFilter *uuid.UUID
	if raw := c.Query("user_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user_id inválido"})
			return
		}
		userIDFilter = &id
	}
	events, err := h.svc.EventsRepo.List(c.Request.Context(), userIDFilter, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudieron listar los eventos"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}
