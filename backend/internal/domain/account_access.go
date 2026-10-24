package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// AccountTokenPurpose distingue los tokens de un solo uso enviados por email.
type AccountTokenPurpose string

const (
	TokenPurposeVerifyEmail   AccountTokenPurpose = "verify_email"
	TokenPurposeResetPassword AccountTokenPurpose = "reset_password"
)

var (
	// ErrInvalidToken se devuelve igual para token inexistente, expirado, ya
	// usado o de un usuario inactivo: no se distingue para no filtrar información.
	ErrInvalidToken = &DomainError{Code: "invalid_token", Message: "el enlace es inválido o ya expiró"}

	ErrWeakPassword = &DomainError{Code: "weak_password",
		Message: "la contraseña debe tener entre 10 y 128 caracteres y no puede ser tu correo"}

	ErrInvalidCurrentPassword = &DomainError{Code: "invalid_current_password", Message: "la contraseña actual es incorrecta"}

	ErrSamePassword = &DomainError{Code: "same_password", Message: "la nueva contraseña debe ser distinta de la actual"}

	ErrInvalid2FACode = &DomainError{Code: "invalid_2fa_code", Message: "código 2FA requerido o inválido"}
)

// AccountTokenRepository persiste tokens de un solo uso (solo su hash).
type AccountTokenRepository interface {
	// Create guarda un token nuevo e invalida los anteriores sin usar del mismo
	// usuario y propósito (solo el último enlace enviado funciona).
	Create(ctx context.Context, userID uuid.UUID, purpose AccountTokenPurpose, tokenHash string, expiresAt time.Time) error
	// Consume marca el token como usado de forma atómica y devuelve su usuario.
	// ok=false si no existe, ya se usó o expiró (una sola llamada puede ganar).
	Consume(ctx context.Context, purpose AccountTokenPurpose, tokenHash string) (userID uuid.UUID, ok bool, err error)
	// LastCreatedAt devuelve cuándo se creó el último token (para el cooldown).
	LastCreatedAt(ctx context.Context, userID uuid.UUID, purpose AccountTokenPurpose) (t time.Time, found bool, err error)
	// InvalidateUnused anula los tokens pendientes de un usuario y propósito.
	InvalidateUnused(ctx context.Context, userID uuid.UUID, purpose AccountTokenPurpose) error
}

// EmailSender envía un correo de texto plano.
type EmailSender interface {
	Send(ctx context.Context, to, subject, body string) error
}
