package reconciliation

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// Deps wires every collaborator this service reuses. Every field is a port
// ALSO used by custody/gasstation/sweep/treasury — nothing here is a new
// subsystem.
type Deps struct {
	Store     Store
	Movements MovementsSource // reads sweeps/treasury_rebalances/gas_station_refills directly
	Wallets   WalletSource    // *postgres.CustodyWalletRepository (7.1), via a small adapter
	Networks  map[domain.Network]NetworkRuntime
	Registry  domain.AssetRegistry // *postgres.NetworkAssetRepository
	Ledger    LedgerFundsReader    // *postgres.LedgerFundsRepository
	Audit     AuditLogger

	Config Config
	Now    func() time.Time
}

func (d Deps) validate() error {
	switch {
	case d.Store == nil:
		return fmt.Errorf("reconciliation: Store es requerido")
	case d.Movements == nil:
		return fmt.Errorf("reconciliation: Movements es requerido")
	case d.Wallets == nil:
		return fmt.Errorf("reconciliation: Wallets es requerido")
	case d.Registry == nil:
		return fmt.Errorf("reconciliation: Registry es requerido")
	case d.Ledger == nil:
		return fmt.Errorf("reconciliation: Ledger es requerido")
	}
	return nil
}

// Service is the application-layer entry point for Bloque 7.5. It never
// moves funds — see package doc.
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
	if deps.Config.PollInterval <= 0 {
		deps.Config.PollInterval = DefaultConfig().PollInterval
	}
	if deps.Config.BatchSize <= 0 {
		deps.Config.BatchSize = DefaultConfig().BatchSize
	}
	return &Service{deps: deps, now: deps.Now}, nil
}

// ReconcileLedgerIntegrity compares balance_cache and locked_balance to
// their sources. It intentionally does not mutate balances or reservations.
func (s *Service) ReconcileLedgerIntegrity(ctx context.Context) ([]LedgerIntegrityMismatch, error) {
	if !s.deps.Config.Enabled {
		return nil, ErrReconciliationDisabled
	}
	reader, ok := s.deps.Ledger.(LedgerIntegrityReader)
	if !ok {
		return nil, fmt.Errorf("reconciliation: LedgerIntegrityReader no configurado")
	}
	return reader.FindLedgerIntegrityMismatches(ctx)
}

// ---- leg 1: Blockchain -> Custody ----

// ReconcileBlockchainCustody compares, for every ACTIVE custody wallet, its
// live on-chain balance against a baseline plus every tracked movement
// since that baseline. See package doc for the baseline rationale.
func (s *Service) ReconcileBlockchainCustody(ctx context.Context) (RunSummary, error) {
	if !s.deps.Config.Enabled {
		return RunSummary{}, ErrReconciliationDisabled
	}
	wallets, err := s.deps.Wallets.List(ctx, CustodyWalletFilter{Status: strPtr("active")})
	if err != nil {
		return RunSummary{}, fmt.Errorf("reconciliation: listar custody wallets: %w", err)
	}

	var summary RunSummary
	for i, w := range wallets {
		if i >= s.deps.Config.BatchSize {
			break
		}
		summary.Checked++
		if err := s.reconcileWallet(ctx, w, &summary); err != nil {
			summary.Errors++
			log.Printf("reconciliation: wallet %s (%s/%s/%s): %v", w.ID, w.Network, w.AssetCode, w.WalletType, err)
		}
	}
	return summary, nil
}

func (s *Service) reconcileWallet(ctx context.Context, w *CustodyWallet, summary *RunSummary) error {
	rt, ok := s.deps.Networks[w.Network]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNetworkNotConfigured, w.Network)
	}
	netAsset, found, err := s.deps.Registry.Get(ctx, w.Network, w.AssetCode)
	if err != nil {
		return fmt.Errorf("registro de assets: %w", err)
	}
	if !found || !netAsset.Enabled {
		return fmt.Errorf("%w: %s/%s", ErrAssetNotEnabled, w.Network, w.AssetCode)
	}

	actualRaw, err := s.readBalance(ctx, rt.Chain, netAsset, w.Address)
	if err != nil {
		return fmt.Errorf("leer balance on-chain: %w", err)
	}
	actual := weiToDecimalForAsset(actualRaw, netAsset)

	previous, err := s.deps.Store.GetByWallet(ctx, w.ID)
	if err != nil {
		return fmt.Errorf("obtener check previo: %w", err)
	}

	now := s.now()
	var baseline decimal.Decimal
	var baselineAt time.Time
	if previous == nil {
		// First time this wallet is ever reconciled: nothing was tracked
		// before this package existed, so the current on-chain balance IS
		// the baseline — comparing against zero would manufacture a false
		// mismatch on every already-funded wallet.
		baseline = actual
		baselineAt = now
	} else {
		baseline = previous.BaselineAmount
		if previous.BaselineAt != nil {
			baselineAt = *previous.BaselineAt
		} else {
			baselineAt = now
		}
	}

	netChange, err := s.deps.Movements.NetTrackedChange(ctx, w.ID, w.Network, w.AssetCode, baselineAt)
	if err != nil {
		return fmt.Errorf("calcular movimientos rastreados: %w", err)
	}
	expected := baseline.Add(netChange)
	diff := actual.Sub(expected)

	newStatus := s.nextStatus(previous, diff.IsZero())

	c := &Check{
		Scope:           ScopeBlockchainCustody,
		Network:         string(w.Network),
		AssetCode:       w.AssetCode,
		FundCategory:    FundCategoryForWalletType(w.WalletType),
		CustodyWalletID: &w.ID,
		BaselineAmount:  baseline,
		BaselineAt:      &baselineAt,
		ExpectedAmount:  expected,
		ActualAmount:    actual,
		Difference:      diff,
		Status:          newStatus,
		LastCheckedAt:   now,
	}
	if previous != nil {
		c.ID = previous.ID
	}

	ev := s.eventForTransition(previous, newStatus, map[string]any{
		"custody_wallet_id": w.ID.String(), "network": string(w.Network), "asset_code": w.AssetCode,
		"expected": expected.String(), "actual": actual.String(), "difference": diff.String(),
	})
	saved, err := s.deps.Store.Upsert(ctx, c, ev)
	if err != nil {
		return fmt.Errorf("guardar check: %w", err)
	}
	s.tally(summary, previous, saved)
	s.auditTransition(ctx, &w.ID, previous, saved)
	return nil
}

// ---- leg 2: Custody -> Ledger ----

// ReconcileCustodyLedger compares, for every asset the custody registry
// currently holds, live OPERATIONAL (HOT) + TREASURY on-chain balances
// (summed across every network that asset exists on) against the Ledger's
// total user balance for that asset. GAS funds are read and reported but
// deliberately excluded from the comparison itself (see package doc).
func (s *Service) ReconcileCustodyLedger(ctx context.Context) (RunSummary, error) {
	if !s.deps.Config.Enabled {
		return RunSummary{}, ErrReconciliationDisabled
	}
	wallets, err := s.deps.Wallets.List(ctx, CustodyWalletFilter{Status: strPtr("active")})
	if err != nil {
		return RunSummary{}, fmt.Errorf("reconciliation: listar custody wallets: %w", err)
	}

	byAsset := map[string][]*CustodyWallet{}
	for _, w := range wallets {
		byAsset[w.AssetCode] = append(byAsset[w.AssetCode], w)
	}

	var summary RunSummary
	i := 0
	for assetCode, assetWallets := range byAsset {
		if i >= s.deps.Config.BatchSize {
			break
		}
		i++
		summary.Checked++
		if err := s.reconcileAsset(ctx, assetCode, assetWallets, &summary); err != nil {
			summary.Errors++
			log.Printf("reconciliation: asset %s: %v", assetCode, err)
		}
	}
	return summary, nil
}

func (s *Service) reconcileAsset(ctx context.Context, assetCode string, wallets []*CustodyWallet, summary *RunSummary) error {
	var operational, treasuryFunds, gasFunds decimal.Decimal

	for _, w := range wallets {
		rt, ok := s.deps.Networks[w.Network]
		if !ok {
			continue // network not wired for reads — skip, do not fail the whole asset
		}
		netAsset, found, err := s.deps.Registry.Get(ctx, w.Network, w.AssetCode)
		if err != nil || !found || !netAsset.Enabled {
			continue
		}
		raw, err := s.readBalance(ctx, rt.Chain, netAsset, w.Address)
		if err != nil {
			return fmt.Errorf("leer balance de %s en %s: %w", w.WalletType, w.Network, err)
		}
		amount := weiToDecimalForAsset(raw, netAsset)
		switch w.WalletType {
		case "HOT":
			operational = operational.Add(amount)
		case "TREASURY":
			treasuryFunds = treasuryFunds.Add(amount)
		case "GAS":
			gasFunds = gasFunds.Add(amount)
		}
	}

	userTotal, err := s.deps.Ledger.TotalUserBalance(ctx, assetCode)
	if err != nil {
		return fmt.Errorf("leer total de usuarios en el ledger: %w", err)
	}

	custodyTotal := operational.Add(treasuryFunds) // GAS deliberately excluded — see package doc
	diff := custodyTotal.Sub(userTotal)

	previous, err := s.deps.Store.GetByAsset(ctx, assetCode)
	if err != nil {
		return fmt.Errorf("obtener check previo: %w", err)
	}
	// Solvent (custody >= ledger) is MATCHED even with a surplus (expected:
	// treasury buffer). Only a SHORTFALL is a mismatch — see package doc.
	newStatus := s.nextStatus(previous, !diff.IsNegative())

	c := &Check{
		Scope:            ScopeCustodyLedger,
		AssetCode:        assetCode,
		OperationalFunds: operational,
		TreasuryFunds:    treasuryFunds,
		GasFunds:         gasFunds,
		UserFunds:        userTotal,
		ExpectedAmount:   userTotal, // "expected" = what custody must cover
		ActualAmount:     custodyTotal,
		Difference:       diff,
		Status:           newStatus,
		LastCheckedAt:    s.now(),
	}
	if previous != nil {
		c.ID = previous.ID
	}

	ev := s.eventForTransition(previous, newStatus, map[string]any{
		"asset_code": assetCode, "operational_funds": operational.String(), "treasury_funds": treasuryFunds.String(),
		"gas_funds": gasFunds.String(), "user_funds": userTotal.String(), "difference": diff.String(),
	})
	saved, err := s.deps.Store.Upsert(ctx, c, ev)
	if err != nil {
		return fmt.Errorf("guardar check: %w", err)
	}
	s.tally(summary, previous, saved)
	s.auditTransition(ctx, nil, previous, saved)
	return nil
}

// ---- resolution (operator-only, never automatic) ----

// ResolveCheck marks a MISMATCH/PENDING_REVIEW check RESOLVED — but ONLY
// after re-verifying, right now, that the underlying numbers actually
// match again (spec: "No marques automáticamente una diferencia como
// resuelta sin evidencia" — the fresh recheck IS the evidence; a note is
// additionally required so the operator records WHY/HOW it was fixed).
// This is the ONLY path that ever sets RESOLVED — Advance/the worker never
// does.
func (s *Service) ResolveCheck(ctx context.Context, id uuid.UUID, actor, note string) (*Check, error) {
	if strings.TrimSpace(note) == "" {
		return nil, ErrResolutionRequired
	}
	current, err := s.deps.Store.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reconciliation: obtener check: %w", err)
	}
	if current == nil {
		return nil, ErrCheckNotFound
	}
	if current.Status != StatusMismatch && current.Status != StatusPendingReview {
		return nil, ErrNotMismatched
	}

	// Re-verify live, right now — never trust the stored Difference alone.
	var stillMismatched bool
	switch current.Scope {
	case ScopeBlockchainCustody:
		w, err := s.getWallet(ctx, *current.CustodyWalletID)
		if err != nil {
			return nil, err
		}
		var summary RunSummary
		if err := s.reconcileWallet(ctx, w, &summary); err != nil {
			return nil, fmt.Errorf("reconciliation: reverificar wallet: %w", err)
		}
		refreshed, err := s.deps.Store.GetByWallet(ctx, w.ID)
		if err != nil || refreshed == nil {
			return nil, fmt.Errorf("reconciliation: leer check reverificado: %w", err)
		}
		stillMismatched = !refreshed.Difference.IsZero()
	case ScopeCustodyLedger:
		wallets, err := s.deps.Wallets.List(ctx, CustodyWalletFilter{AssetCode: strPtr(current.AssetCode), Status: strPtr("active")})
		if err != nil {
			return nil, fmt.Errorf("reconciliation: listar wallets del asset: %w", err)
		}
		var summary RunSummary
		if err := s.reconcileAsset(ctx, current.AssetCode, wallets, &summary); err != nil {
			return nil, fmt.Errorf("reconciliation: reverificar asset: %w", err)
		}
		refreshed, err := s.deps.Store.GetByAsset(ctx, current.AssetCode)
		if err != nil || refreshed == nil {
			return nil, fmt.Errorf("reconciliation: leer check reverificado: %w", err)
		}
		stillMismatched = refreshed.Difference.IsNegative()
	}
	if stillMismatched {
		return nil, ErrStillMismatched
	}

	// The recheck above already flipped the row back to MATCHED via the
	// normal Upsert path (same as any other pass). Resolve() here is
	// therefore mostly a no-op CAS against a row that is already MATCHED —
	// EXCEPT it additionally records WHO resolved it and WHY, which the
	// automatic recheck never does. If the row is already MATCHED (the
	// common case), record the resolution note as an audit event instead
	// of forcing a CAS from a status it is no longer in.
	final, err := s.deps.Store.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reconciliation: releer check: %w", err)
	}
	if final.Status == StatusMatched {
		_ = s.deps.Store.AddEvent(ctx, id, "reconciliation_resolved", map[string]any{"actor": actor, "note": note})
		return final, nil
	}
	resolved, err := s.deps.Store.Resolve(ctx, id, final.Status, actor, note)
	if err != nil {
		return nil, fmt.Errorf("reconciliation: resolver: %w", err)
	}
	if resolved == nil {
		return nil, ErrConcurrentUpdate
	}
	return resolved, nil
}

func (s *Service) getWallet(ctx context.Context, id uuid.UUID) (*CustodyWallet, error) {
	wallets, err := s.deps.Wallets.List(ctx, CustodyWalletFilter{})
	if err != nil {
		return nil, fmt.Errorf("reconciliation: listar custody wallets: %w", err)
	}
	for _, w := range wallets {
		if w.ID == id {
			return w, nil
		}
	}
	return nil, ErrWalletNotFound
}

// ---- shared helpers ----

// nextStatus implements the escalation rule from the package doc: a fresh
// mismatch is MISMATCH; a mismatch that PERSISTS into a second consecutive
// pass escalates to PENDING_REVIEW; a match always returns to MATCHED
// (never silently stays MISMATCH/PENDING_REVIEW once the numbers agree —
// but note this only ever happens via a real recheck, never a stored
// assumption).
func (s *Service) nextStatus(previous *Check, isMatch bool) Status {
	if isMatch {
		return StatusMatched
	}
	if previous != nil && (previous.Status == StatusMismatch || previous.Status == StatusPendingReview) {
		return StatusPendingReview
	}
	return StatusMismatch
}

func (s *Service) eventForTransition(previous *Check, newStatus Status, detail map[string]any) Event {
	eventType := "reconciliation_matched"
	switch newStatus {
	case StatusMismatch:
		eventType = "reconciliation_mismatch"
	case StatusPendingReview:
		eventType = "reconciliation_review"
	}
	if previous == nil {
		eventType = "reconciliation_started"
	}
	return Event{Type: eventType, Detail: detail}
}

func (s *Service) tally(summary *RunSummary, previous, saved *Check) {
	switch saved.Status {
	case StatusMatched:
		summary.Matched++
	case StatusMismatch:
		summary.Mismatch++
	case StatusPendingReview:
		summary.Mismatch++
		if previous == nil || previous.Status != StatusPendingReview {
			summary.Escalated++
		}
	}
}

func (s *Service) auditTransition(ctx context.Context, walletID *uuid.UUID, previous, saved *Check) {
	if s.deps.Audit == nil {
		return
	}
	if previous != nil && previous.Status == saved.Status {
		return // no change — don't spam the audit log every pass
	}
	action := "reconciliation_matched"
	switch saved.Status {
	case StatusMismatch:
		action = "reconciliation_mismatch_detected"
	case StatusPendingReview:
		action = "reconciliation_escalated_review"
	}
	_ = s.deps.Audit.LogCustodyWallet(ctx, walletID, action, "system:reconciliation", map[string]any{
		"check_id": saved.ID.String(), "scope": string(saved.Scope), "asset_code": saved.AssetCode,
		"difference": saved.Difference.String(),
	})
}

func (s *Service) readBalance(ctx context.Context, chain Chain, na domain.NetworkAsset, address string) (*big.Int, error) {
	if na.Kind == domain.AssetKindToken {
		return chain.TokenBalanceRaw(ctx, na.ContractAddress, address)
	}
	return chain.NativeBalanceWei(ctx, address)
}

func weiToDecimalForAsset(raw *big.Int, na domain.NetworkAsset) decimal.Decimal {
	if na.Kind == domain.AssetKindToken {
		scale := decimal.New(1, na.Decimals)
		return decimal.NewFromBigInt(raw, 0).Div(scale)
	}
	scale := decimal.New(1, 18)
	return decimal.NewFromBigInt(raw, 0).Div(scale)
}

func strPtr(s string) *string { return &s }
