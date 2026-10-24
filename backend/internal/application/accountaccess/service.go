// Package accountaccess implementa la verificación de email, el restablecimiento
// de contraseña por correo y el cambio de contraseña autenticado.
//
// Principios:
//   - Los tokens son aleatorios (256 bits), de un solo uso, con expiración, y
//     solo se guarda su hash SHA-256.
//   - Las operaciones públicas (reenvío, "olvidé mi contraseña") responden
//     siempre igual exista o no la cuenta: no permiten enumerar usuarios.
//   - Cambiar o restablecer la contraseña revoca TODAS las sesiones.
//   - Un fallo al enviar el correo nunca revela si la cuenta existe.
package accountaccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
)

const (
	VerifyEmailTTL    = 24 * time.Hour
	ResetPasswordTTL  = 30 * time.Minute
	ResendCooldown    = 60 * time.Second
	MinPasswordLength = 10
	MaxPasswordLength = 128

	maxFailedChangePassword = 5
	changePasswordLock      = 15 * time.Minute
)

// UserStore es lo que el servicio necesita de la capa de usuarios.
type UserStore interface {
	FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	FindByEmailFold(ctx context.Context, email string) (*domain.User, error)
	Update(ctx context.Context, u *domain.User) error
	IncrementFailedLogin(ctx context.Context, id uuid.UUID) error
	ResetFailedLogin(ctx context.Context, id uuid.UUID) error
	UpdatePassword(ctx context.Context, id uuid.UUID, passwordHash string) error
	MarkEmailVerified(ctx context.Context, id uuid.UUID) error
}

// SessionRevoker revoca todas las sesiones de un usuario.
type SessionRevoker interface {
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

// TwoFAChecker valida un código TOTP. Si el usuario no tiene 2FA, Verify
// devuelve un DomainError con code "2fa_not_enabled".
type TwoFAChecker interface {
	Verify(ctx context.Context, userID uuid.UUID, code string) (bool, error)
}

type Deps struct {
	Users    UserStore
	Tokens   domain.AccountTokenRepository
	Sessions SessionRevoker
	Sender   domain.EmailSender
	Audit    domain.AuditLogger // opcional
	Notifier domain.Notifier    // opcional
	TwoFA    TwoFAChecker       // opcional
	// BaseURL es la URL pública del frontend (https://wallet.ejemplo.com).
	BaseURL string
	// Now permite fijar el reloj en tests.
	Now func() time.Time
}

type Service struct {
	d  Deps
	bg *Background // opcional: envío de correos en segundo plano
}

func NewService(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	d.BaseURL = strings.TrimRight(strings.TrimSpace(d.BaseURL), "/")
	return &Service{d: d}
}

// ---- Verificación de email ---------------------------------------------------

// RequestEmailVerification crea un token y envía el enlace. Respeta un
// cooldown por usuario: si ya se envió uno hace menos de ResendCooldown, no
// hace nada y devuelve nil.
func (s *Service) RequestEmailVerification(ctx context.Context, userID uuid.UUID, ip string) error {
	user, err := s.d.Users.FindByID(ctx, userID)
	if err != nil || user == nil {
		return domain.ErrUserNotFound
	}
	if user.EmailVerified || user.Status == domain.UserStatusDeleted || user.Status == domain.UserStatusBlocked {
		return nil
	}
	return s.issueAndSend(ctx, user, domain.TokenPurposeVerifyEmail, VerifyEmailTTL, "email_verification_requested", ip)
}

// resendVerificationNow hace el trabajo de ResendVerification en línea.
func (s *Service) resendVerificationNow(ctx context.Context, email, ip string) {
	user, err := s.d.Users.FindByEmailFold(ctx, email)
	if err != nil || user == nil {
		return
	}
	if err := s.RequestEmailVerification(ctx, user.ID, ip); err != nil {
		log.Printf("accountaccess: reenvío de verificación falló user=%s: %v", user.ID, err)
	}
}

// VerifyEmail consume el token y marca el correo como verificado; si la cuenta
// estaba pending_verification pasa a active. Nunca reactiva cuentas bloqueadas.
func (s *Service) VerifyEmail(ctx context.Context, token, ip string) error {
	userID, ok, err := s.d.Tokens.Consume(ctx, domain.TokenPurposeVerifyEmail, hashToken(token))
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrInvalidToken
	}
	if err := s.d.Users.MarkEmailVerified(ctx, userID); err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return domain.ErrInvalidToken
		}
		return err
	}
	s.audit(ctx, userID, "email_verified", ip)
	s.notify(ctx, userID, "email_verified", "Correo verificado", "Tu correo electrónico fue verificado correctamente.")
	return nil
}

// ---- Restablecimiento de contraseña ------------------------------------------

// forgotPasswordNow hace el trabajo de ForgotPassword en línea.
func (s *Service) forgotPasswordNow(ctx context.Context, email, ip string) {
	user, err := s.d.Users.FindByEmailFold(ctx, email)
	if err != nil || user == nil {
		return
	}
	if user.Status == domain.UserStatusBlocked || user.Status == domain.UserStatusDeleted {
		return
	}
	if err := s.issueAndSend(ctx, user, domain.TokenPurposeResetPassword, ResetPasswordTTL, "password_reset_requested", ip); err != nil {
		log.Printf("accountaccess: solicitud de reset falló user=%s: %v", user.ID, err)
	}
}

// ResetPassword valida la política de contraseña ANTES de consumir el token
// (una contraseña débil no quema el enlace), cambia la contraseña, revoca todas
// las sesiones y limpia bloqueos. Probar el control del correo equivale a
// verificarlo.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword, ip string) error {
	if len(token) == 0 {
		return domain.ErrInvalidToken
	}
	hash := hashToken(token)
	userID, ok, err := s.peekAndConsume(ctx, newPassword, hash)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrInvalidToken
	}

	user, err := s.d.Users.FindByID(ctx, userID)
	if err != nil || user == nil || user.Status == domain.UserStatusBlocked || user.Status == domain.UserStatusDeleted {
		return domain.ErrInvalidToken
	}

	newHash, err := authjwt.HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.d.Users.UpdatePassword(ctx, user.ID, newHash); err != nil {
		return err
	}
	if err := s.d.Sessions.RevokeAllForUser(ctx, user.ID); err != nil {
		// La contraseña ya cambió: se registra y se continúa, pero es grave.
		log.Printf("accountaccess: CRÍTICO no se pudieron revocar las sesiones tras reset user=%s: %v", user.ID, err)
	}
	_ = s.d.Tokens.InvalidateUnused(ctx, user.ID, domain.TokenPurposeResetPassword)
	if err := s.d.Users.MarkEmailVerified(ctx, user.ID); err != nil {
		log.Printf("accountaccess: no se pudo marcar el correo verificado tras reset user=%s: %v", user.ID, err)
	}

	s.audit(ctx, user.ID, "password_reset_completed", ip)
	s.notifyPasswordChanged(ctx, user, ip)
	return nil
}

// peekAndConsume valida la política y consume el token en ese orden.
func (s *Service) peekAndConsume(ctx context.Context, newPassword, tokenHash string) (uuid.UUID, bool, error) {
	if err := ValidatePassword(newPassword, ""); err != nil {
		return uuid.Nil, false, err
	}
	return s.d.Tokens.Consume(ctx, domain.TokenPurposeResetPassword, tokenHash)
}

// ---- Cambio de contraseña autenticado ----------------------------------------

// ChangePassword exige la contraseña actual (con el mismo bloqueo por intentos
// fallidos que el login) y, si el usuario tiene 2FA, un código válido. Revoca
// todas las sesiones: el cliente debe iniciar sesión de nuevo.
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, current, newPassword, twoFACode, ip string) error {
	user, err := s.d.Users.FindByID(ctx, userID)
	if err != nil || user == nil {
		return domain.ErrUserNotFound
	}
	if user.Status == domain.UserStatusBlocked || user.Status == domain.UserStatusDeleted {
		return domain.ErrAccountLocked
	}
	if user.LockedUntil != nil && user.LockedUntil.After(s.d.Now()) {
		return domain.ErrAccountLocked
	}

	if err := ValidatePassword(newPassword, user.Email); err != nil {
		return err
	}

	ok, verr := authjwt.VerifyPassword(current, user.PasswordHash)
	if verr != nil || !ok {
		_ = s.d.Users.IncrementFailedLogin(ctx, user.ID)
		if user.FailedLoginAttempts+1 >= maxFailedChangePassword {
			locked := s.d.Now().Add(changePasswordLock)
			user.LockedUntil = &locked
			_ = s.d.Users.Update(ctx, user)
		}
		s.audit(ctx, user.ID, "password_change_failed", ip)
		return domain.ErrInvalidCurrentPassword
	}

	if same, _ := authjwt.VerifyPassword(newPassword, user.PasswordHash); same {
		return domain.ErrSamePassword
	}

	if err := s.check2FA(ctx, user.ID, twoFACode); err != nil {
		return err
	}

	newHash, err := authjwt.HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.d.Users.UpdatePassword(ctx, user.ID, newHash); err != nil {
		return err
	}
	if err := s.d.Sessions.RevokeAllForUser(ctx, user.ID); err != nil {
		log.Printf("accountaccess: CRÍTICO no se pudieron revocar las sesiones tras cambio user=%s: %v", user.ID, err)
	}
	_ = s.d.Tokens.InvalidateUnused(ctx, user.ID, domain.TokenPurposeResetPassword)

	s.audit(ctx, user.ID, "password_changed", ip)
	s.notifyPasswordChanged(ctx, user, ip)
	return nil
}

func (s *Service) check2FA(ctx context.Context, userID uuid.UUID, code string) error {
	if s.d.TwoFA == nil {
		return nil
	}
	ok, err := s.d.TwoFA.Verify(ctx, userID, code)
	var de *domain.DomainError
	if errors.As(err, &de) && de.Code == "2fa_not_enabled" {
		return nil // sin 2FA: no se exige código
	}
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrInvalid2FACode
	}
	return nil
}

// ---- Política de contraseña ---------------------------------------------------

// ValidatePassword aplica la política: 10 a 128 caracteres y distinta del correo.
func ValidatePassword(password, email string) error {
	n := utf8.RuneCountInString(password)
	if n < MinPasswordLength || n > MaxPasswordLength {
		return domain.ErrWeakPassword
	}
	if email != "" && strings.EqualFold(strings.TrimSpace(password), strings.TrimSpace(email)) {
		return domain.ErrWeakPassword
	}
	return nil
}

// ---- Internos -----------------------------------------------------------------

func (s *Service) issueAndSend(ctx context.Context, user *domain.User, purpose domain.AccountTokenPurpose,
	ttl time.Duration, auditAction, ip string) error {

	if last, found, err := s.d.Tokens.LastCreatedAt(ctx, user.ID, purpose); err == nil && found &&
		s.d.Now().Sub(last) < ResendCooldown {
		return nil // cooldown: evita bombardear el buzón
	}
	if s.d.BaseURL == "" {
		return errors.New("APP_BASE_URL no configurada: no se pueden construir enlaces")
	}
	if _, err := mail.ParseAddress(user.Email); err != nil {
		return fmt.Errorf("correo del usuario inválido: %w", err)
	}

	token, err := newToken()
	if err != nil {
		return err
	}
	if err := s.d.Tokens.Create(ctx, user.ID, purpose, hashToken(token), s.d.Now().Add(ttl)); err != nil {
		return err
	}

	subject, body := s.compose(user, purpose, token, ttl)
	if err := s.d.Sender.Send(ctx, user.Email, subject, body); err != nil {
		return fmt.Errorf("enviar correo: %w", err)
	}
	s.audit(ctx, user.ID, auditAction, ip)
	return nil
}

func (s *Service) compose(user *domain.User, purpose domain.AccountTokenPurpose, token string, ttl time.Duration) (string, string) {
	name := strings.TrimSpace(user.FirstName)
	if name == "" {
		name = "usuario"
	}
	minutes := int(ttl.Minutes())
	validity := fmt.Sprintf("%d minutos", minutes)
	if minutes >= 60 {
		validity = fmt.Sprintf("%d horas", minutes/60)
	}

	switch purpose {
	case domain.TokenPurposeResetPassword:
		link := s.d.BaseURL + "/reset-password?token=" + token
		return "Restablece tu contraseña de Korzadi Wallet",
			fmt.Sprintf("Hola %s,\n\nRecibimos una solicitud para restablecer tu contraseña.\n\n"+
				"Abre este enlace (válido por %s, un solo uso):\n%s\n\n"+
				"Si no fuiste tú, ignora este correo: tu contraseña no cambiará.\n"+
				"Nunca compartas este enlace con nadie.\n\n— Korzadi Wallet", name, validity, link)
	default:
		link := s.d.BaseURL + "/verify-email?token=" + token
		return "Verifica tu correo en Korzadi Wallet",
			fmt.Sprintf("Hola %s,\n\nConfirma tu correo para activar tu cuenta.\n\n"+
				"Abre este enlace (válido por %s, un solo uso):\n%s\n\n"+
				"Si no creaste esta cuenta, ignora este correo.\n\n— Korzadi Wallet", name, validity, link)
	}
}

func (s *Service) notifyPasswordChanged(ctx context.Context, user *domain.User, ip string) {
	s.notify(ctx, user.ID, "security_alert", "Tu contraseña fue cambiada",
		fmt.Sprintf("La contraseña de tu cuenta fue cambiada desde la IP %s y cerramos tus sesiones. "+
			"Si no fuiste tú, contacta a soporte de inmediato.", ip))
	// Aviso por correo (best-effort): no incluye ningún enlace ni secreto.
	if s.d.Sender != nil {
		body := "Hola,\n\nLa contraseña de tu cuenta de Korzadi Wallet fue cambiada y cerramos tus sesiones activas.\n\n" +
			"Si no fuiste tú, contacta a soporte de inmediato.\n\n— Korzadi Wallet"
		if err := s.d.Sender.Send(ctx, user.Email, "Tu contraseña de Korzadi Wallet fue cambiada", body); err != nil {
			log.Printf("accountaccess: no se pudo enviar el aviso de cambio de contraseña user=%s: %v", user.ID, err)
		}
	}
}

func (s *Service) audit(ctx context.Context, userID uuid.UUID, action, ip string) {
	if s.d.Audit != nil {
		_ = s.d.Audit.Log(ctx, &userID, "user", action, "user", &userID, nil, ip)
	}
}

func (s *Service) notify(ctx context.Context, userID uuid.UUID, eventType, title, body string) {
	if s.d.Notifier != nil {
		_ = s.d.Notifier.Notify(ctx, userID, eventType, title, body)
	}
}

// newToken devuelve 256 bits aleatorios codificados en base64url (43 chars).
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generar token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
