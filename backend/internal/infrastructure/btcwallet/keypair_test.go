package btcwallet

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

func TestGenerateKeyPair_ProducesValidMainnetAddress(t *testing.T) {
	kp, err := GenerateKeyPair(Mainnet)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if !strings.HasPrefix(kp.Address, "bc1q") {
		t.Errorf("dirección = %s, esperaba prefijo bc1q (SegWit nativo v0, mainnet)", kp.Address)
	}
	if err := ValidateAddress(kp.Address, Mainnet); err != nil {
		t.Errorf("la dirección generada no pasa su propia validación: %v", err)
	}
}

func TestGenerateKeyPair_ProducesValidTestnetAddress(t *testing.T) {
	kp, err := GenerateKeyPair(Testnet)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if !strings.HasPrefix(kp.Address, "tb1q") {
		t.Errorf("dirección = %s, esperaba prefijo tb1q (SegWit nativo v0, testnet)", kp.Address)
	}
}

func TestGenerateKeyPair_NeverProducesDuplicateAddresses(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		kp, err := GenerateKeyPair(Mainnet)
		if err != nil {
			t.Fatalf("no esperaba error: %v", err)
		}
		if seen[kp.Address] {
			t.Fatalf("dirección duplicada generada: %s", kp.Address)
		}
		seen[kp.Address] = true
	}
}

func TestAddressFromPublicKey_DeterministicForSameKey(t *testing.T) {
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generar clave: %v", err)
	}
	pub := privKey.PubKey().SerializeCompressed()

	addr1, err := AddressFromPublicKey(pub, Mainnet)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	addr2, err := AddressFromPublicKey(pub, Mainnet)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if addr1 != addr2 {
		t.Errorf("la misma clave pública produjo direcciones distintas: %s vs %s", addr1, addr2)
	}
}

func TestAddressFromPublicKey_RejectsUncompressedKey(t *testing.T) {
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generar clave: %v", err)
	}
	uncompressed := privKey.PubKey().SerializeUncompressed() // 65 bytes
	_, err = AddressFromPublicKey(uncompressed, Mainnet)
	if err == nil {
		t.Fatal("esperaba error: SegWit nativo requiere clave pública comprimida (33 bytes)")
	}
}

func TestValidateAddress_RejectsWrongNetwork(t *testing.T) {
	kp, err := GenerateKeyPair(Mainnet)
	if err != nil {
		t.Fatalf("generar par de claves: %v", err)
	}
	if err := ValidateAddress(kp.Address, Testnet); err == nil {
		t.Error("esperaba error: una dirección mainnet no debe validar contra testnet")
	}
}

func TestValidateAddress_RejectsGarbage(t *testing.T) {
	if err := ValidateAddress("not-a-bitcoin-address", Mainnet); err == nil {
		t.Error("esperaba error para una cadena que no es una dirección bech32")
	}
}

func TestValidateAddress_RejectsTaprootAddress(t *testing.T) {
	// A real mainnet Taproot (P2TR, witness v1) address — this Wallet only
	// issues/accepts witness v0 (P2WPKH), so this must be rejected even
	// though it's a perfectly valid Bitcoin address in general.
	taprootExample := "bc1p5d7rjq7g6rdk2yhzks9smlaqtedr4dekq08ge8ztwac72sfr9rusxg3297"
	if err := ValidateAddress(taprootExample, Mainnet); err == nil {
		t.Error("esperaba error: este Wallet solo soporta SegWit nativo v0 (P2WPKH), no Taproot")
	}
}

func TestPrivateKeyHex_RoundTripsToSameAddress(t *testing.T) {
	kp, err := GenerateKeyPair(Mainnet)
	if err != nil {
		t.Fatalf("generar par de claves: %v", err)
	}

	raw, err := hex.DecodeString(kp.PrivateKeyHex)
	if err != nil {
		t.Fatalf("decodificar hex: %v", err)
	}
	privKey := secp256k1.PrivKeyFromBytes(raw)

	rederived, err := AddressFromPublicKey(privKey.PubKey().SerializeCompressed(), Mainnet)
	if err != nil {
		t.Fatalf("re-derivar dirección: %v", err)
	}
	if rederived != kp.Address {
		t.Errorf("la dirección re-derivada desde PrivateKeyHex (%s) no coincide con la original (%s)", rederived, kp.Address)
	}
}
