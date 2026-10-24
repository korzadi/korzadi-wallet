package tronwallet

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/btcsuite/btcutil/base58"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/crypto/sha3"
)

const tronAddressVersion = 0x41

type KeyPair struct {
	PrivateKeyHex string
	Address       string
}

func GenerateKeyPair() (*KeyPair, error) {
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return nil, fmt.Errorf("generar clave privada: %w", err)
	}
	defer privKey.Zero()

	privBytes := privKey.Serialize()
	privHex := hex.EncodeToString(privBytes)

	address, err := addressFromPublicKeyBytes(privKey.PubKey().SerializeUncompressed())
	if err != nil {
		return nil, err
	}

	return &KeyPair{PrivateKeyHex: privHex, Address: address}, nil
}

func AddressFromPrivateKeyHex(privHex string) (string, error) {
	raw, err := hex.DecodeString(privHex)
	if err != nil || len(raw) != 32 {
		return "", errors.New("clave privada inválida")
	}
	privKey := secp256k1.PrivKeyFromBytes(raw)
	defer privKey.Zero()
	return addressFromPublicKeyBytes(privKey.PubKey().SerializeUncompressed())
}

func addressFromPublicKeyBytes(pubBytes []byte) (string, error) {
	if len(pubBytes) != 65 || pubBytes[0] != 0x04 {
		return "", errors.New("formato de clave pública inesperado")
	}
	hash := sha3.NewLegacyKeccak256()
	hash.Write(pubBytes[1:])
	digest := hash.Sum(nil)

	addressBytes := append([]byte{tronAddressVersion}, digest[len(digest)-20:]...)
	return base58CheckEncode(addressBytes), nil
}

func base58CheckEncode(payload []byte) string {
	checksum := doubleSHA256(payload)[:4]
	full := append(payload, checksum...)
	return base58.Encode(full)
}

func doubleSHA256(b []byte) []byte {
	h1 := sha256.Sum256(b)
	h2 := sha256.Sum256(h1[:])
	return h2[:]
}

func ValidateAddress(address string) error {
	decoded := base58.Decode(address)
	if len(decoded) != 25 {
		return errors.New("dirección TRON inválida: longitud incorrecta")
	}
	payload, checksum := decoded[:21], decoded[21:]
	if payload[0] != tronAddressVersion {
		return errors.New("dirección TRON inválida: byte de versión incorrecto (¿es de otra red?)")
	}
	expected := doubleSHA256(payload)[:4]
	for i := range checksum {
		if checksum[i] != expected[i] {
			return errors.New("dirección TRON inválida: checksum no coincide")
		}
	}
	return nil
}
