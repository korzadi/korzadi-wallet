package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/application/integrations/korzadivpn"
)

type KorzadiVPNHandler struct{ service *korzadivpn.Service }

func NewKorzadiVPNHandler(s *korzadivpn.Service) *KorzadiVPNHandler {
	return &KorzadiVPNHandler{service: s}
}

type vpnPaymentRequest struct {
	UserID          string `json:"user_id" binding:"required"`
	ExternalOrderID string `json:"external_order_id" binding:"required"`
	AssetCode       string `json:"asset_code" binding:"required"`
	Amount          string `json:"amount" binding:"required"`
}

// POST /api/integrations/korzadivpn/payment
// Protegido por middleware.ServerToServerHMAC — solo KorzadiVPN puede invocarlo.
// Responde "completed" únicamente si el cobro realmente se aplicó al ledger.
func (h *KorzadiVPNHandler) Pay(c *gin.Context) {
	var req vpnPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id inválido"})
		return
	}
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "amount inválido"})
		return
	}

	result, err := h.service.Charge(c.Request.Context(), korzadivpn.ChargeInput{
		UserID: userID, ExternalOrderID: req.ExternalOrderID, AssetCode: req.AssetCode, Amount: amount,
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
