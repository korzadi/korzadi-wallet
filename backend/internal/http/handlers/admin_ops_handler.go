package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	bw "korzadi-wallet/backend/internal/application/blockchainwithdrawal"
	"korzadi-wallet/backend/internal/application/custody"
	"korzadi-wallet/backend/internal/application/treasury"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

// Puertos mínimos que usa el handler (se satisfacen con los servicios reales y
// se sustituyen por fakes en los tests).
type withdrawalReviewer interface {
	ListNeedsReview(ctx context.Context, limit, offset int) ([]*bw.Withdrawal, error)
	ResolveNeedsReview(ctx context.Context, in bw.ResolveInput) (*bw.Withdrawal, error)
}

type custodyAdmin interface {
	Register(ctx context.Context, in custody.RegisterInput) (*custody.CustodyWallet, error)
	Get(ctx context.Context, id uuid.UUID) (*custody.CustodyWallet, error)
	List(ctx context.Context, f custody.ListFilter) ([]*custody.CustodyWallet, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, s custody.Status, actor string) (*custody.CustodyWallet, error)
}

type treasuryAdmin interface {
	CreatePolicy(ctx context.Context, in treasury.CreatePolicyInput) (*treasury.Policy, error)
	ListPolicies(ctx context.Context, f treasury.PolicyFilter) ([]*treasury.Policy, error)
	SetPolicyEnabled(ctx context.Context, id uuid.UUID, enabled bool, actor string) (*treasury.Policy, error)
}

type gasPolicyAdmin interface {
	Get(ctx context.Context, custodyWalletID uuid.UUID) (*postgres.GasStationPolicy, error)
	Upsert(ctx context.Context, p postgres.GasStationPolicy) (*postgres.GasStationPolicy, error)
}

// KeyGenerator crea una clave NUEVA dentro del servidor para el tipo de
// dirección ("evm" | "tron") y devuelve su dirección y la clave en hex. La
// clave solo se usa para cifrarla en custody; nunca se devuelve ni se registra.
type KeyGenerator func(addressType string) (address, privateKeyHex string, err error)

// HotBalanceReader consulta saldos on-chain; nunca necesita acceder a claves privadas.
type HotBalanceReader func(ctx context.Context, network, address string) (usdtBalance, nativeBalance string, err error)

// AdminOpsHandler agrupa las operaciones administrativas sensibles: resolver
// retiros en needs_review y registrar wallets HOT/GAS/TREASURY con sus
// políticas. Se monta SOLO bajo super_admin + 2FA (ver router.RegisterAdminOpsRoutes)
// y toda acción —exitosa o rechazada— queda en la auditoría.
type AdminOpsHandler struct {
	withdrawals      withdrawalReviewer
	custody          custodyAdmin
	treasury         treasuryAdmin
	gasPolicies      gasPolicyAdmin
	audit            domain.AuditLogger
	keygen           KeyGenerator
	allowKeyImport   bool
	hotBalanceReader HotBalanceReader
}

func NewAdminOpsHandler(w withdrawalReviewer, c custodyAdmin, t treasuryAdmin, g gasPolicyAdmin, audit domain.AuditLogger, keygen KeyGenerator) *AdminOpsHandler {
	return &AdminOpsHandler{withdrawals: w, custody: c, treasury: t, gasPolicies: g, audit: audit, keygen: keygen}
}

// AllowKeyImport habilita registrar una clave privada ENVIADA por HTTP. Por
// defecto está apagado: las wallets se generan en el servidor y la clave nunca
// viaja ni se ve.
func (h *AdminOpsHandler) AllowKeyImport(v bool) *AdminOpsHandler { h.allowKeyImport = v; return h }

// WithHotWalletReader conecta la lectura de balances reales de las redes.
func (h *AdminOpsHandler) WithHotWalletReader(fn HotBalanceReader) *AdminOpsHandler {
	h.hotBalanceReader = fn
	return h
}

// HotWalletBalances devuelve balances on-chain de las wallets HOT activas con USDT.
func (h *AdminOpsHandler) HotWalletBalances(c *gin.Context) {
	wt := custody.WalletType("HOT")
	st := custody.Status("active")
	wallets, err := h.custody.List(c.Request.Context(), custody.ListFilter{
		WalletType: &wt,
		Status:     &st,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hot_wallet_list_failed"})
		return
	}

	items := make([]gin.H, 0, len(wallets))
	for _, w := range wallets {
		network := strings.ToUpper(string(w.Network))
		if w.AssetCode != "USDT" {
			continue
		}
		if network != "TRON" && network != "ETHEREUM" && network != "BSC" {
			continue
		}

		item := gin.H{
			"id":         w.ID,
			"network":    w.Network,
			"address":    w.Address,
			"asset_code": w.AssetCode,
			"status":     w.Status,
		}

		if h.hotBalanceReader == nil {
			item["error"] = "balance_reader_not_configured"
		} else {
			usdt, native, readErr := h.hotBalanceReader(c.Request.Context(), network, w.Address)
			if readErr != nil {
				// No fingir un saldo de cero cuando falla el RPC.
				item["error"] = "onchain_balance_unavailable"
			} else {
				item["usdt_balance"] = usdt
				item["native_balance"] = native
				switch network {
				case "TRON":
					item["native_asset"] = "TRX"
				case "ETHEREUM":
					item["native_asset"] = "ETH"
				case "BSC":
					item["native_asset"] = "BNB"
				}
			}
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{"items": items})
}

const minAdminReason = 5

func adminActor(c *gin.Context) string {
	role := "super_admin"
	if v, ok := c.Get("admin_role"); ok {
		if r, ok := v.(string); ok && r != "" {
			role = r
		}
	}
	return role + ":" + currentUserID(c).String()
}

func (h *AdminOpsHandler) logAudit(c *gin.Context, action, entityType string, entityID *uuid.UUID, meta map[string]any) {
	if h.audit == nil {
		return
	}
	actor := currentUserID(c)
	role := "super_admin"
	if v, ok := c.Get("admin_role"); ok {
		if r, ok := v.(string); ok && r != "" {
			role = r
		}
	}
	_ = h.audit.Log(c.Request.Context(), &actor, role, action, entityType, entityID, meta, c.ClientIP())
}

func badRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": msg})
}

func validReason(r string) bool { return len([]rune(strings.TrimSpace(r))) >= minAdminReason }

// ---------------- retiros en revisión ----------------

func withdrawalView(w *bw.Withdrawal) gin.H {
	return gin.H{
		"id": w.ID, "user_id": w.UserID, "network": w.Network, "asset_code": w.AssetCode,
		"amount": w.Amount.String(), "to_address": w.ToAddress, "from_address": w.FromAddress,
		"status": w.Status, "needs_review": w.NeedsReview, "failure_code": w.FailureCode(),
		"error_detail": w.ErrorDetail, "tx_hash": w.TxHash, "attempts": w.Attempts,
		"created_at": w.CreatedAt, "updated_at": w.UpdatedAt,
	}
}

// GET /api/admin/withdrawals/review
func (h *AdminOpsHandler) ListWithdrawalReviews(c *gin.Context) {
	limit, offset := parsePagination(c)
	items, err := h.withdrawals.ListNeedsReview(c.Request.Context(), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo listar los retiros en revisión"})
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, w := range items {
		out = append(out, withdrawalView(w))
	}
	c.JSON(http.StatusOK, gin.H{"items": out, "limit": limit, "offset": offset})
}

type resolveWithdrawalRequest struct {
	Action string `json:"action" binding:"required,oneof=retry_settlement release"`
	Reason string `json:"reason" binding:"required,min=10,max=500"`
}

// POST /api/admin/withdrawals/:id/resolve {action: retry_settlement|release, reason}
func (h *AdminOpsHandler) ResolveWithdrawal(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "id inválido")
		return
	}
	var req resolveWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "action (retry_settlement|release) y reason (10-500) son obligatorios")
		return
	}
	actor := currentUserID(c)
	w, err := h.withdrawals.ResolveNeedsReview(c.Request.Context(), bw.ResolveInput{
		ID: id, Action: bw.ReviewAction(req.Action), Reason: req.Reason, Actor: actor.String(),
	})
	meta := map[string]any{"action": req.Action, "reason": req.Reason}
	if err != nil {
		meta["error"] = err.Error()
		h.logAudit(c, "withdrawal_review_failed", "withdrawal", &id, meta)
		respondReviewError(c, err)
		return
	}
	h.logAudit(c, "withdrawal_review_resolved", "withdrawal", &id, meta)
	c.JSON(http.StatusOK, gin.H{"withdrawal": withdrawalView(w)})
}

func respondReviewError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, bw.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
	case errors.Is(err, bw.ErrNotInReview), errors.Is(err, bw.ErrStaleState):
		c.JSON(http.StatusConflict, gin.H{"error": "not_in_review", "message": err.Error()})
	case errors.Is(err, bw.ErrReviewReasonInvalid), errors.Is(err, bw.ErrReviewActionInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
	case errors.Is(err, bw.ErrReviewTxNotFinal), errors.Is(err, bw.ErrReviewTxOnChain), errors.Is(err, bw.ErrReviewTxMaybeLive):
		c.JSON(http.StatusConflict, gin.H{"error": "chain_state_conflict", "message": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo resolver el retiro; revisa los logs"})
	}
}

// ---------------- custody wallets ----------------

func walletView(w *custody.CustodyWallet) gin.H {
	return gin.H{
		"id": w.ID, "network": w.Network, "asset_code": w.AssetCode, "wallet_type": w.WalletType,
		"address": w.Address, "address_type": w.AddressType, "status": w.Status,
		"created_at": w.CreatedAt, "updated_at": w.UpdatedAt,
	}
}

type registerWalletRequest struct {
	Network     string `json:"network" binding:"required,max=20"`
	AssetCode   string `json:"asset_code" binding:"required,max=20"`
	WalletType  string `json:"wallet_type" binding:"required,oneof=HOT GAS TREASURY"`
	AddressType string `json:"address_type" binding:"required,oneof=evm tron"`
	Reason      string `json:"reason" binding:"required,min=5,max=500"`
	// Importación de una wallet existente (deshabilitada por defecto).
	Address    string `json:"address"`
	PrivateKey string `json:"private_key"`
}

// POST /api/admin/custody/wallets
// Sin address/private_key: la clave se GENERA en el servidor y solo se devuelve
// la dirección. Con ellos (solo si el servidor permite importar): se cifra y se
// guarda la clave enviada.
func (h *AdminOpsHandler) RegisterWallet(c *gin.Context) {
	var req registerWalletRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "network, asset_code, wallet_type (HOT|GAS|TREASURY), address_type (evm|tron) y reason son obligatorios")
		return
	}
	in := custody.RegisterInput{
		Network: domain.Network(req.Network), AssetCode: req.AssetCode,
		WalletType: custody.WalletType(req.WalletType), AddressType: req.AddressType,
		Actor: adminActor(c),
	}
	imported := req.PrivateKey != "" || req.Address != ""
	switch {
	case imported:
		if !h.allowKeyImport {
			c.JSON(http.StatusForbidden, gin.H{"error": "key_import_disabled",
				"message": "la importación de claves por API está deshabilitada; omite address/private_key para generar la wallet en el servidor"})
			return
		}
		if req.PrivateKey == "" || req.Address == "" {
			badRequest(c, "para importar se requieren address y private_key")
			return
		}
		in.Address, in.PrivateKeyPlaintext = req.Address, req.PrivateKey
	default:
		if h.keygen == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "keygen_unavailable"})
			return
		}
		addr, priv, err := h.keygen(req.AddressType)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo generar la clave"})
			return
		}
		in.Address, in.PrivateKeyPlaintext = addr, priv
	}

	w, err := h.custody.Register(c.Request.Context(), in)
	// NUNCA se incluye la clave en auditoría ni en la respuesta.
	meta := map[string]any{"network": req.Network, "asset_code": req.AssetCode, "wallet_type": req.WalletType,
		"address_type": req.AddressType, "imported": imported, "reason": req.Reason}
	if err != nil {
		meta["error"] = err.Error()
		h.logAudit(c, "custody_wallet_register_failed", "custody_wallet", nil, meta)
		respondCustodyError(c, err)
		return
	}
	meta["address"] = w.Address
	h.logAudit(c, "custody_wallet_registered", "custody_wallet", &w.ID, meta)
	c.JSON(http.StatusCreated, gin.H{"wallet": walletView(w)})
}

func respondCustodyError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, custody.ErrDuplicateAddress), errors.Is(err, custody.ErrConcurrentStatusChange):
		c.JSON(http.StatusConflict, gin.H{"error": "conflict", "message": err.Error()})
	case errors.Is(err, custody.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
	case errors.Is(err, custody.ErrInvalidWalletType), errors.Is(err, custody.ErrInvalidStatus),
		errors.Is(err, custody.ErrInvalidAddressType), errors.Is(err, custody.ErrInvalidAddress),
		errors.Is(err, custody.ErrEmptyPrivateKey), errors.Is(err, custody.ErrUnknownNetwork),
		errors.Is(err, custody.ErrUnknownAsset), errors.Is(err, custody.ErrAssetNotEnabledOnNetwork),
		errors.Is(err, custody.ErrInvalidStatusTransition):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error interno al operar la custody wallet"})
	}
}

// GET /api/admin/custody/wallets?network=&asset_code=&wallet_type=&status=
func (h *AdminOpsHandler) ListWallets(c *gin.Context) {
	var f custody.ListFilter
	if v := c.Query("network"); v != "" {
		n := domain.Network(v)
		f.Network = &n
	}
	if v := c.Query("asset_code"); v != "" {
		f.AssetCode = &v
	}
	if v := c.Query("wallet_type"); v != "" {
		wt := custody.WalletType(v)
		f.WalletType = &wt
	}
	if v := c.Query("status"); v != "" {
		st := custody.Status(v)
		f.Status = &st
	}
	rows, err := h.custody.List(c.Request.Context(), f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudieron listar las wallets"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, w := range rows {
		out = append(out, walletView(w))
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

type walletStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=active disabled retired"`
	Reason string `json:"reason" binding:"required,min=5,max=500"`
}

// POST /api/admin/custody/wallets/:id/status {status, reason}
func (h *AdminOpsHandler) SetWalletStatus(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "id inválido")
		return
	}
	var req walletStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "status (active|disabled|retired) y reason son obligatorios")
		return
	}
	w, err := h.custody.UpdateStatus(c.Request.Context(), id, custody.Status(req.Status), adminActor(c))
	meta := map[string]any{"status": req.Status, "reason": req.Reason}
	if err != nil {
		meta["error"] = err.Error()
		h.logAudit(c, "custody_wallet_status_failed", "custody_wallet", &id, meta)
		respondCustodyError(c, err)
		return
	}
	h.logAudit(c, "custody_wallet_status_changed", "custody_wallet", &id, meta)
	c.JSON(http.StatusOK, gin.H{"wallet": walletView(w)})
}

// ---------------- políticas de treasury ----------------

func policyView(p *treasury.Policy) gin.H {
	return gin.H{
		"id": p.ID, "network": p.Network, "asset_code": p.AssetCode,
		"hot_custody_wallet_id": p.HotCustodyWalletID, "treasury_custody_wallet_id": p.TreasuryCustodyWalletID,
		"min_balance": p.MinBalance.String(), "target_balance": p.TargetBalance.String(), "max_balance": p.MaxBalance.String(),
		"rebalance_threshold": p.RebalanceThreshold.String(), "max_rebalance_amount": p.MaxRebalanceAmount.String(),
		"enabled": p.Enabled, "created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
	}
}

type createTreasuryPolicyRequest struct {
	Network                 string `json:"network" binding:"required,max=20"`
	AssetCode               string `json:"asset_code" binding:"required,max=20"`
	HotCustodyWalletID      string `json:"hot_custody_wallet_id" binding:"required"`
	TreasuryCustodyWalletID string `json:"treasury_custody_wallet_id" binding:"required"`
	MinBalance              string `json:"min_balance" binding:"required"`
	TargetBalance           string `json:"target_balance" binding:"required"`
	MaxBalance              string `json:"max_balance" binding:"required"`
	RebalanceThreshold      string `json:"rebalance_threshold" binding:"required"`
	MaxRebalanceAmount      string `json:"max_rebalance_amount" binding:"required"`
	Reason                  string `json:"reason" binding:"required,min=5,max=500"`
}

// POST /api/admin/treasury/policies — siempre se crea DESHABILITADA; activarla
// es una acción aparte (POST .../enabled).
func (h *AdminOpsHandler) CreateTreasuryPolicy(c *gin.Context) {
	var req createTreasuryPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "faltan campos obligatorios de la política (incluye reason)")
		return
	}
	hot, err1 := uuid.Parse(req.HotCustodyWalletID)
	tre, err2 := uuid.Parse(req.TreasuryCustodyWalletID)
	if err1 != nil || err2 != nil {
		badRequest(c, "hot_custody_wallet_id / treasury_custody_wallet_id inválidos")
		return
	}
	dec := func(s string) (decimal.Decimal, error) { return decimal.NewFromString(strings.TrimSpace(s)) }
	minB, e1 := dec(req.MinBalance)
	target, e2 := dec(req.TargetBalance)
	maxB, e3 := dec(req.MaxBalance)
	thr, e4 := dec(req.RebalanceThreshold)
	maxR, e5 := dec(req.MaxRebalanceAmount)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil {
		badRequest(c, "los montos deben ser decimales válidos (cadenas)")
		return
	}
	p, err := h.treasury.CreatePolicy(c.Request.Context(), treasury.CreatePolicyInput{
		Network: domain.Network(req.Network), AssetCode: req.AssetCode,
		HotCustodyWalletID: hot, TreasuryCustodyWalletID: tre,
		MinBalance: minB, TargetBalance: target, MaxBalance: maxB,
		RebalanceThreshold: thr, MaxRebalanceAmount: maxR,
		Enabled: false, Actor: adminActor(c),
	})
	meta := map[string]any{"network": req.Network, "asset_code": req.AssetCode, "reason": req.Reason,
		"hot_custody_wallet_id": hot.String(), "treasury_custody_wallet_id": tre.String()}
	if err != nil {
		meta["error"] = err.Error()
		h.logAudit(c, "treasury_policy_create_failed", "treasury_policy", nil, meta)
		respondTreasuryError(c, err)
		return
	}
	h.logAudit(c, "treasury_policy_created", "treasury_policy", &p.ID, meta)
	c.JSON(http.StatusCreated, gin.H{"policy": policyView(p)})
}

// GET /api/admin/treasury/policies
func (h *AdminOpsHandler) ListTreasuryPolicies(c *gin.Context) {
	rows, err := h.treasury.ListPolicies(c.Request.Context(), treasury.PolicyFilter{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudieron listar las políticas"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, p := range rows {
		out = append(out, policyView(p))
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

type enabledRequest struct {
	Enabled *bool  `json:"enabled" binding:"required"`
	Reason  string `json:"reason" binding:"required,min=5,max=500"`
}

// POST /api/admin/treasury/policies/:id/enabled {enabled, reason}
func (h *AdminOpsHandler) SetTreasuryPolicyEnabled(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "id inválido")
		return
	}
	var req enabledRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		badRequest(c, "enabled (booleano) y reason son obligatorios")
		return
	}
	p, err := h.treasury.SetPolicyEnabled(c.Request.Context(), id, *req.Enabled, adminActor(c))
	meta := map[string]any{"enabled": *req.Enabled, "reason": req.Reason}
	if err != nil {
		meta["error"] = err.Error()
		h.logAudit(c, "treasury_policy_enable_failed", "treasury_policy", &id, meta)
		respondTreasuryError(c, err)
		return
	}
	h.logAudit(c, "treasury_policy_enabled_changed", "treasury_policy", &id, meta)
	c.JSON(http.StatusOK, gin.H{"policy": policyView(p)})
}

func respondTreasuryError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, treasury.ErrPolicyNotFound), errors.Is(err, treasury.ErrWalletNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": err.Error()})
	case errors.Is(err, treasury.ErrDuplicatePolicy), errors.Is(err, treasury.ErrConcurrentPolicyChange):
		c.JSON(http.StatusConflict, gin.H{"error": "conflict", "message": err.Error()})
	case errors.Is(err, treasury.ErrInvalidNetwork), errors.Is(err, treasury.ErrInvalidAsset),
		errors.Is(err, treasury.ErrNegativeAmount), errors.Is(err, treasury.ErrMinExceedsTarget),
		errors.Is(err, treasury.ErrTargetExceedsMax), errors.Is(err, treasury.ErrThresholdNotPositive),
		errors.Is(err, treasury.ErrMaxAmountNotPositive), errors.Is(err, treasury.ErrHotEqualsTreasury),
		errors.Is(err, treasury.ErrWalletWrongType), errors.Is(err, treasury.ErrWalletNotActive),
		errors.Is(err, treasury.ErrWalletNetworkAssetMismatch):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error interno en la política de treasury"})
	}
}

// ---------------- política de gas station ----------------

func gasPolicyView(p *postgres.GasStationPolicy) gin.H {
	return gin.H{"custody_wallet_id": p.CustodyWalletID, "minimum_balance": p.MinimumBalance.String(),
		"target_balance": p.TargetBalance.String(), "max_refill": p.MaxRefill.String(), "enabled": p.Enabled,
		"created_at": p.CreatedAt, "updated_at": p.UpdatedAt}
}

// GET /api/admin/gas-station/policies/:wallet_id
func (h *AdminOpsHandler) GetGasPolicy(c *gin.Context) {
	id, err := uuid.Parse(c.Param("wallet_id"))
	if err != nil {
		badRequest(c, "wallet_id inválido")
		return
	}
	p, err := h.gasPolicies.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo leer la política"})
		return
	}
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"policy": gasPolicyView(p)})
}

type putGasPolicyRequest struct {
	MinimumBalance string `json:"minimum_balance" binding:"required"`
	TargetBalance  string `json:"target_balance" binding:"required"`
	MaxRefill      string `json:"max_refill" binding:"required"`
	Enabled        bool   `json:"enabled"`
	Reason         string `json:"reason" binding:"required,min=5,max=500"`
}

// PUT /api/admin/gas-station/policies/:wallet_id — la wallet debe ser de tipo
// GAS y estar activa.
func (h *AdminOpsHandler) PutGasPolicy(c *gin.Context) {
	id, err := uuid.Parse(c.Param("wallet_id"))
	if err != nil {
		badRequest(c, "wallet_id inválido")
		return
	}
	var req putGasPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "minimum_balance, target_balance, max_refill (cadenas decimales) y reason son obligatorios")
		return
	}
	minB, e1 := decimal.NewFromString(strings.TrimSpace(req.MinimumBalance))
	target, e2 := decimal.NewFromString(strings.TrimSpace(req.TargetBalance))
	maxR, e3 := decimal.NewFromString(strings.TrimSpace(req.MaxRefill))
	if e1 != nil || e2 != nil || e3 != nil {
		badRequest(c, "los montos deben ser decimales válidos (cadenas)")
		return
	}
	wallet, err := h.custody.Get(c.Request.Context(), id)
	if err != nil {
		respondCustodyError(c, err)
		return
	}
	if wallet.WalletType != custody.WalletTypeGas || wallet.Status != custody.StatusActive {
		badRequest(c, "la wallet debe ser de tipo GAS y estar activa")
		return
	}
	saved, err := h.gasPolicies.Upsert(c.Request.Context(), postgres.GasStationPolicy{
		CustodyWalletID: id, MinimumBalance: minB, TargetBalance: target, MaxRefill: maxR, Enabled: req.Enabled,
	})
	meta := map[string]any{"enabled": req.Enabled, "reason": req.Reason,
		"minimum_balance": minB.String(), "target_balance": target.String(), "max_refill": maxR.String()}
	if err != nil {
		meta["error"] = err.Error()
		h.logAudit(c, "gas_station_policy_put_failed", "custody_wallet", &id, meta)
		if errors.Is(err, postgres.ErrGasStationPolicyInvalid) {
			badRequest(c, err.Error())
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo guardar la política"})
		return
	}
	h.logAudit(c, "gas_station_policy_saved", "custody_wallet", &id, meta)
	c.JSON(http.StatusOK, gin.H{"policy": gasPolicyView(saved)})
}
