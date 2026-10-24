package custody

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

var validWalletTypes = map[WalletType]bool{
	WalletTypeHot:      true,
	WalletTypeGas:      true,
	WalletTypeTreasury: true,
}

// ValidateWalletType rejects anything outside HOT/GAS/TREASURY — no
// arbitrary values, per spec ("No permitir valores arbitrarios que
// contradigan...").
func ValidateWalletType(t WalletType) error {
	if !validWalletTypes[t] {
		return fmt.Errorf("%w: %q", ErrInvalidWalletType, t)
	}
	return nil
}

var validStatuses = map[Status]bool{
	StatusActive:   true,
	StatusDisabled: true,
	StatusRetired:  true,
}

func ValidateStatus(s Status) error {
	if !validStatuses[s] {
		return fmt.Errorf("%w: %q", ErrInvalidStatus, s)
	}
	return nil
}

// validAddressTypes mirrors crypto_addresses.address_type's CHECK
// constraint (0006_wallet_address_registry) — the same vocabulary, not a
// new one.
var validAddressTypes = map[string]bool{
	"evm":            true,
	"bitcoin_segwit": true,
	"tron":           true,
}

func ValidateAddressType(t string) error {
	if !validAddressTypes[t] {
		return fmt.Errorf("%w: %q", ErrInvalidAddressType, t)
	}
	return nil
}

var hexAddressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// ValidateAddress applies a light, address-family-aware format check and
// returns the canonical form to store (EIP-55 checksummed for EVM).
//
// For EVM this reuses the same "0x" + 40 hex + optional EIP-55 checksum
// rule already enforced for withdrawal destinations
// (application/blockchainwithdrawal.ValidateDestination) — re-implemented
// here rather than imported to avoid coupling this registry package to
// the withdrawal package for an unrelated reason, and because the
// destination-only checks there (zero address as a hard reject, "not the
// token contract") don't apply identically to a custody wallet's own
// address (a treasury wallet is never itself a withdrawal destination
// check target).
//
// For "tron" and "bitcoin_segwit" this block does not have visibility
// into the project's TRON/Bitcoin address validators (tronwallet /
// btcwallet were not part of the code slice this block was implemented
// against), so only conservative generic checks apply here (non-empty,
// no whitespace, within the column's length). Flagged for review: wire in
// the real network-specific validators when integrating this into the
// full repository, rather than guessing at a regex without visibility
// into their actual address formats.
func ValidateAddress(addressType, address string) (string, error) {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return "", fmt.Errorf("%w: vacía", ErrInvalidAddress)
	}
	if trimmed != address {
		return "", fmt.Errorf("%w: contiene espacios", ErrInvalidAddress)
	}
	if len(address) > 64 {
		return "", fmt.Errorf("%w: excede 64 caracteres", ErrInvalidAddress)
	}

	if addressType == "evm" {
		return validateEVMAddress(address)
	}
	return address, nil
}

func validateEVMAddress(address string) (string, error) {
	if !hexAddressPattern.MatchString(address) {
		return "", fmt.Errorf("%w: se esperaba 0x seguido de 40 caracteres hexadecimales", ErrInvalidAddress)
	}
	parsed := common.HexToAddress(address)
	if parsed == (common.Address{}) {
		return "", fmt.Errorf("%w: dirección cero", ErrInvalidAddress)
	}

	hasLower, hasUpper := false, false
	for _, r := range address[2:] {
		switch {
		case r >= 'a' && r <= 'f':
			hasLower = true
		case r >= 'A' && r <= 'F':
			hasUpper = true
		}
	}
	checksummed := parsed.Hex()
	if hasLower && hasUpper && checksummed != address {
		return "", fmt.Errorf("%w: checksum EIP-55 inválido", ErrInvalidAddress)
	}
	return checksummed, nil
}
