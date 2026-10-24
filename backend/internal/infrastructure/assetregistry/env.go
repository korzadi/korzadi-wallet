// Package assetregistry loads asset definitions (which tokens/native coins
// exist on which network, at which contract, with how many decimals) from
// environment variables at boot, and upserts them into the network_assets
// table so the rest of the backend (watchers, balance queries) reads a
// single source of truth instead of each piece re-reading env vars.
//
// This is the piece that turns "BSC hardcodes USDT" into "any EVM network
// can register any number of tokens via config" — the architectural fix
// for the CRÍTICO finding in the Block 1 audit.
package assetregistry

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"korzadi-wallet/backend/internal/domain"
)

// EVMNetworkEnvPrefix mirrors evmclient's env prefix per network, kept
// separate (not imported from evmclient) so this package has no dependency
// on any specific chain family — the same loader shape could one day load
// TRON's asset list too.
var evmEnvPrefix = map[domain.Network]string{
	domain.NetworkETHEREUM: "ETH",
	domain.NetworkBSC:      "BSC",
	domain.NetworkPOLYGON:  "POLYGON",
	domain.NetworkARBITRUM: "ARBITRUM",
	domain.NetworkOPTIMISM: "OPTIMISM",
	domain.NetworkBASE:     "BASE",
}

var evmStandard = map[domain.Network]domain.TokenStandard{
	domain.NetworkETHEREUM: domain.TokenStandardERC20,
	domain.NetworkBSC:      domain.TokenStandardBEP20,
	domain.NetworkPOLYGON:  domain.TokenStandardERC20,
	domain.NetworkARBITRUM: domain.TokenStandardERC20,
	domain.NetworkOPTIMISM: domain.TokenStandardERC20,
	domain.NetworkBASE:     domain.TokenStandardERC20,
}

// nativeAssetCode is the ledger asset_code used for each EVM network's
// native coin. Optimism and Base, like Ethereum, settle in ETH.
var nativeAssetCode = map[domain.Network]string{
	domain.NetworkETHEREUM: "ETH",
	domain.NetworkBSC:      "BNB",
	domain.NetworkPOLYGON:  "POL",
	domain.NetworkARBITRUM: "ETH",
	domain.NetworkOPTIMISM: "ETH",
	domain.NetworkBASE:     "ETH",
}

// KnownEVMTokenCodes is the set of token asset codes this build knows how
// to look for in the environment per network: {ASSET}_{PREFIX}_CONTRACT
// and {ASSET}_DECIMALS (falling back to {ASSET}_{PREFIX}_DECIMALS first).
// Adding a new supported token to an existing network is a one-line change
// here plus the two env vars — never a code change to any client/watcher.
var KnownEVMTokenCodes = []string{"USDT", "USDC"}

// LoadEVMNetworkAssets reads which assets are configured for `network`
// from the environment: the native coin is always registered (enabled),
// and each of KnownEVMTokenCodes is registered only if its
// {ASSET}_{PREFIX}_CONTRACT env var is set. Returns an empty, non-error
// result for a network with no token env vars set — the native coin alone
// is still valid (e.g. deposits in raw ETH/BNB, no USDT configured yet).
func LoadEVMNetworkAssets(network domain.Network, confirmationsRequired uint64) ([]domain.NetworkAsset, error) {
	prefix, ok := evmEnvPrefix[network]
	if !ok {
		return nil, fmt.Errorf("assetregistry: red EVM desconocida: %s", network)
	}

	assets := []domain.NetworkAsset{
		{
			Network:               network,
			AssetCode:             nativeAssetCode[network],
			Kind:                  domain.AssetKindNative,
			Standard:              domain.TokenStandardNone,
			Decimals:              18,
			Enabled:               true,
			ConfirmationsRequired: confirmationsRequired,
		},
	}

	for _, code := range KnownEVMTokenCodes {
		contractVar := fmt.Sprintf("%s_%s_CONTRACT", code, prefix)
		contract := os.Getenv(contractVar)
		if contract == "" {
			continue // this token isn't configured on this network — not an error
		}
		if !looksLikeEVMAddress(contract) {
			return nil, fmt.Errorf("assetregistry: %s no tiene formato de dirección EVM válida: %q", contractVar, contract)
		}

		decimals, err := loadDecimals(code, prefix)
		if err != nil {
			return nil, err
		}
		if code == "USDT" {
			if err := ValidateOfficialUSDT(network, contract, decimals); err != nil {
				return nil, err
			}
		}

		assets = append(assets, domain.NetworkAsset{
			Network:               network,
			AssetCode:             code,
			Kind:                  domain.AssetKindToken,
			ContractAddress:       contract,
			Standard:              evmStandard[network],
			Decimals:              decimals,
			Enabled:               true,
			ConfirmationsRequired: confirmationsRequired,
		})
	}

	return assets, nil
}

// loadDecimals reads {ASSET}_{PREFIX}_DECIMALS if set, else {ASSET}_DECIMALS,
// else a sane per-network default (BSC's USDT is famously 18 decimals,
// unlike ERC-20/TRC-20 USDT which use 6 — never assume, but do provide a
// documented fallback consistent with what bscclient.Config used before).
func loadDecimals(assetCode, prefix string) (int32, error) {
	specific := os.Getenv(fmt.Sprintf("%s_%s_DECIMALS", assetCode, prefix))
	generic := os.Getenv(fmt.Sprintf("%s_DECIMALS", assetCode))

	raw := specific
	if raw == "" {
		raw = generic
	}
	if raw == "" {
		if assetCode == "USDT" && prefix == "BSC" {
			return 18, nil
		}
		return 6, nil // ERC-20/TRC-20 USDT/USDC convention
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 36 {
		return 0, fmt.Errorf("assetregistry: decimales inválidos para %s en %s: %q", assetCode, prefix, raw)
	}
	return int32(n), nil
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
