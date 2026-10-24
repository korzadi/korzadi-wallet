package cryptutil

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// KeyProvider supplies the AEADs the Encryptor uses. It deliberately hands
// out an AEAD, not raw key bytes, so a KMS/HSM-backed implementation can
// perform the crypto remotely without the key ever entering this process.
//
// Implementations: EnvKeyProvider (today).
// KMS / HSM: BLOQUEADO POR DEPENDENCIA EXTERNA (requiere elegir proveedor,
// credenciales y política de acceso; no hay nada que implementar sin ello).
type KeyProvider interface {
	// ActiveKeyID is the only key used to ENCRYPT.
	ActiveKeyID() string
	// AEAD returns the cipher for keyID (active or decrypt-only).
	AEAD(keyID string) (cipher.AEAD, error)
	// KeyIDs lists every known key, active first (used to try legacy blobs,
	// which carry no key id).
	KeyIDs() []string
}

// ErrUnknownKeyID is returned when a ciphertext names a key this provider
// does not have. The message never contains key material.
var ErrUnknownKeyID = errors.New("cryptutil: key id desconocido")

var keyIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// DefaultKeyID is the id given to a lone CUSTODY_ENCRYPTION_KEY when
// CUSTODY_ENCRYPTION_KEYS is not set.
const DefaultKeyID = "default"

// EnvKeyProvider keeps the keys in memory, loaded from the environment.
type EnvKeyProvider struct {
	active string
	order  []string
	aeads  map[string]cipher.AEAD
}

// NewEnvKeyProvider parses spec = "id1=<64 hex>,id2=<64 hex>". activeID may
// be empty only when spec holds exactly one key. Errors name key ids only,
// never key bytes (and never wrap hex/aes errors, which can echo input).
func NewEnvKeyProvider(spec, activeID string) (*EnvKeyProvider, error) {
	p := &EnvKeyProvider{aeads: map[string]cipher.AEAD{}}
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		id, hexKey, ok := strings.Cut(item, "=")
		id, hexKey = strings.TrimSpace(id), strings.TrimSpace(hexKey)
		if !ok || !keyIDRe.MatchString(id) {
			return nil, errors.New("cryptutil: entrada inválida en CUSTODY_ENCRYPTION_KEYS (formato id=hex, id [A-Za-z0-9_-]{1,32})")
		}
		if _, dup := p.aeads[id]; dup {
			return nil, fmt.Errorf("cryptutil: key id duplicado: %s", id)
		}
		aead, err := aeadFromHex(hexKey)
		if err != nil {
			return nil, fmt.Errorf("cryptutil: clave %q inválida: %w", id, err)
		}
		p.aeads[id] = aead
		p.order = append(p.order, id)
	}
	if len(p.order) == 0 {
		return nil, errors.New("cryptutil: no hay claves configuradas")
	}
	activeID = strings.TrimSpace(activeID)
	if activeID == "" {
		if len(p.order) != 1 {
			return nil, errors.New("cryptutil: con varias claves hay que indicar CUSTODY_ACTIVE_KEY_ID")
		}
		activeID = p.order[0]
	}
	if _, ok := p.aeads[activeID]; !ok {
		return nil, fmt.Errorf("cryptutil: CUSTODY_ACTIVE_KEY_ID %q no está en CUSTODY_ENCRYPTION_KEYS", activeID)
	}
	p.active = activeID
	// active first
	ordered := []string{activeID}
	for _, id := range p.order {
		if id != activeID {
			ordered = append(ordered, id)
		}
	}
	p.order = ordered
	return p, nil
}

// LoadEnvKeyProvider reads CUSTODY_ENCRYPTION_KEYS / CUSTODY_ACTIVE_KEY_ID,
// falling back to the legacy single CUSTODY_ENCRYPTION_KEY (id "default").
func LoadEnvKeyProvider(getenv func(string) string) (*EnvKeyProvider, error) {
	if spec := strings.TrimSpace(getenv("CUSTODY_ENCRYPTION_KEYS")); spec != "" {
		return NewEnvKeyProvider(spec, getenv("CUSTODY_ACTIVE_KEY_ID"))
	}
	single := strings.TrimSpace(getenv("CUSTODY_ENCRYPTION_KEY"))
	if single == "" {
		return nil, errors.New("cryptutil: define CUSTODY_ENCRYPTION_KEYS (o CUSTODY_ENCRYPTION_KEY)")
	}
	return NewEnvKeyProvider(DefaultKeyID+"="+single, DefaultKeyID)
}

func (p *EnvKeyProvider) ActiveKeyID() string { return p.active }
func (p *EnvKeyProvider) KeyIDs() []string    { return append([]string(nil), p.order...) }

func (p *EnvKeyProvider) AEAD(keyID string) (cipher.AEAD, error) {
	a, ok := p.aeads[keyID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKeyID, keyID)
	}
	return a, nil
}

func aeadFromHex(hexKey string) (cipher.AEAD, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		// not wrapped: hex errors echo the offending byte of the key.
		return nil, errors.New("debe ser hex de 64 caracteres")
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()
	if len(key) != 32 {
		return nil, errors.New("debe tener exactamente 32 bytes (64 caracteres hex)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("clave AES inválida")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("no se pudo inicializar AES-GCM")
	}
	return gcm, nil
}
