package assetregistry

import (
	"errors"
	"strings"

	"korzadi-wallet/backend/internal/domain"
)

// TronDecimals: TRX and the TRC-20 USDT both use 6 decimals.
const TronDecimals = 6

// LoadTronNetworkAssets returns TRON's registry entries: the native TRX (needed
// by the gas station and the fee engine; never deposited/withdrawn by users)
// and the configured USDT TRC-20 token. It does NOT verify the contract —
// call tronclient.VerifyUSDTContract first (main.go does, and aborts on mismatch).
//
// Registration is the caller's decision: main.go only registers TRON when the
// TRON deposit watcher is active.
func LoadTronNetworkAssets(usdtContract string, confirmationsRequired uint64) ([]domain.NetworkAsset, error) {
	usdtContract = strings.TrimSpace(usdtContract)
	if usdtContract == "" {
		return nil, errors.New("assetregistry: TRON_USDT_CONTRACT vacío")
	}
	return []domain.NetworkAsset{
		{
			Network:               domain.NetworkTRON,
			AssetCode:             "TRX",
			Kind:                  domain.AssetKindNative,
			Decimals:              TronDecimals,
			Enabled:               true,
			ConfirmationsRequired: confirmationsRequired,
		},
		{
			Network:               domain.NetworkTRON,
			AssetCode:             "USDT",
			Kind:                  domain.AssetKindToken,
			ContractAddress:       usdtContract,
			Standard:              domain.TokenStandardTRC20,
			Decimals:              TronDecimals,
			Enabled:               true,
			ConfirmationsRequired: confirmationsRequired,
		},
	}, nil
}
