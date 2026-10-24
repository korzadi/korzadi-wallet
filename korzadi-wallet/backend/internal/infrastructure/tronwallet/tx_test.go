package tronwallet

import (
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
)

func testBlockID(num uint64) []byte {
	id := make([]byte, 32)
	for i := 0; i < 8; i++ {
		id[7-i] = byte(num >> (8 * uint(i)))
	}
	for i := 8; i < 32; i++ {
		id[i] = byte(i * 7)
	}
	return id
}

func mustKP(t *testing.T) *KeyPair {
	t.Helper()
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

func buildSigned(t *testing.T, owner *KeyPair, to, contract string, amount int64) (string, []byte, TRC20TransferParams) {
	t.Helper()
	p := TRC20TransferParams{
		Owner: owner.Address, Contract: contract, To: to,
		AmountRaw: big.NewInt(amount), RefBlockID: testBlockID(70_000_123),
		TimestampMs: 1_700_000_000_000, ExpirationMs: 1_700_000_300_000, FeeLimitSun: 100_000_000,
	}
	raw, err := BuildTRC20TransferRaw(p)
	if err != nil {
		t.Fatal(err)
	}
	id, signed, err := SignRaw(owner.PrivateKeyHex, raw)
	if err != nil {
		t.Fatal(err)
	}
	return id, signed, p
}

func TestBuildSignParseRoundtrip(t *testing.T) {
	hot, dest := mustKP(t), mustKP(t)
	contract := mustKP(t).Address // cualquier dirección válida sirve como "contrato" en el test
	id, signed, p := buildSigned(t, hot, dest.Address, contract, 12_345_678)

	got, err := ParseSignedTx(signed)
	if err != nil {
		t.Fatal(err)
	}
	if got.TxID != id {
		t.Fatalf("txID %s != %s", got.TxID, id)
	}
	sum := sha256.Sum256(got.Raw)
	if hex.EncodeToString(sum[:]) != id {
		t.Fatal("txID no es sha256(raw_data)")
	}
	if !got.IsTransfer || got.Owner != hot.Address || got.Contract != contract || got.To != dest.Address {
		t.Fatalf("campos incorrectos: %+v", got)
	}
	if got.AmountRaw.Cmp(big.NewInt(12_345_678)) != 0 {
		t.Fatalf("monto %s", got.AmountRaw)
	}
	if got.ExpirationMs != p.ExpirationMs || got.TimestampMs != p.TimestampMs || got.FeeLimitSun != p.FeeLimitSun {
		t.Fatalf("tiempos/fee_limit incorrectos: %+v", got)
	}
	// ref_block_bytes = blockID[6:8]; ref_block_hash = blockID[8:16]
	if hex.EncodeToString(got.RefBlockBytes) != hex.EncodeToString(p.RefBlockID[6:8]) ||
		hex.EncodeToString(got.RefBlockHash) != hex.EncodeToString(p.RefBlockID[8:16]) {
		t.Fatal("ref_block incorrecto")
	}
	signer, err := got.RecoverSigner()
	if err != nil || signer != hot.Address {
		t.Fatalf("el firmante recuperado debe ser el HOT: %v %s", err, signer)
	}
}

func TestTamperedTxChangesSigner(t *testing.T) {
	hot, dest := mustKP(t), mustKP(t)
	_, signed, _ := buildSigned(t, hot, dest.Address, mustKP(t).Address, 1_000_000)
	got, _ := ParseSignedTx(signed)
	got.Raw = append([]byte(nil), got.Raw...)
	got.Raw[len(got.Raw)-1] ^= 0x01 // alteración mínima de raw_data
	if signer, err := got.RecoverSigner(); err == nil && signer == hot.Address {
		t.Fatal("una tx alterada no debe recuperar al firmante original")
	}
}

func TestBuildRejectsBadInput(t *testing.T) {
	hot, dest := mustKP(t), mustKP(t)
	base := TRC20TransferParams{
		Owner: hot.Address, Contract: mustKP(t).Address, To: dest.Address,
		AmountRaw: big.NewInt(1), RefBlockID: testBlockID(1),
		TimestampMs: 10, ExpirationMs: 20, FeeLimitSun: 1,
	}
	cases := map[string]func(*TRC20TransferParams){
		"owner":       func(p *TRC20TransferParams) { p.Owner = "TNoEsUnaDireccion" },
		"destino":     func(p *TRC20TransferParams) { p.To = "0x0000000000000000000000000000000000000001" },
		"monto cero":  func(p *TRC20TransferParams) { p.AmountRaw = big.NewInt(0) },
		"monto nil":   func(p *TRC20TransferParams) { p.AmountRaw = nil },
		"monto >256b": func(p *TRC20TransferParams) { p.AmountRaw = new(big.Int).Lsh(big.NewInt(1), 256) },
		"blockID":     func(p *TRC20TransferParams) { p.RefBlockID = []byte{1} },
		"expira<=ts":  func(p *TRC20TransferParams) { p.ExpirationMs = 10 },
		"fee_limit":   func(p *TRC20TransferParams) { p.FeeLimitSun = 0 },
	}
	for name, mut := range cases {
		p := base
		mut(&p)
		if _, err := BuildTRC20TransferRaw(p); err == nil {
			t.Errorf("%s: debía fallar", name)
		}
	}
	if _, err := BuildTRC20TransferRaw(base); err != nil {
		t.Fatalf("caso base válido: %v", err)
	}
}

func TestParseNeverPanicsOnGarbage(t *testing.T) {
	for _, in := range [][]byte{nil, {}, {0xff}, {0x0a, 0xff, 0x01}, {0x0a, 0x05, 1, 2}, []byte("garbage-garbage-garbage")} {
		if _, err := ParseSignedTx(in); err == nil {
			t.Errorf("%x: debía fallar", in)
		}
	}
}

func TestSignErrorsNeverEchoKey(t *testing.T) {
	bad := strings.Repeat("a", 63) + "g"
	_, _, err := SignRaw(bad, []byte("x"))
	if err == nil {
		t.Fatal("clave inválida debía fallar")
	}
	if strings.Contains(err.Error(), "'g'") || strings.Contains(err.Error(), bad) || strings.Contains(err.Error(), "hex character") {
		t.Fatalf("el error filtra la clave: %v", err)
	}
}

func TestAddressBytesRoundtrip(t *testing.T) {
	kp := mustKP(t)
	b, err := AddressToBytes21(kp.Address)
	if err != nil || len(b) != 21 || b[0] != 0x41 {
		t.Fatalf("%v", err)
	}
	back, err := AddressFromBytes21(b)
	if err != nil || back != kp.Address {
		t.Fatalf("roundtrip: %v", err)
	}
	if _, err := AddressFromBytes21(b[:20]); err == nil {
		t.Fatal("longitud incorrecta debía fallar")
	}
}

func TestBuildSignParseTRXTransfer(t *testing.T) {
	gas, hot := mustKP(t), mustKP(t)
	raw, err := BuildTRXTransferRaw(TRXTransferParams{
		Owner: gas.Address, To: hot.Address, AmountSun: 25_000_000,
		RefBlockID: testBlockID(70_000_500), TimestampMs: 1_700_000_000_000, ExpirationMs: 1_700_000_300_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	id, signed, err := SignRaw(gas.PrivateKeyHex, raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseSignedTx(signed)
	if err != nil {
		t.Fatal(err)
	}
	if got.TxID != id || !got.IsTRXTransfer || got.IsTransfer || got.Owner != gas.Address || got.To != hot.Address ||
		got.TRXAmountSun != 25_000_000 || got.FeeLimitSun != 0 {
		t.Fatalf("TRX transfer mal decodificado: %+v", got)
	}
	if signer, err := got.RecoverSigner(); err != nil || signer != gas.Address {
		t.Fatalf("firmante: %v %s", err, signer)
	}
	if _, err := BuildTRXTransferRaw(TRXTransferParams{Owner: gas.Address, To: hot.Address, AmountSun: 0,
		RefBlockID: testBlockID(1), TimestampMs: 1, ExpirationMs: 2}); err == nil {
		t.Fatal("monto 0 debía fallar")
	}
}
