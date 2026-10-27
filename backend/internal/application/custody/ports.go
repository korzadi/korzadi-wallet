package custody

import (
	"context"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

// Repository is the persistence port for custody wallets — implemented by
// postgres.CustodyWalletRepository. Named here (rather than referencing
// the concrete type directly in Service) so the service can be unit
// tested against a fake instead of a real Postgres instance, the same
// pattern application/wallet.AddressStore already uses.
type Repository interface {
	Create(ctx context.Context, w *postgres.CustodyWallet) (*postgres.CustodyWallet, error)
	Get(ctx context.Context, id uuid.UUID) (*postgres.CustodyWallet, error)
	List(ctx context.Context, f postgres.CustodyWalletFilter) ([]*postgres.CustodyWallet, error)
	// UpdateStatus performs a compare-and-set on (id, expectFrom) -> to.
	// Returns (nil, nil) if the row's current status was not expectFrom.
	UpdateStatus(ctx context.Context, id uuid.UUID, expectFrom, to string) (*postgres.CustodyWallet, error)
}

// Encryptor is the narrow custody-encryption port — exactly
// cryptutil.Encryptor's Encrypt method, the only one this service calls.
// Reused verbatim from application/crypto.Service and application/wallet
// (spec: "Si la arquitectura actual ya dispone de... CustodyEncryptor...
// REUTILÍZALO. NO inventes otro sistema de cifrado").
type Encryptor interface {
	Encrypt(plaintext string, aad cryptutil.AAD) (string, error)
	ActiveKeyID() string
	KeyIDs() []string
	HasRewriter(slot string) bool
}

// AuditLogger is the narrow custody-audit port — the new
// LogCustodyWallet method added to the existing CustodyAuditRepository,
// which reuses custody_audit_logs (via its new custody_wallet_id column)
// instead of a parallel audit table.
type AuditLogger interface {
	LogCustodyWallet(ctx context.Context, custodyWalletID *uuid.UUID, action, actor string, metadata map[string]any) error
}

// NetworkAssetValidator checks that a (network, asset_code) pair is a
// real, enabled entry in network_assets — the asset registry introduced
// in Bloque 1 (migration 0004_multi_chain_asset_registry), most likely
// implemented by an existing internal/infrastructure/assetregistry-style
// component per that migration's own comments. That component was not
// part of the code slice this block was implemented against, so it is
// wired in here as an OPTIONAL port instead of guessed at or duplicated:
//
//   - nil (not wired in): Register() only enforces the DB-level foreign
//     keys (assets.code, hd_wallet_state.network) — it will reject an
//     asset code or network that doesn't exist at all, but not a
//     (network, asset) pair that exists individually yet was never
//     enabled together (e.g. registering a TREASURY wallet for an asset
//     network_assets never enabled on that network).
//   - non-nil: Register() also enforces that stronger pairing check,
//     which is what the spec asks for ("No permitir valores arbitrarios
//     que contradigan: assets, network_assets").
//
// Wire the real implementation in at construction time (NewService) once
// this block is integrated into the full repository.
type NetworkAssetValidator interface {
	IsEnabled(ctx context.Context, network domain.Network, assetCode string) (bool, error)
}
