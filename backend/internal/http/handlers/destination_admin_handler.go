package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/security"
	"korzadi-wallet/backend/internal/domain"
)

// DestinationAdminHandler permite a un super_admin aprobar antes de tiempo
// (allowlisted), bloquear o marcar como conocida (known) una dirección de
// retiro de un usuario. Exige motivo y queda en auditoría.
type DestinationAdminHandler struct {
	guard *security.DestinationGuard
	audit domain.AuditLogger
}

func NewDestinationAdminHandler(g *security.DestinationGuard, a domain.AuditLogger) *DestinationAdminHandler {
	return &DestinationAdminHandler{guard: g, audit: a}
}

type setDestinationRequest struct {
	UserID    string `json:"user_id" binding:"required"`
	AssetCode string `json:"asset_code" binding:"required,max=20"`
	Network   string `json:"network" binding:"required,max=20"`
	Address   string `json:"address" binding:"required,max=128"`
	Status    string `json:"status" binding:"required,oneof=allowlisted blocked known"`
	Reason    string `json:"reason" binding:"required,min=5,max=500"`
}

// POST /api/admin/security/destinations
func (h *DestinationAdminHandler) Set(c *gin.Context) {
	var req setDestinationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request",
			"message": "user_id, asset_code, network, address, status (allowlisted|blocked|known) y reason (5-500) son obligatorios"})
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "user_id inválido"})
		return
	}
	if err := h.guard.SetStatus(c.Request.Context(), userID, req.AssetCode, req.Network, req.Address, security.DestinationStatus(req.Status)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo actualizar el destino"})
		return
	}
	actor := currentUserID(c)
	if h.audit != nil {
		// Nunca se registra la dirección en claro: solo su hash.
		_ = h.audit.Log(c.Request.Context(), &actor, "super_admin", "withdrawal_destination_"+req.Status, "user", &userID,
			map[string]any{"asset_code": req.AssetCode, "network": req.Network,
				"destination_hash": security.HashDestination(req.Address), "reason": req.Reason}, c.ClientIP())
	}
	c.JSON(http.StatusOK, gin.H{"message": "destino actualizado", "status": req.Status})
}
