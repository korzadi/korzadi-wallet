// Package treasury implements Bloque 7.4: Treasury / Rebalancing.
//
// This block moves funds BETWEEN two custody_wallets (Bloque 7.1) that
// Korzadi itself controls for one (network, asset_code):
//
//	HOT      — the operational wallet that pays out sweeps/withdrawals/gas
//	           refills day to day. Kept lean on purpose: it is the wallet
//	           most exposed to automated signing.
//	TREASURY — the reserve wallet. Sweeps (Bloque 7.3) already land funds
//	           here; this block is the ONLY thing that ever moves money
//	           back out of TREASURY, and only into the paired HOT wallet.
//
// A rebalance is deliberately NEVER:
//   - HOT      -> any external address
//   - TREASURY -> any external address
//   - Sweeps (7.3): a sweep's destination is always resolved as "the
//     active TREASURY wallet for this network/asset" and never asks a
//     treasury policy for permission — 7.4 governs TREASURY -> HOT only.
//   - Gas Station (7.2): that block tops up the NATIVE gas of arbitrary
//     addresses (deposit addresses, HOT wallets) from a GAS custody
//     wallet. Treasury/Rebalancing moves the ASSET the policy actually
//     governs (which may itself be the native asset, e.g. rebalancing BNB
//     between a HOT and TREASURY wallet — in which case EnsureGasFor is
//     still used, transparently, to make sure the SOURCE wallet has
//     enough native gas to pay for the rebalance transaction itself).
//
// One treasury_policies row = the operator-configured thresholds for one
// (network, asset_code, HOT wallet, TREASURY wallet) combination. One
// treasury_rebalances row = one funds-movement attempt, walking exactly
// the same kind of persisted state machine as sweeps (7.3) and
// blockchain_withdrawals (6B): PostgreSQL compare-and-set, the shared
// evm_nonces table, the shared custody decryption path (same one
// gasstation.Service already uses for a custody-wallet-to-address
// transfer — NOT blockchainwithdrawal.Signer/KeyResolver, which is scoped
// to USER keys in crypto_addresses and does not apply to custody_wallets
// at all), and the shared custody audit log. Nothing in this package
// invents a second signer, nonce manager, broadcaster, encryption scheme
// or audit trail.
package treasury

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// Direction is which way a rebalance moves funds. Both directions stay
// strictly inside the (HOT, TREASURY) pair named by the policy — never to
// an external address (spec: "Nunca: HOT → external, TREASURY →
// external").
type Direction string

const (
	DirectionHotToTreasury Direction = "hot_to_treasury"
	DirectionTreasuryToHot Direction = "treasury_to_hot"
)

// Reason records WHY a rebalance was created, mirroring 7.4.2's two cases.
type Reason string

const (
	// ReasonDeficit: hot_balance < min_balance. Direction is always
	// TREASURY -> HOT.
	ReasonDeficit Reason = "deficit"
	// ReasonExcess: hot_balance > max_balance. Direction is always
	// HOT -> TREASURY.
	ReasonExcess Reason = "excess"
)

// Status is the persisted lifecycle of one rebalance — same vocabulary and
// same shape as sweep.Status (7.3), because the underlying operation is
// the same kind of thing: prepare -> sign -> broadcast -> confirm.
type Status string

const (
	StatusPending     Status = "pending"
	StatusProcessing  Status = "processing"
	StatusBroadcasted Status = "broadcasted"
	StatusConfirming  Status = "confirming"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
)

func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed
}

// ActiveStatuses are the states the worker must keep advancing.
var ActiveStatuses = []Status{StatusPending, StatusProcessing, StatusBroadcasted, StatusConfirming}

// TrackingStatuses only OBSERVE the chain — the sole states touched while
// the kill switch (TREASURY_REBALANCE_ENABLED=false) is off, same posture
// sweep/blockchainwithdrawal already take for their own kill switches.
var TrackingStatuses = []Status{StatusBroadcasted, StatusConfirming}

var allowedTransitions = map[Status][]Status{
	StatusPending:     {StatusProcessing, StatusFailed},
	StatusProcessing:  {StatusBroadcasted, StatusFailed},
	StatusBroadcasted: {StatusConfirming, StatusFailed},
	// confirming -> broadcasted: a reorg dropped the receipt.
	StatusConfirming: {StatusCompleted, StatusBroadcasted, StatusFailed},
}

// CanTransition reports whether from -> to is a legal state change.
func CanTransition(from, to Status) bool {
	for _, allowed := range allowedTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Policy is treasury_policies (7.4.1): the operator-configured rebalancing
// contract for one (network, asset_code, HOT wallet, TREASURY wallet)
// combination.
type Policy struct {
	ID uuid.UUID

	Network   domain.Network
	AssetCode string

	HotCustodyWalletID      uuid.UUID
	TreasuryCustodyWalletID uuid.UUID

	// All amounts are in the asset's own human-readable units — same
	// convention as gas_station_policies (0010) and
	// blockchain_withdrawals — never floats, never base units at this
	// layer (base-unit conversion happens in process.go with the exact
	// same bw.ToRawUnits gasstation already reuses for the identical
	// reason).
	MinBalance         decimal.Decimal
	TargetBalance      decimal.Decimal
	MaxBalance         decimal.Decimal
	RebalanceThreshold decimal.Decimal
	MaxRebalanceAmount decimal.Decimal

	Enabled bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreatePolicyInput is everything needed to register a new treasury
// policy. Validated field-by-field in validate.go before any repository
// call (spec 7.4.1's full validation list) and again cross-checked against
// the two referenced custody wallets in service.go (correct wallet_type,
// same network/asset, active).
type CreatePolicyInput struct {
	Network                 domain.Network
	AssetCode               string
	HotCustodyWalletID      uuid.UUID
	TreasuryCustodyWalletID uuid.UUID
	MinBalance              decimal.Decimal
	TargetBalance           decimal.Decimal
	MaxBalance              decimal.Decimal
	RebalanceThreshold      decimal.Decimal
	MaxRebalanceAmount      decimal.Decimal
	// Enabled defaults to false: a freshly created policy never starts the
	// worker moving funds on its own (spec: "No activar mainnet
	// automáticamente" applied at the policy granularity too — enabling a
	// policy is a deliberate, separate operator action).
	Enabled bool
	Actor   string
}

// PolicyFilter narrows List(); every non-nil field is ANDed.
type PolicyFilter struct {
	Network   *domain.Network
	AssetCode *string
	Enabled   *bool
}

// Rebalance is one treasury_rebalances row: a single funds-movement
// attempt created by the worker for a policy currently in deficit or
// excess.
type Rebalance struct {
	ID       uuid.UUID
	PolicyID uuid.UUID

	Network   domain.Network
	AssetCode string
	AssetKind domain.AssetKind

	Direction Direction
	Reason    Reason

	FromCustodyWalletID uuid.UUID
	FromAddress         string
	ToCustodyWalletID   uuid.UUID
	ToAddress           string

	// Amount is fixed once, from live on-chain balances, at creation time
	// (see Service.evaluatePolicy) and re-validated against a FRESH
	// balance read immediately before signing (process.go) — never
	// assumed to still be correct just because it was computed earlier,
	// same discipline sweeps already apply to their own amount.
	Amount decimal.Decimal
	Status Status

	Nonce                 *int64
	GasLimit              *int64
	GasPriceWei           decimal.NullDecimal
	SignedRawTx           []byte
	TxHash                string
	BlockNumber           *int64
	BlockHash             string
	Confirmations         int64
	RequiredConfirmations *int64

	Attempts      int
	NextAttemptAt *time.Time
	LockedBy      string
	LockedUntil   *time.Time
	SignedAt      *time.Time
	BroadcastedAt *time.Time

	NeedsReview bool
	ErrorDetail string // "code: message" — same convention as sweep.Sweep.ErrorDetail

	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

// FailureCode returns the short machine code of ErrorDetail (the part
// before the first ':'), same convention as sweep.Sweep.FailureCode /
// blockchainwithdrawal.Withdrawal.FailureCode.
func (r *Rebalance) FailureCode() string {
	for i := 0; i < len(r.ErrorDetail); i++ {
		if r.ErrorDetail[i] == ':' {
			return r.ErrorDetail[:i]
		}
	}
	return r.ErrorDetail
}
