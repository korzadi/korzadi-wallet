package evmwallet

import (
	"math/big"
	"testing"
)

func TestVerifySender(t *testing.T) {
	chainID := big.NewInt(56)
	owner, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	other, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	to := other.Address

	signed, err := BuildAndSignNativeTransfer(owner.PrivateKeyHex, chainID, 7, to, big.NewInt(1000), 21000, big.NewInt(1_000_000_000))
	if err != nil {
		t.Fatal(err)
	}

	if err := VerifySender(signed.RawTx, chainID, owner.Address); err != nil {
		t.Fatalf("remitente correcto rechazado: %v", err)
	}
	if err := VerifySender(signed.RawTx, chainID, other.Address); err == nil {
		t.Fatal("una tx firmada por otra clave debe ser rechazada")
	}
	if err := VerifySender(signed.RawTx, big.NewInt(1), owner.Address); err == nil {
		t.Fatal("una tx con otro chain id debe ser rechazada")
	}
	if err := VerifySender(signed.RawTx, chainID, "no-es-una-direccion"); err == nil {
		t.Fatal("una dirección inválida debe ser rechazada")
	}
	if err := VerifySender([]byte{0x01, 0x02}, chainID, owner.Address); err == nil {
		t.Fatal("bytes basura deben ser rechazados")
	}
}
