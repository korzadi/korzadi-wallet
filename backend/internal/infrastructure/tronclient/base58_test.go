package tronclient

import "testing"

// TestTronAddressToHex_KnownUSDTContract verifies decoding against the
// well-known, publicly documented TRON USDT (TRC-20) contract address and
// its widely-published hex equivalent. This is the single most important
// test in this file: if it passes, the checksum/decoding logic is correct
// for every other address too (same algorithm, same alphabet).
func TestTronAddressToHex_KnownUSDTContract(t *testing.T) {
	got, err := tronAddressToHex(USDTContractMainnet) // "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	want := "41a614f803b6fd780986a42c78ec9c7f77e6ded13c"
	if got != want {
		t.Errorf("tronAddressToHex(%q) = %q, esperaba %q", USDTContractMainnet, got, want)
	}
}

func TestTronAddressToHex_InvalidChecksum(t *testing.T) {
	// Same address with the last character altered — must fail checksum,
	// not silently decode to a different, wrong address.
	tampered := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6x"
	_, err := tronAddressToHex(tampered)
	if err == nil {
		t.Fatal("esperaba error de checksum para una dirección alterada")
	}
}

func TestTronAddressToHex_InvalidCharacter(t *testing.T) {
	_, err := tronAddressToHex("TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t0") // '0' is not in the base58 alphabet
	if err == nil {
		t.Fatal("esperaba error por carácter fuera del alfabeto base58")
	}
}

func TestTronAddressToHex_Empty(t *testing.T) {
	_, err := tronAddressToHex("")
	if err == nil {
		t.Fatal("esperaba error para dirección vacía")
	}
}
