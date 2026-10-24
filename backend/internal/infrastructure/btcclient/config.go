// Package btcclient implements domain.UTXOProvider against a Bitcoin Core
// (or Bitcoin-Core-RPC-compatible) full node. It is a completely separate
// adapter from internal/infrastructure/evmclient — Bitcoin is not forced
// into the EVM shape anywhere in this package. See domain.UTXOProvider for
// why: no chain ID, no contracts, no logs, no accounts — a deposit is "an
// output of some transaction, in some block, paying an address we manage."
//
// This client asks the node itself to decode output addresses (via
// getblock verbosity=2 / getrawtransaction verbose=true, both of which
// return `scriptPubKey.address` pre-decoded on any Bitcoin Core >= 22),
// instead of parsing scriptPubKeys locally. That keeps this adapter free
// of any address-encoding/script-parsing dependency and, more
// importantly, means address decoding always matches whatever network
// (mainnet/testnet/regtest) the connected node is actually running.
package btcclient

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	RPCURL  string
	RPCUser string
	RPCPass string
	// Network is informational only (used in logs/health output) — the
	// node itself is the source of truth for which chain it serves.
	Network               string
	ConfirmationsRequired uint64
	RPCTimeout            time.Duration
	RPCMaxRetries         int
	RPCBackoffBase        time.Duration
	RPCBackoffMax         time.Duration
}

// LoadConfigFromEnv reads BTC_RPC_URL / BTC_RPC_USER / BTC_RPC_PASS and
// friends. Like every other network in this backend, Bitcoin is optional:
// ok=false (no error) when BTC_RPC_URL is unset. Credentials are read from
// the environment only — never hardcoded, never logged.
func LoadConfigFromEnv() (cfg Config, ok bool, err error) {
	rpcURL := os.Getenv("BTC_RPC_URL")
	if rpcURL == "" {
		return Config{}, false, nil
	}

	cfg = Config{
		RPCURL:                rpcURL,
		RPCUser:               os.Getenv("BTC_RPC_USER"),
		RPCPass:               os.Getenv("BTC_RPC_PASS"),
		Network:               envOr("BTC_NETWORK", "mainnet"),
		ConfirmationsRequired: 3,
		RPCTimeout:            15 * time.Second,
		RPCMaxRetries:         3,
		RPCBackoffBase:        250 * time.Millisecond,
		RPCBackoffMax:         5 * time.Second,
	}

	if v := os.Getenv("BTC_CONFIRMATIONS_REQUIRED"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return Config{}, false, fmt.Errorf("BTC_CONFIRMATIONS_REQUIRED inválido: %w", err)
		}
		cfg.ConfirmationsRequired = n
	}
	if v := os.Getenv("BTC_RPC_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, false, fmt.Errorf("BTC_RPC_TIMEOUT inválido: %w", err)
		}
		cfg.RPCTimeout = d
	}
	if v := os.Getenv("BTC_RPC_MAX_RETRIES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, false, fmt.Errorf("BTC_RPC_MAX_RETRIES inválido: %w", err)
		}
		cfg.RPCMaxRetries = n
	}
	if v := os.Getenv("BTC_RPC_BACKOFF_BASE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, false, fmt.Errorf("BTC_RPC_BACKOFF_BASE inválido: %w", err)
		}
		cfg.RPCBackoffBase = d
	}
	if v := os.Getenv("BTC_RPC_BACKOFF_MAX"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, false, fmt.Errorf("BTC_RPC_BACKOFF_MAX inválido: %w", err)
		}
		cfg.RPCBackoffMax = d
	}

	return cfg, true, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
