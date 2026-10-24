package assetregistry

import (
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"korzadi-wallet/backend/internal/domain"
)

const (
	OfficialUSDTBSCAddress      = "0x55d398326f99059fF775485246999027B3197955"
	OfficialUSDTEthereumAddress = "0xdAC17F958D2ee523a2206206994597C13D831ec7"
)

// ValidateOfficialUSDT validates the only supported USDT contracts and
// their chain-specific decimal precision. TRON is checked by tronclient's
// startup validator and uses six decimals in the registry.
func ValidateOfficialUSDT(network domain.Network, contract string, decimals int32) error {
	var official string
	wantDecimals := int32(0)
	switch network {
	case domain.NetworkBSC:
		official, wantDecimals = OfficialUSDTBSCAddress, 18
	case domain.NetworkETHEREUM:
		official, wantDecimals = OfficialUSDTEthereumAddress, 6
	default:
		return nil
	}
	if !common.IsHexAddress(contract) || !strings.EqualFold(common.HexToAddress(contract).Hex(), common.HexToAddress(official).Hex()) {
		return fmt.Errorf("assetregistry: contrato USDT no oficial para %s", network)
	}
	if decimals != wantDecimals {
		return fmt.Errorf("assetregistry: USDT en %s requiere %d decimales (configurados %d)", network, wantDecimals, decimals)
	}
	return nil
}
