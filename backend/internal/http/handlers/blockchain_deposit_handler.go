package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/deposit"
)

// BlockchainDepositHandler exposes read-only history for blockchain
// deposits (Bloque 4 §8-9) — GET /wallet/deposits/crypto and
// GET /wallet/deposits/crypto/:id. This is deliberately separate from
// DepositHandler (the existing fiat/PaymentProvider POST /wallet/deposits
// flow, Bloque unrelated to this one) rather than reusing that path: the
// two are different money-in mechanisms with different domain models
// (domain.Deposit vs blockchain_deposits), and overloading one path with
// both would blur that distinction rather than clarify it.
//
// Ownership is never taken from the request: every method resolves the
// caller exclusively via currentUserID(c) (the authenticated JWT
// subject), and the underlying repository queries filter by that user_id
// in SQL itself — there is no code path here that could return another
// user's deposit, by ID guessing or otherwise (spec §9, §10 IDOR).
type BlockchainDepositHandler struct {
	history *deposit.HistoryService
}

func NewBlockchainDepositHandler(history *deposit.HistoryService) *BlockchainDepositHandler {
	return &BlockchainDepositHandler{history: history}
}

type blockchainDepositResponse struct {
	ID                    string  `json:"id"`
	Network               string  `json:"network"`
	AssetCode             string  `json:"asset_code"`
	Amount                string  `json:"amount"`
	TxHash                string  `json:"tx_hash"`
	FromAddress           string  `json:"from_address,omitempty"`
	ToAddress             string  `json:"to_address"`
	Confirmations         uint64  `json:"confirmations"`
	RequiredConfirmations int     `json:"required_confirmations"`
	Status                string  `json:"status"`
	DetectedAt            string  `json:"detected_at"`
	CompletedAt           *string `json:"completed_at,omitempty"`
}

func toDepositResponse(d *deposit.BlockchainDepositRecord) blockchainDepositResponse {
	resp := blockchainDepositResponse{
		ID:                    d.ID.String(),
		Network:               string(d.Network),
		AssetCode:             d.AssetCode,
		Amount:                d.Amount.String(),
		TxHash:                d.TxHash,
		FromAddress:           d.FromAddress,
		ToAddress:             d.ToAddress,
		Confirmations:         d.Confirmations,
		RequiredConfirmations: d.RequiredConfirmations,
		Status:                d.Status,
		DetectedAt:            d.DetectedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if d.CompletedAt != nil {
		formatted := d.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
		resp.CompletedAt = &formatted
	}
	return resp
}

// List handles GET /wallet/deposits/crypto?limit=&offset= — only the
// authenticated user's own deposits, newest first.
func (h *BlockchainDepositHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))

	records, err := h.history.ListForUser(c.Request.Context(), currentUserID(c), limit, offset)
	if err != nil {
		respondDomainError(c, err)
		return
	}
	out := make([]blockchainDepositResponse, 0, len(records))
	for _, d := range records {
		out = append(out, toDepositResponse(d))
	}
	c.JSON(http.StatusOK, gin.H{"deposits": out})
}

// Get handles GET /wallet/deposits/crypto/:id — a single deposit, only if
// it belongs to the authenticated user. An invalid ID, a non-existent
// deposit, and someone else's deposit all return the same 404 shape —
// deliberately indistinguishable, so this endpoint can never be used to
// probe whether a given ID exists at all (spec §9).
func (h *BlockchainDepositHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "depósito no encontrado"})
		return
	}

	record, err := h.history.GetForUser(c.Request.Context(), id, currentUserID(c))
	if err != nil {
		if errors.Is(err, deposit.ErrDepositNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "depósito no encontrado"})
			return
		}
		respondDomainError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"deposit": toDepositResponse(record)})
}
