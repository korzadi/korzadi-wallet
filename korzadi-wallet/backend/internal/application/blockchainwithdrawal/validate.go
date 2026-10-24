package blockchainwithdrawal

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:\-]{1,100}$`)

// ValidateIdempotencyKey accepts 1–100 chars of [A-Za-z0-9._:-].
func ValidateIdempotencyKey(key string) error {
	if !idempotencyKeyPattern.MatchString(key) {
		return ErrInvalidIdempotencyKey
	}
	return nil
}

// ---- Destination address ----

// ValidateDestination validates `address` for (network, asset) and returns
// its canonical form (EIP-55 checksummed for EVM). It NEVER accepts an
// arbitrary string:
//
//   - the network must be an EVM network (Bitcoin → ErrBitcoinDisabled,
//     anything else → ErrUnsupportedNetwork), so a bitcoin address can
//     never be used for an EVM withdrawal and vice-versa;
//   - format: "0x" + exactly 40 hex characters, no whitespace;
//   - checksum: a MIXED-case address must carry a valid EIP-55 checksum
//     (all-lower / all-upper carry no checksum and are accepted);
//   - the zero address is rejected;
//   - for a token withdrawal, the token's own contract address is rejected
//     (the classic "sent tokens to the token contract" loss).
//
// tokenContract is the asset's registry contract ("" for native assets).
func ValidateDestination(network domain.Network, tokenContract, address string) (string, error) {
	switch network.Family() {
	case domain.FamilyBitcoin:
		return "", ErrBitcoinDisabled
	case domain.FamilyTron:
		return validateTronDestination(tokenContract, address)
	case domain.FamilyEVM:
		// handled below
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedNetwork, network)
	}

	if address != strings.TrimSpace(address) {
		return "", fmt.Errorf("%w: contiene espacios", ErrInvalidAddress)
	}
	if len(address) != 42 || !strings.HasPrefix(address, "0x") {
		return "", fmt.Errorf("%w: se esperaba 0x seguido de 40 caracteres hexadecimales", ErrInvalidAddress)
	}
	body := address[2:]
	hasLower, hasUpper := false, false
	for _, r := range body {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
			hasLower = true
		case r >= 'A' && r <= 'F':
			hasUpper = true
		default:
			return "", fmt.Errorf("%w: carácter no hexadecimal", ErrInvalidAddress)
		}
	}
	parsed := common.HexToAddress(address)
	checksummed := parsed.Hex()
	if hasLower && hasUpper && checksummed != address {
		return "", fmt.Errorf("%w: checksum EIP-55 inválido", ErrInvalidAddress)
	}
	if parsed == (common.Address{}) {
		return "", fmt.Errorf("%w: dirección cero", ErrInvalidAddress)
	}
	if tokenContract != "" && common.IsHexAddress(tokenContract) && common.HexToAddress(tokenContract) == parsed {
		return "", ErrDestinationIsContract
	}
	return checksummed, nil
}

// ---- Amount ----

var plainDecimalPattern = regexp.MustCompile(`^[0-9]{1,30}(\.[0-9]{1,30})?$`)

// ParseAmount parses a plain positive decimal string ("0.1", "100"): digits
// with an optional fractional part — no sign, no exponent, no whitespace,
// no float parsing anywhere. The result is strictly > 0.
func ParseAmount(raw string) (decimal.Decimal, error) {
	if !plainDecimalPattern.MatchString(raw) {
		return decimal.Zero, fmt.Errorf("%w: formato", ErrInvalidAmount)
	}
	d, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: %v", ErrInvalidAmount, err)
	}
	if !d.IsPositive() {
		return decimal.Zero, fmt.Errorf("%w: debe ser mayor que cero", ErrInvalidAmount)
	}
	return d, nil
}

// ValidateAmount checks `amount` against the asset's precision (the tighter
// of the network's on-chain decimals and the ledger asset's decimals), the
// uint256 range, and the configured min/max.
func ValidateAmount(amount decimal.Decimal, networkDecimals, ledgerDecimals int32, limits AssetLimits) error {
	if !amount.IsPositive() {
		return fmt.Errorf("%w: debe ser mayor que cero", ErrInvalidAmount)
	}
	precision := networkDecimals
	if ledgerDecimals > 0 && ledgerDecimals < precision {
		precision = ledgerDecimals
	}
	if !amount.Truncate(precision).Equal(amount) {
		return fmt.Errorf("%w: máximo %d decimales", ErrInvalidAmount, precision)
	}
	if _, err := ToRawUnits(amount, networkDecimals); err != nil {
		return err
	}
	if limits.Min.IsPositive() && amount.LessThan(limits.Min) {
		return fmt.Errorf("%w: mínimo %s", ErrAmountBelowMinimum, limits.Min.String())
	}
	if amount.GreaterThan(limits.Max) {
		return fmt.Errorf("%w: máximo %s", ErrAmountAboveMaximum, limits.Max.String())
	}
	return nil
}

// ToRawUnits converts a human amount to the chain's integer base units
// (amount × 10^decimals) without ever touching float64. It fails if the
// result is not an exact positive integer or does not fit in a uint256.
func ToRawUnits(amount decimal.Decimal, decimals int32) (*big.Int, error) {
	if decimals < 0 || decimals > 36 {
		return nil, fmt.Errorf("%w: decimales de red inválidos (%d)", ErrInvalidAmount, decimals)
	}
	scaled := amount.Shift(decimals)
	if !scaled.Equal(scaled.Truncate(0)) {
		return nil, fmt.Errorf("%w: no representable con %d decimales", ErrInvalidAmount, decimals)
	}
	raw := scaled.BigInt()
	if raw.Sign() <= 0 {
		return nil, fmt.Errorf("%w: debe ser mayor que cero", ErrInvalidAmount)
	}
	if raw.BitLen() > 255 {
		return nil, fmt.Errorf("%w: desborda uint256", ErrInvalidAmount)
	}
	return raw, nil
}

// WeiToDecimal converts an integer amount of wei (18 decimals) to a decimal.
func WeiToDecimal(wei *big.Int) decimal.Decimal {
	if wei == nil {
		return decimal.Zero
	}
	return decimal.NewFromBigInt(wei, -18)
}
