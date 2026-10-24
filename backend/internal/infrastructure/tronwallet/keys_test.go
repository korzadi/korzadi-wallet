package tronwallet

import "testing"

func TestKeyPairConsistency(t *testing.T) {
	for i := 0; i < 20; i++ {
		kp, err := GenerateKeyPair()
		if err != nil {
			t.Fatalf("GenerateKeyPair falló: %v", err)
		}
		if err := ValidateAddress(kp.Address); err != nil {
			t.Fatalf("la dirección generada no pasa su propia validación: %v (dirección: %s)", err, kp.Address)
		}
		rederived, err := AddressFromPrivateKeyHex(kp.PrivateKeyHex)
		if err != nil {
			t.Fatalf("no se pudo re-derivar la dirección: %v", err)
		}
		if rederived != kp.Address {
			t.Fatalf("INCONSISTENCIA CRÍTICA: dirección original %s != re-derivada %s", kp.Address, rederived)
		}
	}
}

func TestValidateAddressRejectsGarbage(t *testing.T) {
	cases := []string{
		"",
		"no-es-una-direccion",
		"0x1234567890123456789012345678901234567890",
		"TInvalidChecksumAddressXXXXXXXXXXXX",
	}
	for _, addr := range cases {
		if err := ValidateAddress(addr); err == nil {
			t.Fatalf("se esperaba error validando %q, pero se aceptó como válida", addr)
		}
	}
}

func TestGeneratedAddressesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		kp, err := GenerateKeyPair()
		if err != nil {
			t.Fatalf("GenerateKeyPair falló: %v", err)
		}
		if seen[kp.Address] {
			t.Fatalf("dirección duplicada generada: %s", kp.Address)
		}
		seen[kp.Address] = true
	}
}
