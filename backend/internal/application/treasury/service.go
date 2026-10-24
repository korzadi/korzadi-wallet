package treasury

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
)

// Deps wires every collaborator the treasury service reuses. Every field
// here is a port ALSO used by custody, gasstation or sweep — nothing in
// this list is a new subsystem (spec: "NO dupliques infraestructura
// existente").
type Deps struct {
	Policies   PolicyStore
	Rebalances RebalanceStore
	Wallets    WalletRegistry                    // *postgres.CustodyWalletRepository (7.1), via a small adapter
	Networks   map[domain.Network]NetworkRuntime // reused withdrawalNetworks/sweepNetworks-shaped map
	Nonces     NonceStore                        // *postgres.EVMNonceRepository (0008)
	Registry   domain.AssetRegistry              // *postgres.NetworkAssetRepository
	Decryptor  Decryptor                         // custody encryptor's Decrypt, reused verbatim
	Audit      AuditLogger                       // *postgres.CustodyAuditRepository (7.1), optional

	Config Config

	Now            func() time.Time
	InstanceID     string // worker identity for locked_by / nonce owner uniqueness across replicas
	IsMainnetChain func(network domain.Network) bool
}

func (d Deps) validate() error {
	switch {
	case d.Policies == nil:
		return fmt.Errorf("treasury: Policies es requerido")
	case d.Rebalances == nil:
		return fmt.Errorf("treasury: Rebalances es requerido")
	case d.Wallets == nil:
		return fmt.Errorf("treasury: Wallets es requerido")
	case d.Nonces == nil:
		return fmt.Errorf("treasury: Nonces es requerido")
	case d.Registry == nil:
		return fmt.Errorf("treasury: Registry es requerido")
	case d.Decryptor == nil:
		return fmt.Errorf("treasury: Decryptor es requerido")
	}
	return nil
}

// Service is the application-layer entry point for Bloque 7.4. Its policy
// CRUD half is safe to expose behind an internal/admin HTTP surface (no
// funds move from creating/reading/enabling a policy). Its rebalancing
// half (EvaluatePolicies + Advance) has NO public HTTP trigger — same
// posture as sweep.Service — and is only ever driven by Worker.
type Service struct {
	deps Deps
	now  func() time.Time
}

func NewService(deps Deps) (*Service, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.InstanceID == "" {
		deps.InstanceID = "tr-" + uuid.NewString()[:8]
	}
	if deps.IsMainnetChain == nil {
		deps.IsMainnetChain = func(domain.Network) bool { return false }
	}
	if deps.Config.PollInterval <= 0 {
		deps.Config.PollInterval = DefaultConfig().PollInterval
	}
	if deps.Config.LeaseDuration <= 0 {
		deps.Config.LeaseDuration = DefaultConfig().LeaseDuration
	}
	if deps.Config.BatchSize <= 0 {
		deps.Config.BatchSize = DefaultConfig().BatchSize
	}
	if deps.Config.PolicyScan <= 0 {
		deps.Config.PolicyScan = DefaultConfig().PolicyScan
	}
	return &Service{deps: deps, now: deps.Now}, nil
}

// ---- 7.4.1: Policy CRUD ----

// CreatePolicy validates the field-level rules (validate.go) AND
// cross-checks both referenced custody wallets: they must exist, be
// ACTIVE, carry the EXACT wallet_type the role requires (spec: "La wallet
// operacional debe ser realmente wallet_type = HOT y la reserva
// wallet_type = TREASURY" — never wallet_accounts or
// crypto_addresses.is_hot_wallet, which this package never even queries),
// and match the policy's own network/asset_code.
func (s *Service) CreatePolicy(ctx context.Context, in CreatePolicyInput) (*Policy, error) {
	if err := ValidatePolicyFields(in); err != nil {
		return nil, err
	}
	network := domain.Network(strings.TrimSpace(string(in.Network)))
	assetCode := strings.TrimSpace(in.AssetCode)

	// Literal "HOT"/"TREASURY" strings, NOT custody.WalletTypeHot/
	// custody.WalletTypeTreasury: importing the custody package here would
	// pull in whatever custody/ports.go itself imports (in this repo,
	// that reaches back into infrastructure/postgres), and
	// infrastructure/postgres already imports THIS package to implement
	// treasury.PolicyStore/RebalanceStore — an import cycle. Every other
	// Bloque 7 package (gasstation, sweep) already avoids importing
	// custody's enum for the exact same reason; this keeps that same
	// convention instead of being the one exception.
	hot, err := s.resolveWalletForRole(ctx, in.HotCustodyWalletID, "HOT", network, assetCode)
	if err != nil {
		return nil, err
	}
	treasury_, err := s.resolveWalletForRole(ctx, in.TreasuryCustodyWalletID, "TREASURY", network, assetCode)
	if err != nil {
		return nil, err
	}

	row := &Policy{
		Network:                 network,
		AssetCode:               assetCode,
		HotCustodyWalletID:      hot.ID,
		TreasuryCustodyWalletID: treasury_.ID,
		MinBalance:              in.MinBalance,
		TargetBalance:           in.TargetBalance,
		MaxBalance:              in.MaxBalance,
		RebalanceThreshold:      in.RebalanceThreshold,
		MaxRebalanceAmount:      in.MaxRebalanceAmount,
		Enabled:                 in.Enabled,
	}

	created, err := s.deps.Policies.Create(ctx, row)
	if err != nil {
		return nil, fmt.Errorf("treasury: crear política: %w", err)
	}

	s.auditWallet(ctx, &created.HotCustodyWalletID, "treasury_policy_created", map[string]any{
		"policy_id":                  created.ID.String(),
		"network":                    string(network),
		"asset_code":                 assetCode,
		"hot_custody_wallet_id":      created.HotCustodyWalletID.String(),
		"treasury_custody_wallet_id": created.TreasuryCustodyWalletID.String(),
		"min_balance":                created.MinBalance.String(),
		"target_balance":             created.TargetBalance.String(),
		"max_balance":                created.MaxBalance.String(),
		"enabled":                    created.Enabled,
	}, actorOrSystem(in.Actor))
	return created, nil
}

func (s *Service) resolveWalletForRole(ctx context.Context, id uuid.UUID, wantType string, network domain.Network, assetCode string) (*CustodyWallet, error) {
	if id == uuid.Nil {
		return nil, fmt.Errorf("%w: id requerido para %s", ErrInvalidRequest, wantType)
	}
	w, err := s.deps.Wallets.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("treasury: resolver custody wallet %s: %w", wantType, err)
	}
	if w == nil {
		return nil, fmt.Errorf("%w: %s (%s)", ErrWalletNotFound, id, wantType)
	}
	if w.WalletType != wantType {
		return nil, fmt.Errorf("%w: se esperaba %s, la wallet %s es %s", ErrWalletWrongType, wantType, id, w.WalletType)
	}
	if w.Status != "active" {
		return nil, fmt.Errorf("%w: %s (status=%s)", ErrWalletNotActive, id, w.Status)
	}
	if domain.Network(w.Network) != network || w.AssetCode != assetCode {
		return nil, fmt.Errorf("%w: wallet %s es %s/%s, política es %s/%s", ErrWalletNetworkAssetMismatch, id, w.Network, w.AssetCode, network, assetCode)
	}
	return w, nil
}

func (s *Service) GetPolicy(ctx context.Context, id uuid.UUID) (*Policy, error) {
	p, err := s.deps.Policies.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("treasury: obtener política: %w", err)
	}
	if p == nil {
		return nil, ErrPolicyNotFound
	}
	return p, nil
}

func (s *Service) ListPolicies(ctx context.Context, f PolicyFilter) ([]*Policy, error) {
	rows, err := s.deps.Policies.List(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("treasury: listar políticas: %w", err)
	}
	return rows, nil
}

// SetPolicyEnabled is the ONLY way to turn a policy's automated
// rebalancing on/off — a deliberate, auditable, compare-and-set operator
// action, mirroring custody.Service.UpdateStatus's CAS discipline. It
// never re-validates wallets/thresholds (those cannot change via this
// call) — only the enabled flag.
func (s *Service) SetPolicyEnabled(ctx context.Context, id uuid.UUID, enabled bool, actor string) (*Policy, error) {
	current, err := s.GetPolicy(ctx, id)
	if err != nil {
		return nil, err
	}
	if current.Enabled == enabled {
		return current, nil
	}
	updated, err := s.deps.Policies.SetEnabled(ctx, id, current.Enabled, enabled)
	if err != nil {
		return nil, fmt.Errorf("treasury: actualizar enabled: %w", err)
	}
	if updated == nil {
		return nil, ErrConcurrentPolicyChange
	}
	action := "treasury_policy_disabled"
	if enabled {
		action = "treasury_policy_enabled"
	}
	s.auditWallet(ctx, &updated.HotCustodyWalletID, action, map[string]any{
		"policy_id": updated.ID.String(),
	}, actorOrSystem(actor))
	return updated, nil
}

// ---- shared helpers (same conventions as custody/gasstation/sweep) ----

func actorOrSystem(actor string) string {
	if strings.TrimSpace(actor) == "" {
		return "system"
	}
	return actor
}

func nonceOwner(id uuid.UUID) string { return "treasury:" + id.String() }
func gasOwner(id uuid.UUID) string   { return "treasury-gas:" + id.String() }

// newOwner returns a unique lease token for ONE acquisition — same
// convention as sweep.Service.newOwner / blockchainwithdrawal.Service.newOwner.
func (s *Service) newOwner() string { return s.deps.InstanceID + "/" + uuid.NewString()[:8] }

// sanitizeCause keeps the cause short, single-line and safe to persist —
// duplicated byte-for-byte (not imported) from sweep.sanitizeCause /
// blockchainwithdrawal.sanitizeCause, same "no necessity to couple 6B/7.3
// just to reuse an unexported helper" reasoning sweep already documents.
func sanitizeCause(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = strings.ReplaceAll(msg, "\r\n", " ")
	msg = strings.ReplaceAll(msg, "\n", " ")
	msg = strings.ReplaceAll(msg, "\r", " ")
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}

func (s *Service) refresh(ctx context.Context, r *Rebalance) (*Rebalance, error) {
	updated, err := s.deps.Rebalances.Get(ctx, r.ID)
	if err != nil {
		return r, err
	}
	if updated == nil {
		return r, ErrNotFound
	}
	return updated, nil
}

func (s *Service) unlease(ctx context.Context, id uuid.UUID, owner string, delay time.Duration, bump bool) {
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.deps.Rebalances.Unlease(bg, id, owner, delay, bump); err != nil {
		log.Printf("treasury: no se pudo liberar el lease id=%s: %v", id, err)
	}
}

func (s *Service) releaseNonce(ctx context.Context, r *Rebalance) {
	if r.Nonce == nil {
		return
	}
	if err := s.deps.Nonces.Release(ctx, r.Network, r.FromAddress, nonceOwner(r.ID)); err != nil {
		log.Printf("treasury: no se pudo liberar el nonce id=%s: %v", r.ID, err)
	}
}

// failClosed moves a rebalance that never sent anything on-chain to
// `failed`. Safe: the NEXT evaluation pass will simply reconsider the
// policy (it may or may not still be in deficit/excess) rather than
// treating this as a permanent block.
func (s *Service) failClosed(ctx context.Context, r *Rebalance, code string, cause error) (*Rebalance, error) {
	s.releaseNonce(ctx, r)
	detail := code + ": " + sanitizeCause(cause)
	review := false
	applied, err := s.deps.Rebalances.Apply(ctx, r.ID, r.Status, StatusFailed,
		Update{ErrorDetail: &detail, NeedsReview: &review},
		Event{Type: "failed", Detail: map[string]any{"code": code, "cause": sanitizeCause(cause)}})
	if err != nil {
		return r, err
	}
	if !applied {
		return s.refresh(ctx, r)
	}
	s.auditRebalance(ctx, r, "treasury_rebalance_failed", map[string]any{"code": code})
	log.Printf("treasury: rebalanceo FALLÓ id=%s network=%s asset=%s direction=%s code=%s", r.ID, r.Network, r.AssetCode, r.Direction, code)
	return s.refresh(ctx, r)
}

// failNeedsReview is used once funds MAY already have moved on-chain
// (nonce consumed, tx lost, unresolved ambiguity past the grace window) —
// same posture as sweep.Service.failNeedsReview / blockchainwithdrawal's
// equivalent. Nothing here is auto-retried.
func (s *Service) failNeedsReview(ctx context.Context, r *Rebalance, code string, cause error) (*Rebalance, error) {
	detail := code + ": " + sanitizeCause(cause)
	review := true
	applied, err := s.deps.Rebalances.Apply(ctx, r.ID, r.Status, StatusFailed,
		Update{ErrorDetail: &detail, NeedsReview: &review},
		Event{Type: "needs_review", Detail: map[string]any{"code": code, "cause": sanitizeCause(cause)}})
	if err != nil {
		return r, err
	}
	if !applied {
		return s.refresh(ctx, r)
	}
	s.auditRebalance(ctx, r, "treasury_rebalance_needs_review", map[string]any{"code": code, "tx_hash": r.TxHash})
	log.Printf("treasury: rebalanceo REQUIERE REVISIÓN MANUAL id=%s network=%s asset=%s tx=%s code=%s (NO se libera nada automáticamente)",
		r.ID, r.Network, r.AssetCode, r.TxHash, code)
	return s.refresh(ctx, r)
}

func (s *Service) auditWallet(ctx context.Context, custodyWalletID *uuid.UUID, action string, meta map[string]any, actor string) {
	if s.deps.Audit == nil {
		return
	}
	if actor == "" {
		actor = "system:treasury"
	}
	// Best-effort: an audit-log failure never blocks or reverses an
	// operation — same posture custody/gasstation/sweep already take.
	_ = s.deps.Audit.LogCustodyWallet(ctx, custodyWalletID, action, actor, meta)
}

func (s *Service) auditRebalance(ctx context.Context, r *Rebalance, action string, extra map[string]any) {
	meta := map[string]any{
		"rebalance_id": r.ID.String(),
		"policy_id":    r.PolicyID.String(),
		"network":      string(r.Network),
		"asset_code":   r.AssetCode,
		"direction":    string(r.Direction),
		"reason":       string(r.Reason),
		"from_address": r.FromAddress,
		"to_address":   r.ToAddress,
		"amount":       r.Amount.String(),
	}
	for k, v := range extra {
		meta[k] = v
	}
	s.auditWallet(ctx, &r.FromCustodyWalletID, action, meta, "system:treasury")
}

// runtimeFor returns the configured EVM runtime for a network, or
// ok=false if treasury rebalancing is not wired for it (fail closed).
func (s *Service) runtimeFor(network domain.Network) (NetworkRuntime, bool) {
	rt, ok := s.deps.Networks[network]
	return rt, ok
}
