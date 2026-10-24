package treasury

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ValidatePolicyFields enforces exactly the validation list from spec
// 7.4.1, and nothing more — cross-checks against the actual custody
// wallets (correct wallet_type, matching network/asset, active status) are
// NOT this function's job: they need a repository lookup and live in
// Service.CreatePolicy, against postgres.CustodyWalletRepository, the same
// registry custody/gasstation/sweep already share.
//
//	min_balance >= 0
//	min_balance <= target_balance
//	target_balance <= max_balance
//	rebalance_threshold > 0
//	max_rebalance_amount > 0
//	HOT != TREASURY
func ValidatePolicyFields(in CreatePolicyInput) error {
	network := domain.Network(strings.TrimSpace(string(in.Network)))
	if network == "" {
		return fmt.Errorf("%w: red vacía", ErrInvalidNetwork)
	}
	assetCode := strings.TrimSpace(in.AssetCode)
	if assetCode == "" {
		return fmt.Errorf("%w: asset vacío", ErrInvalidAsset)
	}

	if in.MinBalance.IsNegative() {
		return fmt.Errorf("%w: min_balance", ErrNegativeAmount)
	}
	if in.TargetBalance.IsNegative() {
		return fmt.Errorf("%w: target_balance", ErrNegativeAmount)
	}
	if in.MaxBalance.IsNegative() {
		return fmt.Errorf("%w: max_balance", ErrNegativeAmount)
	}
	if in.MinBalance.GreaterThan(in.TargetBalance) {
		return ErrMinExceedsTarget
	}
	if in.TargetBalance.GreaterThan(in.MaxBalance) {
		return ErrTargetExceedsMax
	}
	if in.RebalanceThreshold.Cmp(decimal.Zero) <= 0 {
		return ErrThresholdNotPositive
	}
	if in.MaxRebalanceAmount.Cmp(decimal.Zero) <= 0 {
		return ErrMaxAmountNotPositive
	}
	if in.HotCustodyWalletID == in.TreasuryCustodyWalletID {
		return ErrHotEqualsTreasury
	}
	return nil
}
