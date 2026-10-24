package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidateUserIdempotencyKey(t *testing.T) {
	valid := []string{
		"abcd1234", "550e8400-e29b-41d4-a716-446655440000", "order_2024.10:retry-1",
		strings.Repeat("a", 128),
	}
	for _, k := range valid {
		if err := ValidateUserIdempotencyKey(k); err != nil {
			t.Errorf("clave %q debía ser válida: %v", k, err)
		}
	}

	invalid := map[string]string{
		"corta":         "abc1234",
		"larga":         strings.Repeat("a", 129),
		"espacio":       "abcd 1234",
		"slash":         "abcd/1234",
		"unicode":       "clave-ñandú-1",
		"vacia":         "",
		"salto de line": "abcd1234\n",
	}
	for name, k := range invalid {
		if err := ValidateUserIdempotencyKey(k); err == nil {
			t.Errorf("%s: clave %q debía rechazarse", name, k)
		}
	}
}

func TestValidateUserIdempotencyKey_RejectsReservedPrefixes(t *testing.T) {
	reserved := []string{
		"blockchain-deposit:BSC:USDT:0x55d3:0xdeadbeef:1",
		"BLOCKCHAIN-DEPOSIT:BSC:USDT:0x55d3:0xdeadbeef:1", // mayúsculas
		"withdrawal-settle:3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		"korzadivpn:order-12345",
		"fee_quote:withdrawal:3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		"transfer:abc:12345678",
		"fiat-deposit:abc:12345678",
	}
	for _, k := range reserved {
		if err := ValidateUserIdempotencyKey(k); err == nil {
			t.Errorf("la clave reservada %q debía rechazarse", k)
		}
	}
}

func TestNamespacedIdempotencyKey_IsPerUserAndPerKind(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	k := "same-user-key"

	if NamespacedIdempotencyKey(IdemPrefixTransfer, a, k) == NamespacedIdempotencyKey(IdemPrefixTransfer, b, k) {
		t.Fatal("dos usuarios con la misma clave no deben compartir clave de ledger")
	}
	if NamespacedIdempotencyKey(IdemPrefixTransfer, a, k) == NamespacedIdempotencyKey(IdemPrefixFiatDeposit, a, k) {
		t.Fatal("transfer y fiat-deposit no deben compartir clave de ledger")
	}
	internal := "blockchain-deposit:BSC:USDT:0x55d3:0xdeadbeef:1"
	if got := NamespacedIdempotencyKey(IdemPrefixTransfer, a, internal); got == internal {
		t.Fatal("una clave de usuario nunca debe poder igualar una clave interna")
	}
}
