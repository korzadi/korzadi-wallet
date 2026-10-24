// Package btcwallet generates Bitcoin key pairs and derives their address.
//
// Address type decision (spec explicitly asks this to be documented, not
// implemented exhaustively): this package generates ONLY native SegWit
// (P2WPKH, bech32, "bc1q...") addresses. Legacy (P2PKH, "1...") and
// nested SegWit (P2SH-P2WPKH, "3...") are deliberately not implemented —
// they exist for backward compatibility with old wallets/exchanges, not
// because they're better for a new deposit address. Taproot (P2TR,
// "bc1p...") is also deliberately not implemented — it solves problems
// (script-path spending privacy, multisig efficiency) this Wallet doesn't
// have yet; adding it later is a compatible addition, not a breaking
// change, since every address type coexists on the same chain. Native
// SegWit is the right default today: lowest transaction fees of any type
// (relevant for withdrawals later), broad exchange/wallet support since
// 2019, and simpler to implement correctly than Taproot's script-tree
// commitments.
//
// This uses only already-vendored, audited primitives — no new
// dependency was added to go.mod/go.sum for this package:
//   - github.com/decred/dcrd/dcrec/secp256k1/v4 for key generation
//     (already used by tronwallet)
//   - crypto/sha256 (stdlib) + golang.org/x/crypto/ripemd160 (already a
//     transitive dependency via golang.org/x/crypto, already used by
//     tronwallet's sha3 import) for HASH160
//   - github.com/btcsuite/btcutil/bech32 (a subpackage of the btcutil
//     module already used by tronwallet for base58) for BIP-173 bech32
//     encoding
//
// No private key material is ever logged. Callers must encrypt
// PrivateKeyHex before persisting it, exactly like evmwallet/tronwallet.
package btcwallet

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcutil/bech32"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/crypto/ripemd160" //nolint:staticcheck // deprecated but this is exactly what HASH160 requires; no alternative exists in the stdlib
)

// Network selects which bech32 human-readable prefix to use — "bc" for
// mainnet, "tb" for testnet. Kept as a simple string (not a whole
// chaincfg.Params, which isn't vendored) since HRP selection is the only
// network-specific detail this package needs.
type Network string

const (
	Mainnet Network = "bc"
	Testnet Network = "tb"
)

type KeyPair struct {
	Address       string // bech32 native SegWit address, e.g. bc1q...
	PrivateKeyHex string // caller MUST encrypt this before persisting; never log it
}

// GenerateKeyPair creates a fresh secp256k1 key pair and derives its
// native SegWit (P2WPKH) address for the given network.
func GenerateKeyPair(network Network) (*KeyPair, error) {
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return nil, fmt.Errorf("btcwallet: generar clave privada: %w", err)
	}
	defer privKey.Zero()

	address, err := AddressFromPublicKey(privKey.PubKey().SerializeCompressed(), network)
	if err != nil {
		return nil, err
	}

	return &KeyPair{
		Address:       address,
		PrivateKeyHex: hex.EncodeToString(privKey.Serialize()),
	}, nil
}

// AddressFromPublicKey derives the bech32 native SegWit (P2WPKH) address
// for a COMPRESSED public key (33 bytes) — SegWit addresses are only ever
// derived from compressed keys per BIP-141/143, unlike legacy addresses
// which historically allowed uncompressed keys too.
func AddressFromPublicKey(compressedPubKey []byte, network Network) (string, error) {
	if len(compressedPubKey) != 33 {
		return "", fmt.Errorf("btcwallet: clave pública comprimida debe tener 33 bytes, tiene %d", len(compressedPubKey))
	}

	hash160, err := hash160(compressedPubKey)
	if err != nil {
		return "", err
	}

	return encodeSegwitAddress(string(network), 0, hash160)
}

// hash160 is RIPEMD160(SHA256(data)) — the standard Bitcoin pubkey-hash
// function used by every address type, not something specific to SegWit.
func hash160(data []byte) ([]byte, error) {
	sha := sha256.Sum256(data)
	ripemd := ripemd160.New()
	if _, err := ripemd.Write(sha[:]); err != nil {
		return nil, fmt.Errorf("btcwallet: hash160: %w", err)
	}
	return ripemd.Sum(nil), nil
}

// encodeSegwitAddress implements the BIP-173 SegWit address encoding
// (witness version + program, bech32-checksummed) using only the raw
// bech32 primitives — btcutil's higher-level btcutil.NewAddressWitnessPubKeyHash
// helper needs a chaincfg.Params value, and the chaincfg package (unlike
// bech32, which lives in the same already-vendored btcutil module) is not
// vendored in this project; rather than add a new dependency, this
// function does the same well-defined, standardized encoding directly.
// This is encoding a public specification (BIP-173), not inventing
// cryptography — the checksum/encoding primitives themselves come from
// the audited bech32 library.
func encodeSegwitAddress(hrp string, witnessVersion byte, program []byte) (string, error) {
	converted, err := bech32.ConvertBits(program, 8, 5, true)
	if err != nil {
		return "", fmt.Errorf("btcwallet: convertir programa a grupos de 5 bits: %w", err)
	}

	data := make([]byte, 0, len(converted)+1)
	data = append(data, witnessVersion)
	data = append(data, converted...)

	address, err := bech32.Encode(hrp, data)
	if err != nil {
		return "", fmt.Errorf("btcwallet: codificar bech32: %w", err)
	}
	return address, nil
}
