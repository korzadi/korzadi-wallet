package tronclient

import (
	"fmt"
	"strings"

	"korzadi-wallet/backend/internal/infrastructure/tronwallet"
)

// OfficialUSDTMainnet is Tether's official USDT TRC-20 contract on TRON mainnet.
// It is the ONLY contract accepted on mainnet; a typo or a look-alike token
// would otherwise credit deposits / send withdrawals of a worthless asset.
const OfficialUSDTMainnet = USDTContractMainnet

// VerifyUSDTContract fails unless `contract` is on the allow-list for `mode`:
//
//	"mainnet": exactly OfficialUSDTMainnet.
//	"testnet": must appear in extraAllowed (e.g. TRON_USDT_TESTNET_ALLOWLIST);
//	           no testnet address is hardcoded here on purpose.
//
// Meant to be called at startup (fail fast). It does not contact the network.
func VerifyUSDTContract(mode, contract string, extraAllowed []string) error {
	contract = strings.TrimSpace(contract)
	if err := tronwallet.ValidateAddress(contract); err != nil {
		return fmt.Errorf("contrato USDT TRON inválido: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "mainnet":
		if contract != OfficialUSDTMainnet {
			return fmt.Errorf("el contrato USDT configurado (%s) NO es el oficial de mainnet (%s)", contract, OfficialUSDTMainnet)
		}
		return nil
	case "testnet":
		for _, a := range extraAllowed {
			if strings.TrimSpace(a) == contract {
				return nil
			}
		}
		return fmt.Errorf("el contrato USDT configurado (%s) no está en la lista blanca de testnet (TRON_USDT_TESTNET_ALLOWLIST)", contract)
	default:
		return fmt.Errorf("TRON_NETWORK_MODE debe ser mainnet o testnet (got %q)", mode)
	}
}
