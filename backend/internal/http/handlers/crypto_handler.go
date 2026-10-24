package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"korzadi-wallet/backend/internal/application/crypto"
)

type CryptoHandler struct {
	service *crypto.Service
}

// NewCryptoHandler: Bloque 6B removed the legacy BSC-only withdrawal from
// this handler — every blockchain withdrawal now goes through
// BlockchainWithdrawalHandler.
func NewCryptoHandler(s *crypto.Service) *CryptoHandler {
	return &CryptoHandler{service: s}
}

// ---- TRON (unchanged) ----

// tronDepositsEnabled stays false until a TRON deposit watcher exists.
// AUDIT FIX (HIGH): without a watcher, any USDT sent to an address issued by
// GetDepositAddress would never be credited, so we refuse to issue one.
// Read-only balance lookups (GetOnChainBalance) are unaffected.
const tronDepositsEnabled = false

func (h *CryptoHandler) GetDepositAddress(c *gin.Context) {
	if !tronDepositsEnabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "tron_deposits_unavailable",
			"message": "Los depósitos en TRON no están disponibles por ahora.",
		})
		return
	}
	address, err := h.service.GetOrCreateDepositAddress(c.Request.Context(), currentUserID(c))
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"network": "TRON",
		"asset":   "USDT",
		"address": address,
		"notice":  "Envía únicamente USDT (TRC20) a esta dirección. Enviar otro activo o usar otra red puede resultar en pérdida irreversible de fondos.",
	})
}

func (h *CryptoHandler) GetOnChainBalance(c *gin.Context) {
	balance, err := h.service.GetOnChainBalance(c.Request.Context(), currentUserID(c))
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"network": "TRON",
		"asset":   "USDT",
		"balance": balance.String(),
		"notice":  "Este es el saldo real en blockchain, todavía no reflejado en tu saldo interno de Korzadi Wallet.",
	})
}

// ---- BSC / USDT BEP-20 (new) ----

func (h *CryptoHandler) GetBSCDepositAddress(c *gin.Context) {
	address, err := h.service.GetOrCreateBSCDepositAddress(c.Request.Context(), currentUserID(c))
	if err != nil {
		respondBSCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"network": "BSC",
		"asset":   "USDT",
		"address": address,
		"notice":  "Envía únicamente USDT (BEP-20) en BNB Smart Chain a esta dirección. Enviar otro activo, usar otra red, u otro contrato puede resultar en pérdida irreversible de fondos.",
	})
}

func (h *CryptoHandler) GetBSCOnChainBalance(c *gin.Context) {
	balance, err := h.service.GetBSCOnChainBalance(c.Request.Context(), currentUserID(c))
	if err != nil {
		respondBSCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"network": "BSC",
		"asset":   "USDT",
		"balance": balance.String(),
		"notice":  "Este es el saldo real en blockchain. El watcher acredita automáticamente tu saldo interno tras las confirmaciones requeridas.",
	})
}

func (h *CryptoHandler) GetBSCHealth(c *gin.Context) {
	health, err := h.service.BSCHealth(c.Request.Context())
	status := http.StatusOK
	if err != nil {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{
		"network":           health.Network,
		"rpc_reachable":     health.RPCReachable,
		"chain_id_ok":       health.ChainIDOK,
		"expected_chain_id": health.ExpectedChainID,
		"actual_chain_id":   health.ActualChainID,
		"latest_block":      health.LatestBlock,
		"contract_ok":       health.ContractOK,
		"error":             publicHealthError(health.RPCReachable, health.ChainIDOK, health.Err),
	})
}

// publicHealthError maps the provider's raw health error (which can embed the
// RPC URL, API key included) to a coarse, safe code for this unauthenticated
// endpoint. The raw detail stays in server logs only.
func publicHealthError(rpcReachable, chainIDOK bool, raw string) string {
	switch {
	case raw == "":
		return ""
	case !rpcReachable:
		return "rpc_unreachable"
	case !chainIDOK:
		return "chain_id_mismatch"
	default:
		return "unhealthy"
	}
}

func respondBSCError(c *gin.Context, err error) {
	if errors.Is(err, crypto.ErrBSCNotConfigured) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bsc_not_configured", "message": err.Error()})
		return
	}
	respondDomainError(c, err)
}
