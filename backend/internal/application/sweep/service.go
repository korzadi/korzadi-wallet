package sweep

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
)

// Deps wires every collaborator the sweep service reuses. Every field here
// is a port that is ALSO used by blockchainwithdrawal, gasstation or
// custody — nothing in this list is a new subsystem.
type Deps struct {
	Store          Store
	Candidates     CandidateSource
	Networks       map[domain.Network]NetworkRuntime // reused withdrawalNetworks-shaped map
	Nonces         NonceStore                        // *postgres.EVMNonceRepository (0008)
	Signer         Signer                            // *blockchainwithdrawal.EVMSigner (0008)
	Registry       domain.AssetRegistry              // *postgres.NetworkAssetRepository
	Wallets        CustodyWalletRegistry             // *postgres.CustodyWalletRepository (7.1)
	Audit          AuditLogger                       // *postgres.CustodyAuditRepository (7.1), optional
	Config         Config
	Now            func() time.Time
	InstanceID     string                            // worker identity for locked_by / nonce owner uniqueness across replicas
	IsMainnetChain func(network domain.Network) bool // reused chain-id classifier (same one Bloque 6B uses)
}

func (d Deps) validate() error {
	switch {
	case d.Store == nil:
		return fmt.Errorf("sweep: Store es requerido")
	case d.Candidates == nil:
		return fmt.Errorf("sweep: Candidates es requerido")
	case d.Nonces == nil:
		return fmt.Errorf("sweep: Nonces es requerido")
	case d.Signer == nil:
		return fmt.Errorf("sweep: Signer es requerido")
	case d.Registry == nil:
		return fmt.Errorf("sweep: Registry es requerido")
	case d.Wallets == nil:
		return fmt.Errorf("sweep: Wallets (CustodyWalletRegistry) es requerido")
	}
	return nil
}

// Service orchestrates the sweep lifecycle. It has no HTTP surface — sweeps
// are internal infrastructure and are only ever driven by Worker (spec:
// "NO crear endpoint público para que un usuario dispare un sweep").
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
		deps.InstanceID = "sw-" + uuid.NewString()[:8]
	}
	if deps.IsMainnetChain == nil {
		deps.IsMainnetChain = func(domain.Network) bool { return false }
	}
	// Defensive defaults so a zero-value Config{} (e.g. sweeps left
	// unconfigured on purpose) never produces a zero poll interval — a
	// disabled worker still ticks (to keep observing in-flight sweeps),
	// it simply never starts new ones.
	if deps.Config.PollInterval <= 0 {
		deps.Config.PollInterval = DefaultConfig().PollInterval
	}
	if deps.Config.LeaseDuration <= 0 {
		deps.Config.LeaseDuration = DefaultConfig().LeaseDuration
	}
	if deps.Config.BatchSize <= 0 {
		deps.Config.BatchSize = DefaultConfig().BatchSize
	}
	if deps.Config.CandidateScan <= 0 {
		deps.Config.CandidateScan = DefaultConfig().CandidateScan
	}
	if deps.Config.MaxPrepareTries <= 0 {
		deps.Config.MaxPrepareTries = DefaultConfig().MaxPrepareTries
	}
	if deps.Config.BroadcastGrace <= 0 {
		deps.Config.BroadcastGrace = DefaultConfig().BroadcastGrace
	}
	return &Service{deps: deps, now: deps.Now}, nil
}

func nonceOwner(id uuid.UUID) string { return "sweep:" + id.String() }
func gasOwner(id uuid.UUID) string   { return "sweep-gas:" + id.String() }

// newOwner returns a unique lease token for ONE acquisition (instance id +
// random suffix): a lease that expired and was re-taken by someone else can
// never be freed by its previous holder. Same convention as
// blockchainwithdrawal.Service.newOwner.
func (s *Service) newOwner() string { return s.deps.InstanceID + "/" + uuid.NewString()[:8] }

// sanitizeCause keeps the cause short and single-line so it is safe to
// persist in error_detail / events — never the raw multi-line provider
// error, and never anything that could embed key material. Same discipline
// as blockchainwithdrawal.sanitizeCause.
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

func (s *Service) graceExceeded(since *time.Time) bool {
	return since != nil && s.now().Sub(*since) > s.deps.Config.BroadcastGrace
}

func (s *Service) refresh(ctx context.Context, sw *Sweep) (*Sweep, error) {
	updated, err := s.deps.Store.Get(ctx, sw.ID)
	if err != nil {
		return sw, err
	}
	if updated == nil {
		return sw, ErrNotFound
	}
	return updated, nil
}

// unlease frees the lease with a context that survives the caller's
// cancellation, same reasoning as blockchainwithdrawal.Service.unlease.
func (s *Service) unlease(ctx context.Context, id uuid.UUID, owner string, delay time.Duration, bump bool) {
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.deps.Store.Unlease(bg, id, owner, delay, bump); err != nil {
		log.Printf("sweep: no se pudo liberar el lease id=%s: %v", id, err)
	}
}

// releaseNonce best-effort releases a nonce a sweep allocated but never
// signed with — mirrors blockchainwithdrawal's releaseNonces posture: a
// failure to release is logged, never escalated (the nonce simply expires
// or is reused by NonceStore's own bookkeeping).
func (s *Service) releaseNonce(ctx context.Context, sw *Sweep) {
	if sw.Nonce == nil {
		return
	}
	if err := s.deps.Nonces.Release(ctx, sw.Network, sw.FromAddress, nonceOwner(sw.ID)); err != nil {
		log.Printf("sweep: no se pudo liberar el nonce id=%s: %v", sw.ID, err)
	}
}

// failClosed moves a sweep that never sent anything on-chain to `failed`.
// Safe to retry sweeping the same address later (a fresh candidate scan
// will pick it up again once whatever blocked it is resolved) because
// nothing was ever broadcast.
func (s *Service) failClosed(ctx context.Context, sw *Sweep, code string, cause error) (*Sweep, error) {
	s.releaseNonce(ctx, sw)
	detail := code + ": " + sanitizeCause(cause)
	review := false
	applied, err := s.deps.Store.Apply(ctx, sw.ID, sw.Status, StatusFailed,
		Update{ErrorDetail: &detail, NeedsReview: &review},
		Event{Type: "failed", Detail: map[string]any{"code": code, "cause": sanitizeCause(cause)}})
	if err != nil {
		return sw, err
	}
	if !applied {
		return s.refresh(ctx, sw)
	}
	s.auditWallet(ctx, &sw.ToCustodyWalletID, "sweep_failed", map[string]any{
		"sweep_id": sw.ID.String(), "code": code, "network": string(sw.Network), "asset_code": sw.AssetCode,
	})
	log.Printf("sweep: FALLÓ id=%s network=%s asset=%s from=%s code=%s", sw.ID, sw.Network, sw.AssetCode, sw.FromAddress, code)
	return s.refresh(ctx, sw)
}

// failNeedsReview moves a sweep to `failed` with NeedsReview=true — used
// only once funds MAY have already moved on-chain (nonce consumed, tx
// lost, ambiguous outcome unresolved past the grace period). Nothing here
// is auto-retried; an operator must reconcile before this address can be
// swept again (the in-flight unique index still frees on any terminal
// status, but the review flag is the human signal that something here
// needs eyes before assuming the funds are simply "still there").
func (s *Service) failNeedsReview(ctx context.Context, sw *Sweep, code string, cause error) (*Sweep, error) {
	detail := code + ": " + sanitizeCause(cause)
	review := true
	applied, err := s.deps.Store.Apply(ctx, sw.ID, sw.Status, StatusFailed,
		Update{ErrorDetail: &detail, NeedsReview: &review},
		Event{Type: "needs_review", Detail: map[string]any{"code": code, "cause": sanitizeCause(cause)}})
	if err != nil {
		return sw, err
	}
	if !applied {
		return s.refresh(ctx, sw)
	}
	s.auditWallet(ctx, &sw.ToCustodyWalletID, "sweep_needs_review", map[string]any{
		"sweep_id": sw.ID.String(), "code": code, "network": string(sw.Network), "asset_code": sw.AssetCode, "tx_hash": sw.TxHash,
	})
	log.Printf("sweep: REQUIERE REVISIÓN MANUAL id=%s network=%s asset=%s tx=%s code=%s (NO se libera nada automáticamente)",
		sw.ID, sw.Network, sw.AssetCode, sw.TxHash, code)
	return s.refresh(ctx, sw)
}

func (s *Service) auditWallet(ctx context.Context, custodyWalletID *uuid.UUID, action string, meta map[string]any) {
	if s.deps.Audit == nil {
		return
	}
	// Best-effort: an audit-log failure never blocks or reverses a sweep —
	// same posture custody.Service and gasstation.Service already take.
	_ = s.deps.Audit.LogCustodyWallet(ctx, custodyWalletID, action, "sweep", meta)
}

// runtimeFor returns the configured EVM runtime for a network, or
// ok=false if sweeps are not wired for it (fail closed).
func (s *Service) runtimeFor(network domain.Network) (NetworkRuntime, bool) {
	rt, ok := s.deps.Networks[network]
	return rt, ok
}
