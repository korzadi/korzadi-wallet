package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
)

const (
	// LoginChallengeTTL is deliberately short: the challenge only exists
	// between password validation and the TOTP/recovery-code check.
	LoginChallengeTTL = 5 * time.Minute
	// LoginChallengePurpose prevents a challenge from being reused for any
	// other flow if the table is ever shared.
	LoginChallengePurpose = "login_2fa"
	// MaxLoginChallengeAttempts bounds wrong codes per temporary challenge.
	// twofactor.Service also enforces its own user-wide lock (5 failures).
	MaxLoginChallengeAttempts = 5
)

// ErrInvalidLoginChallenge is intentionally generic: unknown, consumed and
// already-exhausted challenges all look the same to an attacker.
var ErrInvalidLoginChallenge = &domain.DomainError{
	Code:    "invalid_2fa_challenge",
	Message: "el desafío de verificación no es válido o ya fue utilizado",
}

// ErrLoginChallengeExpired is returned when the challenge existed but its
// short TTL passed. The token itself is unguessable, so distinguishing this
// from "invalid" does not help enumeration.
var ErrLoginChallengeExpired = &domain.DomainError{
	Code:    "2fa_challenge_expired",
	Message: "el desafío de verificación expiró; inicia sesión de nuevo",
}

// ErrTwoFactorUnavailable fails closed when the 2FA state cannot be read.
var ErrTwoFactorUnavailable = &domain.DomainError{
	Code:    "2fa_unavailable",
	Message: "no se pudo verificar el estado de 2FA; inténtalo más tarde",
}

// LoginChallenge is the server-side state of a pending 2FA login. Only the
// SHA-256 hash of the opaque token is persisted.
type LoginChallenge struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	TokenHash   string
	Purpose     string
	Attempts    int
	MaxAttempts int
	IPAddress   string
	UserAgent   string
	ExpiresAt   time.Time
	Consumed    bool
}

// LoginChallengeRepository persists one-time pre-2FA login challenges.
// Implementations must make Consume atomic across concurrent requests.
type LoginChallengeRepository interface {
	Create(ctx context.Context, c *LoginChallenge) error
	FindByTokenHash(ctx context.Context, tokenHash string) (*LoginChallenge, error)
	// RegisterFailure increments the attempt counter atomically and marks the
	// challenge consumed when maxAttempts is reached. Returns the new count.
	RegisterFailure(ctx context.Context, id uuid.UUID) (attempts int, err error)
	// Consume atomically invalidates a still-valid challenge. Only one caller
	// may receive true, which is what prevents two concurrent completions.
	Consume(ctx context.Context, id uuid.UUID) (bool, error)
}

func hashLoginToken(plain string) string {
	sum := sha256.Sum256([]byte("korzadi-login-challenge-v1:" + plain))
	return hex.EncodeToString(sum[:])
}

func newLoginChallenge(userID uuid.UUID, ip, userAgent string, now time.Time) (plain string, c *LoginChallenge, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", nil, err
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	c = &LoginChallenge{
		ID:          uuid.New(),
		UserID:      userID,
		TokenHash:   hashLoginToken(plain),
		Purpose:     LoginChallengePurpose,
		MaxAttempts: MaxLoginChallengeAttempts,
		IPAddress:   ip,
		UserAgent:   userAgent,
		ExpiresAt:   now.Add(LoginChallengeTTL),
	}
	return plain, c, nil
}
