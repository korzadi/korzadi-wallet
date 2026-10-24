package twofactor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
)

// memRepo reproduce la semántica atómica del repositorio Postgres.
type memRepo struct {
	mu       sync.Mutex
	exists   bool
	secret   string
	enabled  bool
	lastStep int64
	failed   int
	locked   *time.Time
	codes    []string
	creates  int
	clock    func() time.Time
}

func (m *memRepo) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

func (m *memRepo) Create(ctx context.Context, userID uuid.UUID, secretEncrypted string, hashes []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secret, m.enabled, m.exists, m.codes = secretEncrypted, false, true, append([]string(nil), hashes...)
	m.lastStep, m.failed, m.locked = 0, 0, nil
	m.creates++
	return nil
}
func (m *memRepo) Get(ctx context.Context, userID uuid.UUID) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.secret, m.enabled, nil
}
func (m *memRepo) Enable(ctx context.Context, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = true
	return nil
}
func (m *memRepo) GetState(ctx context.Context, userID uuid.UUID) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.exists {
		return State{}, nil
	}
	return State{Exists: true, SecretEncrypted: m.secret, Enabled: m.enabled, LastUsedStep: m.lastStep,
		LockedUntil: m.locked, RecoveryCodesRemaining: len(m.codes)}, nil
}
func (m *memRepo) ClaimStep(ctx context.Context, userID uuid.UUID, step int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastStep < step {
		m.lastStep = step
		return true, nil
	}
	return false, nil
}
func (m *memRepo) RegisterFailure(ctx context.Context, userID uuid.UUID, threshold int, lockFor time.Duration) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed+1 >= threshold {
		m.failed = 0
		t := m.now().Add(lockFor)
		m.locked = &t
		return &t, nil
	}
	m.failed++
	return nil, nil
}
func (m *memRepo) ResetFailures(ctx context.Context, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failed = 0
	return nil
}
func (m *memRepo) ConsumeRecoveryCode(ctx context.Context, userID uuid.UUID, hash string) (int, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.enabled {
		return 0, false, nil
	}
	for i, h := range m.codes {
		if h == hash {
			m.codes = append(m.codes[:i:i], m.codes[i+1:]...)
			return len(m.codes), true, nil
		}
	}
	return 0, false, nil
}
func (m *memRepo) ReplaceRecoveryCodes(ctx context.Context, userID uuid.UUID, hashes []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codes = append([]string(nil), hashes...)
	return nil
}
func (m *memRepo) Delete(ctx context.Context, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exists, m.enabled, m.secret, m.codes, m.lastStep, m.failed, m.locked = false, false, "", nil, 0, 0, nil
	return nil
}

type recAlerter struct {
	mu     sync.Mutex
	titles []string
}

func (a *recAlerter) Alert(ctx context.Context, id uuid.UUID, title, body string) {
	a.mu.Lock()
	a.titles = append(a.titles, title)
	a.mu.Unlock()
}
func (a *recAlerter) has(title string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.titles {
		if t == title {
			return true
		}
	}
	return false
}

type env struct {
	svc     *Service
	repo    *memRepo
	alerter *recAlerter
	uid     uuid.UUID
	secret  string // base32 en claro (el que ve el usuario)
	codes   []string
	clock   time.Time
	mu      sync.Mutex
}

func (e *env) now() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.clock }
func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.clock = e.clock.Add(d)
	e.mu.Unlock()
}

// totpAt genera el código TOTP válido para el reloj actual del entorno (+offset de intervalos).
func (e *env) totpAt(t *testing.T, offsetSteps int64) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(e.secret, e.now().Add(time.Duration(offsetSteps)*30*time.Second), totp.ValidateOpts{
		Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("generar TOTP: %v", err)
	}
	return code
}

// newEnabledEnv deja el usuario con 2FA YA ACTIVADO (setup + confirm) y devuelve el entorno.
func newEnabledEnv(t *testing.T) *env {
	t.Helper()
	encr, err := cryptutil.NewEncryptor(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{repo: &memRepo{}, alerter: &recAlerter{}, uid: uuid.New(), clock: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)}
	e.repo.clock = e.now
	e.svc = NewService(e.repo, "test", encr).WithAlerter(e.alerter)
	e.svc.now = e.now

	res, err := e.svc.BeginSetup(context.Background(), e.uid, "u@example.com")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	e.secret, e.codes = res.SecretBase32, res.RecoveryCodes
	if err := e.svc.Confirm(context.Background(), e.uid, e.totpAt(t, 0)); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	e.advance(31 * time.Second) // el código de Confirm ya quedó consumido: pasamos al siguiente intervalo
	return e
}

// ---- setup (comportamiento existente) ---------------------------------------------

func TestBeginSetup_RefusesWhenAlreadyEnabled(t *testing.T) {
	encr, err := cryptutil.NewEncryptor(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := &memRepo{}
	svc := NewService(repo, "test", encr)
	uid := uuid.New()

	if _, err := svc.BeginSetup(context.Background(), uid, "u@example.com"); err != nil {
		t.Fatalf("primer setup: %v", err)
	}
	if _, err := svc.BeginSetup(context.Background(), uid, "u@example.com"); err != nil { // aún pendiente
		t.Fatalf("re-setup pendiente: %v", err)
	}

	repo.enabled = true
	before := repo.secret
	_, err = svc.BeginSetup(context.Background(), uid, "u@example.com")
	de, ok := err.(*domain.DomainError)
	if !ok || de.Code != "2fa_already_enabled" {
		t.Fatalf("esperaba 2fa_already_enabled, obtuve %v", err)
	}
	if repo.secret != before || !repo.enabled {
		t.Fatal("el secreto/estado de 2FA habilitado no debe modificarse")
	}
}

func TestBeginSetup_RecoveryCodes_Are80BitsAndDistinct(t *testing.T) {
	e := newEnabledEnv(t)
	if len(e.codes) != RecoveryCodeCount {
		t.Fatalf("códigos = %d, esperaba %d", len(e.codes), RecoveryCodeCount)
	}
	seen := map[string]bool{}
	for _, c := range e.codes {
		parts := strings.Split(c, "-")
		if len(parts) != 4 || len(strings.ReplaceAll(c, "-", "")) != 16 || seen[c] {
			t.Fatalf("código con formato inválido o repetido: %q", c)
		}
		seen[c] = true
	}
}

// ---- anti-replay ---------------------------------------------------------------------

func TestVerify_TOTP_IsSingleUse(t *testing.T) {
	e := newEnabledEnv(t)
	ctx := context.Background()
	code := e.totpAt(t, 0)

	if ok, err := e.svc.Verify(ctx, e.uid, code); err != nil || !ok {
		t.Fatalf("primer uso: ok=%v err=%v", ok, err)
	}
	if ok, _ := e.svc.Verify(ctx, e.uid, code); ok {
		t.Fatal("REPLAY: el mismo código no debe aceptarse dos veces")
	}
	// Un código de un intervalo ANTERIOR al último usado tampoco sirve (orden estricto).
	if ok, _ := e.svc.Verify(ctx, e.uid, e.totpAt(t, -1)); ok {
		t.Fatal("un código de un intervalo anterior al último usado no debe aceptarse")
	}
	// El siguiente intervalo sí.
	e.advance(31 * time.Second)
	if ok, err := e.svc.Verify(ctx, e.uid, e.totpAt(t, 0)); err != nil || !ok {
		t.Fatalf("código del siguiente intervalo: ok=%v err=%v", ok, err)
	}
}

func TestConfirm_ConsumesCode_NoImmediateReplayForWithdrawal(t *testing.T) {
	encr, _ := cryptutil.NewEncryptor(strings.Repeat("ab", 32))
	e := &env{repo: &memRepo{}, uid: uuid.New(), clock: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)}
	e.svc = NewService(e.repo, "test", encr)
	e.svc.now = e.now
	res, _ := e.svc.BeginSetup(context.Background(), e.uid, "u@example.com")
	e.secret = res.SecretBase32

	code := e.totpAt(t, 0)
	if err := e.svc.Confirm(context.Background(), e.uid, code); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if ok, _ := e.svc.Verify(context.Background(), e.uid, code); ok {
		t.Fatal("el código usado para activar el 2FA no debe servir de inmediato para un retiro")
	}
}

func TestVerify_Concurrent_SameCode_ExactlyOneWins(t *testing.T) {
	e := newEnabledEnv(t)
	code := e.totpAt(t, 0)
	const n = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := e.svc.Verify(context.Background(), e.uid, code); err == nil && ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d verificaciones exitosas con el mismo código, esperaba exactamente 1", wins)
	}
}

// ---- bloqueo --------------------------------------------------------------------------

func TestVerify_LocksAfterFiveFailures_AndAlerts(t *testing.T) {
	e := newEnabledEnv(t)
	ctx := context.Background()

	for i := 1; i <= MaxFailedAttempts; i++ {
		ok, err := e.svc.Verify(ctx, e.uid, "000000")
		if ok || err != nil {
			t.Fatalf("intento %d: ok=%v err=%v (esperaba false sin error)", i, ok, err)
		}
	}
	if !e.alerter.has("Verificación 2FA bloqueada") {
		t.Fatal("al bloquear debe avisarse al titular")
	}

	// Bloqueado: ni un código CORRECTO se acepta.
	_, err := e.svc.Verify(ctx, e.uid, e.totpAt(t, 0))
	var le *LockedError
	if !errors.As(err, &le) || !errors.Is(err, ErrLocked) {
		t.Fatalf("esperaba LockedError, obtuve %v", err)
	}
	if d := le.RetryAfter(e.now()); d <= 0 || d > LockDuration {
		t.Fatalf("RetryAfter = %v", d)
	}

	// Pasado el bloqueo vuelve a funcionar.
	e.advance(LockDuration + time.Second)
	if ok, err := e.svc.Verify(ctx, e.uid, e.totpAt(t, 0)); err != nil || !ok {
		t.Fatalf("tras el bloqueo: ok=%v err=%v", ok, err)
	}
}

func TestVerify_SuccessResetsFailureCounter(t *testing.T) {
	e := newEnabledEnv(t)
	ctx := context.Background()
	for i := 0; i < MaxFailedAttempts-1; i++ {
		e.svc.Verify(ctx, e.uid, "000000")
	}
	if ok, _ := e.svc.Verify(ctx, e.uid, e.totpAt(t, 0)); !ok {
		t.Fatal("el código correcto debía aceptarse")
	}
	// Con el contador a cero, otros 4 fallos NO bloquean.
	for i := 0; i < MaxFailedAttempts-1; i++ {
		e.svc.Verify(ctx, e.uid, "000000")
	}
	e.advance(31 * time.Second)
	if _, err := e.svc.Verify(ctx, e.uid, e.totpAt(t, 0)); err != nil {
		t.Fatalf("no debía estar bloqueado: %v", err)
	}
}

func TestVerify_ReplayAttemptsCountAsFailures(t *testing.T) {
	e := newEnabledEnv(t)
	ctx := context.Background()
	code := e.totpAt(t, 0)
	e.svc.Verify(ctx, e.uid, code) // válido
	for i := 0; i < MaxFailedAttempts; i++ {
		e.svc.Verify(ctx, e.uid, code) // replays
	}
	if _, err := e.svc.Verify(ctx, e.uid, e.totpAt(t, 1)); !errors.Is(err, ErrLocked) {
		t.Fatalf("5 replays deben bloquear la verificación: %v", err)
	}
}

func TestVerify_NotEnabled_ReturnsStableError(t *testing.T) {
	encr, _ := cryptutil.NewEncryptor(strings.Repeat("ab", 32))
	svc := NewService(&memRepo{}, "test", encr)
	_, err := svc.Verify(context.Background(), uuid.New(), "123456")
	var de *domain.DomainError
	if !errors.As(err, &de) || de.Code != "2fa_not_enabled" {
		t.Fatalf("esperaba 2fa_not_enabled (accountaccess depende de este código), obtuve %v", err)
	}
}

// ---- códigos de recuperación -------------------------------------------------------------

func TestVerify_RecoveryCode_SingleUse_CaseAndDashInsensitive(t *testing.T) {
	e := newEnabledEnv(t)
	ctx := context.Background()
	code := e.codes[0]

	// minúsculas y sin guiones, con espacios alrededor
	mangled := " " + strings.ToLower(strings.ReplaceAll(code, "-", "")) + " "
	if ok, err := e.svc.Verify(ctx, e.uid, mangled); err != nil || !ok {
		t.Fatalf("código de recuperación válido: ok=%v err=%v", ok, err)
	}
	if ok, _ := e.svc.Verify(ctx, e.uid, code); ok {
		t.Fatal("un código de recuperación solo sirve una vez")
	}
	if !e.alerter.has("Código de recuperación usado") {
		t.Fatal("el uso de un código de recuperación debe avisarse al titular")
	}
	if _, rem, _ := e.svc.Status(ctx, e.uid); rem != RecoveryCodeCount-1 {
		t.Fatalf("quedan %d códigos, esperaba %d", rem, RecoveryCodeCount-1)
	}
}

func TestVerify_RecoveryCode_WrongOrMalformed_CountsAsFailure(t *testing.T) {
	e := newEnabledEnv(t)
	ctx := context.Background()
	for _, bad := range []string{"AAAA-BBBB-CCCC-DDDD", "no es un codigo", "12345", "", "1234567"} {
		if ok, _ := e.svc.Verify(ctx, e.uid, bad); ok {
			t.Fatalf("%q no debía aceptarse", bad)
		}
	}
	// 5 fallos => bloqueado
	if _, err := e.svc.Verify(ctx, e.uid, e.codes[1]); !errors.Is(err, ErrLocked) {
		t.Fatalf("los intentos con códigos inválidos deben bloquear: %v", err)
	}
}

// Los códigos de 8 caracteres emitidos ANTES de la Fase E (hash SHA-256 plano) siguen valiendo.
func TestVerify_LegacyRecoveryCode_StillAccepted(t *testing.T) {
	e := newEnabledEnv(t)
	legacy := "ABCD2345"
	e.repo.mu.Lock()
	e.repo.codes = append(e.repo.codes, recoveryHash(legacy))
	e.repo.mu.Unlock()

	if ok, err := e.svc.Verify(context.Background(), e.uid, legacy); err != nil || !ok {
		t.Fatalf("código legacy: ok=%v err=%v", ok, err)
	}
	if ok, _ := e.svc.Verify(context.Background(), e.uid, legacy); ok {
		t.Fatal("el código legacy también es de un solo uso")
	}
}

func TestRecoveryHash_Formats(t *testing.T) {
	if h := recoveryHash("ABCD2345"); len(h) != 64 || strings.HasPrefix(h, recoveryHashPrefix) {
		t.Fatalf("hash legacy inesperado: %q", h)
	}
	if h := recoveryHash("ABCD2345ABCD2345"); !strings.HasPrefix(h, recoveryHashPrefix) {
		t.Fatalf("hash v2 sin prefijo: %q", h)
	}
}

func TestVerify_LowRecoveryCodes_WarnsUser(t *testing.T) {
	e := newEnabledEnv(t)
	ctx := context.Background()
	for i := 0; i < RecoveryCodeCount-lowRecoveryCodes; i++ {
		if ok, _ := e.svc.Verify(ctx, e.uid, e.codes[i]); !ok {
			t.Fatalf("código %d debía valer", i)
		}
	}
	e.alerter.mu.Lock()
	n := len(e.alerter.titles)
	e.alerter.mu.Unlock()
	if n != RecoveryCodeCount-lowRecoveryCodes {
		t.Fatalf("avisos = %d", n)
	}
	if _, rem, _ := e.svc.Status(ctx, e.uid); rem != lowRecoveryCodes {
		t.Fatalf("quedan %d", rem)
	}
}

// ---- desactivar / regenerar / reset de soporte ---------------------------------------------

func TestDisable_RequiresValidCode_AndAllowsReprovision(t *testing.T) {
	e := newEnabledEnv(t)
	ctx := context.Background()

	if err := e.svc.Disable(ctx, e.uid, "000000"); !errors.Is(err, domain.ErrInvalid2FACode) {
		t.Fatalf("código inválido: %v", err)
	}
	if enabled, _, _ := e.svc.Status(ctx, e.uid); !enabled {
		t.Fatal("con un código inválido el 2FA debe seguir activo")
	}
	if err := e.svc.Disable(ctx, e.uid, e.totpAt(t, 0)); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if enabled, _, _ := e.svc.Status(ctx, e.uid); enabled {
		t.Fatal("el 2FA debía quedar desactivado")
	}
	if !e.alerter.has("2FA desactivado") {
		t.Fatal("desactivar el 2FA debe avisarse al titular")
	}
	// Sin 2FA no hay verificación posible (los retiros quedan bloqueados) y se puede volver a configurar.
	if _, err := e.svc.Verify(ctx, e.uid, e.totpAt(t, 0)); err == nil {
		t.Fatal("sin 2FA activo Verify debe devolver error")
	}
	if _, err := e.svc.BeginSetup(ctx, e.uid, "u@example.com"); err != nil {
		t.Fatalf("re-configurar tras desactivar: %v", err)
	}
}

func TestDisable_WithRecoveryCode_LostDevice(t *testing.T) {
	e := newEnabledEnv(t)
	if err := e.svc.Disable(context.Background(), e.uid, e.codes[3]); err != nil {
		t.Fatalf("perdió el dispositivo y usa un código de recuperación: %v", err)
	}
}

func TestRegenerateRecoveryCodes_InvalidatesOldOnes(t *testing.T) {
	e := newEnabledEnv(t)
	ctx := context.Background()

	if _, err := e.svc.RegenerateRecoveryCodes(ctx, e.uid, "000000"); !errors.Is(err, domain.ErrInvalid2FACode) {
		t.Fatalf("sin código válido: %v", err)
	}
	fresh, err := e.svc.RegenerateRecoveryCodes(ctx, e.uid, e.totpAt(t, 0))
	if err != nil || len(fresh) != RecoveryCodeCount {
		t.Fatalf("regenerar: %v (%d códigos)", err, len(fresh))
	}
	e.advance(31 * time.Second)
	if ok, _ := e.svc.Verify(ctx, e.uid, e.codes[0]); ok {
		t.Fatal("los códigos anteriores deben dejar de servir")
	}
	if ok, err := e.svc.Verify(ctx, e.uid, fresh[0]); err != nil || !ok {
		t.Fatalf("un código nuevo debía servir: ok=%v err=%v", ok, err)
	}
	if !e.alerter.has("Códigos de recuperación regenerados") {
		t.Fatal("falta el aviso al titular")
	}
}

func TestAdminReset_DeletesCredentials_AndAlerts(t *testing.T) {
	e := newEnabledEnv(t)
	if err := e.svc.AdminReset(context.Background(), e.uid); err != nil {
		t.Fatalf("AdminReset: %v", err)
	}
	if enabled, _, _ := e.svc.Status(context.Background(), e.uid); enabled {
		t.Fatal("el 2FA debía quedar eliminado")
	}
	if !e.alerter.has("2FA restablecido por soporte") {
		t.Fatal("falta el aviso al titular")
	}
}

func TestConfirm_WrongCode_RejectedAndCounted(t *testing.T) {
	encr, _ := cryptutil.NewEncryptor(strings.Repeat("ab", 32))
	e := &env{repo: &memRepo{}, uid: uuid.New(), clock: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)}
	e.svc = NewService(e.repo, "test", encr)
	e.svc.now = e.now
	e.repo.clock = e.now
	e.svc.BeginSetup(context.Background(), e.uid, "u@example.com")

	err := e.svc.Confirm(context.Background(), e.uid, "000000")
	var de *domain.DomainError
	if !errors.As(err, &de) || de.Code != "invalid_2fa_code" {
		t.Fatalf("esperaba invalid_2fa_code, obtuve %v", err)
	}
	for i := 0; i < MaxFailedAttempts; i++ {
		e.svc.Confirm(context.Background(), e.uid, "000000")
	}
	if err := e.svc.Confirm(context.Background(), e.uid, "000000"); !errors.Is(err, ErrLocked) {
		t.Fatalf("la activación también debe bloquearse: %v", err)
	}
}

func TestConfirm_WithoutSetup_StableError(t *testing.T) {
	encr, _ := cryptutil.NewEncryptor(strings.Repeat("ab", 32))
	svc := NewService(&memRepo{}, "test", encr)
	err := svc.Confirm(context.Background(), uuid.New(), "123456")
	var de *domain.DomainError
	if !errors.As(err, &de) || de.Code != "2fa_not_setup" {
		t.Fatalf("esperaba 2fa_not_setup, obtuve %v", err)
	}
}
