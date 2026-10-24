package domain

import "context"

// ---- Asset registry ----
//
// This is the piece that was missing before this block: a single place
// that answers "which assets exist on which network, and what are their
// on-chain specifics" — instead of that information being hardcoded inside
// a single network's client config (as USDTContractAddress/USDTDecimals
// used to live inside bscclient.Config, implicitly assuming BSC only ever
// carries one token).
//
// A "network asset" is the tuple the spec asks to keep distinct:
// Blockchain/Network + Native Asset/Token + Contract Address + Token
// Standard + Decimals. AssetCode ("USDT", "USDC", "BTC", "ETH", "BNB", ...)
// is the ledger-facing identifier and already exists as the `assets` table
// (code, name, is_enabled). NetworkAsset is the network-specific row that
// says "this asset, on this network, looks like this on-chain" — the same
// AssetCode can have many NetworkAsset rows (USDT on Ethereum as ERC-20
// with 6 decimals, USDT on BSC as BEP-20 with 18 decimals, USDT on TRON as
// TRC-20 with 6 decimals — never mixed together).

type TokenStandard string

const (
	TokenStandardNone  TokenStandard = ""       // native coin, not a token
	TokenStandardERC20 TokenStandard = "ERC-20" // Ethereum, Polygon, Arbitrum, Optimism, Base
	TokenStandardBEP20 TokenStandard = "BEP-20" // BNB Smart Chain
	TokenStandardTRC20 TokenStandard = "TRC-20" // TRON
)

// NetworkAsset is one row of the asset registry: a specific asset as it
// exists on a specific network.
type NetworkAsset struct {
	Network         Network
	AssetCode       string
	Kind            AssetKind // native or token
	ContractAddress string    // empty for native assets
	Standard        TokenStandard
	Decimals        int32
	Enabled         bool
	// ConfirmationsRequired lets each (network, asset) pair override the
	// network-wide default if ever needed. Zero means "use the network's
	// default confirmations count", never "zero confirmations required".
	ConfirmationsRequired uint64
}

// AssetRegistry is the port application code (watchers, balance queries,
// withdrawal services) uses to discover what to scan/support on a given
// network, instead of a hardcoded single-token assumption. Implementations
// may be backed by Postgres (infrastructure/postgres.NetworkAssetRepository)
// or, for tests, an in-memory map.
type AssetRegistry interface {
	// ListEnabled returns every enabled NetworkAsset for `network`,
	// including its native asset if one is registered. Watchers use this
	// to know which contracts to scan and which native coin to track.
	ListEnabled(ctx context.Context, network Network) ([]NetworkAsset, error)

	// Get returns a single NetworkAsset, or found=false if none is
	// registered (enabled or not) for that (network, assetCode) pair.
	Get(ctx context.Context, network Network, assetCode string) (asset NetworkAsset, found bool, err error)
}
