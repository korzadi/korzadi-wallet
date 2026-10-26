package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/application/fee"
	"korzadi-wallet/backend/internal/application/security"
	"korzadi-wallet/backend/internal/application/transfer"
	"korzadi-wallet/backend/internal/domain"
)

type WalletHandler struct {
	wallets       domain.WalletRepository
	ledger        domain.LedgerRepository
	transferSvc   *transfer.Service
	priceProvider fee.PriceProvider
}

func NewWalletHandler(
	w domain.WalletRepository,
	l domain.LedgerRepository,
	t *transfer.Service,
	priceProvider fee.PriceProvider,
) *WalletHandler {
	return &WalletHandler{
		wallets:       w,
		ledger:        l,
		transferSvc:   t,
		priceProvider: priceProvider,
	}
}

func (h *WalletHandler) Balances(c *gin.Context) {
	ctx := c.Request.Context()
	userID := currentUserID(c)

	wallet, err := h.wallets.FindWalletByUserID(ctx, userID)
	if err != nil || wallet == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "wallet no encontrada"})
		return
	}

	accounts, err := h.wallets.ListAccounts(ctx, wallet.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error al listar cuentas"})
		return
	}

	result := make([]gin.H, 0, len(accounts))
	totalUSDT := decimal.Zero
	valuationComplete := true

	for _, acc := range accounts {
		balance, err := h.ledger.GetBalance(ctx, acc.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "error al calcular saldo"})
			return
		}

		item := gin.H{
			"asset":   acc.AssetCode,
			"balance": balance.String(),
		}

		if balance.IsZero() {
			item["price_usdt"] = "0"
			item["value_usdt"] = "0"
			item["valuation_available"] = true
			result = append(result, item)
			continue
		}

		if h.priceProvider == nil {
			valuationComplete = false
			item["valuation_available"] = false
			item["valuation_error"] = "proveedor de precios no configurado"
			result = append(result, item)
			continue
		}

		price, err := h.priceProvider.GetPrice(ctx, acc.AssetCode, "USDT")
		if err != nil {
			// El saldo sigue siendo válido aunque no exista precio de mercado.
			// No inventamos una valoración.
			valuationComplete = false
			item["valuation_available"] = false
			item["valuation_error"] = "precio no disponible"
			result = append(result, item)
			continue
		}

		valueUSDT := balance.Mul(price.Value)
		totalUSDT = totalUSDT.Add(valueUSDT)

		item["price_usdt"] = price.Value.String()
		item["value_usdt"] = valueUSDT.String()
		item["price_source"] = price.Source
		item["price_as_of"] = price.AsOf
		item["valuation_available"] = true

		result = append(result, item)
	}

	response := gin.H{
		"wallet_id":          wallet.ID,
		"balances":           result,
		"total_usdt":         totalUSDT.String(),
		"valuation_complete": valuationComplete,
		"valuation_asset":    "USDT",
	}

	c.JSON(http.StatusOK, response)
}

func (h *WalletHandler) Transactions(c *gin.Context) {
	userID := currentUserID(c)
	wallet, err := h.wallets.FindWalletByUserID(c.Request.Context(), userID)
	if err != nil || wallet == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "wallet no encontrada"})
		return
	}
	accounts, err := h.wallets.ListAccounts(c.Request.Context(), wallet.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error al listar cuentas"})
		return
	}

	var entries []gin.H
	for _, acc := range accounts {
		list, err := h.ledger.ListEntriesByAccount(c.Request.Context(), acc.ID, 50, 0)
		if err != nil {
			continue
		}
		for _, e := range list {
			entries = append(entries, gin.H{
				"id": e.ID, "asset": e.AssetCode, "amount": e.Amount.String(),
				"ledger_transaction_id": e.LedgerTransactionID, "created_at": e.CreatedAt,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"transactions": entries})
}

type transferRequest struct {
	Recipient string `json:"recipient" binding:"required"`
	AssetCode string `json:"asset" binding:"required"`
	Amount    string `json:"amount" binding:"required"`
	Note      string `json:"note"`
}

func (h *WalletHandler) CreateTransfer(c *gin.Context) {
	var req transferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	amount, err := parseAndValidateAmount(req.Amount)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	in := transfer.CreateTransferInput{
		SenderUserID: currentUserID(c),
		AssetCode:    req.AssetCode,
		Amount:       amount,
		Note:         req.Note,
		IP:           c.ClientIP(),
		// Bloque 8: only consulted when the amount crosses this
		// operation's step-up threshold; harmless (ignored) otherwise.
		TwoFACode: c.GetHeader("X-2FA-Code"),
		PinCode:    c.GetHeader("X-PIN-Code"),
	}
	// recipient is either a verified email or a raw internal user ID —
	// never trust it as an arbitrary field name, resolve strictly one way
	// or the other so there's no ambiguity about which path validated it.
	if recipientID, parseErr := uuid.Parse(req.Recipient); parseErr == nil {
		in.RecipientUserID = recipientID
	} else {
		in.RecipientEmail = req.Recipient
	}

	idemKey, _ := c.Get("idempotency_key")
	in.IdempotencyKey = idemKey.(string)

	result, err := h.transferSvc.Execute(c.Request.Context(), in)
	if err != nil {
		if status, code, ok := security.HTTPStatusFor(err); ok {
			c.JSON(status, gin.H{"error": code})
			return
		}
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// ListTransfers handles GET /wallet/transfers — the authenticated user's
// own transfers (sent and received), newest first.
func (h *WalletHandler) ListTransfers(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))

	views, err := h.transferSvc.ListForUser(c.Request.Context(), currentUserID(c), limit, offset)
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"transfers": views})
}

// GetTransfer handles GET /wallet/transfers/:id — a single transfer, only
// if the authenticated user was the sender or the receiver. A malformed
// ID, a non-existent transfer, and someone else's transfer all return the
// same 404 shape, indistinguishably.
func (h *WalletHandler) GetTransfer(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "transferencia no encontrada"})
		return
	}
	view, err := h.transferSvc.GetForUser(c.Request.Context(), id, currentUserID(c))
	if err != nil {
		if errors.Is(err, transfer.ErrTransferNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "transferencia no encontrada"})
			return
		}
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"transfer": view})
}

func currentUserID(c *gin.Context) uuid.UUID {
	raw, _ := c.Get("user_id")
	id, _ := uuid.Parse(raw.(string))
	return id
}
