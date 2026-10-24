package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"korzadi-wallet/backend/internal/application/affiliate"
)

type AffiliateHandler struct{ service *affiliate.Service }

func NewAffiliateHandler(s *affiliate.Service) *AffiliateHandler {
	return &AffiliateHandler{service: s}
}

// POST /api/affiliate — convierte al usuario actual en afiliado (genera código único).
func (h *AffiliateHandler) Enroll(c *gin.Context) {
	aff, err := h.service.Enroll(c.Request.Context(), currentUserID(c))
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, aff)
}

// GET /api/affiliate — devuelve el estado de afiliado del usuario actual.
func (h *AffiliateHandler) Me(c *gin.Context) {
	aff, err := h.service.Enroll(c.Request.Context(), currentUserID(c)) // idempotente: crea si no existe
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, aff)
}

type registerReferralRequest struct {
	AffiliateCode string `json:"affiliate_code" binding:"required"`
}

// POST /api/affiliate/referrals — vincula al usuario actual como referido de un código.
func (h *AffiliateHandler) RegisterReferral(c *gin.Context) {
	var req registerReferralRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.RegisterReferral(c.Request.Context(), req.AffiliateCode, currentUserID(c)); err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "referido registrado"})
}
