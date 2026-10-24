// Package custody is the Application-layer home for Bloque 7.1: the
// custody registry for Korzadi's OWN infrastructure wallets (HOT / GAS /
// TREASURY), kept explicitly separate from every user-facing wallet
// concept (application/wallet, application/crypto, the `wallets` /
// `wallet_accounts` / `crypto_addresses` tables).
//
// This package intentionally does NOT implement gas station automation,
// sweeps, treasury rebalancing, reconciliation, or any worker/scheduler —
// it is only the registry those future blocks (7.2–7.6) will build on top
// of (spec: "dejar una base limpia para que esos bloques puedan utilizar
// el registry posteriormente").
package custody

import (
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
)

// WalletType is the operational role of a custody wallet.
type WalletType string

const (
	WalletTypeHot      WalletType = "HOT"
	WalletTypeGas      WalletType = "GAS"
	WalletTypeTreasury WalletType = "TREASURY"

	// COLD, WARM, STAKING, RECOVERY and FEE are explicitly NOT implemented
	// yet (spec: "No ampliar el alcance").
)

// Status mirrors custody_wallets.status. Deliberately the same vocabulary
// crypto_addresses already uses (active/disabled/retired) since Bloque 3 —
// no duplicate state machine with different names for the same meaning.
type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
	StatusRetired  Status = "retired"
)

// CustodyWallet is the public-facing view of a custody_wallets row. It
// deliberately excludes EncryptedPrivateKey — a custody wallet's key
// material must never appear in an API response, log line, or test
// fixture assertion (spec: "Nunca incluir material secreto en... API
// responses").
type CustodyWallet struct {
	ID          uuid.UUID
	Network     domain.Network
	AssetCode   string
	WalletType  WalletType
	Address     string
	AddressType string
	Status      Status
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// RegisterInput is everything needed to register a new custody wallet.
// PrivateKeyPlaintext is encrypted by the service before it ever reaches
// the repository/database — it is never persisted or logged as-is.
type RegisterInput struct {
	Network             domain.Network
	AssetCode           string
	WalletType          WalletType
	Address             string
	AddressType         string // "evm" | "bitcoin_segwit" | "tron"
	PrivateKeyPlaintext string
	// Actor identifies who/what triggered this registration, for the
	// audit trail (e.g. "admin:jdoe", "migration-script"). Defaults to
	// "system" when empty.
	Actor string
}

// ListFilter narrows List(); every non-nil field is ANDed.
type ListFilter struct {
	Network    *domain.Network
	AssetCode  *string
	WalletType *WalletType
	Status     *Status
}
