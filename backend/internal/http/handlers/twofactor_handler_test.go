package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"

	"korzadi-wallet/backend/internal/application/twofactor"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
)

// h2faRepo: repositorio 2FA en memoria con la misma semántica atómica que Postgres.
type h2faRepo struct {
	mu       sync.Mutex
	exists   bool
	secret   string
	enabled  bool
	lastStep int64
	failed   int
	locked   *time.Time
	codes    []string
}

func (r *h2faRepo) Create(ctx context.Context, u uuid.UUID, secret string, hashes []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exists, r.secret, r.enabled, r.codes, r.lastStep, r.failed, r.locked = true, secret, false, hashes, 0, 0, nil
	return nil
}
func (r *h2faRepo) Get(ctx context.Context, u uuid.UUID) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.secret, r.enabled, nil
}
func (r *h2faRepo) Enable(ctx context.Context, u uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = true
	return nil
}
func (r *h2faRepo) GetState(ctx context.Context, u uuid.UUID) (twofactor.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.exists {
		return twofactor.State{}, nil
	}
	return twofactor.State{Exists: true, SecretEncrypted: r.secret, Enabled: r.enabled, LastUsedStep: r.lastStep,
		LockedUntil: r.locked, RecoveryCodesRemaining: len(r.codes)}, nil
}
func (r *h2faRepo) ClaimStep(ctx context.Context, u uuid.UUID, step int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastStep < step {
		r.lastStep = step
		return true, nil
	}
	return false, nil
}
func (r *h2faRepo) RegisterFailure(ctx context.Context, u uuid.UUID, threshold int, lockFor time.Duration) (*time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed+1 >= threshold {
		r.failed = 0
		t := time.Now().Add(lockFor)
		r.locked = &t
		return &t, nil
	}
	r.failed++
	return nil, nil
}
func (r *h2faRepo) ResetFailures(ctx context.Context, u uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = 0
	return nil
}
func (r *h2faRepo) ConsumeRecoveryCode(ctx context.Context, u uuid.UUID, hash string) (int, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, h := range r.codes {
		if h == hash {
			r.codes = append(r.codes[:i:i], r.codes[i+1:]...)
			return len(r.codes), true, nil
		}
	}
	return 0, false, nil
}
func (r *h2faRepo) ReplaceRecoveryCodes(ctx context.Context, u uuid.UUID, hashes []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.codes = hashes
	return nil
}
func (r *h2faRepo) Delete(ctx context.Context, u uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exists, r.enabled, r.secret, r.codes = false, false, "", nil
	return nil
}

type h2faPasswords struct{ err error }

func (p h2faPasswords) ConfirmPassword(ctx context.Context, u uuid.UUID, pw, ip string) error {
	return p.err
}

type h2faSessions struct {
	mu      sync.Mutex
	revoked []uuid.UUID
}

func (s *h2faSessions) RevokeAllForUser(ctx context.Context, id uuid.UUID) error {
	s.mu.Lock()
	s.revoked = append(s.revoked, id)
	s.mu.Unlock()
	return nil
}

type h2faEnv struct {
	router   *gin.Engine
	svc      *twofactor.Service
	repo     *h2faRepo
	uid      uuid.UUID
	secret   string
	codes    []string
	audit    *acctAudit
	sessions *h2faSessions
	users    *acctUsers
}

func nextTOTP(secret string) string {
	code, _ := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	return code
}

// newH2FAEnv deja a un usuario con 2FA ACTIVADO y un router con todas las rutas del 2FA.
func newH2FAEnv(t *testing.T, pwErr error) *h2faEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	encr, err := cryptutil.NewEncryptor(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	e := &h2faEnv{repo: &h2faRepo{}, uid: uuid.New(), audit: &acctAudit{}, sessions: &h2faSessions{}, users: newAcctUsers()}
	e.users.add(&domain.User{ID: e.uid, Email: "ana@example.com", Status: domain.UserStatusActive, EmailVerified: true})
	e.svc = twofactor.NewService(e.repo, "test", encr)

	res, err := e.svc.BeginSetup(context.Background(), e.uid, "ana@example.com")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	e.secret, e.codes = res.SecretBase32, res.RecoveryCodes
	confirm, _ := totp.GenerateCode(e.secret, time.Now())
	if err := e.svc.Confirm(context.Background(), e.uid, confirm); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	h := NewTwoFactorHandler(e.svc, e.users).WithPasswordConfirmer(h2faPasswords{err: pwErr}).WithAudit(e.audit).WithSessions(e.sessions)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", e.uid.String()); c.Next() })
	r.POST("/sensitive", TwoFAVerifyMiddleware(e.svc), func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.POST("/2fa/setup", h.Setup)
	r.GET("/2fa/status", h.Status)
	r.POST("/2fa/disable", h.Disable)
	r.POST("/2fa/recovery-codes/regenerate", h.RegenerateRecoveryCodes)
	r.POST("/admin/users/:id/reset-2fa", h.AdminReset)
	e.router = r
	return e
}

func (e *h2faEnv) do(method, path string, body any, _ map[string]string) *httptest.ResponseRecorder {
	if method == http.MethodGet {
		w := httptest.NewRecorder()
		e.router.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}
	return acctPost(e.router, path, body)
}

// ---- middleware ----------------------------------------------------------------------

func TestTwoFAMiddleware_RequiredInvalidValidReplay(t *testing.T) {
	e := newH2FAEnv(t, nil)

	// el helper acctPostH hace una petición extra sin header: usamos peticiones directas para este test
	call := func(code string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/sensitive", strings.NewReader("{}"))
		if code != "" {
			req.Header.Set("X-2FA-Code", code)
		}
		w := httptest.NewRecorder()
		e.router.ServeHTTP(w, req)
		return w
	}

	if w := call(""); w.Code != http.StatusForbidden {
		t.Fatalf("sin código: %d", w.Code)
	}
	if w := call("000000"); w.Code != http.StatusForbidden || errCode(w) != "invalid_2fa_code" {
		t.Fatalf("código inválido: %d %s", w.Code, w.Body.String())
	}
	good := nextTOTP(e.secret)
	if w := call(good); w.Code != http.StatusOK {
		t.Fatalf("código válido: %d %s", w.Code, w.Body.String())
	}
	if w := call(good); w.Code != http.StatusForbidden {
		t.Fatalf("REPLAY del mismo código debe rechazarse: %d", w.Code)
	}
	// Un código de recuperación vale como segundo factor, una sola vez.
	if w := call(e.codes[0]); w.Code != http.StatusOK {
		t.Fatalf("código de recuperación: %d %s", w.Code, w.Body.String())
	}
	if w := call(e.codes[0]); w.Code != http.StatusForbidden {
		t.Fatalf("un código de recuperación no puede reutilizarse: %d", w.Code)
	}
}

func TestTwoFAMiddleware_Locked_Returns429WithRetryAfter(t *testing.T) {
	e := newH2FAEnv(t, nil)
	call := func(code string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/sensitive", strings.NewReader("{}"))
		req.Header.Set("X-2FA-Code", code)
		w := httptest.NewRecorder()
		e.router.ServeHTTP(w, req)
		return w
	}
	for i := 0; i < twofactor.MaxFailedAttempts; i++ {
		call("000000")
	}
	// Bloqueado: incluso un código correcto recibe 429, no 403.
	w := call(nextTOTP(e.secret))
	if w.Code != http.StatusTooManyRequests || errCode(w) != "2fa_locked" {
		t.Fatalf("esperaba 429 2fa_locked, obtuve %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("falta la cabecera Retry-After")
	}
}

// ---- status / setup ------------------------------------------------------------------

func TestTwoFAStatus(t *testing.T) {
	e := newH2FAEnv(t, nil)
	w := e.do(http.MethodGet, "/2fa/status", nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":true`) ||
		!strings.Contains(w.Body.String(), `"recovery_codes_remaining":10`) {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
}

// El frontend lee secret/recovery_codes; el cliente histórico leía SecretBase32/RecoveryCodes.
func TestTwoFASetup_ResponseHasBothKeySets(t *testing.T) {
	e := newH2FAEnv(t, nil)
	e.repo.Delete(context.Background(), e.uid) // volver a "sin 2FA" para poder hacer setup
	w := e.do(http.MethodPost, "/2fa/setup", gin.H{}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	for _, key := range []string{`"secret"`, `"otpauth_url"`, `"recovery_codes"`, `"SecretBase32"`, `"OTPAuthURL"`, `"RecoveryCodes"`} {
		if !strings.Contains(w.Body.String(), key) {
			t.Errorf("falta la clave %s en la respuesta de setup", key)
		}
	}
}

// ---- desactivar / regenerar -----------------------------------------------------------

func TestTwoFADisable_RequiresPasswordAndCode(t *testing.T) {
	// contraseña incorrecta
	e := newH2FAEnv(t, domain.ErrInvalidCurrentPassword)
	body := gin.H{"password": "mala", "code": nextTOTP(e.secret)}
	if w := e.do(http.MethodPost, "/2fa/disable", body, nil); w.Code != http.StatusBadRequest || errCode(w) != "invalid_current_password" {
		t.Fatalf("contraseña incorrecta: %d %s", w.Code, w.Body.String())
	}
	if on, _, _ := e.svc.Status(context.Background(), e.uid); !on {
		t.Fatal("con la contraseña incorrecta el 2FA debe seguir activo")
	}

	// contraseña correcta, código incorrecto
	e = newH2FAEnv(t, nil)
	if w := e.do(http.MethodPost, "/2fa/disable", gin.H{"password": "ok", "code": "000000"}, nil); w.Code != http.StatusForbidden || errCode(w) != "invalid_2fa_code" {
		t.Fatalf("código incorrecto: %d %s", w.Code, w.Body.String())
	}
	if on, _, _ := e.svc.Status(context.Background(), e.uid); !on {
		t.Fatal("con el código incorrecto el 2FA debe seguir activo")
	}

	// ambos correctos
	if w := e.do(http.MethodPost, "/2fa/disable", gin.H{"password": "ok", "code": nextTOTP(e.secret)}, nil); w.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	if on, _, _ := e.svc.Status(context.Background(), e.uid); on {
		t.Fatal("el 2FA debía quedar desactivado")
	}
	if len(e.audit.actions) != 1 || e.audit.actions[0] != "two_factor_disabled" {
		t.Fatalf("auditoría: %v", e.audit.actions)
	}
}

func TestTwoFADisable_MalformedBody_And_NoPasswordConfirmer(t *testing.T) {
	e := newH2FAEnv(t, nil)
	if w := e.do(http.MethodPost, "/2fa/disable", gin.H{"code": "123456"}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("sin contraseña: %d", w.Code)
	}
	// Handler sin PasswordConfirmer => 503 (nunca desactiva sin verificar la contraseña).
	h := NewTwoFactorHandler(e.svc, e.users)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", e.uid.String()); c.Next() })
	r.POST("/d", h.Disable)
	if w := acctPost(r, "/d", gin.H{"password": "x", "code": "123456"}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("sin confirmador: %d", w.Code)
	}
}

func TestTwoFARegenerateRecoveryCodes(t *testing.T) {
	e := newH2FAEnv(t, nil)
	w := e.do(http.MethodPost, "/2fa/recovery-codes/regenerate", gin.H{"password": "ok", "code": e.codes[2]}, nil)
	if w.Code != http.StatusOK || strings.Count(w.Body.String(), "-") < 30 { // 10 códigos con 3 guiones cada uno
		t.Fatalf("regenerate: %d %s", w.Code, w.Body.String())
	}
	if len(e.audit.actions) != 1 || e.audit.actions[0] != "two_factor_recovery_codes_regenerated" {
		t.Fatalf("auditoría: %v", e.audit.actions)
	}
	// El código usado para autorizar y los antiguos ya no sirven.
	if ok, _ := e.svc.Verify(context.Background(), e.uid, e.codes[0]); ok {
		t.Fatal("los códigos antiguos deben dejar de servir")
	}
}

// ---- reset de soporte -------------------------------------------------------------------

func TestTwoFAAdminReset(t *testing.T) {
	e := newH2FAEnv(t, nil)
	path := "/admin/users/" + e.uid.String() + "/reset-2fa"

	if w := e.do(http.MethodPost, path, gin.H{}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("sin motivo: %d", w.Code)
	}
	if w := e.do(http.MethodPost, "/admin/users/"+uuid.NewString()+"/reset-2fa", gin.H{"reason": "pérdida del dispositivo"}, nil); w.Code != http.StatusNotFound {
		t.Fatalf("usuario inexistente: %d", w.Code)
	}
	if w := e.do(http.MethodPost, path, gin.H{"reason": "pérdida del dispositivo y de los códigos"}, nil); w.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
	if on, _, _ := e.svc.Status(context.Background(), e.uid); on {
		t.Fatal("el 2FA debía quedar eliminado")
	}
	if len(e.sessions.revoked) != 1 || e.sessions.revoked[0] != e.uid {
		t.Fatalf("debe cerrar las sesiones del usuario: %v", e.sessions.revoked)
	}
	if len(e.audit.actions) != 1 || e.audit.actions[0] != "two_factor_admin_reset" {
		t.Fatalf("auditoría: %v", e.audit.actions)
	}
}

// respondDomainError traduce el bloqueo del 2FA en cualquier endpoint (p. ej. change-password).
func TestRespondDomainError_TwoFALocked_Is429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondDomainError(c, &twofactor.LockedError{Until: time.Now().Add(10 * time.Minute)})
	if w.Code != http.StatusTooManyRequests || errCode(w) != "2fa_locked" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
