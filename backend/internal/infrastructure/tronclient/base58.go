package tronclient

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
)

// base58Alphabet is the Bitcoin/TRON base58 alphabet (no 0, O, I, l).
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var base58Index [256]int8

func init() {
	for i := range base58Index {
		base58Index[i] = -1
	}
	for i, c := range base58Alphabet {
		base58Index[c] = int8(i)
	}
}

// base58Decode decodes a base58 string (no checksum handling here) into raw
// bytes, preserving leading-zero semantics (each leading '1' becomes a
// leading 0x00 byte, as required by Base58Check).
func base58Decode(s string) ([]byte, error) {
	num := new(big.Int)
	base := big.NewInt(58)
	for _, c := range s {
		idx := base58Index[c]
		if idx < 0 {
			return nil, fmt.Errorf("carácter base58 inválido: %q", c)
		}
		num.Mul(num, base)
		num.Add(num, big.NewInt(int64(idx)))
	}

	decoded := num.Bytes()

	// Count leading '1' characters -> leading zero bytes.
	leadingZeros := 0
	for _, c := range s {
		if c != '1' {
			break
		}
		leadingZeros++
	}

	out := make([]byte, leadingZeros+len(decoded))
	copy(out[leadingZeros:], decoded)
	return out, nil
}

// tronAddressToHex converts a TRON base58check address (e.g.
// "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t") into its 21-byte hex form
// (0x41-prefixed, 42 hex chars), verifying the embedded checksum so a
// malformed or mistyped address is rejected instead of silently
// misinterpreted — critical since this hex address is what actually gets
// sent to the chain in triggerconstantcontract calls.
func tronAddressToHex(address string) (string, error) {
	if len(address) == 0 {
		return "", fmt.Errorf("dirección TRON vacía")
	}

	decoded, err := base58Decode(address)
	if err != nil {
		return "", fmt.Errorf("decodificar dirección TRON %q: %w", address, err)
	}
	if len(decoded) < 25 {
		return "", fmt.Errorf("dirección TRON %q demasiado corta tras decodificar (%d bytes)", address, len(decoded))
	}

	payload := decoded[:len(decoded)-4]
	checksum := decoded[len(decoded)-4:]

	h1 := sha256.Sum256(payload)
	h2 := sha256.Sum256(h1[:])
	expectedChecksum := h2[:4]

	if !bytesEqual(checksum, expectedChecksum) {
		return "", fmt.Errorf("checksum inválido en dirección TRON %q — verifica que la dirección esté bien escrita", address)
	}
	if len(payload) != 21 || payload[0] != 0x41 {
		return "", fmt.Errorf("dirección TRON %q no tiene el formato mainnet esperado (prefijo 0x41)", address)
	}

	return hex.EncodeToString(payload), nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
