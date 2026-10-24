// Package bscclient is now a thin, BSC-specific adapter over the generic
// evmclient package (see internal/infrastructure/evmclient). BSC no longer
// has its own RPC/retry/ERC-20/scanning implementation — Client and
// Provider are literally evmclient's types, configured for network=BSC.
// This file only keeps the exact env var names already deployed
// (BSC_RPC_URL, USDT_BSC_CONTRACT, USDT_DECIMALS, ...) so existing
// deployments and existing callers (crypto/service.go, bscwithdrawal,
// main.go) don't need to change beyond the domain.BlockchainProvider
// signature updates required elsewhere in this block.
package bscclient

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/evmclient"
)

// Client and Provider are the exact evmclient types — BSC now IS "EVM
// configured for network=BSC", not a separate implementation.
type Client = evmclient.Client
type Provider = evmclient.Provider

var bscEnvDefaults = evmclient.NetworkEnvDefaults{
	Network:   domain.NetworkBSC,
	EnvPrefix: "BSC",
	MainnetID: 56,
}

// Config wraps evmclient.Config with the BSC/USDT-specific fields that
// used to be the whole struct. Everything network-generic (RPCURL,
// ChainID, timeouts/retries, ConfirmationsRequired) is promoted from the
// embedded evmclient.Config, so `cfg.ChainID`, `cfg.ConfirmationsRequired`
// etc. still work exactly as before at every call site.
type Config struct {
	evmclient.Config
	USDTContractAddress string // checksummed BEP-20 USDT contract address
	USDTDecimals        int
}

// LoadConfigFromEnv reads BSC_* and USDT_BSC_CONTRACT/USDT_DECIMALS. It
// fails closed: any missing required value is an error, never a
// silently-assumed default — a wrong RPC URL or contract address for a
// value-moving system is worse than a startup crash. Unlike the generic
// evmclient.LoadConfigFromEnv (where an unset RPC URL just means "network
// not configured"), here it's an error because callers of this function
// have already checked BSC_RPC_URL != "" before calling it (see main.go).
func LoadConfigFromEnv() (Config, error) {
	base, ok, err := evmclient.LoadConfigFromEnv(bscEnvDefaults)
	if err != nil {
		return Config{}, err
	}
	if !ok {
		return Config{}, fmt.Errorf("BSC_RPC_URL no definido")
	}

	contract := os.Getenv("USDT_BSC_CONTRACT")
	if contract == "" {
		return Config{}, fmt.Errorf("USDT_BSC_CONTRACT no definido — no asumas la dirección, debe configurarse explícitamente")
	}
	if !looksLikeEVMAddress(contract) {
		return Config{}, fmt.Errorf("USDT_BSC_CONTRACT no tiene formato de dirección EVM válida: %q", contract)
	}

	decimalsStr := envOr("USDT_DECIMALS", "18") // BEP-20 USDT on BSC uses 18 decimals (unlike ERC-20/TRC-20 USDT which use 6)
	decimals, err := strconv.Atoi(decimalsStr)
	if err != nil || decimals < 0 || decimals > 36 {
		return Config{}, fmt.Errorf("USDT_DECIMALS inválido: %q", decimalsStr)
	}
	if decimals != 18 {
		return Config{}, fmt.Errorf("USDT_DECIMALS inválido para BSC: se esperan 18, recibido %d", decimals)
	}

	return Config{
		Config:              base,
		USDTContractAddress: contract,
		USDTDecimals:        decimals,
	}, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func looksLikeEVMAddress(s string) bool {
	s = strings.TrimPrefix(s, "0x")
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}
