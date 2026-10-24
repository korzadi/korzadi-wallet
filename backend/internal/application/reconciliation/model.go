// Package reconciliation implements Bloque 7.5: Reconciliation.
//
// This block NEVER modifies a balance. It only compares three views of the
// same money and records what it finds for a human to act on:
//
//	Blockchain  (live on-chain balance of a custody wallet)
//	    ↕
//	Custody     (what custody/gas/sweeps/treasury's own tracked movements
//	             say that balance SHOULD be)
//	    ↕
//	Ledger      (what the Ledger says Korzadi owes its users, per asset)
//
// Two independent legs, run separately (spec: "Implementar reconciliación
// de: Blockchain → Custody" and "Custody → Ledger" as two named checks):
//
//  1. Blockchain → Custody (per custody wallet): does the wallet's live
//     on-chain balance match a baseline plus every COMPLETED movement this
//     codebase itself tracked for it (sweeps landing, treasury rebalances
//     in/out, gas station refills out)? A drift here means either an
//     untracked external movement (a key compromise, a manual/off-system
//     transfer) or a bug in one of 7.1–7.4.
//
//  2. Custody → Ledger (per asset, across networks): do the funds Korzadi
//     actually holds for that asset in OPERATIONAL (HOT) + TREASURY
//     custody wallets cover what the Ledger says users are entitled to
//     withdraw? GAS funds are deliberately excluded from this comparison —
//     spec: "No asumir que todos los fondos de custody pertenecen a
//     usuarios" — gas is Korzadi's own operating expense pool, never a
//     user asset. A shortfall here (custody < ledger) is the single most
//     serious finding this package can produce: it means Korzadi
//     literally does not hold enough on-chain to cover what it owes.
//
// Every check is idempotent and re-runnable: the same (scope, subject)
// pair always updates the SAME row (reconciliation_checks), never inserts
// a duplicate — see postgres.ReconciliationRepository.Upsert.
//
// States (spec 7.5): MATCHED, MISMATCH, PENDING_REVIEW, RESOLVED. A fresh
// mismatch is recorded as MISMATCH; if the SAME check is still mismatched
// on the NEXT pass (ruling out a one-off timing blip between reading the
// chain and reading tracked movements), it is escalated to
// PENDING_REVIEW. Only an explicit operator action
// (Service.ResolveCheck), which re-verifies the live numbers itself before
// writing anything, can ever set RESOLVED — nothing in this package
// resolves a mismatch on its own (spec: "No marques automáticamente una
// diferencia como resuelta sin evidencia").
package reconciliation

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// Scope names which of the two legs a check belongs to.
type Scope string

const (
	ScopeBlockchainCustody Scope = "blockchain_custody"
	ScopeCustodyLedger     Scope = "custody_ledger"
)

// FundCategory classifies WHOSE money a blockchain_custody check's wallet
// holds — derived directly from custody_wallets.wallet_type, never
// invented separately (spec: "Separar: user funds, operational funds, gas
// funds, treasury funds"). A custody_ledger check does not have a single
// category (it compares operational+treasury against user funds at once)
// so its row leaves this empty and instead breaks the amounts down in its
// own fields (see Check.CustodyLedgerBreakdown).
type FundCategory string

const (
	FundCategoryOperational FundCategory = "operational" // HOT
	FundCategoryTreasury    FundCategory = "treasury"    // TREASURY
	FundCategoryGas         FundCategory = "gas"         // GAS
	FundCategoryUser        FundCategory = "user"        // Ledger account_type='user' totals (custody_ledger leg only)
)

// FundCategoryForWalletType maps a custody_wallets.wallet_type to the
// category a blockchain_custody check records for it.
func FundCategoryForWalletType(walletType string) FundCategory {
	switch walletType {
	case "HOT":
		return FundCategoryOperational
	case "TREASURY":
		return FundCategoryTreasury
	case "GAS":
		return FundCategoryGas
	default:
		return FundCategory(walletType)
	}
}

// Status is the persisted lifecycle of one reconciliation check.
type Status string

const (
	StatusMatched       Status = "matched"
	StatusMismatch      Status = "mismatch"
	StatusPendingReview Status = "pending_review"
	StatusResolved      Status = "resolved"
)

// Check is one row of reconciliation_checks: the latest known state of ONE
// (scope, subject) comparison. "Subject" is a custody wallet for
// blockchain_custody, or an asset code for custody_ledger.
type Check struct {
	ID uuid.UUID

	Scope        Scope
	Network      string // empty for custody_ledger (asset-level, spans networks)
	AssetCode    string
	FundCategory FundCategory

	// CustodyWalletID is set only for ScopeBlockchainCustody.
	CustodyWalletID *uuid.UUID

	// Baseline* let a blockchain_custody check compare CHANGES since a
	// known-good point instead of assuming the on-chain balance was zero
	// before this package ever ran (spec never asks for that assumption,
	// and it would be wrong: HOT/TREASURY/GAS wallets are funded before
	// 7.5 exists). Set once, on the FIRST check ever recorded for a
	// wallet, to that run's live balance — from then on, expected =
	// baseline + every tracked movement completed strictly after
	// BaselineAt.
	BaselineAmount decimal.Decimal
	BaselineAt     *time.Time

	ExpectedAmount decimal.Decimal
	ActualAmount   decimal.Decimal
	Difference     decimal.Decimal // ActualAmount - ExpectedAmount (blockchain_custody) or CustodyTotal - LedgerUserTotal (custody_ledger)

	// CustodyLedgerBreakdown fields are populated ONLY for
	// ScopeCustodyLedger — the amounts behind Difference, kept apart per
	// fund category so an operator never has to guess what was compared.
	OperationalFunds decimal.Decimal
	TreasuryFunds    decimal.Decimal
	GasFunds         decimal.Decimal
	UserFunds        decimal.Decimal

	Status Status

	ResolvedAt     *time.Time
	ResolvedBy     string
	ResolutionNote string

	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastCheckedAt time.Time
}

// Event is one append-only audit record (reconciliation_events).
type Event struct {
	Type   string
	Detail map[string]any
}

// RunSummary is what one pass of a leg returns — for logging and for the
// worker's own log line, never persisted as-is (each individual Check IS
// the persisted record).
type RunSummary struct {
	Checked   int
	Matched   int
	Mismatch  int
	Escalated int // MISMATCH -> PENDING_REVIEW this pass
	Errors    int
}

// LedgerIntegrityMismatch reports a derived account field that disagrees
// with its source rows. Reconciliation is read-only and never repairs it.
type LedgerIntegrityMismatch struct {
	WalletAccountID    uuid.UUID
	AssetCode          string
	BalanceCache       decimal.Decimal
	LedgerBalance      decimal.Decimal
	LockedBalance      decimal.Decimal
	ActiveReservations decimal.Decimal
}

// networkAssetKey is an internal grouping key — Network+AssetCode — used
// while aggregating custody balances for the custody_ledger leg (a token
// like USDT can exist on more than one network; the Ledger tracks it as
// ONE asset, so this package must sum across every network before
// comparing, never compare network-by-network against a network-less
// Ledger total).
type networkAssetKey struct {
	Network   domain.Network
	AssetCode string
}
