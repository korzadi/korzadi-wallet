package cryptutil

import (
	"encoding/binary"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// ErrInvalidAAD is returned when an AAD has an empty component.
var ErrInvalidAAD = errors.New("cryptutil: AAD inválido (tabla, columna y referencia son obligatorias)")

// AAD (additional authenticated data) binds a ciphertext to the place it is
// stored: table + column + a stable reference of the row (an address or a
// user id — NOT a DB-generated id, which does not exist yet at encrypt
// time in most repositories). A blob copied to another row/column, or
// swapped between two wallets, fails authentication on decrypt.
//
// AAD is authenticated, not secret and not stored: callers recompute it.
type AAD struct {
	Table  string
	Column string
	Ref    string
}

// Slot identifies the encrypted column ("table.column"). It is the key under
// which a Rewriter (lazy re-encryption) is registered.
func (a AAD) Slot() string { return a.Table + "." + a.Column }

// bytes serializes the AAD unambiguously (length-prefixed, so
// ("ab","c") and ("a","bc") can never collide).
func (a AAD) bytes() ([]byte, error) {
	if a.Table == "" || a.Column == "" || a.Ref == "" {
		return nil, ErrInvalidAAD
	}
	b := []byte("korzadi/aad/v1")
	for _, p := range [...]string{a.Table, a.Column, a.Ref} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(p)))
		b = append(b, p...)
	}
	return b, nil
}

// NormalizeAddress returns the canonical form used inside an AAD: EVM
// addresses are case-insensitive (checksummed vs lowercase must not change
// the AAD); every other family (TRON/base58, BTC) is kept verbatim.
func NormalizeAddress(address string) string {
	a := strings.TrimSpace(address)
	if len(a) == 42 && (strings.HasPrefix(a, "0x") || strings.HasPrefix(a, "0X")) {
		return strings.ToLower(a)
	}
	return a
}

// Columns that hold custody ciphertext. Kept here so the writer, the reader
// and the re-encryption command can never disagree on the AAD.
const (
	TableCryptoAddresses = "crypto_addresses"
	TableCustodyWallets  = "custody_wallets"
	TableTwoFactor       = "two_factor_credentials"
	TableEnv             = "env"
)

// Slots (table.column) under which lazy-migration Rewriters are registered.
const (
	SlotCryptoAddresses = TableCryptoAddresses + ".encrypted_private_key"
	SlotCustodyWallets  = TableCustodyWallets + ".encrypted_private_key"
	SlotTwoFactor       = TableTwoFactor + ".secret_encrypted"
)

// ForCryptoAddress: crypto_addresses.encrypted_private_key (address is UNIQUE).
func ForCryptoAddress(address string) AAD {
	return AAD{Table: TableCryptoAddresses, Column: "encrypted_private_key", Ref: NormalizeAddress(address)}
}

// ForCustodyWallet: custody_wallets.encrypted_private_key (address is UNIQUE).
func ForCustodyWallet(address string) AAD {
	return AAD{Table: TableCustodyWallets, Column: "encrypted_private_key", Ref: NormalizeAddress(address)}
}

// ForTwoFactor: two_factor_credentials.secret_encrypted (user_id is the PK).
func ForTwoFactor(userID uuid.UUID) AAD {
	return AAD{Table: TableTwoFactor, Column: "secret_encrypted", Ref: userID.String()}
}

// ForEnvSecret: a ciphertext that lives in an environment variable rather
// than a DB row (e.g. GAS_STATION_ENCRYPTED_PRIVATE_KEY), bound to its
// variable name and the address it controls.
func ForEnvSecret(varName, address string) AAD {
	return AAD{Table: TableEnv, Column: varName, Ref: NormalizeAddress(address)}
}

// ForTable rebuilds the AAD of a stored row from its table and row
// reference (address for crypto_addresses/custody_wallets, user id for
// two_factor_credentials). Used by the bulk re-encryption command so it
// authenticates exactly like the runtime readers do.
func ForTable(table, ref string) (AAD, error) {
	switch table {
	case TableCryptoAddresses:
		return ForCryptoAddress(ref), nil
	case TableCustodyWallets:
		return ForCustodyWallet(ref), nil
	case TableTwoFactor:
		id, err := uuid.Parse(ref)
		if err != nil {
			return AAD{}, errors.New("cryptutil: user id inválido para two_factor_credentials")
		}
		return ForTwoFactor(id), nil
	default:
		return AAD{}, errors.New("cryptutil: tabla desconocida: " + table)
	}
}
