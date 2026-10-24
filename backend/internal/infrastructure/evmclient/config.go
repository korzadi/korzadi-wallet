// Package evmclient implements the single, reusable BlockchainProvider for
// every EVM-compatible network Korzadi Wallet supports: Ethereum, BNB
// Smart Chain, Polygon, Arbitrum, Optimism and Base. It knows nothing
// network-specific beyond what's in Config — chain identity, RPC
// endpoint, and retry/timeout policy. Token identity (contract address,
// decimals, standard) is never held here; it comes from the asset
// registry and is passed into each call.
//
// Design constraints carried over from the original BSC-only client (see
// korzadi spec):
//   - one long-lived *ethclient.Client, dialed once at startup, never
//     re-dialed per request.
//   - every call goes through context.Context with a bounded timeout.
//   - transient failures are retried with exponential backoff, capped.
//   - the chain ID is verified against configuration on every dial, so a
//     misconfigured RPC pointing at the wrong network is caught instead of
//     silently trusted.
//   - nothing here ever logs a raw RPC URL with embedded credentials, a
//     private key, or a secret.
package evmclient

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"korzadi-wallet/backend/internal/domain"
)

// Config is deliberately network-agnostic: the same struct shape
// configures Ethereum, BSC, Polygon, Arbitrum, Optimism or Base — only the
// values differ. Nothing token-specific (contract addresses, decimals)
// lives here; see the asset registry for that.
type Config struct {
	Network        domain.Network
	RPCURL         string
	ChainID        uint64
	RPCTimeout     time.Duration
	RPCMaxRetries  int
	RPCBackoffBase time.Duration
	RPCBackoffMax  time.Duration
	// ConfirmationsRequired is the network-wide default used when an asset
	// in the registry doesn't override it.
	ConfirmationsRequired uint64
}

// NetworkEnvDefaults captures the one thing that legitimately differs per
// network at the "which env vars do I read" level: the variable name
// prefix and the well-known default chain ID (used only for documentation
// / sanity, never trusted over what LoadConfigFromEnv actually reads).
type NetworkEnvDefaults struct {
	Network   domain.Network
	EnvPrefix string // e.g. "BSC", "ETH", "POLYGON"
	MainnetID uint64
}

var knownEVMNetworks = map[domain.Network]NetworkEnvDefaults{
	domain.NetworkBSC:      {Network: domain.NetworkBSC, EnvPrefix: "BSC", MainnetID: 56},
	domain.NetworkETHEREUM: {Network: domain.NetworkETHEREUM, EnvPrefix: "ETH", MainnetID: 1},
	domain.NetworkPOLYGON:  {Network: domain.NetworkPOLYGON, EnvPrefix: "POLYGON", MainnetID: 137},
	domain.NetworkARBITRUM: {Network: domain.NetworkARBITRUM, EnvPrefix: "ARBITRUM", MainnetID: 42161},
	domain.NetworkOPTIMISM: {Network: domain.NetworkOPTIMISM, EnvPrefix: "OPTIMISM", MainnetID: 10},
	domain.NetworkBASE:     {Network: domain.NetworkBASE, EnvPrefix: "BASE", MainnetID: 8453},
}

// KnownEVMNetworks returns every EVM network this build knows how to
// configure from the environment, in a stable order — used by main.go to
// loop over "try to bring up every configured EVM network" without an
// if/else chain per chain.
func KnownEVMNetworks() []NetworkEnvDefaults {
	order := []domain.Network{
		domain.NetworkETHEREUM, domain.NetworkBSC, domain.NetworkPOLYGON,
		domain.NetworkARBITRUM, domain.NetworkOPTIMISM, domain.NetworkBASE,
	}
	out := make([]NetworkEnvDefaults, 0, len(order))
	for _, n := range order {
		out = append(out, knownEVMNetworks[n])
	}
	return out
}

// LoadConfigFromEnv reads {PREFIX}_RPC_URL, {PREFIX}_CHAIN_ID and friends
// for the given network. It returns ok=false (no error) when
// {PREFIX}_RPC_URL is simply unset — that network is optional and the rest
// of the backend must start fine without it, exactly as BSC was optional
// before this block. A malformed value for a var that IS set (bad
// duration, non-numeric chain id) is always an error, never silently
// ignored, since that indicates a real misconfiguration rather than "not
// configured".
func LoadConfigFromEnv(net NetworkEnvDefaults) (cfg Config, ok bool, err error) {
	rpcURL := os.Getenv(net.EnvPrefix + "_RPC_URL")
	if rpcURL == "" {
		return Config{}, false, nil
	}

	cfg = Config{
		Network:               net.Network,
		RPCURL:                rpcURL,
		ChainID:               net.MainnetID,
		RPCTimeout:            10 * time.Second,
		RPCMaxRetries:         3,
		RPCBackoffBase:        250 * time.Millisecond,
		RPCBackoffMax:         5 * time.Second,
		ConfirmationsRequired: defaultConfirmations(net.Network),
	}

	if v := os.Getenv(net.EnvPrefix + "_CHAIN_ID"); v != "" {
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return Config{}, false, fmt.Errorf("%s_CHAIN_ID inválido: %w", net.EnvPrefix, err)
		}
		cfg.ChainID = id
	}
	if v := os.Getenv(net.EnvPrefix + "_CONFIRMATIONS_REQUIRED"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return Config{}, false, fmt.Errorf("%s_CONFIRMATIONS_REQUIRED inválido: %w", net.EnvPrefix, err)
		}
		cfg.ConfirmationsRequired = n
	}
	if net.Network == domain.NetworkETHEREUM && cfg.ConfirmationsRequired < 64 {
		return Config{}, false, fmt.Errorf("ETH_CONFIRMATIONS_REQUIRED debe ser al menos 64")
	}
	if v := os.Getenv(net.EnvPrefix + "_RPC_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, false, fmt.Errorf("%s_RPC_TIMEOUT inválido: %w", net.EnvPrefix, err)
		}
		cfg.RPCTimeout = d
	}
	if v := os.Getenv(net.EnvPrefix + "_RPC_MAX_RETRIES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, false, fmt.Errorf("%s_RPC_MAX_RETRIES inválido: %w", net.EnvPrefix, err)
		}
		cfg.RPCMaxRetries = n
	}
	if v := os.Getenv(net.EnvPrefix + "_RPC_BACKOFF_BASE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, false, fmt.Errorf("%s_RPC_BACKOFF_BASE inválido: %w", net.EnvPrefix, err)
		}
		cfg.RPCBackoffBase = d
	}
	if v := os.Getenv(net.EnvPrefix + "_RPC_BACKOFF_MAX"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, false, fmt.Errorf("%s_RPC_BACKOFF_MAX inválido: %w", net.EnvPrefix, err)
		}
		cfg.RPCBackoffMax = d
	}

	return cfg, true, nil
}

func defaultConfirmations(network domain.Network) uint64 {
	if network == domain.NetworkETHEREUM {
		return 64
	}
	return 15
}

// IsMainnetChainID reports whether chainID is the well-known MAINNET chain
// id of `network` (Bloque 6B mainnet safety switch: WITHDRAWALS_ENABLED
// alone must never be enough to withdraw on mainnet).
func IsMainnetChainID(network domain.Network, chainID uint64) bool {
	def, ok := knownEVMNetworks[network]
	return ok && def.MainnetID == chainID
}
