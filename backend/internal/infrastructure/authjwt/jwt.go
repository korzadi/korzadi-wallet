package authjwt

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
)

type Claims struct {
	UserID string `json:"uid"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewManager(secret string, accessTTL, refreshTTL time.Duration) *Manager {
	return &Manager{secret: []byte(secret), accessTTL: accessTTL, refreshTTL: refreshTTL}
}

// GenerateAccessToken crea un JWT de vida corta (recomendado 15 min).
func (m *Manager) GenerateAccessToken(userID uuid.UUID) (string, time.Time, error) {
	exp := time.Now().Add(m.accessTTL)
	claims := Claims{
		UserID: userID.String(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "korzadi-wallet",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	return signed, exp, err
}

func (m *Manager) ValidateAccessToken(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("método de firma inesperado")
		}
		return m.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("token inválido")
	}
	return claims, nil
}

// GenerateRefreshToken genera un token opaco aleatorio (no JWT) — se guarda solo su hash en DB.
func GenerateRefreshToken() (plain string, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	h := sha256.Sum256([]byte(plain))
	hash = hex.EncodeToString(h[:])
	return plain, hash, nil
}

func HashRefreshToken(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}

// ---- Password hashing (Argon2id) ----

const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	encoded := base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash)
	return encoded, nil
}

func VerifyPassword(password, encoded string) (bool, error) {
	parts := splitOnce(encoded, '$')
	if parts[0] == "" || parts[1] == "" {
		return false, errors.New("formato de hash inválido")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[0])
	if err != nil {
		return false, err
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	if len(actual) != len(expected) {
		return false, nil
	}
	var diff byte
	for i := range actual {
		diff |= actual[i] ^ expected[i]
	}
	return diff == 0, nil
}

func splitOnce(s string, sep byte) [2]string {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return [2]string{s[:i], s[i+1:]}
		}
	}
	return [2]string{s, ""}
}
