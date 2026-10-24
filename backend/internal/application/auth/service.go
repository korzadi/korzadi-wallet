package auth

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
)

const maxFailedLoginAttempts = 5
const lockDuration = 15 * time.Minute

type Service struct {
	users      domain.UserRepository
	sessions   domain.SessionRepository
	wallets    domain.WalletRepository
	jwtMgr     *authjwt.Manager
	audit      domain.AuditLogger
	notifier   domain.Notifier
	refreshTTL time.Duration
	verifier   EmailVerifier // opcional (Fase C)
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
		audit: audit, notifier: notifier, refreshTTL: refreshTTL}
}

type RegisterInput struct {
	Email     string
	Password  string
	FirstName string
	LastName  string
	Phone     *string
}

type AuthResult struct {
	User         *domain.User
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
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

	return s.issueTokens(ctx, user, ip, userAgent)
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

	if s.audit != nil {
		_ = s.audit.Log(ctx, &user.ID, "user", "login_success", "user", &user.ID, nil, ip)
	}
	if s.notifier != nil {
		_ = s.notifier.Notify(ctx, user.ID, "new_login",
			"Nuevo inicio de sesión", fmt.Sprintf("Se inició sesión en tu cuenta desde la IP %s.", ip))
	}

	return s.issueTokens(ctx, user, ip, userAgent)
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

	return s.issueTokensWithFamily(ctx, user, session.FamilyID, ip, userAgent)
}

func (s *Service) Logout(ctx context.Context, refreshTokenPlain string) error {
	hash := authjwt.HashRefreshToken(refreshTokenPlain)
	session, err := s.sessions.FindByRefreshHash(ctx, hash)
	if err != nil || session == nil {
		return nil
	}
	return s.sessions.RevokeFamily(ctx, session.FamilyID)
}

func (s *Service) issueTokens(ctx context.Context, user *domain.User, ip, userAgent string) (*AuthResult, error) {
	return s.issueTokensWithFamily(ctx, user, uuid.New(), ip, userAgent)
}

func (s *Service) issueTokensWithFamily(ctx context.Context, user *domain.User, familyID uuid.UUID, ip, userAgent string) (*AuthResult, error) {
	access, exp, err := s.jwtMgr.GenerateAccessToken(user.ID)
	if err != nil {
		return nil, err
	}

	refreshPlain, refreshHash, err := authjwt.GenerateRefreshToken()
	if err != nil {
		return nil, err
	}

	session := &domain.Session{
		ID:               uuid.New(),
		UserID:           user.ID,
		RefreshTokenHash: refreshHash,
		FamilyID:         familyID,
		UserAgent:        userAgent,
		IPAddress:        ip,
		ExpiresAt:        time.Now().Add(s.refreshTTL),
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, err
	}

	return &AuthResult{User: user, AccessToken: access, RefreshToken: refreshPlain, ExpiresAt: exp}, nil
}
