package evmwallet

import (
	"math/big"
	"strings"
	"testing"
)

// An invalid private key must never be echoed (not even one character of it)
// in the error returned by the signing functions: callers wrap and log it.
func TestSigningErrorsNeverEchoPrivateKey(t *testing.T) {
	badKeys := []string{
		strings.Repeat("a", 63) + "g", // carácter hex inválido
		strings.Repeat("b", 62),       // longitud incorrecta
		"0x" + strings.Repeat("c", 63) + "Z",
	}
	to := "0x0000000000000000000000000000000000000001"
	for _, k := range badKeys {
		_, e1 := BuildAndSignNativeTransfer(k, big.NewInt(56), 0, to, big.NewInt(1), 21000, big.NewInt(1))
		_, e2 := BuildAndSignERC20Transfer(k, big.NewInt(56), 0, to, to, big.NewInt(1), 60000, big.NewInt(1))
		for _, err := range []error{e1, e2} {
			if err == nil {
				t.Fatalf("una clave inválida debía fallar")
			}
			msg := err.Error()
			if strings.Contains(msg, "'g'") || strings.Contains(msg, "'Z'") ||
				strings.Contains(msg, strings.TrimPrefix(k, "0x")) || strings.Contains(msg, "hex character") {
				t.Errorf("el error filtra información de la clave: %q", msg)
			}
		}
	}
}
