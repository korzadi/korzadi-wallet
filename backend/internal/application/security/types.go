// Package security implements Bloque 8 — Security / Operational Controls /
// Limits: a cross-cutting layer that protects sensitive operations
// (withdrawals, internal transfers, treasury rebalancing, gas refills,
// sweeps and admin actions) with configurable limits, atomic concurrency
// control, rate limiting hooks, security events, emergency freeze and
// kill-switch reporting.
//
// This package deliberately does NOT reimplement anything that already
// exists in the repository:
//   - authentication, JWT and password hashing        -> authjwt, auth
//   - roles / RBAC                                     -> postgres.RoleRepository, middleware.RequireRole
//   - 2FA / TOTP                                        -> twofactor.Service
//   - the Ledger, reservations (balance-level) and idempotent posting -> domain.LedgerRepository / ReservationRepository
//   - the general audit trail                           -> domain.AuditLogger / postgres.AuditRepository
//   - per-asset withdrawal limits (already atomic, already enforced)   -> blockchainwithdrawal.Config / Store.CreateIfAbsent
//   - per-subsystem kill switches (WITHDRAWALS_ENABLED, SWEEPS_ENABLED,
//     TREASURY_REBALANCE_ENABLED, ...)                  -> each subsystem's own Config
//
// It adds what those pieces do not cover on their own: a single place to
// define and enforce limits per (operation, user, asset), a concurrency-safe
// reserve/commit/release primitive for operations that had none (starting
// with internal transfers), a dedicated security-events log, a runtime
// (no-restart) emergency freeze, and configuration/secret validation
// helpers used at startup.
package security

import "github.com/shopspring/decimal"

// OperationType enumerates the sensitive operations Bloque 8 controls
// (spec §3). Kept as a closed set of string constants — not an open type —
// so every caller and every SQL CHECK constraint agree on the same
// vocabulary.
type OperationType string

const (
	OpWithdrawal        OperationType = "WITHDRAWAL"
	OpTransfer          OperationType = "TRANSFER"
	OpTreasuryRebalance OperationType = "TREASURY_REBALANCE"
	OpGasRefill         OperationType = "GAS_REFILL"
	OpSweep             OperationType = "SWEEP"
	OpAdminOperation    OperationType = "ADMIN_OPERATION"
)

// Valid reports whether op is one of the known operation types.
func (op OperationType) Valid() bool {
	switch op {
	case OpWithdrawal, OpTransfer, OpTreasuryRebalance, OpGasRefill, OpSweep, OpAdminOperation:
		return true
	default:
		return false
	}
}

// Category separates who/what originates an operation (spec §3). It is
// informational + used to pick default authorization rules; it never
// substitutes the role system in postgres.RoleRepository.
type Category string

const (
	CategoryUser    Category = "USER_OPERATION"
	CategorySystem  Category = "SYSTEM_OPERATION"
	CategoryAdmin   Category = "ADMIN_OPERATION"
	CategoryCustody Category = "CUSTODY_OPERATION"
)

// AnyAsset is the sentinel asset_code used for a policy or a usage row that
// aggregates across every asset for a given operation (cross-asset daily/
// monthly limit), matching the '*' default used in the Bloque 8 migration.
const AnyAsset = "*"

// Money is a small helper alias kept local to this package so call sites
// don't need to import shopspring/decimal just to build a security.Amount.
type Money = decimal.Decimal
