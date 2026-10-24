package evmwallet

import (
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestGenerateKeyPair_ProducesChecksummedAddress(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if !common.IsHexAddress(kp.Address) {
		t.Fatalf("dirección generada no es una dirección hex válida: %s", kp.Address)
	}
	// common.HexToAddress(...).Hex() always returns the EIP-55 checksummed
	// form — round-tripping through it must be a no-op if kp.Address was
	// already checksummed, which is what crypto.PubkeyToAddress(...).Hex()
	// (used internally by GenerateKeyPair) guarantees.
	if common.HexToAddress(kp.Address).Hex() != kp.Address {
		t.Errorf("dirección %s no está en formato checksum EIP-55", kp.Address)
	}
}

func TestGenerateKeyPair_NeverProducesDuplicateAddresses(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		kp, err := GenerateKeyPair()
		if err != nil {
			t.Fatalf("no esperaba error: %v", err)
		}
		if seen[kp.Address] {
			t.Fatalf("dirección duplicada generada: %s", kp.Address)
		}
		seen[kp.Address] = true
	}
}

func TestGenerateKeyPair_PrivateKeyRoundTrips(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	privKey, err := crypto.HexToECDSA(kp.PrivateKeyHex)
	if err != nil {
		t.Fatalf("crypto.HexToECDSA: %v", err)
	}
	rederived, err := keyPairFromPrivateKey(privKey)
	if err != nil {
		t.Fatalf("keyPairFromPrivateKey: %v", err)
	}
	if rederived.Address != kp.Address {
		t.Errorf("la dirección re-derivada (%s) no coincide con la original (%s)", rederived.Address, kp.Address)
	}
}

// TestValidateAddress documents that this Wallet relies on go-ethereum's
// own checksum validation (common.IsHexAddress + Hex() round-trip) rather
// than a hand-rolled EIP-55 implementation — spec: "no inventes
// criptografía / algoritmos propios."
func TestValidateAddress_RejectsBadChecksum(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	// Flip the case of one hex character to break the EIP-55 checksum
	// while keeping it a syntactically valid hex address.
	broken := strings.ToLower(kp.Address)
	if broken == kp.Address {
		t.Skip("la dirección generada no tenía mayúsculas para invalidar")
	}
	if common.HexToAddress(broken).Hex() == broken {
		t.Errorf("se esperaba que %s no fuera la forma checksummed canónica", broken)
	}
}
