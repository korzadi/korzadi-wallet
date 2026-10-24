package handlers

import (
	"errors"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	appwallet "korzadi-wallet/backend/internal/application/wallet"
	"korzadi-wallet/backend/internal/domain"
)

// AddressHandler exposes GET/POST /wallet/addresses for every network
// appwallet.AddressService supports (Ethereum, Polygon, Arbitrum,
// Optimism, Base, Bitcoin). TRON and BSC keep their existing endpoints
// under /wallet/crypto/usdt — unchanged, not duplicated here.
type AddressHandler struct {
	service *appwallet.AddressService
	// allowed is the set of networks that actually have a running deposit
	// watcher. nil means "no restriction" (tests). AUDIT FIX: without this, the
	// API handed out deposit addresses for networks nobody watches (e.g.
	// Bitcoin without a node), so a user could send real funds that would
	// never be credited.
	allowed map[domain.Network]bool
}

// SetAllowedNetworks restricts address issuance to networks with an active
// deposit watcher. Called once at startup, after every watcher is wired.
func (h *AddressHandler) SetAllowedNetworks(nets []domain.Network) {
	h.allowed = make(map[domain.Network]bool, len(nets))
	for _, n := range nets {
		h.allowed[n] = true
	}
}

// ActiveNetworks handles GET /wallet/networks — the networks a client may
// offer for crypto deposits right now. Read-only, authenticated, no
// user-specific data.
func (h *AddressHandler) ActiveNetworks(c *gin.Context) {
	out := make([]string, 0, len(h.allowed))
	if h.allowed == nil {
		out = append(out, supportedNetworkNames()...)
	} else {
		for n, ok := range h.allowed {
			if ok {
				out = append(out, string(n))
			}
		}
	}
	sort.Strings(out)
	c.JSON(http.StatusOK, gin.H{"networks": out})
}

func NewAddressHandler(service *appwallet.AddressService) *AddressHandler {
	return &AddressHandler{service: service}
}

// addressResponse is the ONLY shape returned to clients — deliberately
// excludes anything the spec flags as internal (derivation index, private
// key material, internal wallet/user IDs beyond the opaque address row id).
type addressResponse struct {
	ID        string `json:"id"`
	Network   string `json:"network"`
	Address   string `json:"address"`
	Type      string `json:"address_type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func toResponse(a appwallet.Address) addressResponse {
	return addressResponse{
		ID:        a.ID.String(),
		Network:   string(a.Network),
		Address:   a.Address,
		Type:      string(a.Type),
		Status:    a.Status,
		CreatedAt: a.CreatedAt,
	}
}

// List handles GET /wallet/addresses — every address the AUTHENTICATED
// user owns, across every network. Ownership is resolved exclusively from
// the auth context (currentUserID), never from a query parameter or body
// field, so a user can never enumerate another user's addresses.
func (h *AddressHandler) List(c *gin.Context) {
	rows, err := h.service.ListAddresses(c.Request.Context(), currentUserID(c))
	if err != nil {
		respondDomainError(c, err)
		return
	}
	out := make([]addressResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, toResponse(r))
	}
	c.JSON(http.StatusOK, gin.H{"addresses": out})
}

type createAddressRequest struct {
	Network string `json:"network" binding:"required"`
}

// Create handles POST /wallet/addresses — get-or-create semantics: calling
// it twice for the same network returns the same address, never generates
// a second one. The user_id is ALWAYS the authenticated caller
// (currentUserID) — the request body only carries `network`, so there is
// no field a client could set to request an address for someone else.
func (h *AddressHandler) Create(c *gin.Context) {
	var req createAddressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "network es requerido"})
		return
	}

	if h.allowed != nil && !h.allowed[domain.Network(req.Network)] {
		enabled := make([]string, 0, len(h.allowed))
		for _, n := range appwallet.SupportedNetworks() {
			if h.allowed[n] {
				enabled = append(enabled, string(n))
			}
		}
		sort.Strings(enabled)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":              "red no habilitada para depósitos",
			"supported_networks": enabled,
		})
		return
	}

	addr, err := h.service.GetOrCreateAddress(c.Request.Context(), currentUserID(c), domain.Network(req.Network))
	if err != nil {
		if errors.Is(err, appwallet.ErrUnsupportedNetwork) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":              "red no soportada para generación de direcciones",
				"supported_networks": supportedNetworkNames(),
			})
			return
		}
		respondDomainError(c, err)
		return
	}

	c.JSON(http.StatusOK, toResponse(*addr))
}

func supportedNetworkNames() []string {
	nets := appwallet.SupportedNetworks()
	out := make([]string, 0, len(nets))
	for _, n := range nets {
		out = append(out, string(n))
	}
	return out
}
