// Package blockchainwithdrawal implements Bloque 6B: on-chain withdrawals.
//
// One withdrawal = one row of blockchain_withdrawals (reused from 0002,
// extended by 0008) walking a persisted state machine:
//
//	pending ─► reserved ─► processing ─► broadcasted ─► confirming ─► completed
//	   │          │            │              │              │
//	   ├──────────┴────────────┴──────────────┴──────────────┴──► failed
//	   └──► cancelled (only from pending/reserved)
//
// Everything critical lives in PostgreSQL. The HTTP request only creates
// the row and reserves funds; a persistent Worker advances every non-final
// withdrawal, so a crash/restart/deploy at ANY point is recoverable.
package blockchainwithdrawal

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// Status is the persisted lifecycle state of a withdrawal.
type Status string

const (
	// StatusPending: request recorded, funds NOT yet protected.
	StatusPending Status = "pending"
	// StatusReserved: amount and fee are protected by Reservations.
	StatusReserved Status = "reserved"
	// StatusProcessing: the transaction is being prepared/signed/sent. A
	// signed raw transaction may already be persisted.
	StatusProcessing Status = "processing"
	// StatusBroadcasted: the network accepted the transaction; tx hash exists.
	StatusBroadcasted Status = "broadcasted"
	// StatusConfirming: mined, waiting for the configured confirmations.
	StatusConfirming Status = "confirming"
	// StatusCompleted: confirmations reached AND ledger settlement done.
	StatusCompleted Status = "completed"
	// StatusFailed: cannot continue automatically (see failure reason /
	// needs_review).
	StatusFailed Status = "failed"
	// StatusCancelled: cancelled before any on-chain step.
	StatusCancelled Status = "cancelled"
)

func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// activeStatuses are the states the worker must keep advancing.
var activeStatuses = []Status{StatusPending, StatusReserved, StatusProcessing, StatusBroadcasted, StatusConfirming}

// trackingStatuses are the states that only OBSERVE the chain (never send a
// new transaction) — the only ones the worker touches when withdrawals are
// disabled (kill switch).
var trackingStatuses = []Status{StatusBroadcasted, StatusConfirming}

var allowedTransitions = map[Status][]Status{
	StatusPending:    {StatusReserved, StatusFailed, StatusCancelled},
	StatusReserved:   {StatusProcessing, StatusFailed, StatusCancelled},
	StatusProcessing: {StatusBroadcasted, StatusFailed},
	// broadcasted → processing: a TRON tx that expired without ever being mined
	// is discarded and signed again (see resource.go). EVM never uses this edge.
	StatusBroadcasted: {StatusConfirming, StatusFailed, StatusProcessing},
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

// FeeAssetCode is the ONLY asset fees are charged in (Bloque 6A contract).
const FeeAssetCode = "USDT"

// Withdrawal is the domain model of one on-chain withdrawal.
type Withdrawal struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Network        domain.Network
	AssetCode      string
	Amount         decimal.Decimal
	ToAddress      string
	FromAddress    string // the user's own deposit address (custody model of Bloque 3)
	Status         Status
	IdempotencyKey string
	TwoFAVerified  bool

	// Fees — always USDT, ALWAYS kept as separate components so both can be
	// reconstructed (Total == Service + EstimatedNetwork, enforced by a DB
	// CHECK).
	FeeQuoteID                *uuid.UUID
	ServiceFeeUSDT            decimal.Decimal
	EstimatedNetworkFeeUSDT   decimal.Decimal
	TotalFeeUSDT              decimal.Decimal
	ActualNetworkFeeUSDT      decimal.NullDecimal // known only after mining; never adjusts what the user paid
	EstimatedNetworkFeeNative decimal.Decimal
	ActualNetworkFeeNative    decimal.NullDecimal
	NativeAssetCode           string

	// Ledger linkage.
	AmountReservationID *uuid.UUID
	FeeReservationID    *uuid.UUID
	LedgerTransactionID *uuid.UUID // settlement transaction

	// On-chain execution state.
	Nonce                 *int64
	GasLimit              *int64
	GasPriceWei           decimal.NullDecimal
	SignedRawTx           []byte
	TxHash                string
	BlockNumber           *int64
	BlockHash             string
	Confirmations         int64
	RequiredConfirmations *int64

	// Worker bookkeeping.
	Attempts      int
	NextAttemptAt *time.Time
	LockedBy      string
	LockedUntil   *time.Time
	SignedAt      *time.Time
	BroadcastedAt *time.Time

	NeedsReview bool
	ErrorDetail string // "code: message" — only the code is ever shown to users

	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

// FailureCode returns the short machine code of ErrorDetail (the part before
// the first ':'), safe to show to the user.
func (w *Withdrawal) FailureCode() string {
	for i := 0; i < len(w.ErrorDetail); i++ {
		if w.ErrorDetail[i] == ':' {
			return w.ErrorDetail[:i]
		}
	}
	return w.ErrorDetail
}
