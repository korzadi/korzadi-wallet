// Package evmwallet generates EVM-compatible (BSC/Ethereum/Polygon) key
// pairs for deposit address assignment.
//
// Scope note (spec §12): this package only generates addresses and returns
// the private key hex so the caller can encrypt it before persisting
// (mirroring the existing tronwallet flow). It never itself performs
// transaction signing and never persists anything — that stays the
// responsibility of the crypto application service + custody encryptor,
// keeping "address generation", "transaction monitoring" and "transaction
// signing" as separate concerns per spec §12.
package evmwallet

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

type KeyPair struct {
	Address       string // checksummed 0x... address
	PrivateKeyHex string // caller MUST encrypt this before persisting; never log it
}

// GenerateKeyPair creates a fresh secp256k1 key pair and derives its EVM
// address. The same address format is valid on BSC, Ethereum, and Polygon —
// which network it's "for" is a matter of which deposit_addresses row it's
// attached to, not anything intrinsic to the key.
func GenerateKeyPair() (*KeyPair, error) {
	privKey, err := crypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("evmwallet: generar clave: %w", err)
	}
	return keyPairFromPrivateKey(privKey)
}

func KeyFromPrivateKeyHex(privateKeyHex string) (*ecdsa.PrivateKey, error) {
	cleaned := strings.TrimPrefix(privateKeyHex, "0x")
	k, err := crypto.HexToECDSA(cleaned)
	if err != nil {
		return nil, fmt.Errorf("clave privada EVM inválida: %w", err)
	}
	return k, nil
}

func AddressFromPrivateKeyHex(privateKeyHex string) (string, error) {
	privKey, err := KeyFromPrivateKeyHex(privateKeyHex)
	if err != nil {
		return "", err
	}
	kp, err := keyPairFromPrivateKey(privKey)
	if err != nil {
		return "", err
	}
	return kp.Address, nil
}

func keyPairFromPrivateKey(privKey *ecdsa.PrivateKey) (*KeyPair, error) {
	address := crypto.PubkeyToAddress(privKey.PublicKey).Hex()
	privBytes := crypto.FromECDSA(privKey)
	return &KeyPair{
		Address:       address,
		PrivateKeyHex: hex.EncodeToString(privBytes),
	}, nil
}
