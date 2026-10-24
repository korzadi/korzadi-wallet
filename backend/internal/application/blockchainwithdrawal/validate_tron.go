package blockchainwithdrawal

import (
	"fmt"
	"strings"

	"korzadi-wallet/backend/internal/infrastructure/tronwallet"
)

// tronZeroAddress is the base58 of 0x41 + 20 zero bytes (computed, not typed).
var tronZeroAddress = func() string {
	a, _ := tronwallet.AddressFromBytes21(append([]byte{0x41}, make([]byte, 20)...))
	return a
}()

// validateTronDestination validates a TRON destination: exact base58check
// (version 0x41 + checksum, no whitespace), not the zero address and — for a
// token withdrawal — not the token's own contract (tokens sent there are lost).
// It returns the address unchanged: base58 is case-sensitive, there is no
// canonical re-casing like EIP-55.
func validateTronDestination(tokenContract, address string) (string, error) {
	if address != strings.TrimSpace(address) {
		return "", fmt.Errorf("%w: contiene espacios", ErrInvalidAddress)
	}
	if err := tronwallet.ValidateAddress(address); err != nil {
		return "", fmt.Errorf("%w: dirección TRON inválida", ErrInvalidAddress)
	}
	if address == tronZeroAddress {
		return "", fmt.Errorf("%w: dirección cero", ErrInvalidAddress)
	}
	if tokenContract != "" && address == tokenContract {
		return "", fmt.Errorf("%w: es el contrato del token", ErrInvalidAddress)
	}
	return address, nil
}
