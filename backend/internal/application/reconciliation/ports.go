package reconciliation

import (
	"context"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ---- persistence ----

// Store is the persistence port, implemented by
// postgres.ReconciliationRepository.
type Store interface {
	// Get returns one check by id, or (nil, nil) if not found.
	Get(ctx context.Context, id uuid.UUID) (*Check, error)
	// GetByWallet returns the current blockchain_custody check for a
	// custody wallet, or (nil, nil) if none has ever been recorded.
	GetByWallet(ctx context.Context, custodyWalletID uuid.UUID) (*Check, error)
	// GetByAsset returns the current custody_ledger check for an asset, or
	// (nil, nil) if none has ever been recorded.
	GetByAsset(ctx context.Context, assetCode string) (*Check, error)

	// Upsert writes the FULL current state of one check (the caller has
	// already decided the new Status — Store never decides business
	// logic) and appends ev in the same transaction. It updates the SAME
	// row for a given (scope, subject) instead of ever inserting a
	// duplicate — same idempotency guarantee sweep/treasury get from a
	// unique index, enforced here via ON CONFLICT instead because this is
	// a monotonically-updated snapshot, not a queue of attempts.
	Upsert(ctx context.Context, c *Check, ev Event) (*Check, error)

	// Resolve performs a compare-and-set: applies only if the row's
	// current status is exactly expectFrom (mismatch or pending_review).
	// Returns (nil, nil) if it was not — same CAS discipline as every
	// other Bloque 7 status transition.
	Resolve(ctx context.Context, id uuid.UUID, expectFrom Status, resolvedBy, note string) (*Check, error)

	AddEvent(ctx context.Context, checkID uuid.UUID, eventType string, detail map[string]any) error

	List(ctx context.Context, f Filter) ([]*Check, error)
}

// Filter narrows Store.List(); every non-nil field is ANDed.
type Filter struct {
	Scope  *Scope
	Status *Status
}

// ---- tracked-movements source (blockchain_custody leg) ----

// MovementsSource sums every COMPLETED/CONFIRMED movement this codebase
// itself already tracks for one custody wallet, strictly AFTER `since` —
// implemented in postgres by querying sweeps, treasury_rebalances and
// gas_station_refills directly (7.3/7.4/7.2's own tables; no new
// bookkeeping ledger is introduced for this). Returns net change in the
// asset's own human-readable units (positive = wallet received more than
// it sent since `since`).
type MovementsSource interface {
	NetTrackedChange(ctx context.Context, custodyWalletID uuid.UUID, network domain.Network, assetCode string, since time.Time) (decimal.Decimal, error)
}

// ---- custody wallet resolution ----

// CustodyWallet is the minimal projection of postgres.CustodyWallet (7.1)
// this package needs.
type CustodyWallet struct {
	ID         uuid.UUID
	WalletType string
	Network    domain.Network
	AssetCode  string
	Address    string
	Status     string
}

// CustodyWalletFilter mirrors postgres.CustodyWalletFilter structurally
// (same convention every Bloque 7 package uses to avoid an import cycle
// with infrastructure/postgres, which must import this package to
// implement Store).
type CustodyWalletFilter struct {
	Network    *string
	AssetCode  *string
	WalletType *string
	Status     *string
}

// WalletSource lists custody wallets — the SAME
// *postgres.CustodyWalletRepository instance every other Bloque 7 package
// already shares, via a small adapter (no second custody registry).
type WalletSource interface {
	List(ctx context.Context, f CustodyWalletFilter) ([]*CustodyWallet, error)
}

// ---- chain access (read-only subset — this package never signs or broadcasts) ----

// Chain is the minimal balance-reading surface this package needs,
// redeclared structurally like every other Bloque 7 package's Chain
// interface, so the SAME per-network *evmclient.WithdrawalChain instance
// serves reconciliation too.
type Chain interface {
	NativeBalanceWei(ctx context.Context, address string) (*big.Int, error)
	TokenBalanceRaw(ctx context.Context, contract, address string) (*big.Int, error)
}

// NetworkRuntime bundles what is needed to read balances on one network. A
// network with no runtime cannot be reconciled (fail closed) — same
// contract as every other Bloque 7 NetworkRuntime.
type NetworkRuntime struct {
	Chain Chain
}

// ---- Ledger funds reader (custody_ledger leg) ----
//
// NOTE ON VISIBILITY: this package's context slice includes
// infrastructure/postgres/ledger_repository.go, which confirms
// wallet_accounts has an `account_type` column ('user' | 'system_clearing')
// and a derived `balance_cache` column, and that ledger_entries carries its
// own `asset_code`. It does NOT show wallet_accounts' full column list, so
// TotalUserBalance's implementation (postgres.LedgerFundsRepository)
// assumes wallet_accounts also has an `asset_code` column (one account per
// user per asset — implied by GetBalance/postEntriesTx never filtering by
// asset when reading a single account's balance). Flagged for the
// integrator to confirm against the real wallet_accounts DDL rather than
// silently trusting the assumption.
type LedgerFundsReader interface {
	// TotalUserBalance sums wallet_accounts.balance_cache for every
	// account_type='user' account of one asset — what Korzadi owes its
	// users in that asset, period.
	TotalUserBalance(ctx context.Context, assetCode string) (decimal.Decimal, error)
}

// LedgerIntegrityReader compares account caches with their append-only
// ledger and reservation records.
type LedgerIntegrityReader interface {
	FindLedgerIntegrityMismatches(ctx context.Context) ([]LedgerIntegrityMismatch, error)
}

// AuditLogger is the same shape every Bloque 7 package already declares.
type AuditLogger interface {
	LogCustodyWallet(ctx context.Context, custodyWalletID *uuid.UUID, action, actor string, metadata map[string]any) error
}
