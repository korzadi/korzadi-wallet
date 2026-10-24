// Package sweep implements Bloque 7.3: Sweeps.
//
// A sweep moves funds that were ALREADY credited to a user (via the
// existing blockchain deposit flow, application/deposit +
// deposit.BlockchainDepositService) from the physical address where they
// landed on-chain (a crypto_addresses deposit address, Bloque 3) to a
// Korzadi-operated custody wallet (custody_wallets, wallet_type =
// 'TREASURY', Bloque 7.1).
//
// A sweep is deliberately NOT:
//   - a user withdrawal (blockchainwithdrawal / Bloque 6B): that package
//     moves a user's LEDGER balance to an address the user names, debiting
//     their wallet_account. A sweep never touches the Ledger at all — the
//     credit already happened when the deposit was confirmed (see
//     deposit.BlockchainDepositService.ConfirmBlockchainDeposit), and that
//     accounting entry does not depend on which physical address the funds
//     currently sit at on-chain.
//   - Gas Station (Bloque 7.2): that package tops addresses UP with native
//     gas. A sweep drains a deposit address DOWN to (near) zero. A sweep
//     may ask Gas Station for gas (via the exact same GasTopUp port
//     blockchainwithdrawal already uses) when the deposit address itself
//     lacks the native coin to pay for its own sweep transaction.
//   - Treasury/Rebalancing (Bloque 7.4, NOT implemented here): moving
//     funds BETWEEN custody wallets (e.g. TREASURY → COLD) is out of
//     scope. A sweep's destination is always resolved fresh from
//     custody_wallets; nothing here decides how much TREASURY should hold.
//
// One sweep = one row of `sweeps`, walking a persisted state machine built
// on exactly the same primitives Bloque 6B/7.2 already use: PostgreSQL
// compare-and-set (Store.Apply), the shared evm_nonces table (NonceStore),
// the shared EVM signer (blockchainwithdrawal.Signer), and the shared
// custody audit log (custody_audit_logs via custody_wallet_id). Nothing in
// this package invents a second signer, nonce manager, broadcaster,
// encryption scheme or audit trail.
package sweep

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// Status is the persisted lifecycle state of a sweep.
type Status string

const (
	// StatusPending: the candidate was validated (custody destination
	// exists, asset enabled, on-chain balance above dust) and the row was
	// claimed via the unique in-flight index — nothing on-chain yet.
	StatusPending Status = "pending"
	// StatusProcessing: preparing the transaction — funding gas if needed,
	// allocating the nonce, signing. A signed raw transaction may already
	// be persisted.
	StatusProcessing Status = "processing"
	// StatusBroadcasted: the network accepted the transaction; tx hash exists.
	StatusBroadcasted Status = "broadcasted"
	// StatusConfirming: mined, waiting for the configured confirmations.
	StatusConfirming Status = "confirming"
	// StatusCompleted: confirmations reached. Terminal.
	StatusCompleted Status = "completed"
	// StatusFailed: cannot continue automatically. Terminal — see
	// NeedsReview to distinguish "safely abandoned before anything was
	// sent" from "needs a human because funds may have moved on-chain".
	StatusFailed Status = "failed"
)

func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed
}

// activeStatuses are the states the worker must keep advancing.
var activeStatuses = []Status{StatusPending, StatusProcessing, StatusBroadcasted, StatusConfirming}

// trackingStatuses are the states that only OBSERVE the chain (never send a
// new transaction) — the only ones the worker touches when sweeps are
// disabled (kill switch), mirroring blockchainwithdrawal's own posture.
var trackingStatuses = []Status{StatusBroadcasted, StatusConfirming}

var allowedTransitions = map[Status][]Status{
	StatusPending:     {StatusProcessing, StatusFailed},
	StatusProcessing:  {StatusBroadcasted, StatusFailed},
	StatusBroadcasted: {StatusConfirming, StatusFailed},
	// confirming → broadcasted happens when a reorg drops the receipt.
	StatusConfirming: {StatusCompleted, StatusBroadcasted, StatusFailed},
}

// CanTransition reports whether from → to is a legal state change.
func CanTransition(from, to Status) bool {
	for _, allowed := range allowedTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Sweep is the domain model of one sweep operation.
type Sweep struct {
	ID uuid.UUID

	Network   domain.Network
	AssetCode string
	AssetKind domain.AssetKind

	FromAddress         string
	FromUserID          uuid.UUID
	FromCryptoAddressID uuid.UUID

	ToCustodyWalletID uuid.UUID
	ToAddress         string

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
	ErrorDetail string // "code: message" — mirrors Withdrawal.ErrorDetail

	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

// FailureCode returns the short machine code of ErrorDetail (the part
// before the first ':'), same convention as blockchainwithdrawal.Withdrawal.
func (s *Sweep) FailureCode() string {
	for i := 0; i < len(s.ErrorDetail); i++ {
		if s.ErrorDetail[i] == ':' {
			return s.ErrorDetail[:i]
		}
	}
	return s.ErrorDetail
}

// Candidate is a (network, asset, source address) tuple discovered from
// confirmed deposits that MIGHT be sweepable — before any balance check,
// dust check, or custody-destination lookup has run. See candidates.go.
type Candidate struct {
	Network             domain.Network
	AssetCode           string
	FromAddress         string
	FromUserID          uuid.UUID
	FromCryptoAddressID uuid.UUID
}
