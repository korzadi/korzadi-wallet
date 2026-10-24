package treasury

import (
	"context"
	"fmt"
	"log"
	"math/big"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	bw "korzadi-wallet/backend/internal/application/blockchainwithdrawal"
	"korzadi-wallet/backend/internal/domain"
)

// EvaluatePolicies is step 1–4 of the 7.4.4 worker loop: load ENABLED
// policies, read live on-chain balances, detect deficit/excess, and create
// AT MOST ONE new pending rebalance per policy per pass (never two — see
// uq_treasury_rebalances_inflight_policy / InFlightForPolicy). It never
// signs or broadcasts anything itself (see process.go / Advance) and it
// NEVER runs while the kill switch is off, mirroring sweep's
// DiscoverAndClaim posture: no new work is ever created while disabled,
// though already-created rebalances keep being tracked to completion by
// the worker's tracking-only pass.
func (s *Service) EvaluatePolicies(ctx context.Context) (evaluated, created int, err error) {
	if !s.deps.Config.Enabled {
		return 0, 0, ErrRebalancingDisabled
	}

	policies, err := s.deps.Policies.List(ctx, PolicyFilter{Enabled: boolPtr(true)})
	if err != nil {
		return 0, 0, fmt.Errorf("treasury: listar políticas habilitadas: %w", err)
	}

	limit := s.deps.Config.PolicyScan
	for i, p := range policies {
		if i >= limit {
			break
		}
		evaluated++
		if s.deps.IsMainnetChain(p.Network) && !s.deps.Config.MainnetEnabled {
			log.Printf("treasury: política %s omitida — red %s es mainnet y TREASURY_REBALANCE_MAINNET_ENABLED=false (fail closed)", p.ID, p.Network)
			continue
		}
		didCreate, evalErr := s.evaluatePolicy(ctx, p)
		if evalErr != nil {
			log.Printf("treasury: evaluación de política %s: %v", p.ID, evalErr)
			continue
		}
		if didCreate {
			created++
		}
	}
	return evaluated, created, nil
}

// evaluatePolicy implements 7.4.2's decision exactly:
//
//	déficit: hot_balance < min_balance
//	         amount = min(target_balance - hot_balance, max_rebalance_amount)
//	         requires amount >= rebalance_threshold AND treasury_balance >= amount
//	exceso:  hot_balance > max_balance
//	         amount = min(hot_balance - target_balance, max_rebalance_amount)
//	         requires amount >= rebalance_threshold
//
// Nothing here signs or moves funds — it only decides WHETHER a rebalance
// is warranted and, if so, persists ONE pending row for the worker to
// advance.
func (s *Service) evaluatePolicy(ctx context.Context, p *Policy) (bool, error) {
	inFlight, err := s.deps.Rebalances.InFlightForPolicy(ctx, p.ID)
	if err != nil {
		return false, fmt.Errorf("consultar rebalanceos en curso: %w", err)
	}
	if inFlight {
		return false, nil
	}

	rt, ok := s.runtimeFor(p.Network)
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrNetworkNotConfigured, p.Network)
	}

	netAsset, found, err := s.deps.Registry.Get(ctx, p.Network, p.AssetCode)
	if err != nil {
		return false, fmt.Errorf("registro de assets: %w", err)
	}
	if !found || !netAsset.Enabled {
		return false, fmt.Errorf("%w: %s/%s", ErrAssetNotEnabled, p.Network, p.AssetCode)
	}

	hotWallet, err := s.deps.Wallets.Get(ctx, p.HotCustodyWalletID)
	if err != nil || hotWallet == nil || hotWallet.Status != "active" {
		return false, fmt.Errorf("%w: HOT %s", ErrWalletNotActive, p.HotCustodyWalletID)
	}
	treasuryWallet, err := s.deps.Wallets.Get(ctx, p.TreasuryCustodyWalletID)
	if err != nil || treasuryWallet == nil || treasuryWallet.Status != "active" {
		return false, fmt.Errorf("%w: TREASURY %s", ErrWalletNotActive, p.TreasuryCustodyWalletID)
	}

	hotBalance, err := s.readBalance(ctx, rt.Chain, netAsset, hotWallet.Address)
	if err != nil {
		return false, fmt.Errorf("leer balance HOT: %w", err)
	}
	treasuryBalance, err := s.readBalance(ctx, rt.Chain, netAsset, treasuryWallet.Address)
	if err != nil {
		return false, fmt.Errorf("leer balance TREASURY: %w", err)
	}

	minWei, err := toRawUnits(p.MinBalance, netAsset.Decimals)
	if err != nil {
		return false, fmt.Errorf("min_balance de política inválida: %w", err)
	}
	targetWei, err := toRawUnits(p.TargetBalance, netAsset.Decimals)
	if err != nil {
		return false, fmt.Errorf("target_balance de política inválida: %w", err)
	}
	maxWei, err := toRawUnits(p.MaxBalance, netAsset.Decimals)
	if err != nil {
		return false, fmt.Errorf("max_balance de política inválida: %w", err)
	}
	thresholdWei, err := toRawUnits(p.RebalanceThreshold, netAsset.Decimals)
	if err != nil {
		return false, fmt.Errorf("rebalance_threshold de política inválido: %w", err)
	}
	maxAmountWei, err := toRawUnits(p.MaxRebalanceAmount, netAsset.Decimals)
	if err != nil {
		return false, fmt.Errorf("max_rebalance_amount de política inválido: %w", err)
	}

	var (
		direction Direction
		reason    Reason
		fromID    uuid.UUID
		fromAddr  string
		toID      uuid.UUID
		toAddr    string
		wantWei   *big.Int
	)

	switch {
	case hotBalance.Cmp(minWei) < 0:
		reason = ReasonDeficit
		direction = DirectionTreasuryToHot
		deficit := new(big.Int).Sub(targetWei, hotBalance)
		wantWei = capAmount(deficit, maxAmountWei)
		if wantWei.Cmp(thresholdWei) < 0 {
			return false, nil // below threshold — no-op, not an error
		}
		if treasuryBalance.Cmp(wantWei) < 0 {
			return false, fmt.Errorf("%w: treasury=%s requerido=%s", ErrInsufficientTreasury, treasuryBalance, wantWei)
		}
		fromID, fromAddr, toID, toAddr = treasuryWallet.ID, treasuryWallet.Address, hotWallet.ID, hotWallet.Address

	case hotBalance.Cmp(maxWei) > 0:
		reason = ReasonExcess
		direction = DirectionHotToTreasury
		excess := new(big.Int).Sub(hotBalance, targetWei)
		wantWei = capAmount(excess, maxAmountWei)
		if wantWei.Cmp(thresholdWei) < 0 {
			return false, nil
		}
		// HOT's ability to also cover its own gas is re-checked live at
		// signing time (process.go), not here — this balance read can be
		// stale by the time the worker actually signs.
		fromID, fromAddr, toID, toAddr = hotWallet.ID, hotWallet.Address, treasuryWallet.ID, treasuryWallet.Address

	default:
		return false, nil // within [min_balance, max_balance] — no-op
	}

	amount := weiToDecimalForAsset(wantWei, netAsset)
	row := &Rebalance{
		PolicyID:            p.ID,
		Network:             p.Network,
		AssetCode:           p.AssetCode,
		AssetKind:           netAsset.Kind,
		Direction:           direction,
		Reason:              reason,
		FromCustodyWalletID: fromID,
		FromAddress:         fromAddr,
		ToCustodyWalletID:   toID,
		ToAddress:           toAddr,
		Amount:              amount,
		Status:              StatusPending,
	}

	createdRow, err := s.deps.Rebalances.Create(ctx, row)
	if err != nil {
		return false, fmt.Errorf("crear rebalanceo: %w", err)
	}
	s.auditRebalance(ctx, createdRow, "treasury_rebalance_detected", map[string]any{
		"hot_balance":      weiToDecimalForAsset(hotBalance, netAsset).String(),
		"treasury_balance": weiToDecimalForAsset(treasuryBalance, netAsset).String(),
		"min_balance":      p.MinBalance.String(),
		"target_balance":   p.TargetBalance.String(),
		"max_balance":      p.MaxBalance.String(),
	})
	log.Printf("treasury: rebalanceo detectado id=%s policy=%s network=%s asset=%s direction=%s reason=%s amount=%s",
		createdRow.ID, p.ID, p.Network, p.AssetCode, direction, reason, amount)
	return true, nil
}

func (s *Service) readBalance(ctx context.Context, chain Chain, na domain.NetworkAsset, address string) (*big.Int, error) {
	if na.Kind == domain.AssetKindToken {
		return chain.TokenBalanceRaw(ctx, na.ContractAddress, address)
	}
	return chain.NativeBalanceWei(ctx, address)
}

func capAmount(amount, max *big.Int) *big.Int {
	if amount.Cmp(max) > 0 {
		return new(big.Int).Set(max)
	}
	return new(big.Int).Set(amount)
}

// toRawUnits converts a policy's human-readable amount to base units,
// reusing blockchainwithdrawal.ToRawUnits (the SAME conversion
// gasstation.toWei already reuses for the identical reason) and treating
// an exact zero specially since ToRawUnits rejects non-positive amounts by
// design — a policy's min_balance, unlike a withdrawal amount, may
// legitimately be 0.
func toRawUnits(amount decimal.Decimal, decimals int32) (*big.Int, error) {
	if amount.IsZero() {
		return big.NewInt(0), nil
	}
	return bw.ToRawUnits(amount, decimals)
}

func weiToDecimalForAsset(raw *big.Int, na domain.NetworkAsset) decimal.Decimal {
	if na.Kind == domain.AssetKindToken {
		scale := decimal.New(1, na.Decimals)
		return decimal.NewFromBigInt(raw, 0).Div(scale)
	}
	return bw.WeiToDecimal(raw)
}

func boolPtr(b bool) *bool { return &b }
