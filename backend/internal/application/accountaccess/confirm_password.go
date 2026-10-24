package accountaccess

import (
	"context"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
)

// ConfirmPassword comprueba la contraseña actual de un usuario con sesión, para
// acciones sensibles (desactivar 2FA, regenerar códigos de recuperación). Aplica
// el mismo bloqueo por intentos fallidos que el login y el cambio de contraseña
// (5 fallos => 15 min), de modo que una sesión robada no puede adivinarla.
func (s *Service) ConfirmPassword(ctx context.Context, userID uuid.UUID, password, ip string) error {
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

	ok, verr := authjwt.VerifyPassword(password, user.PasswordHash)
	if verr != nil || !ok {
		_ = s.d.Users.IncrementFailedLogin(ctx, user.ID)
		if user.FailedLoginAttempts+1 >= maxFailedChangePassword {
			locked := s.d.Now().Add(changePasswordLock)
			user.LockedUntil = &locked
			_ = s.d.Users.Update(ctx, user)
		}
		s.audit(ctx, user.ID, "password_confirm_failed", ip)
		return domain.ErrInvalidCurrentPassword
	}
	_ = s.d.Users.ResetFailedLogin(ctx, user.ID)
	return nil
}
