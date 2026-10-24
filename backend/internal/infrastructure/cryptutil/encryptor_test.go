package cryptutil

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const (
	hexA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hexB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hexC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	// A realistic-looking private key used to prove it never leaks.
	secretKeyHex = "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
)

func mustProvider(t *testing.T, spec, active string) *EnvKeyProvider {
	t.Helper()
	p, err := NewEnvKeyProvider(spec, active)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	return p
}

// legacyEncrypt reproduces the pre-v1 format byte for byte: base64(nonce||ct), no AAD.
func legacyEncrypt(t *testing.T, hexKey, plaintext string) string {
	t.Helper()
	key, _ := hex.DecodeString(hexKey)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), nil))
}

func TestRoundtripV1(t *testing.T) {
	enc := New(mustProvider(t, "k1="+hexA, ""))
	aad := ForCryptoAddress("TXyz123")
	ct, err := enc.Encrypt(secretKeyHex, aad)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ct, "v1:k1:") {
		t.Fatalf("formato inesperado: %.10s", ct)
	}
	if strings.Contains(ct, secretKeyHex) {
		t.Fatal("el texto cifrado contiene el plaintext")
	}
	got, err := enc.Decrypt(context.Background(), ct, aad)
	if err != nil || got != secretKeyHex {
		t.Fatalf("roundtrip falló: %v", err)
	}
	// nonce aleatorio: dos cifrados del mismo dato difieren
	ct2, _ := enc.Encrypt(secretKeyHex, aad)
	if ct == ct2 {
		t.Fatal("nonce repetido")
	}
}

func TestWrongAADFails(t *testing.T) {
	enc := New(mustProvider(t, "k1="+hexA, ""))
	ct, _ := enc.Encrypt(secretKeyHex, ForCryptoAddress("TAddressOne"))

	cases := map[string]AAD{
		"otra fila":    ForCryptoAddress("TAddressTwo"),
		"otra tabla":   ForCustodyWallet("TAddressOne"),
		"otra columna": {Table: TableCryptoAddresses, Column: "otra", Ref: "TAddressOne"},
	}
	for name, aad := range cases {
		if _, err := enc.Decrypt(context.Background(), ct, aad); !errors.Is(err, ErrDecryptFail) {
			t.Errorf("%s: debía fallar con ErrDecryptFail, got %v", name, err)
		}
	}
	if _, err := enc.Decrypt(context.Background(), ct, AAD{}); !errors.Is(err, ErrInvalidAAD) {
		t.Errorf("AAD vacío debía ser ErrInvalidAAD, got %v", err)
	}
}

func TestAADIsUnambiguous(t *testing.T) {
	a, _ := AAD{Table: "ab", Column: "c", Ref: "x"}.bytes()
	b, _ := AAD{Table: "a", Column: "bc", Ref: "x"}.bytes()
	if string(a) == string(b) {
		t.Fatal("colisión de AAD entre (ab,c) y (a,bc)")
	}
}

func TestEVMAddressCaseDoesNotChangeAAD(t *testing.T) {
	enc := New(mustProvider(t, "k1="+hexA, ""))
	ct, _ := enc.Encrypt("x", ForCryptoAddress("0xAbCdEf0123456789aBcDeF0123456789AbCdEf01"))
	if _, err := enc.Decrypt(context.Background(), ct, ForCryptoAddress("0xabcdef0123456789abcdef0123456789abcdef01")); err != nil {
		t.Fatalf("EVM checksum vs lowercase debe dar el mismo AAD: %v", err)
	}
	// base58 (TRON) es sensible a mayúsculas: NO se normaliza.
	ct2, _ := enc.Encrypt("x", ForCryptoAddress("TAbC"))
	if _, err := enc.Decrypt(context.Background(), ct2, ForCryptoAddress("Tabc")); err == nil {
		t.Fatal("TRON no debe normalizarse")
	}
}

func TestTwoFactorAADBindsUser(t *testing.T) {
	enc := New(mustProvider(t, "k1="+hexA, ""))
	u1, u2 := uuid.New(), uuid.New()
	ct, _ := enc.Encrypt("JBSWY3DPEHPK3PXP", ForTwoFactor(u1))
	if _, err := enc.Decrypt(context.Background(), ct, ForTwoFactor(u2)); err == nil {
		t.Fatal("el secreto 2FA de un usuario no debe abrirse como el de otro")
	}
}

func TestOldKeyStillDecrypts(t *testing.T) {
	aad := ForCustodyWallet("0x0000000000000000000000000000000000000001")
	old := New(mustProvider(t, "k1="+hexA, "k1"))
	ct, _ := old.Encrypt("secret", aad)

	// k2 pasa a ser la activa; k1 queda solo para descifrar.
	rotated := New(mustProvider(t, "k1="+hexA+",k2="+hexB, "k2"))
	d, err := rotated.DecryptDetailed(ct, aad)
	if err != nil || d.Plaintext != "secret" {
		t.Fatalf("clave antigua debía descifrar: %v", err)
	}
	if d.KeyID != "k1" || !d.NeedsReencrypt || d.Legacy {
		t.Fatalf("metadatos incorrectos: %+v", d)
	}
	// y lo nuevo se escribe con la activa
	ct2, _ := rotated.Encrypt("secret", aad)
	if !strings.HasPrefix(ct2, "v1:k2:") {
		t.Fatalf("debía cifrar con k2: %.8s", ct2)
	}
}

func TestRetiredKeyRemovedFails(t *testing.T) {
	aad := ForCryptoAddress("TA")
	ct, _ := New(mustProvider(t, "k1="+hexA, "")).Encrypt("s", aad)
	only2 := New(mustProvider(t, "k2="+hexB, ""))
	if _, err := only2.DecryptDetailed(ct, aad); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("esperaba ErrUnknownKeyID, got %v", err)
	}
}

func TestRotationReencrypt(t *testing.T) {
	aad := ForCryptoAddress("TRot")
	ct, _ := New(mustProvider(t, "k1="+hexA, "")).Encrypt("payload", aad)
	enc := New(mustProvider(t, "k1="+hexA+",k2="+hexB, "k2"))

	out, changed, err := enc.Reencrypt(ct, aad)
	if err != nil || !changed || !strings.HasPrefix(out, "v1:k2:") {
		t.Fatalf("reencrypt: changed=%v err=%v", changed, err)
	}
	// k1 ya puede retirarse: lo re-cifrado abre solo con k2
	onlyNew := New(mustProvider(t, "k2="+hexB, ""))
	if got, err := onlyNew.Decrypt(context.Background(), out, aad); err != nil || got != "payload" {
		t.Fatalf("tras retirar k1: %v", err)
	}
	// idempotente
	again, changed2, err := enc.Reencrypt(out, aad)
	if err != nil || changed2 || again != out {
		t.Fatalf("segundo reencrypt debía ser no-op (changed=%v err=%v)", changed2, err)
	}
}

func TestLegacyStillReadable(t *testing.T) {
	enc := New(mustProvider(t, "k1="+hexA, ""))
	legacy := legacyEncrypt(t, hexA, secretKeyHex)
	if !IsLegacy(legacy) {
		t.Fatal("IsLegacy debía ser true")
	}
	d, err := enc.DecryptDetailed(legacy, ForCryptoAddress("TAny"))
	if err != nil || d.Plaintext != secretKeyHex || !d.Legacy || !d.NeedsReencrypt || d.KeyID != "" {
		t.Fatalf("legacy: %+v err=%v", d, err)
	}
}

func TestLegacyWithRetiredButPresentKey(t *testing.T) {
	// legacy cifrado con la clave que ahora NO es la activa: se prueba con todas.
	legacy := legacyEncrypt(t, hexA, "x")
	enc := New(mustProvider(t, "k1="+hexA+",k2="+hexB, "k2"))
	if got, err := enc.Decrypt(context.Background(), legacy, ForCryptoAddress("TA")); err != nil || got != "x" {
		t.Fatalf("legacy con clave no activa: %v", err)
	}
	// legacy con una clave desconocida falla
	foreign := legacyEncrypt(t, hexC, "x")
	if _, err := enc.Decrypt(context.Background(), foreign, ForCryptoAddress("TA")); !errors.Is(err, ErrDecryptFail) {
		t.Fatalf("esperaba ErrDecryptFail, got %v", err)
	}
}

func TestLazyMigrationRewritesLegacy(t *testing.T) {
	enc := New(mustProvider(t, "k1="+hexA, ""))
	aad := ForCryptoAddress("TLazy")
	legacy := legacyEncrypt(t, hexA, "lazy-secret")

	var gotOld, gotNew string
	calls := 0
	enc.RegisterRewriter(aad.Slot(), func(_ context.Context, a AAD, oldCT, newCT string) error {
		calls++
		gotOld, gotNew = oldCT, newCT
		if a != aad {
			t.Errorf("AAD recibido incorrecto: %+v", a)
		}
		return nil
	})
	got, err := enc.Decrypt(context.Background(), legacy, aad)
	if err != nil || got != "lazy-secret" {
		t.Fatalf("decrypt: %v", err)
	}
	if calls != 1 || gotOld != legacy || !strings.HasPrefix(gotNew, "v1:k1:") {
		t.Fatalf("rewriter mal invocado: calls=%d new=%.8s", calls, gotNew)
	}
	// el valor nuevo abre con el mismo AAD y ya no dispara rewrite
	if _, err := enc.Decrypt(context.Background(), gotNew, aad); err != nil || calls != 1 {
		t.Fatalf("v1 activo no debe re-escribirse (calls=%d err=%v)", calls, err)
	}
}

func TestLazyRewriteFailureNeverFailsDecrypt(t *testing.T) {
	enc := New(mustProvider(t, "k1="+hexA, ""))
	aad := ForCustodyWallet("0x0000000000000000000000000000000000000002")
	enc.RegisterRewriter(aad.Slot(), func(context.Context, AAD, string, string) error {
		return errors.New("db caída")
	})
	got, err := enc.Decrypt(context.Background(), legacyEncrypt(t, hexA, "ok"), aad)
	if err != nil || got != "ok" {
		t.Fatalf("un rewrite fallido no debe romper el descifrado: %v", err)
	}
}

func TestMalformedInputs(t *testing.T) {
	enc := New(mustProvider(t, "k1="+hexA, ""))
	aad := ForCryptoAddress("TM")
	for _, bad := range []string{"", "v1:", "v1:k1:", "v1:k1:!!!notb64", "v1:k1", "%%%", "AAAA"} {
		if _, err := enc.DecryptDetailed(bad, aad); err == nil {
			t.Errorf("%q debía fallar", bad)
		}
	}
	// manipular un byte del ct v1 debe fallar la autenticación
	ct, _ := enc.Encrypt("data", aad)
	tampered := ct[:len(ct)-3] + "AAA"
	if tampered != ct {
		if _, err := enc.DecryptDetailed(tampered, aad); err == nil {
			t.Error("ct manipulado debía fallar")
		}
	}
}

func TestErrorsNeverLeakSecrets(t *testing.T) {
	enc := New(mustProvider(t, "k1="+hexA, ""))
	aad := ForCryptoAddress("TLeak")
	ct, _ := enc.Encrypt(secretKeyHex, aad)

	_, e1 := enc.Decrypt(context.Background(), ct, ForCryptoAddress("TOther"))
	_, e2 := enc.Decrypt(context.Background(), legacyEncrypt(t, hexC, secretKeyHex), aad)
	_, e3 := enc.Decrypt(context.Background(), "v1:zz:"+strings.Split(ct, ":")[2], aad)
	for i, err := range []error{e1, e2, e3} {
		if err == nil {
			t.Fatalf("caso %d debía fallar", i)
		}
		msg := err.Error()
		for _, forbidden := range []string{secretKeyHex, hexA, hexB, hexC, strings.Split(ct, ":")[2]} {
			if strings.Contains(msg, forbidden) {
				t.Errorf("caso %d: el error filtra material sensible", i)
			}
		}
	}
}

func TestProviderConfigErrorsNeverEchoKeys(t *testing.T) {
	badHex := "zz" + hexA[2:]
	for _, spec := range []string{"k1=" + badHex, "k1=abcd", "bad id=" + hexA, "k1=" + hexA + ",k1=" + hexB, ""} {
		_, err := NewEnvKeyProvider(spec, "")
		if err == nil {
			t.Errorf("%q debía fallar", spec)
			continue
		}
		if strings.Contains(err.Error(), hexA) || strings.Contains(err.Error(), badHex) || strings.Contains(err.Error(), "U+") {
			t.Errorf("el error de configuración filtra la clave: %v", err)
		}
	}
	if _, err := NewEnvKeyProvider("k1="+hexA+",k2="+hexB, ""); err == nil {
		t.Error("varias claves sin activa debía fallar")
	}
	if _, err := NewEnvKeyProvider("k1="+hexA, "k9"); err == nil {
		t.Error("activa inexistente debía fallar")
	}
}

func TestLoadEnvKeyProvider(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	p, err := LoadEnvKeyProvider(env(map[string]string{"CUSTODY_ENCRYPTION_KEYS": "k1=" + hexA + ", k2=" + hexB, "CUSTODY_ACTIVE_KEY_ID": "k2"}))
	if err != nil || p.ActiveKeyID() != "k2" || p.KeyIDs()[0] != "k2" || len(p.KeyIDs()) != 2 {
		t.Fatalf("multi-clave: %v", err)
	}
	// fallback a la variable histórica
	p, err = LoadEnvKeyProvider(env(map[string]string{"CUSTODY_ENCRYPTION_KEY": hexA}))
	if err != nil || p.ActiveKeyID() != DefaultKeyID {
		t.Fatalf("fallback: %v", err)
	}
	if _, err = LoadEnvKeyProvider(env(nil)); err == nil {
		t.Fatal("sin variables debía fallar")
	}
}

func TestNewEncryptorLegacyConstructor(t *testing.T) {
	enc, err := NewEncryptor(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	aad := ForTwoFactor(uuid.New())
	ct, _ := enc.Encrypt("s", aad)
	if got, err := enc.Decrypt(context.Background(), ct, aad); err != nil || got != "s" {
		t.Fatalf("%v", err)
	}
	if _, err := NewEncryptor("tooshort"); err == nil {
		t.Fatal("clave corta debía fallar")
	}
}
