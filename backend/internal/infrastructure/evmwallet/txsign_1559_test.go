package evmwallet

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
)

func TestBuildAndSignNativeTransfer1559(t *testing.T) {
	// Deterministic test-only key. The signed bytes are decoded locally; no
	// RPC, broadcast, or production key is involved.
	const key = "4c0883a69102937d6231471b5dbb6204fe5129617082791e3e6b3d8c6f8f0b11"
	signed, err := BuildAndSignNativeTransfer1559(key, big.NewInt(1), 7, "0x0000000000000000000000000000000000000001", big.NewInt(123), 21000, big.NewInt(100), big.NewInt(3))
	if err != nil {
		t.Fatal(err)
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(signed.RawTx); err != nil {
		t.Fatal(err)
	}
	if tx.Type() != types.DynamicFeeTxType || tx.Nonce() != 7 || tx.GasFeeCap().Cmp(big.NewInt(100)) != 0 || tx.GasTipCap().Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("unexpected type-2 fields: type=%d nonce=%d cap=%s tip=%s", tx.Type(), tx.Nonce(), tx.GasFeeCap(), tx.GasTipCap())
	}
}

func TestBuildAndSign1559RejectsUnboundedFeeParameters(t *testing.T) {
	_, err := BuildAndSignNativeTransfer1559("", big.NewInt(1), 0, "0x0000000000000000000000000000000000000001", big.NewInt(1), 21000, big.NewInt(2), big.NewInt(3))
	if err == nil {
		t.Fatal("expected priority fee above max fee to be rejected")
	}
}
