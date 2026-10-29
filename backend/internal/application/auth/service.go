package auth

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
)

const maxFailedLoginAttempts = 5
const lockDuration = 15 * time.Minute

// TwoFactorGate is the subset of twofactor.Service that the login flow needs.
// Status must fail closed: an error means the login cannot be completed.
type TwoFactorGate interface {
	Status(ctx context.Context, userID uuid.UUID) (enabled bool, recoveryRemaining int, err error)
	Verify(ctx context.Context, userID uuid.UUID, code string) (bool, error)
}

type Service struct {
	users        domain.UserRepository
	sessions     domain.SessionRepository
	wallets      domain.WalletRepository
	jwtMgr       *authjwt.Manager
	audit        domain.AuditLogger
	notifier     domain.Notifier
	refreshTTL   time.Duration
	verifier     EmailVerifier // opcional (Fase C)
	twoFactor    TwoFactorGate
	challenges   LoginChallengeRepository
	challengeTTL time.Duration
}

// EmailVerifier envía el enlace de verificación de correo a un usuario recién
// registrado. Es opcional y su fallo NUNCA hace fallar el registro.
type EmailVerifier interface {
	RequestEmailVerification(ctx context.Context, userID uuid.UUID, ip string) error
}

// WithEmailVerifier activa el envío del correo de verificación al registrarse.
func (s *Service) WithEmailVerifier(v EmailVerifier) *Service {
	s.verifier = v
	return s
}

func NewService(users domain.UserRepository, sessions domain.SessionRepository,
	wallets domain.WalletRepository, jwtMgr *authjwt.Manager, audit domain.AuditLogger,
	notifier domain.Notifier, refreshTTL time.Duration) *Service {
	return &Service{users: users, sessions: sessions, wallets: wallets, jwtMgr: jwtMgr,
		audit: audit, notifier: notifier, refreshTTL: refreshTTL, challengeTTL: LoginChallengeTTL}
}

// WithTwoFactor wires the mandatory second-factor gate. Without it Login and
// Refresh fail closed, so a deployment cannot accidentally skip 2FA.
func (s *Service) WithTwoFactor(g TwoFactorGate) *Service {
	s.twoFactor = g
	return s
}

// WithLoginChallenges wires the persistent one-time challenge store.
func (s *Service) WithLoginChallenges(r LoginChallengeRepository) *Service {
	s.challenges = r
	return s
}

type RegisterInput struct {
	Email     string
	Password  string
	FirstName string
	LastName  string
	Phone     *string
}

type AuthResult struct {
	User             *domain.User
	AccessToken      string
	RefreshToken     string
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time

	// TwoFactorRequired is true when the password was correct but the account
	// has 2FA enabled. In that case AccessToken/RefreshToken are empty and
	// only TwoFactorToken may be used, exclusively at POST /api/auth/login/2fa.
	TwoFactorRequired  bool
	TwoFactorToken     string
	TwoFactorExpiresAt time.Time
}

func (s *Service) Register(ctx context.Context, in RegisterInput, ip, userAgent string) (*AuthResult, error) {
	if existing, _ := s.users.FindByEmail(ctx, in.Email); existing != nil {
		return nil, &domain.DomainError{Code: "email_taken", Message: "el correo ya está registrado"}
	}

	hash, err := authjwt.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}

	user := &domain.User{
		ID:           uuid.New(),
		Email:        in.Email,
		Phone:        in.Phone,
		PasswordHash: hash,
		FirstName:    in.FirstName,
		LastName:     in.LastName,
		Status:       domain.UserStatusPendingVerification,
	}
	if err := s.users.Create(ctx, user); err != nil {
		return nil, err
	}

	wallet := &domain.Wallet{ID: uuid.New(), UserID: user.ID, Status: domain.WalletStatusActive}
	if err := s.wallets.CreateWallet(ctx, wallet); err != nil {
		return nil, err
	}
	if _, err := s.wallets.GetOrCreateAccount(ctx, wallet.ID, "USD"); err != nil {
		return nil, err
	}

	if s.audit != nil {
		_ = s.audit.Log(ctx, &user.ID, "user", "user_registered", "user", &user.ID, nil, ip)
	}
	if s.notifier != nil {
		_ = s.notifier.Notify(ctx, user.ID, "welcome",
			"¡Bienvenido a Korzadi Wallet!", fmt.Sprintf("Hola %s, tu cuenta fue creada correctamente.", user.FirstName))
	}

	if s.verifier != nil {
		if err := s.verifier.RequestEmailVerification(ctx, user.ID, ip); err != nil {
			log.Printf("auth: no se pudo enviar el correo de verificación a user=%s: %v", user.ID, err)
		}
	}

	// El registro no crea sesión: la cuenta queda pendiente hasta que el
	// usuario confirme el correo. Esto evita cuentas no verificadas con acceso.
	return &AuthResult{User: user}, nil
}

func (s *Service) Login(ctx context.Context, email, password, ip, userAgent string) (*AuthResult, error) {
	user, err := s.users.FindByEmail(ctx, email)
	if err != nil || user == nil {
		return nil, domain.ErrInvalidCredentials
	}

	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		return nil, domain.ErrAccountLocked
	}
	if user.Status == domain.UserStatusBlocked {
		return nil, domain.ErrAccountLocked
	}

	ok, err := authjwt.VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		_ = s.users.IncrementFailedLogin(ctx, user.ID)
		if user.FailedLoginAttempts+1 >= maxFailedLoginAttempts {
			locked := time.Now().Add(lockDuration)
			user.LockedUntil = &locked
			_ = s.users.Update(ctx, user)
		}
		if s.audit != nil {
			_ = s.audit.Log(ctx, &user.ID, "user", "login_failed", "user", &user.ID, nil, ip)
		}
		return nil, domain.ErrInvalidCredentials
	}

	_ = s.users.ResetFailedLogin(ctx, user.ID)

	// Una cuenta eliminada no debe poder autenticarse aunque conozca la
	// contraseña. Una cuenta pendiente tampoco recibe sesión hasta verificar.
	if user.Status == domain.UserStatusDeleted {
		return nil, domain.ErrInvalidCredentials
	}
	if user.Status == domain.UserStatusPendingVerification {
		return nil, &domain.DomainError{Code: "email_not_verified", Message: "verifica tu correo electrónico antes de iniciar sesión"}
	}

	// AUDIT FIX (CRITICAL): a user with 2FA used to receive a full session as
	// soon as the password was valid. Check the 2FA state server-side BEFORE
	// issuing anything, and fail closed if the state cannot be read.
	if s.twoFactor == nil {
		return nil, ErrTwoFactorUnavailable
	}
	enabled, _, err := s.twoFactor.Status(ctx, user.ID)
	if err != nil {
		return nil, ErrTwoFactorUnavailable
	}
	if enabled {
		return s.issueLoginChallenge(ctx, user, ip, userAgent)
	}

	s.recordLoginSuccess(ctx, user.ID, ip)
	return s.issueTokens(ctx, user, ip, userAgent, false)
}

// issueLoginChallenge creates a short-lived, single-use challenge. No access
// token, refresh token or session row is created here.
func (s *Service) issueLoginChallenge(ctx context.Context, user *domain.User, ip, userAgent string) (*AuthResult, error) {
	if s.challenges == nil {
		return nil, ErrTwoFactorUnavailable
	}
	plain, challenge, err := newLoginChallenge(user.ID, ip, userAgent, time.Now())
	if err != nil {
		return nil, err
	}
	if s.challengeTTL > 0 {
		challenge.ExpiresAt = time.Now().Add(s.challengeTTL)
	}
	if err := s.challenges.Create(ctx, challenge); err != nil {
		return nil, err
	}
	if s.audit != nil {
		_ = s.audit.Log(ctx, &user.ID, "user", "login_2fa_challenge_issued", "user", &user.ID, nil, ip)
	}
	return &AuthResult{
		User:               user,
		TwoFactorRequired:  true,
		TwoFactorToken:     plain,
		TwoFactorExpiresAt: challenge.ExpiresAt,
	}, nil
}

// CompleteTwoFactorLogin verifies the second factor against a challenge and
// only then mints the normal session. The challenge is consumed atomically,
// so a repeated/concurrent completion can win at most once.
func (s *Service) CompleteTwoFactorLogin(ctx context.Context, challengeToken, code, ip, userAgent string) (*AuthResult, error) {
	if s.twoFactor == nil || s.challenges == nil {
		return nil, ErrTwoFactorUnavailable
	}
	challengeToken = strings.TrimSpace(challengeToken)
	if challengeToken == "" {
		return nil, ErrInvalidLoginChallenge
	}

	ch, err := s.challenges.FindByTokenHash(ctx, hashLoginToken(challengeToken))
	if err != nil {
		return nil, err
	}
	if ch == nil || ch.Purpose != LoginChallengePurpose || ch.Consumed ||
		ch.Attempts >= ch.MaxAttempts {
		return nil, ErrInvalidLoginChallenge
	}
	if !time.Now().Before(ch.ExpiresAt) {
		return nil, ErrLoginChallengeExpired
	}

	user, err := s.users.FindByID(ctx, ch.UserID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}
	if user.Status == domain.UserStatusBlocked || user.Status == domain.UserStatusDeleted {
		_, _ = s.challenges.Consume(ctx, ch.ID)
		return nil, domain.ErrAccountLocked
	}

	ok, err := s.twoFactor.Verify(ctx, user.ID, strings.TrimSpace(code))
	if err != nil {
		return nil, err
	}
	if !ok {
		attempts, ferr := s.challenges.RegisterFailure(ctx, ch.ID)
		if ferr != nil {
			return nil, ferr
		}
		if attempts == 0 {
			return nil, ErrInvalidLoginChallenge
		}
		if s.audit != nil {
			_ = s.audit.Log(ctx, &user.ID, "user", "login_2fa_failed", "user", &user.ID, nil, ip)
		}
		return nil, domain.ErrInvalid2FACode
	}

	// The code was valid, but only one request may turn this challenge into a
	// session. If another concurrent request already consumed it, refuse.
	consumed, err := s.challenges.Consume(ctx, ch.ID)
	if err != nil {
		return nil, err
	}
	if !consumed {
		return nil, ErrInvalidLoginChallenge
	}

	s.recordLoginSuccess(ctx, user.ID, ip)
	return s.issueTokens(ctx, user, ip, userAgent, true)
}

func (s *Service) recordLoginSuccess(ctx context.Context, userID uuid.UUID, ip string) {
	if s.audit != nil {
		_ = s.audit.Log(ctx, &userID, "user", "login_success", "user", &userID, nil, ip)
	}
	if s.notifier != nil {
		_ = s.notifier.Notify(ctx, userID, "new_login",
			"Nuevo inicio de sesión", fmt.Sprintf("Se inició sesión en tu cuenta desde la IP %s.", ip))
	}
}

func (s *Service) Refresh(ctx context.Context, refreshTokenPlain, ip, userAgent string) (*AuthResult, error) {
	hash := authjwt.HashRefreshToken(refreshTokenPlain)
	session, err := s.sessions.FindByRefreshHash(ctx, hash)
	if err != nil || session == nil {
		return nil, &domain.DomainError{Code: "invalid_refresh", Message: "refresh token inválido"}
	}

	if session.Revoked {
		_ = s.sessions.RevokeFamily(ctx, session.FamilyID)
		if s.audit != nil {
			_ = s.audit.Log(ctx, &session.UserID, "user", "refresh_reuse_detected", "session", &session.ID, nil, ip)
		}
		if s.notifier != nil {
			_ = s.notifier.Notify(ctx, session.UserID, "security_alert",
				"Actividad sospechosa detectada", "Detectamos un intento de reutilizar una sesión ya cerrada. Por seguridad, cerramos todas tus sesiones activas.")
		}
		return nil, &domain.DomainError{Code: "refresh_reuse_detected", Message: "sesión comprometida, inicia sesión de nuevo"}
	}

	if session.ExpiresAt.Before(time.Now()) {
		return nil, &domain.DomainError{Code: "refresh_expired", Message: "sesión expirada"}
	}

	user, err := s.users.FindByID(ctx, session.UserID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}

	// AUDIT FIX: a blocked/deleted user could keep minting access tokens from an
	// existing refresh token (BlockUser does not revoke sessions).
	if user.Status == domain.UserStatusBlocked || user.Status == domain.UserStatusDeleted {
		_ = s.sessions.RevokeFamily(ctx, session.FamilyID)
		if s.audit != nil {
			_ = s.audit.Log(ctx, &user.ID, "user", "refresh_denied_inactive_user", "session", &session.ID, nil, ip)
		}
		return nil, domain.ErrAccountLocked
	}

	// AUDIT FIX (CRITICAL): if 2FA is enabled, only sessions created AFTER a
	// successful second-factor check may rotate. Existing pre-2FA sessions are
	// revoked instead of silently bypassing the requirement forever. A failure
	// to read the 2FA state fails closed (no refresh).
	if s.twoFactor == nil {
		return nil, ErrTwoFactorUnavailable
	}
	enabled, _, err := s.twoFactor.Status(ctx, user.ID)
	if err != nil {
		return nil, ErrTwoFactorUnavailable
	}
	if enabled && !session.TwoFactorVerified {
		_ = s.sessions.RevokeFamily(ctx, session.FamilyID)
		if s.audit != nil {
			_ = s.audit.Log(ctx, &user.ID, "user", "refresh_denied_2fa_not_verified", "session", &session.ID, nil, ip)
		}
		return nil, domain.Err2FARequired
	}

	// AUDIT FIX: atomic rotation when the repository supports it (the postgres
	// one does). Losing the race means the token was already used: treat it as
	// reuse and kill the family, exactly like the Revoked branch above.
	if ar, ok := s.sessions.(interface {
		RevokeIfActive(ctx context.Context, id uuid.UUID) (bool, error)
	}); ok {
		won, rerr := ar.RevokeIfActive(ctx, session.ID)
		if rerr != nil {
			return nil, rerr
		}
		if !won {
			_ = s.sessions.RevokeFamily(ctx, session.FamilyID)
			return nil, &domain.DomainError{Code: "refresh_reuse_detected", Message: "sesión comprometida, inicia sesión de nuevo"}
		}
	} else {
		_ = s.sessions.Revoke(ctx, session.ID)
	}

	return s.issueTokensWithFamily(ctx, user, session.FamilyID, ip, userAgent, session.TwoFactorVerified)
}

func (s *Service) Logout(ctx context.Context, refreshTokenPlain string) error {
	hash := authjwt.HashRefreshToken(refreshTokenPlain)
	session, err := s.sessions.FindByRefreshHash(ctx, hash)
	if err != nil || session == nil {
		return nil
	}
	return s.sessions.RevokeFamily(ctx, session.FamilyID)
}

func (s *Service) issueTokens(ctx context.Context, user *domain.User, ip, userAgent string, twoFactorVerified bool) (*AuthResult, error) {
	return s.issueTokensWithFamily(ctx, user, uuid.New(), ip, userAgent, twoFactorVerified)
}

func (s *Service) issueTokensWithFamily(ctx context.Context, user *domain.User, familyID uuid.UUID, ip, userAgent string, twoFactorVerified bool) (*AuthResult, error) {
	access, exp, err := s.jwtMgr.GenerateAccessToken(user.ID)
	if err != nil {
		return nil, err
	}

	refreshPlain, refreshHash, err := authjwt.GenerateRefreshToken()
	if err != nil {
		return nil, err
	}

	session := &domain.Session{
		ID:                uuid.New(),
		UserID:            user.ID,
		RefreshTokenHash:  refreshHash,
		FamilyID:          familyID,
		UserAgent:         userAgent,
		IPAddress:         ip,
		ExpiresAt:         time.Now().Add(s.refreshTTL),
		TwoFactorVerified: twoFactorVerified,
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, err
	}

	return &AuthResult{
		User: user, AccessToken: access, RefreshToken: refreshPlain,
		ExpiresAt: exp, RefreshExpiresAt: session.ExpiresAt,
	}, nil
}
