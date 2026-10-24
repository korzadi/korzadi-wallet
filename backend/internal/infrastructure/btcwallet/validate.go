package btcwallet

import (
	"fmt"
	"strings"

	"github.com/btcsuite/btcutil/bech32"
)

// ValidateAddress checks that address is a well-formed native SegWit
// (P2WPKH, witness version 0, 20-byte program) address for the given
// network — the only address shape this Wallet issues. It deliberately
// rejects other valid Bitcoin address types (legacy, nested SegWit,
// Taproot) since accepting them here would suggest they're supported for
// deposits when they are not.
func ValidateAddress(address string, network Network) error {
	hrp, data, err := bech32.Decode(address)
	if err != nil {
		return fmt.Errorf("dirección bech32 inválida: %w", err)
	}
	if !strings.EqualFold(hrp, string(network)) {
		return fmt.Errorf("prefijo de red inesperado: %q (esperaba %q)", hrp, network)
	}
	if len(data) == 0 {
		return fmt.Errorf("dirección sin datos de versión/programa")
	}

	witnessVersion := data[0]
	if witnessVersion != 0 {
		return fmt.Errorf("versión de witness %d no soportada — este Wallet solo emite direcciones SegWit nativas v0 (P2WPKH)", witnessVersion)
	}

	program, err := bech32.ConvertBits(data[1:], 5, 8, false)
	if err != nil {
		return fmt.Errorf("programa de witness inválido: %w", err)
	}
	if len(program) != 20 {
		return fmt.Errorf("longitud de programa inesperada para P2WPKH: %d bytes (esperaba 20)", len(program))
	}

	return nil
}
