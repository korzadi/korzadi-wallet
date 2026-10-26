// Package prefs implementa preferencias de seguridad del usuario: PIN, bloqueo
// de app, biometría (WebAuthn) y alertas por correo. Nada de esto toca saldos;
// solo autoriza/desbloquea operaciones sensibles y registra credenciales.
package prefs

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"golang.org/x/crypto/argon2"
)

var (
	ErrPinRequired      = errors.New("prefs: PIN requerido")
	ErrPinInvalid       = errors.New("prefs: PIN incorrecto")
	ErrPinLocked        = errors.New("prefs: PIN bloqueado por intentos fallidos")
	ErrPinLength        = errors.New("prefs: el PIN debe tener entre 4 y 16 dígitos")
	ErrAppLockRequired  = errors.New("prefs: activa el bloqueo de la app antes de registrar biometría")
	ErrBiometricAlready = errors.New("prefs: biometría ya registrada para este usuario")
	ErrCredentialNotFound = errors.New("prefs: credencial no encontrada")
)

const (
	pinArgonTime    = 1
	pinArgonMemory  = 64 * 1024
	pinArgonThreads = 4
	pinArgonKeyLen  = 32
)

type Prefs struct {
	UserID             uuid.UUID
	PinHash            string
	PinEnabled         bool
	AppLockEnabled     bool
	BiometricEnabled   bool
	EmailAlertsEnabled bool
	UpdatedAt          string
}

type Credential struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	CredentialID string
	PublicKey   string
	SignCount   int
	Transports  []string
	AAGUID      string
	Label       string
	CreatedAt   string
	LastUsedAt  *string
}

// PasswordConfirmer valida la contraseña actual del usuario (implementado por
// accountaccess.Service, con bloqueo por intentos).
type PasswordConfirmer interface {
	ConfirmPassword(ctx context.Context, userID uuid.UUID, password, ip string) error
}

// Repository persiste preferencias y desafíos WebAuthn.
type Repository interface {
	GetPrefs(ctx context.Context, userID uuid.UUID) (*Prefs, error)
	UpsertPrefs(ctx context.Context, userID uuid.UUID, pinHash string, pinEnabled, appLockEnabled, biometricEnabled, emailAlertsEnabled bool) error
	ClearPin(ctx context.Context, userID uuid.UUID) error
	IncPinFailures(ctx context.Context, userID uuid.UUID) error
	ResetPinFailures(ctx context.Context, userID uuid.UUID) error
	GetPinFailureState(ctx context.Context, userID uuid.UUID) (failures int, lockedUntil time.Time, err error)

	CreateChallenge(ctx context.Context, userID uuid.UUID, kind, challenge string, expiresAt string) error
	ConsumeChallenge(ctx context.Context, challenge string) (userID uuid.UUID, kind string, sessionJSON string, err error)
}

type Service struct {
	repo     Repository
	confirmer PasswordConfirmer
	now      func() time.Time
}

func NewService(repo Repository, confirmer PasswordConfirmer) *Service {
	return &Service{repo: repo, confirmer: confirmer, now: time.Now}
}

func (s *Service) GetPrefs(ctx context.Context, userID uuid.UUID) (*Prefs, error) {
	return s.repo.GetPrefs(ctx, userID)
}

func (s *Service) Sanitize(p *Prefs) *Prefs {
	if p == nil {
		return nil
	}
	return &Prefs{
		UserID:             p.UserID,
		PinEnabled:         p.PinEnabled,
		AppLockEnabled:     p.AppLockEnabled,
		BiometricEnabled:   p.BiometricEnabled,
		EmailAlertsEnabled: p.EmailAlertsEnabled,
		UpdatedAt:          p.UpdatedAt,
	}
}

func hashPin(pin string) (string, error) {
	salt := make([]byte, 16)
	if err := readRandom(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(pin), salt, pinArgonTime, pinArgonMemory, pinArgonThreads, pinArgonKeyLen)
	return base64Std(salt) + "$" + base64Std(hash), nil
}

func verifyPin(pin, encoded string) bool {
	saltB64, hashB64 := splitOnce(encoded, "$")
	if saltB64 == "" || hashB64 == "" {
		return false
	}
	salt, err := base64Decode(saltB64)
	if err != nil {
		return false
	}
	expected, err := base64Decode(hashB64)
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(pin), salt, pinArgonTime, pinArgonMemory, pinArgonThreads, pinArgonKeyLen)
	if len(actual) != len(expected) {
		return false
	}
	var diff byte
	for i := range actual {
		diff |= actual[i] ^ expected[i]
	}
	return diff == 0
}

func (s *Service) SetupPin(ctx context.Context, userID uuid.UUID, pin, currentPassword, ip string) error {
	if len(pin) < 4 || len(pin) > 16 || !onlyDigits(pin) {
		return ErrPinLength
	}
	if err := s.confirmer.ConfirmPassword(ctx, userID, currentPassword, ip); err != nil {
		return err
	}
	pinHash, err := hashPin(pin)
	if err != nil {
		return err
	}
	return s.repo.UpsertPrefs(ctx, userID, pinHash, true, false, false, false)
}

func (s *Service) VerifyPin(ctx context.Context, userID uuid.UUID, pin string) (bool, error) {
	failures, lockedUntil, err := s.repo.GetPinFailureState(ctx, userID)
	if err != nil {
		return false, err
	}
	if failures >= 5 && !lockedUntil.IsZero() && s.now().Before(lockedUntil) {
		return false, ErrPinLocked
	}
	prefs, err := s.repo.GetPrefs(ctx, userID)
	if err != nil {
		return false, err
	}
	if !prefs.PinEnabled {
		return false, ErrPinRequired
	}
	ok := verifyPin(pin, prefs.PinHash)
	if !ok {
		_ = s.repo.IncPinFailures(ctx, userID)
		return false, ErrPinInvalid
	}
	_ = s.repo.ResetPinFailures(ctx, userID)
	return true, nil
}

func (s *Service) DisablePin(ctx context.Context, userID uuid.UUID, currentPassword, ip string) error {
	if err := s.confirmer.ConfirmPassword(ctx, userID, currentPassword, ip); err != nil {
		return err
	}
	return s.repo.ClearPin(ctx, userID)
}

func (s *Service) UpdatePrefs(ctx context.Context, userID uuid.UUID, appLockEnabled, biometricEnabled, emailAlertsEnabled bool) error {
	prefs, err := s.repo.GetPrefs(ctx, userID)
	if err != nil {
		return err
	}
	if biometricEnabled && !appLockEnabled {
		return ErrAppLockRequired
	}
	return s.repo.UpsertPrefs(ctx, userID, prefs.PinHash, prefs.PinEnabled, appLockEnabled, biometricEnabled, emailAlertsEnabled)
}

func readRandom(buf []byte) error {
	_, err := rand.Read(buf)
	return err
}

func base64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func base64Decode(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

func splitOnce(s, sep string) (string, string) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func onlyDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
