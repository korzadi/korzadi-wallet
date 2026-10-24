package handlers

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	appfee "korzadi-wallet/backend/internal/application/fee"
	"korzadi-wallet/backend/internal/domain"
)

// FeeHandler exposes the Universal Fee Engine over HTTP — spec §26:
// POST /wallet/fees/estimate. Authenticated: a quote is tied to a user
// (spec §21 audit trail, and a future per-user policy extension point per
// spec §4), resolved exclusively from the JWT via currentUserID, never
// from the request body.
type FeeHandler struct {
	engine *appfee.Engine
}

func NewFeeHandler(engine *appfee.Engine) *FeeHandler {
	return &FeeHandler{engine: engine}
}

type estimateFeeRequest struct {
	Operation string `json:"operation" binding:"required"`
	Network   string `json:"network" binding:"required"`
	Asset     string `json:"asset" binding:"required"`
	Amount    string `json:"amount"` // optional — only needed for a percentage-based service fee component
}

type estimateFeeResponse struct {
	QuoteID          string `json:"quote_id"`
	Operation        string `json:"operation"`
	Network          string `json:"network"`
	Asset            string `json:"asset"`
	FeeAsset         string `json:"fee_asset"`
	ServiceFee       string `json:"service_fee"`
	NetworkFee       string `json:"network_fee"`
	TotalFee         string `json:"total_fee"`
	NetworkFeeNative string `json:"network_fee_native"`
	NativeAsset      string `json:"native_asset"`
	ExchangeRate     string `json:"exchange_rate"`
	PriceSource      string `json:"price_source"`
	CreatedAt        string `json:"created_at"`
	ExpiresAt        string `json:"expires_at"`
}

func toEstimateFeeResponse(q *appfee.FeeQuote) estimateFeeResponse {
	return estimateFeeResponse{
		QuoteID:          q.ID.String(),
		Operation:        string(q.OperationType),
		Network:          string(q.Network),
		Asset:            q.OperationAssetCode,
		FeeAsset:         q.FeeAssetCode,
		ServiceFee:       q.ServiceFee.String(),
		NetworkFee:       q.NetworkFee.String(),
		TotalFee:         q.TotalFee.String(),
		NetworkFeeNative: q.NetworkFeeNative.String(),
		NativeAsset:      q.NativeAssetCode,
		ExchangeRate:     q.ExchangeRate.String(),
		PriceSource:      q.PriceSource,
		CreatedAt:        q.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		ExpiresAt:        q.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// Estimate handles POST /wallet/fees/estimate.
func (h *FeeHandler) Estimate(c *gin.Context) {
	var req estimateFeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	amount := decimal.Zero
	if req.Amount != "" {
		parsed, err := decimal.NewFromString(req.Amount)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "amount inválido"})
			return
		}
		amount = parsed
	}

	quote, err := h.engine.EstimateFee(c.Request.Context(), appfee.EstimateFeeInput{
		UserID:             currentUserID(c),
		OperationType:      appfee.OperationType(req.Operation),
		Network:            domain.Network(req.Network),
		OperationAssetCode: req.Asset,
		OperationAmount:    amount,
	})
	if err != nil {
		respondFeeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toEstimateFeeResponse(quote))
}

// GetQuote handles GET /wallet/fees/quotes/:id — lets a user re-check a
// quote they already requested (spec §18: "el futuro withdrawal debe
// poder verificar quote still valid"). Ownership enforced the same way as
// every other lookup in this codebase.
func (h *FeeHandler) GetQuote(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "cotización no encontrada"})
		return
	}
	quote, err := h.engine.GetQuote(c.Request.Context(), id, currentUserID(c))
	if err != nil {
		respondFeeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toEstimateFeeResponse(quote))
}

// respondFeeError maps fee package sentinel errors to HTTP status codes.
// Kept local to this handler (rather than extending the shared
// respondDomainError, which only understands *domain.DomainError) since
// the fee package deliberately uses plain sentinel errors, not
// domain.DomainError — see fee/errors.go's doc comment.
func respondFeeError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	known := true
	switch {
	case errors.Is(err, appfee.ErrQuoteNotFound):
		status = http.StatusNotFound
	case errors.Is(err, appfee.ErrUnsupportedNetwork), errors.Is(err, appfee.ErrUnsupportedAsset),
		errors.Is(err, appfee.ErrInvalidAmount), errors.Is(err, appfee.ErrReservationFailed):
		status = http.StatusBadRequest
	case errors.Is(err, appfee.ErrPriceUnavailable):
		status = http.StatusServiceUnavailable
		c.JSON(status, gin.H{"error": "price_unavailable"})
		return
	case errors.Is(err, appfee.ErrPriceStale):
		status = http.StatusServiceUnavailable
		c.JSON(status, gin.H{"error": "price_stale"})
		return
	case errors.Is(err, appfee.ErrFeeTooHigh), errors.Is(err, appfee.ErrFeeTooLow):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, appfee.ErrQuoteExpired), errors.Is(err, appfee.ErrQuoteNotActive):
		status = http.StatusConflict
	case errors.Is(err, appfee.ErrInvalidConfig):
		status = http.StatusInternalServerError
	default:
		known = false
	}
	if !known {
		// AUDIT FIX: unknown errors can wrap RPC/price-provider failures that
		// embed provider URLs (API keys) — log server-side, return a generic body.
		log.Printf("fee handler: error interno: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
