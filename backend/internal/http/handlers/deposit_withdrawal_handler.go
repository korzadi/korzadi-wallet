package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/deposit"
)

type DepositHandler struct{ service *deposit.Service }

func NewDepositHandler(s *deposit.Service) *DepositHandler { return &DepositHandler{service: s} }

type createDepositRequest struct {
	AssetCode string `json:"asset_code" binding:"required"`
	Amount    string `json:"amount" binding:"required"`
}

func (h *DepositHandler) Create(c *gin.Context) {
	var req createDepositRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	amount, err := parseAndValidateAmount(req.Amount)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	idemKey, _ := c.Get("idempotency_key")

	dep, err := h.service.Create(c.Request.Context(), deposit.CreateDepositInput{
		UserID: currentUserID(c), AssetCode: req.AssetCode, Amount: amount,
		IdempotencyKey: idemKey.(string), IP: c.ClientIP(),
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"deposit": dep,
		"notice":  "El depósito queda pendiente hasta confirmación real del proveedor de pagos.",
	})
}

func (h *DepositHandler) Confirm(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id de depósito inválido"})
		return
	}

	dep, err := h.service.Confirm(c.Request.Context(), id)
	if err != nil {
		respondDomainError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"deposit": dep})
}
