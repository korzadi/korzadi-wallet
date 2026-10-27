package cryptutil

import (
	"context"
	"errors"
	"testing"
)

func TestLazyRewrite_DoesNotBlockDecrypt(t *testing.T) {
	e, err := NewEncryptor("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	aad := ForCryptoAddress("0xdead")

	cipher, err := e.Encrypt("hello", aad)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Decrypt con un rewriter que siempre falla: debe seguir funcionando.
	e.RegisterRewriter(aad.Slot(), func(ctx context.Context, aad AAD, oldCiphertext, newCiphertext string) error {
		return errors.New("simulated rewrite failure")
	})

	pt, err := e.Decrypt(context.Background(), cipher, aad)
	if err != nil {
		t.Fatalf("decrypt con rewriter fallido debe funcionar: %v", err)
	}
	if pt != "hello" {
		t.Errorf("plaintext = %q, esperaba %q", pt, "hello")
	}
}

func TestLazyRewrite_NeedsReencrypt_Detected(t *testing.T) {
	e, err := NewEncryptor("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	aad := ForCryptoAddress("0xbeef")

	cipher, err := e.Encrypt("test", aad)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	d, err := e.DecryptDetailed(cipher, aad)
	if err != nil {
		t.Fatalf("decrypt detailed: %v", err)
	}
	if d.Legacy {
		t.Errorf("no debe ser legacy")
	}
	if d.NeedsReencrypt {
		t.Errorf("no debe necesitar re-cifrado con clave activa igual")
	}
}
