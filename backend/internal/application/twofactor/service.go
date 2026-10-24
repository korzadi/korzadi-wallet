// Package twofactor implementa el segundo factor (TOTP + códigos de recuperación).
//
// Garantías:
//   - Anti-replay: un código TOTP solo se acepta UNA vez (se guarda el último
//     intervalo de 30 s usado y se reclama con una operación atómica).
//   - Bloqueo: tras MaxFailedAttempts códigos incorrectos seguidos la
//     verificación se bloquea LockDuration (y se avisa al titular). Mientras
//     está bloqueada ni siquiera se evalúa el código.
//   - Códigos de recuperación de 80 bits, de un solo uso, aceptados en Verify.
//   - Desactivar el 2FA o regenerar los códigos exige un código válido.
package twofactor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
)

const (
	totpPeriod = 30
	totpSkew   = 1

	// MaxFailedAttempts es cuántos códigos incorrectos seguidos disparan el bloqueo.
	MaxFailedAttempts = 5
	// LockDuration es cuánto dura el bloqueo de la verificación.
	LockDuration = 15 * time.Minute
	// RecoveryCodeCount es cuántos códigos de recuperación se generan.
	RecoveryCodeCount = 10

	lowRecoveryCodes   = 2
	recoveryHashPrefix = "v2:"
	recoveryHashDomain = "korzadi-2fa-recovery-v2:"
)

// State es la instantánea de las credenciales 2FA de un usuario.
type State struct {
	Exists                 bool
	SecretEncrypted        string
	Enabled                bool
	LastUsedStep           int64
	LockedUntil            *time.Time
	RecoveryCodesRemaining int
}

type Repository interface {
	Create(ctx context.Context, userID uuid.UUID, secretEncrypted string, recoveryCodesHash []string) error
	Get(ctx context.Context, userID uuid.UUID) (secretEncrypted string, enabled bool, err error)
	Enable(ctx context.Context, userID uuid.UUID) error

	GetState(ctx context.Context, userID uuid.UUID) (State, error)
	// ClaimStep reclama de forma atómica un intervalo TOTP: true solo si es
	// estrictamente mayor que el último usado (anti-replay, una sola llamada gana).
	ClaimStep(ctx context.Context, userID uuid.UUID, step int64) (bool, error)
	// RegisterFailure suma un fallo; al llegar a threshold bloquea lockFor, pone el
	// contador a 0 y devuelve hasta cuándo. Devuelve nil si no se bloqueó.
	RegisterFailure(ctx context.Context, userID uuid.UUID, threshold int, lockFor time.Duration) (lockedUntil *time.Time, err error)
	ResetFailures(ctx context.Context, userID uuid.UUID) error
	// ConsumeRecoveryCode borra el hash de forma atómica (un solo uso) y devuelve cuántos quedan.
	ConsumeRecoveryCode(ctx context.Context, userID uuid.UUID, codeHash string) (remaining int, ok bool, err error)
	ReplaceRecoveryCodes(ctx context.Context, userID uuid.UUID, codeHashes []string) error
	Delete(ctx context.Context, userID uuid.UUID) error
}

// Alerter avisa al titular de eventos de seguridad (notificación + correo).
type Alerter interface {
	Alert(ctx context.Context, userID uuid.UUID, title, body string)
}

var ErrLocked = errors.New("twofactor: verificación bloqueada por demasiados intentos")

// LockedError indica que la verificación está bloqueada hasta Until.
type LockedError struct{ Until time.Time }

func (e *LockedError) Error() string {
	return fmt.Sprintf("%s (hasta %s)", ErrLocked.Error(), e.Until.UTC().Format(time.RFC3339))
}
func (e *LockedError) Is(target error) bool { return target == ErrLocked }

// RetryAfter devuelve cuánto falta para que termine el bloqueo.
func (e *LockedError) RetryAfter(now time.Time) time.Duration {
	if d := e.Until.Sub(now); d > 0 {
		return d
	}
	return 0
}

type Service struct {
	repo      Repository
	issuer    string
	encryptor *cryptutil.Encryptor
	alerter   Alerter
	now       func() time.Time
}

func NewService(repo Repository, issuer string, encryptor *cryptutil.Encryptor) *Service {
	return &Service{repo: repo, issuer: issuer, encryptor: encryptor, now: time.Now}
}

// WithAlerter activa los avisos al titular (bloqueo, códigos de recuperación, cambios).
func (s *Service) WithAlerter(a Alerter) *Service {
	s.alerter = a
	return s
}

type SetupResult struct {
	SecretBase32  string
	OTPAuthURL    string
	RecoveryCodes []string
}

func (s *Service) BeginSetup(ctx context.Context, userID uuid.UUID, userEmail string) (*SetupResult, error) {
	// AUDIT FIX (HIGH): Create() upserts and resets enabled=false, so an
	// authenticated session (e.g. a stolen access token) could call setup again,
	// replace the victim's TOTP secret and re-enable it with the attacker's
	// own authenticator — taking over the 2FA that protects withdrawals.
	// Once 2FA is enabled it can no longer be re-provisioned from here.
	if _, enabled, err := s.repo.Get(ctx, userID); err != nil {
		return nil, err
	} else if enabled {
		return nil, &domain.DomainError{Code: "2fa_already_enabled", Message: "2FA ya está activado para esta cuenta"}
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      s.issuer,
		AccountName: userEmail,
	})
	if err != nil {
		return nil, err
	}

	codes, hashes, err := generateRecoveryCodes()
	if err != nil {
		return nil, err
	}

	encryptedSecret, err := s.encryptor.Encrypt(key.Secret(), cryptutil.ForTwoFactor(userID))
	if err != nil {
		return nil, err
	}

	if err := s.repo.Create(ctx, userID, encryptedSecret, hashes); err != nil {
		return nil, err
	}

	return &SetupResult{SecretBase32: key.Secret(), OTPAuthURL: key.URL(), RecoveryCodes: codes}, nil
}

// Confirm activa el 2FA tras validar el primer código. El código usado queda
// consumido (no se puede reutilizar enseguida para un retiro).
func (s *Service) Confirm(ctx context.Context, userID uuid.UUID, code string) error {
	st, err := s.repo.GetState(ctx, userID)
	if err != nil {
		return err
	}
	if !st.Exists {
		return &domain.DomainError{Code: "2fa_not_setup", Message: "primero inicia la configuración de 2FA"}
	}
	if st.Enabled {
		return nil
	}
	if st.LockedUntil != nil && s.now().Before(*st.LockedUntil) {
		return &LockedError{Until: *st.LockedUntil}
	}
	secret, err := s.encryptor.Decrypt(ctx, st.SecretEncrypted, cryptutil.ForTwoFactor(userID))
	if err != nil {
		return err
	}
	step, ok := matchStep(secret, strings.TrimSpace(code), s.now())
	if ok {
		claimed, cerr := s.repo.ClaimStep(ctx, userID, step)
		if cerr != nil {
			return cerr
		}
		ok = claimed
	}
	if !ok {
		s.registerFailure(ctx, userID)
		return &domain.DomainError{Code: "invalid_2fa_code", Message: "código incorrecto"}
	}
	if err := s.repo.Enable(ctx, userID); err != nil {
		return err
	}
	_ = s.repo.ResetFailures(ctx, userID)
	return nil
}

// Verify valida un código TOTP (de un solo uso) o un código de recuperación
// (de un solo uso). Si no hay 2FA devuelve el error "2fa_not_enabled"; si la
// verificación está bloqueada devuelve *LockedError sin evaluar el código.
func (s *Service) Verify(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	st, err := s.repo.GetState(ctx, userID)
	if err != nil {
		return false, err
	}
	if !st.Exists || !st.Enabled {
		return false, &domain.DomainError{Code: "2fa_not_enabled", Message: "2FA no está activado para esta cuenta"}
	}
	if st.LockedUntil != nil && s.now().Before(*st.LockedUntil) {
		return false, &LockedError{Until: *st.LockedUntil}
	}

	code = strings.TrimSpace(code)
	valid := false
	switch {
	case totpRe.MatchString(code):
		secret, derr := s.encryptor.Decrypt(ctx, st.SecretEncrypted, cryptutil.ForTwoFactor(userID))
		if derr != nil {
			return false, derr
		}
		if step, ok := matchStep(secret, code, s.now()); ok {
			claimed, cerr := s.repo.ClaimStep(ctx, userID, step)
			if cerr != nil {
				return false, cerr
			}
			valid = claimed // false => código ya usado (replay)
		}
	default:
		ok, rerr := s.consumeRecovery(ctx, userID, code)
		if rerr != nil {
			return false, rerr
		}
		valid = ok
	}

	if !valid {
		s.registerFailure(ctx, userID)
		return false, nil
	}
	_ = s.repo.ResetFailures(ctx, userID)
	return true, nil
}

// Status devuelve si el 2FA está activo y cuántos códigos de recuperación quedan.
func (s *Service) Status(ctx context.Context, userID uuid.UUID) (enabled bool, recoveryRemaining int, err error) {
	st, err := s.repo.GetState(ctx, userID)
	if err != nil {
		return false, 0, err
	}
	return st.Exists && st.Enabled, st.RecoveryCodesRemaining, nil
}

// Disable desactiva el 2FA y borra sus credenciales. Exige un código válido
// (TOTP o de recuperación); el llamador debe haber confirmado además la contraseña.
func (s *Service) Disable(ctx context.Context, userID uuid.UUID, code string) error {
	ok, err := s.Verify(ctx, userID, code)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrInvalid2FACode
	}
	if err := s.repo.Delete(ctx, userID); err != nil {
		return err
	}
	s.alert(ctx, userID, "2FA desactivado",
		"Se desactivó la autenticación de dos factores de tu cuenta. Mientras esté desactivada no podrás retirar fondos. "+
			"Si no fuiste tú, cambia tu contraseña de inmediato y contacta a soporte.")
	return nil
}

// RegenerateRecoveryCodes reemplaza TODOS los códigos de recuperación por 10
// nuevos (los anteriores dejan de servir). Exige un código válido.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	ok, err := s.Verify(ctx, userID, code)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.ErrInvalid2FACode
	}
	codes, hashes, err := generateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, userID, hashes); err != nil {
		return nil, err
	}
	s.alert(ctx, userID, "Códigos de recuperación regenerados",
		"Generaste códigos de recuperación nuevos; los anteriores ya no funcionan. Si no fuiste tú, contacta a soporte de inmediato.")
	return codes, nil
}

// AdminReset borra las credenciales 2FA de un usuario (soporte: pérdida del
// dispositivo y de los códigos). Sin 2FA no podrá retirar hasta volver a activarlo.
func (s *Service) AdminReset(ctx context.Context, userID uuid.UUID) error {
	if err := s.repo.Delete(ctx, userID); err != nil {
		return err
	}
	s.alert(ctx, userID, "2FA restablecido por soporte",
		"Soporte restableció la autenticación de dos factores de tu cuenta. Debes volver a activarla para poder retirar fondos.")
	return nil
}

// ---- internos -------------------------------------------------------------------

var (
	totpRe     = regexp.MustCompile(`^[0-9]{6}$`)
	recoveryRe = regexp.MustCompile(`^([A-Z2-7]{8}|[A-Z2-7]{16})$`)
)

func (s *Service) registerFailure(ctx context.Context, userID uuid.UUID) {
	until, err := s.repo.RegisterFailure(ctx, userID, MaxFailedAttempts, LockDuration)
	if err != nil {
		log.Printf("twofactor: no se pudo registrar el intento fallido user=%s: %v", userID, err)
		return
	}
	if until != nil {
		log.Printf("twofactor: verificación BLOQUEADA user=%s hasta %s", userID, until.UTC().Format(time.RFC3339))
		s.alert(ctx, userID, "Verificación 2FA bloqueada",
			fmt.Sprintf("Detectamos %d códigos 2FA incorrectos seguidos y bloqueamos la verificación por %d minutos. "+
				"Si no fuiste tú, alguien puede tener tu sesión: cambia tu contraseña.", MaxFailedAttempts, int(LockDuration.Minutes())))
	}
}

func (s *Service) consumeRecovery(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	norm := normalizeRecovery(code)
	if !recoveryRe.MatchString(norm) {
		return false, nil
	}
	remaining, ok, err := s.repo.ConsumeRecoveryCode(ctx, userID, recoveryHash(norm))
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	body := fmt.Sprintf("Se usó un código de recuperación para verificar tu identidad. Te quedan %d.", remaining)
	if remaining <= lowRecoveryCodes {
		body += " Quedan pocos: genera códigos nuevos desde Seguridad."
	}
	s.alert(ctx, userID, "Código de recuperación usado", body+" Si no fuiste tú, cambia tu contraseña de inmediato.")
	return true, nil
}

func (s *Service) alert(ctx context.Context, userID uuid.UUID, title, body string) {
	if s.alerter != nil {
		s.alerter.Alert(ctx, userID, title, body)
	}
}

// matchStep devuelve el intervalo TOTP al que corresponde el código (±1 intervalo
// de tolerancia de reloj). La comparación es en tiempo constante.
func matchStep(secret, code string, now time.Time) (int64, bool) {
	if !totpRe.MatchString(code) {
		return 0, false
	}
	base := now.Unix() / totpPeriod
	var matched int64
	found := false
	for _, off := range []int64{-1, 0, 1} {
		step := base + off
		expected, err := totp.GenerateCodeCustom(secret, time.Unix(step*totpPeriod, 0), totp.ValidateOpts{
			Period: totpPeriod, Skew: totpSkew, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 && (!found || step > matched) {
			matched, found = step, true
		}
	}
	return matched, found
}

func normalizeRecovery(code string) string {
	r := strings.NewReplacer("-", "", " ", "")
	return strings.ToUpper(r.Replace(code))
}

// recoveryHash: los códigos nuevos (16 caracteres, 80 bits) llevan un prefijo de
// dominio; los antiguos (8 caracteres, 40 bits) conservan su hash SHA-256 plano
// para que los ya emitidos sigan funcionando.
func recoveryHash(norm string) string {
	if len(norm) == 8 {
		h := sha256.Sum256([]byte(norm))
		return hex.EncodeToString(h[:])
	}
	h := sha256.Sum256([]byte(recoveryHashDomain + norm))
	return recoveryHashPrefix + hex.EncodeToString(h[:])
}

// generateRecoveryCodes crea RecoveryCodeCount códigos de 80 bits, mostrados
// como XXXX-XXXX-XXXX-XXXX, y los hashes que se guardan.
func generateRecoveryCodes() (codes, hashes []string, err error) {
	codes = make([]string, RecoveryCodeCount)
	hashes = make([]string, RecoveryCodeCount)
	for i := range codes {
		buf := make([]byte, 10)
		if _, err := rand.Read(buf); err != nil {
			return nil, nil, err
		}
		raw := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf) // 16 chars
		codes[i] = raw[0:4] + "-" + raw[4:8] + "-" + raw[8:12] + "-" + raw[12:16]
		hashes[i] = recoveryHash(raw)
	}
	return codes, hashes, nil
}
