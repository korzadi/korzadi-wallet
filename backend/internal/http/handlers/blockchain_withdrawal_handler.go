package handlers

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	bw "korzadi-wallet/backend/internal/application/blockchainwithdrawal"
	appfee "korzadi-wallet/backend/internal/application/fee"
	"korzadi-wallet/backend/internal/application/security"
	"korzadi-wallet/backend/internal/domain"
)

// BlockchainWithdrawalHandler exposes Bloque 6B:
//
//	POST /wallet/withdrawals        create (JWT + 2FA + Idempotency-Key)
//	GET  /wallet/withdrawals        history (own only)
//	GET  /wallet/withdrawals/:id    detail  (own only)
//
// The authenticated user always comes from the JWT (currentUserID). The
// request can never carry a user id, fees, contract, RPC, chain id or
// account: fees come from the Fee Engine, everything else from registries.
type BlockchainWithdrawalHandler struct {
	service *bw.Service
}

func NewBlockchainWithdrawalHandler(s *bw.Service) *BlockchainWithdrawalHandler {
	return &BlockchainWithdrawalHandler{service: s}
}

type createBlockchainWithdrawalRequest struct {
	Asset              string `json:"asset" binding:"required"`
	Network            string `json:"network" binding:"required"`
	Amount             string `json:"amount" binding:"required"`
	DestinationAddress string `json:"destination_address" binding:"required"`
	// IdempotencyKey is optional in the body: the Idempotency-Key HEADER is
	// mandatory (existing platform convention). If both are sent they must match.
	IdempotencyKey string `json:"idempotency_key"`
	// FeeQuoteID optionally pins a previously issued, still-valid quote.
	FeeQuoteID string `json:"fee_quote_id"`
}

func (h *BlockchainWithdrawalHandler) Create(c *gin.Context) {
	var req createBlockchainWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "asset, network, amount y destination_address son requeridos"})
		return
	}

	headerKey, _ := c.Get("idempotency_key")
	key, _ := headerKey.(string)
	if req.IdempotencyKey != "" && req.IdempotencyKey != key {
		c.JSON(http.StatusBadRequest, gin.H{"error": "idempotency_key_mismatch", "message": "el idempotency_key del cuerpo debe coincidir con el header Idempotency-Key"})
		return
	}

	amount, err := bw.ParseAmount(req.Amount)
	if err != nil {
		respondWithdrawalError(c, err)
		return
	}

	var quoteID *uuid.UUID
	if req.FeeQuoteID != "" {
		parsed, perr := uuid.Parse(req.FeeQuoteID)
		if perr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_fee_quote_id"})
			return
		}
		quoteID = &parsed
	}

	twoFA, _ := c.Get("two_fa_verified")
	verified, _ := twoFA.(bool)

	result, err := h.service.Create(c.Request.Context(), bw.CreateInput{
		UserID:             currentUserID(c),
		AssetCode:          req.Asset,
		Network:            req.Network,
		Amount:             amount,
		DestinationAddress: req.DestinationAddress,
		IdempotencyKey:     key,
		FeeQuoteID:         quoteID,
		IP:                 c.ClientIP(),
		TwoFAVerified:      verified,
	})
	if err != nil {
		respondWithdrawalError(c, err)
		return
	}

	status := http.StatusCreated
	if !result.Created {
		status = http.StatusOK // idempotent replay: same request, same result
	}
	c.JSON(status, gin.H{
		"withdrawal": toWithdrawalDTO(result.Withdrawal, false),
		"replayed":   !result.Created,
		"notice":     "El retiro fue registrado y sus fondos reservados. La transmisión y las confirmaciones se procesan en segundo plano; consulta su estado con GET /wallet/withdrawals/:id.",
	})
}

func (h *BlockchainWithdrawalHandler) List(c *gin.Context) {
	limit := queryInt(c, "limit", 20)
	offset := queryInt(c, "offset", 0)

	items, err := h.service.List(c.Request.Context(), currentUserID(c), limit, offset)
	if err != nil {
		respondWithdrawalError(c, err)
		return
	}
	out := make([]withdrawalDTO, 0, len(items))
	for _, w := range items {
		out = append(out, toWithdrawalDTO(w, true)) // destination masked in listings
	}
	c.JSON(http.StatusOK, gin.H{"withdrawals": out, "limit": limit, "offset": offset})
}

func (h *BlockchainWithdrawalHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id"})
		return
	}
	w, err := h.service.Get(c.Request.Context(), currentUserID(c), id)
	if err != nil {
		respondWithdrawalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"withdrawal": toWithdrawalDTO(w, false)})
}

func queryInt(c *gin.Context, key string, def int) int {
	raw := c.Query(key)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return def
	}
	return v
}

// ---- DTO ----

// withdrawalDTO is the ONLY shape ever returned. It deliberately omits
// everything internal: the source address, nonce, raw transaction, gas
// details, reservation and ledger ids, lease state and raw error text.
type withdrawalDTO struct {
	ID                 string  `json:"id"`
	Asset              string  `json:"asset"`
	Network            string  `json:"network"`
	Amount             string  `json:"amount"`
	DestinationAddress string  `json:"destination_address"`
	Status             string  `json:"status"`
	FeeQuoteID         *string `json:"fee_quote_id"`
	FeeAsset           string  `json:"fee_asset"`
	ServiceFee         string  `json:"service_fee"`
	NetworkFee         string  `json:"network_fee"`
	TotalFee           string  `json:"total_fee"`
	ActualNetworkFee   *string `json:"actual_network_fee,omitempty"`
	TransactionHash    *string `json:"transaction_hash"`
	Confirmations      int64   `json:"confirmations"`
	RequiredConfirm    *int64  `json:"required_confirmations,omitempty"`
	FailureReason      *string `json:"failure_reason,omitempty"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
	CompletedAt        *string `json:"completed_at,omitempty"`
}

func toWithdrawalDTO(w *bw.Withdrawal, maskDestination bool) withdrawalDTO {
	dest := w.ToAddress
	if maskDestination {
		dest = maskAddress(dest)
	}
	dto := withdrawalDTO{
		ID:                 w.ID.String(),
		Asset:              w.AssetCode,
		Network:            string(w.Network),
		Amount:             w.Amount.String(),
		DestinationAddress: dest,
		Status:             string(w.Status),
		FeeAsset:           bw.FeeAssetCode,
		ServiceFee:         w.ServiceFeeUSDT.String(),
		NetworkFee:         w.EstimatedNetworkFeeUSDT.String(),
		TotalFee:           w.TotalFeeUSDT.String(),
		Confirmations:      w.Confirmations,
		RequiredConfirm:    w.RequiredConfirmations,
		CreatedAt:          w.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:          w.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if w.FeeQuoteID != nil {
		q := w.FeeQuoteID.String()
		dto.FeeQuoteID = &q
	}
	if w.ActualNetworkFeeUSDT.Valid {
		v := w.ActualNetworkFeeUSDT.Decimal.String()
		dto.ActualNetworkFee = &v
	}
	if w.TxHash != "" {
		h := w.TxHash
		dto.TransactionHash = &h
	}
	if w.CompletedAt != nil {
		v := w.CompletedAt.UTC().Format("2006-01-02T15:04:05Z")
		dto.CompletedAt = &v
	}
	if code := w.FailureCode(); code != "" && (w.Status == bw.StatusFailed || w.Status == bw.StatusCancelled) {
		dto.FailureReason = &code
	}
	return dto
}

// maskAddress shows only the ends of an address in listings (0x1234…abcd).
func maskAddress(a string) string {
	if len(a) <= 12 {
		return a
	}
	return a[:6] + "…" + a[len(a)-4:]
}

// ---- errors ----

// respondWithdrawalError maps domain/service errors to HTTP responses using
// errors.Is (the existing respondDomainError does a bare type assertion and
// would turn every wrapped error into a 500). Unknown errors are logged
// server-side and returned as a generic 500 — never leaking internals.
func respondWithdrawalError(c *gin.Context, err error) {
	type mapping struct {
		target error
		status int
		code   string
	}
	table := []mapping{
		{bw.ErrBitcoinDisabled, http.StatusNotImplemented, "bitcoin_withdrawals_disabled"},
		{bw.ErrWithdrawalsDisabled, http.StatusServiceUnavailable, "withdrawals_disabled"},
		{bw.ErrNetworkNotConfigured, http.StatusServiceUnavailable, "network_not_available"},
		{bw.ErrUnsupportedNetwork, http.StatusBadRequest, "unsupported_network"},
		{bw.ErrAssetNotEnabled, http.StatusBadRequest, "asset_not_enabled"},
		{bw.ErrInvalidAddress, http.StatusBadRequest, "invalid_destination_address"},
		{bw.ErrDestinationIsContract, http.StatusBadRequest, "destination_is_token_contract"},
		{bw.ErrDestinationIsSelf, http.StatusBadRequest, "destination_is_own_address"},
		{bw.ErrInvalidAmount, http.StatusBadRequest, "invalid_amount"},
		{bw.ErrAmountBelowMinimum, http.StatusBadRequest, "amount_below_minimum"},
		{bw.ErrAmountAboveMaximum, http.StatusBadRequest, "amount_above_maximum"},
		{bw.ErrDailyLimitExceeded, http.StatusUnprocessableEntity, "daily_limit_exceeded"},
		{bw.ErrInvalidIdempotencyKey, http.StatusBadRequest, "invalid_idempotency_key"},
		{bw.ErrIdempotencyKeyReused, http.StatusConflict, "idempotency_key_reused"},
		{bw.ErrQuoteMismatch, http.StatusBadRequest, "fee_quote_mismatch"},
		{bw.ErrQuoteAlreadyUsed, http.StatusConflict, "fee_quote_already_used"},
		{bw.ErrInsufficientFunds, http.StatusUnprocessableEntity, "insufficient_funds"},
		{bw.ErrAccountInactive, http.StatusForbidden, "account_inactive"},
		{bw.ErrNoDepositAddress, http.StatusUnprocessableEntity, "no_address_on_network"},
		{bw.ErrNoHotWallet, http.StatusServiceUnavailable, "withdrawals_unavailable"},
		{bw.ErrNotFound, http.StatusNotFound, "withdrawal_not_found"},
		{bw.ErrCannotCancel, http.StatusConflict, "cannot_cancel"},
		{appfee.ErrQuoteExpired, http.StatusConflict, "fee_quote_expired"},
		{appfee.ErrQuoteNotActive, http.StatusConflict, "fee_quote_not_active"},
		{appfee.ErrQuoteNotFound, http.StatusNotFound, "fee_quote_not_found"},
	}
	// Controles de seguridad (Bloque 8): destino nuevo en espera, aprobación manual,
	// velocidad, límites, freeze... se devuelven con su código, nunca como 500.
	var cooldown *security.DestinationCooldownError
	if errors.As(err, &cooldown) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":        "new_destination_cooldown",
			"message":      "por seguridad, una dirección nueva solo puede usarse tras un período de espera; te avisamos por correo y notificación",
			"available_at": cooldown.AvailableAt.UTC().Format(time.RFC3339),
		})
		return
	}
	if errors.Is(err, security.ErrManualApprovalRequired) {
		c.JSON(http.StatusForbidden, gin.H{"error": "manual_approval_required",
			"message": "este monto requiere aprobación manual; contacta a soporte"})
		return
	}
	if errors.Is(err, bw.ErrSecurityUnavailable) {
		log.Printf("withdrawal handler: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "security_unavailable"})
		return
	}
	if status, code, ok := security.HTTPStatusFor(err); ok {
		c.JSON(status, gin.H{"error": code})
		return
	}
	for _, m := range table {
		if errors.Is(err, m.target) {
			body := gin.H{"error": m.code}
			switch m.code {
			case "fee_quote_expired":
				body["message"] = "la cotización de fee expiró; solicita un nuevo retiro para obtener una cotización nueva"
			case "bitcoin_withdrawals_disabled":
				body["message"] = "bitcoin withdrawals are currently disabled"
			}
			c.JSON(m.status, body)
			return
		}
	}

	var de *domain.DomainError
	if errors.As(err, &de) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": de.Code, "message": de.Message})
		return
	}
	log.Printf("withdrawal handler: error interno: %v", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
}
