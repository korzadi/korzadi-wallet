package cryptutil

import (
	"context"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log"
	"strings"
	"sync"
	"time"
)

// Wire format (current):  v1:<keyID>:<base64(nonce || ciphertext+tag)>
// authenticated with AAD (see AAD). The legacy format is plain
// base64(nonce || ciphertext+tag) with NO AAD and NO key id; it is still
// readable and is upgraded lazily (see Rewriter) or in bulk (cmd/custody-reencrypt).
// Base64 never contains ':', so the two formats cannot be confused.
const v1Prefix = "v1:"

// Errors never include plaintext, ciphertext or key bytes.
var (
	ErrMalformed   = errors.New("cryptutil: texto cifrado con formato inválido")
	ErrDecryptFail = errors.New("cryptutil: no se pudo descifrar (clave, AAD o dato incorrectos)")
)

// Decrypted is the detailed result of a decryption.
type Decrypted struct {
	Plaintext string
	// KeyID that decrypted it; "" for legacy blobs.
	KeyID string
	// Legacy: blob had no version/key id/AAD.
	Legacy bool
	// NeedsReencrypt: legacy, or written with a key that is no longer active.
	NeedsReencrypt bool
}

// Rewriter persists a re-encrypted value. It MUST be a compare-and-swap on
// oldCiphertext (UPDATE ... WHERE col = old AND <row ref>), so a concurrent
// writer is never overwritten. Registered per AAD.Slot().
type Rewriter func(ctx context.Context, aad AAD, oldCiphertext, newCiphertext string) error

type Encryptor struct {
	provider  KeyProvider
	mu        sync.RWMutex
	rewriters map[string]Rewriter
}

// New builds an Encryptor on any KeyProvider.
func New(provider KeyProvider) *Encryptor {
	return &Encryptor{provider: provider, rewriters: map[string]Rewriter{}}
}

// NewEncryptor keeps the historical single-key constructor (used by the
// 2FA key and by existing tests): one key, id "default".
func NewEncryptor(hexKey string) (*Encryptor, error) {
	p, err := NewEnvKeyProvider(DefaultKeyID+"="+strings.TrimSpace(hexKey), DefaultKeyID)
	if err != nil {
		return nil, err
	}
	return New(p), nil
}

// RegisterRewriter enables lazy migration for a column. Call once at boot.
func (e *Encryptor) RegisterRewriter(slot string, rw Rewriter) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rewriters[slot] = rw
}

// IsLegacy reports whether the blob is in the pre-v1 format.
func IsLegacy(ciphertext string) bool { return !strings.HasPrefix(ciphertext, v1Prefix) }

// Encrypt always writes v1 with the ACTIVE key and the given AAD.
func (e *Encryptor) Encrypt(plaintext string, aad AAD) (string, error) {
	ad, err := aad.bytes()
	if err != nil {
		return "", err
	}
	id := e.provider.ActiveKeyID()
	aead, err := e.provider.AEAD(id)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", errors.New("cryptutil: sin entropía disponible")
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), ad)
	return v1Prefix + id + ":" + base64.StdEncoding.EncodeToString(sealed), nil
}

// DecryptDetailed decrypts without side effects (no lazy rewrite).
func (e *Encryptor) DecryptDetailed(ciphertext string, aad AAD) (Decrypted, error) {
	if strings.HasPrefix(ciphertext, v1Prefix) {
		return e.decryptV1(ciphertext, aad)
	}
	return e.decryptLegacy(ciphertext)
}

func (e *Encryptor) decryptV1(ciphertext string, aad AAD) (Decrypted, error) {
	ad, err := aad.bytes()
	if err != nil {
		return Decrypted{}, err
	}
	parts := strings.SplitN(ciphertext, ":", 3) // "v1", keyID, payload
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return Decrypted{}, ErrMalformed
	}
	keyID := parts[1]
	aead, err := e.provider.AEAD(keyID)
	if err != nil {
		return Decrypted{}, err
	}
	raw, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return Decrypted{}, ErrMalformed
	}
	pt, ok := open(aead, raw, ad)
	if !ok {
		return Decrypted{}, ErrDecryptFail
	}
	return Decrypted{
		Plaintext:      string(pt),
		KeyID:          keyID,
		NeedsReencrypt: keyID != e.provider.ActiveKeyID(),
	}, nil
}

func (e *Encryptor) decryptLegacy(ciphertext string) (Decrypted, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return Decrypted{}, ErrMalformed
	}
	// Legacy blobs carry no key id: try every known key, active first. GCM
	// authentication makes a wrong key fail cleanly.
	for _, id := range e.provider.KeyIDs() {
		aead, err := e.provider.AEAD(id)
		if err != nil {
			continue
		}
		if pt, ok := open(aead, raw, nil); ok {
			return Decrypted{Plaintext: string(pt), Legacy: true, NeedsReencrypt: true}, nil
		}
	}
	return Decrypted{}, ErrDecryptFail
}

func open(aead cipher.AEAD, raw, ad []byte) ([]byte, bool) {
	n := aead.NonceSize()
	if len(raw) < n+aead.Overhead() {
		return nil, false
	}
	pt, err := aead.Open(nil, raw[:n], raw[n:], ad)
	if err != nil {
		return nil, false
	}
	return pt, true
}

// Reencrypt returns the blob re-encrypted as v1 with the active key and the
// given AAD. changed=false when it already is v1 under the active key.
func (e *Encryptor) Reencrypt(ciphertext string, aad AAD) (string, bool, error) {
	d, err := e.DecryptDetailed(ciphertext, aad)
	if err != nil {
		return "", false, err
	}
	if !d.NeedsReencrypt {
		return ciphertext, false, nil
	}
	out, err := e.Encrypt(d.Plaintext, aad)
	if err != nil {
		return "", false, err
	}
	return out, true, nil
}

const lazyRewriteTimeout = 3 * time.Second

// Decrypt returns the plaintext. If the blob is legacy or under a retired
// key AND a Rewriter is registered for the column, it is re-encrypted and
// persisted best-effort: a failed rewrite NEVER fails the decrypt and logs
// only the slot (never values or the underlying error text).
func (e *Encryptor) Decrypt(ctx context.Context, ciphertext string, aad AAD) (string, error) {
	d, err := e.DecryptDetailed(ciphertext, aad)
	if err != nil {
		return "", err
	}
	if d.NeedsReencrypt {
		e.lazyRewrite(ctx, ciphertext, d.Plaintext, aad)
	}
	return d.Plaintext, nil
}

func (e *Encryptor) lazyRewrite(ctx context.Context, old, plaintext string, aad AAD) {
	e.mu.RLock()
	rw := e.rewriters[aad.Slot()]
	e.mu.RUnlock()
	if rw == nil {
		return
	}
	fresh, err := e.Encrypt(plaintext, aad)
	if err != nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, lazyRewriteTimeout)
	defer cancel()
	if err := rw(cctx, aad, old, fresh); err != nil {
		log.Printf("cryptutil: re-cifrado perezoso no aplicado slot=%s", aad.Slot())
	}
}
