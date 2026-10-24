package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/accountaccess"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
)

// ---- fakes mínimos -----------------------------------------------------------------

type acctUsers struct {
	mu    sync.Mutex
	users map[uuid.UUID]*domain.User
}

func newAcctUsers() *acctUsers { return &acctUsers{users: map[uuid.UUID]*domain.User{}} }

func (f *acctUsers) add(u *domain.User)                               { f.mu.Lock(); f.users[u.ID] = u; f.mu.Unlock() }
func (f *acctUsers) Create(ctx context.Context, u *domain.User) error { f.add(u); return nil }
func (f *acctUsers) FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[id]; ok {
		c := *u
		return &c, nil
	}
	return nil, nil
}
func (f *acctUsers) FindByEmail(ctx context.Context, email string) (*domain.User, error) {
	return f.FindByEmailFold(ctx, email)
}
func (f *acctUsers) FindByEmailFold(ctx context.Context, email string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if strings.EqualFold(u.Email, email) {
			c := *u
			return &c, nil
		}
	}
	return nil, nil
}
func (f *acctUsers) Update(ctx context.Context, u *domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := *u
	f.users[u.ID] = &c
	return nil
}
func (f *acctUsers) IncrementFailedLogin(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[id].FailedLoginAttempts++
	return nil
}
func (f *acctUsers) ResetFailedLogin(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[id].FailedLoginAttempts, f.users[id].LockedUntil = 0, nil
	return nil
}
func (f *acctUsers) UpdatePassword(ctx context.Context, id uuid.UUID, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[id].PasswordHash = hash
	return nil
}
func (f *acctUsers) MarkEmailVerified(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.users[id]
	u.EmailVerified = true
	if u.Status == domain.UserStatusPendingVerification {
		u.Status = domain.UserStatusActive
	}
	return nil
}

type acctTokens struct {
	mu   sync.Mutex
	rows map[string]uuid.UUID
}

func (f *acctTokens) Create(ctx context.Context, id uuid.UUID, p domain.AccountTokenPurpose, hash string, exp time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows == nil {
		f.rows = map[string]uuid.UUID{}
	}
	f.rows[string(p)+hash] = id
	return nil
}
func (f *acctTokens) Consume(ctx context.Context, p domain.AccountTokenPurpose, hash string) (uuid.UUID, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.rows[string(p)+hash]
	delete(f.rows, string(p)+hash)
	return id, ok, nil
}
func (f *acctTokens) LastCreatedAt(ctx context.Context, id uuid.UUID, p domain.AccountTokenPurpose) (time.Time, bool, error) {
	return time.Time{}, false, nil
}
func (f *acctTokens) InvalidateUnused(ctx context.Context, id uuid.UUID, p domain.AccountTokenPurpose) error {
	return nil
}

type acctSessions struct{}

func (acctSessions) RevokeAllForUser(ctx context.Context, id uuid.UUID) error { return nil }

type acctSender struct {
	mu   sync.Mutex
	sent int
}

func (s *acctSender) Send(ctx context.Context, to, subject, body string) error {
	s.mu.Lock()
	s.sent++
	s.mu.Unlock()
	return nil
}

type acctTwoFA struct{ enabled bool }

func (t acctTwoFA) Verify(ctx context.Context, id uuid.UUID, code string) (bool, error) {
	if !t.enabled {
		return false, &domain.DomainError{Code: "2fa_not_enabled", Message: "no"}
	}
	return code == "123456", nil
}

type acctAudit struct {
	mu      sync.Mutex
	actions []string
	meta    []map[string]any
}

func (a *acctAudit) Log(ctx context.Context, actor *uuid.UUID, role, action, et string, eid *uuid.UUID, md map[string]any, ip string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, action)
	a.meta = append(a.meta, md)
	return nil
}

func acctRouter(t *testing.T, twofa bool) (*gin.Engine, *acctUsers, *acctSender, uuid.UUID) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	users := newAcctUsers()
	hash, err := authjwt.HashPassword("ContraseñaVieja-123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u := &domain.User{ID: uuid.New(), Email: "ana@example.com", FirstName: "Ana", PasswordHash: hash, Status: domain.UserStatusActive, EmailVerified: true}
	users.add(u)
	sender := &acctSender{}
	svc := accountaccess.NewService(accountaccess.Deps{
		Users: users, Tokens: &acctTokens{}, Sessions: acctSessions{}, Sender: sender,
		TwoFA: acctTwoFA{enabled: twofa}, BaseURL: "https://wallet.example.com",
	})
	h := NewAccountHandler(svc)

	r := gin.New()
	r.POST("/auth/verify-email", h.VerifyEmail)
	r.POST("/auth/resend-verification", h.ResendVerification)
	r.POST("/auth/forgot-password", h.ForgotPassword)
	r.POST("/auth/reset-password", h.ResetPassword)
	authed := r.Group("/account")
	authed.Use(func(c *gin.Context) { c.Set("user_id", u.ID.String()); c.Next() })
	authed.POST("/change-password", h.ChangePassword)
	return r, users, sender, u.ID
}

func acctPost(r *gin.Engine, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func errCode(w *httptest.ResponseRecorder) string {
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	s, _ := m["error"].(string)
	return s
}

// ---- tests de handlers ------------------------------------------------------------------

// Mismo estado y mismo cuerpo para una cuenta que existe y una que no: no se puede enumerar.
func TestForgotPassword_IdenticalResponse_KnownVsUnknown(t *testing.T) {
	r, _, sender, _ := acctRouter(t, false)

	known := acctPost(r, "/auth/forgot-password", gin.H{"email": "ana@example.com"})
	unknown := acctPost(r, "/auth/forgot-password", gin.H{"email": "nadie@example.com"})

	if known.Code != http.StatusAccepted || unknown.Code != http.StatusAccepted {
		t.Fatalf("estados %d / %d, esperaba 202 en ambos", known.Code, unknown.Code)
	}
	if known.Body.String() != unknown.Body.String() {
		t.Fatalf("cuerpos distintos permiten enumerar:\n%s\n%s", known.Body.String(), unknown.Body.String())
	}
	if sender.sent != 1 {
		t.Fatalf("correos enviados = %d, esperaba 1 (solo la cuenta real)", sender.sent)
	}
}

func TestResendVerification_IdenticalResponse(t *testing.T) {
	r, _, _, _ := acctRouter(t, false)
	a := acctPost(r, "/auth/resend-verification", gin.H{"email": "ana@example.com"})
	b := acctPost(r, "/auth/resend-verification", gin.H{"email": "nadie@example.com"})
	if a.Code != http.StatusAccepted || a.Body.String() != b.Body.String() {
		t.Fatalf("respuestas distintas: %d %s / %d %s", a.Code, a.Body.String(), b.Code, b.Body.String())
	}
}

func TestPublicEndpoints_RejectMalformedInput(t *testing.T) {
	r, _, _, _ := acctRouter(t, false)
	cases := []struct {
		path string
		body any
	}{
		{"/auth/forgot-password", gin.H{"email": "no-es-correo"}},
		{"/auth/forgot-password", gin.H{}},
		{"/auth/resend-verification", gin.H{"email": ""}},
		{"/auth/verify-email", gin.H{}},
		{"/auth/verify-email", gin.H{"token": strings.Repeat("a", 201)}},
		{"/auth/reset-password", gin.H{"token": "abc"}},
	}
	for _, c := range cases {
		if w := acctPost(r, c.path, c.body); w.Code != http.StatusBadRequest {
			t.Errorf("%s %v => %d, esperaba 400", c.path, c.body, w.Code)
		}
	}
}

func TestVerifyEmail_InvalidToken_Is400WithStableCode(t *testing.T) {
	r, _, _, _ := acctRouter(t, false)
	w := acctPost(r, "/auth/verify-email", gin.H{"token": "token-inexistente"})
	if w.Code != http.StatusBadRequest || errCode(w) != "invalid_token" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestResetPassword_WeakAndInvalidToken(t *testing.T) {
	r, _, _, _ := acctRouter(t, false)
	if w := acctPost(r, "/auth/reset-password", gin.H{"token": "x", "new_password": "corta"}); w.Code != http.StatusBadRequest || errCode(w) != "weak_password" {
		t.Fatalf("débil: %d %s", w.Code, w.Body.String())
	}
	if w := acctPost(r, "/auth/reset-password", gin.H{"token": "x", "new_password": "ContraseñaFuerte-1"}); w.Code != http.StatusBadRequest || errCode(w) != "invalid_token" {
		t.Fatalf("token inválido: %d %s", w.Code, w.Body.String())
	}
}

// 401 haría que el cliente cierre sesión creyendo que el access token expiró.
func TestChangePassword_WrongCurrent_Is400Not401(t *testing.T) {
	r, _, _, _ := acctRouter(t, false)
	w := acctPost(r, "/account/change-password", gin.H{"current_password": "incorrecta-xxxx", "new_password": "NuevaContraseña-456"})
	if w.Code != http.StatusBadRequest || errCode(w) != "invalid_current_password" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestChangePassword_Success_And2FA(t *testing.T) {
	r, _, _, _ := acctRouter(t, true)
	if w := acctPost(r, "/account/change-password", gin.H{"current_password": "ContraseñaVieja-123", "new_password": "NuevaContraseña-456"}); w.Code != http.StatusForbidden || errCode(w) != "invalid_2fa_code" {
		t.Fatalf("sin 2FA: %d %s", w.Code, w.Body.String())
	}
	if w := acctPost(r, "/account/change-password", gin.H{"current_password": "ContraseñaVieja-123", "new_password": "NuevaContraseña-456", "two_fa_code": "123456"}); w.Code != http.StatusOK {
		t.Fatalf("con 2FA: %d %s", w.Code, w.Body.String())
	}
}

// ---- admin: desbloqueo y verificación manual ----------------------------------------------

func adminRouter(users *acctUsers, audit *acctAudit) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewAdminHandler(nil, users, audit, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", uuid.NewString()); c.Next() })
	r.POST("/users/:id/unblock", h.UnblockUser)
	r.POST("/users/:id/verify-email", h.ManualVerifyEmail)
	return r
}

func adminUser(users *acctUsers, status domain.UserStatus, verified bool) uuid.UUID {
	u := &domain.User{ID: uuid.New(), Email: uuid.NewString() + "@example.com", Status: status, EmailVerified: verified}
	users.add(u)
	return u.ID
}

func TestUnblockUser_RequiresReason(t *testing.T) {
	users, audit := newAcctUsers(), &acctAudit{}
	id := adminUser(users, domain.UserStatusBlocked, true)
	r := adminRouter(users, audit)

	for _, body := range []any{gin.H{}, gin.H{"reason": ""}, gin.H{"reason": "ok"}} {
		if w := acctPost(r, "/users/"+id.String()+"/unblock", body); w.Code != http.StatusBadRequest {
			t.Errorf("body %v => %d, esperaba 400", body, w.Code)
		}
	}
	if u, _ := users.FindByID(context.Background(), id); u.Status != domain.UserStatusBlocked {
		t.Fatal("sin motivo no debe desbloquear")
	}
}

func TestUnblockUser_OnlyBlockedAccounts(t *testing.T) {
	users, audit := newAcctUsers(), &acctAudit{}
	r := adminRouter(users, audit)
	body := gin.H{"reason": "verificación manual de identidad"}

	deleted := adminUser(users, domain.UserStatusDeleted, true)
	if w := acctPost(r, "/users/"+deleted.String()+"/unblock", body); w.Code != http.StatusConflict || errCode(w) != "user_deleted" {
		t.Fatalf("borrada: %d %s", w.Code, w.Body.String())
	}
	if u, _ := users.FindByID(context.Background(), deleted); u.Status != domain.UserStatusDeleted {
		t.Fatal("una cuenta borrada no puede reactivarse")
	}

	active := adminUser(users, domain.UserStatusActive, true)
	if w := acctPost(r, "/users/"+active.String()+"/unblock", body); w.Code != http.StatusConflict || errCode(w) != "user_not_blocked" {
		t.Fatalf("activa: %d %s", w.Code, w.Body.String())
	}
	pending := adminUser(users, domain.UserStatusPendingVerification, false)
	if w := acctPost(r, "/users/"+pending.String()+"/unblock", body); w.Code != http.StatusConflict {
		t.Fatalf("pending: %d (unblock ya no activa cuentas sin verificar)", w.Code)
	}
	if u, _ := users.FindByID(context.Background(), pending); u.Status != domain.UserStatusPendingVerification {
		t.Fatal("unblock no debe saltarse la verificación de correo")
	}
}

func TestUnblockUser_RespectsEmailVerification_AndAudits(t *testing.T) {
	users, audit := newAcctUsers(), &acctAudit{}
	r := adminRouter(users, audit)

	verified := adminUser(users, domain.UserStatusBlocked, true)
	unverified := adminUser(users, domain.UserStatusBlocked, false)

	if w := acctPost(r, "/users/"+verified.String()+"/unblock", gin.H{"reason": "falso positivo de fraude"}); w.Code != http.StatusOK {
		t.Fatalf("verified: %d %s", w.Code, w.Body.String())
	}
	if w := acctPost(r, "/users/"+unverified.String()+"/unblock", gin.H{"reason": "falso positivo de fraude"}); w.Code != http.StatusOK {
		t.Fatalf("unverified: %d %s", w.Code, w.Body.String())
	}
	if u, _ := users.FindByID(context.Background(), verified); u.Status != domain.UserStatusActive {
		t.Fatalf("verificada => active, obtuvo %s", u.Status)
	}
	if u, _ := users.FindByID(context.Background(), unverified); u.Status != domain.UserStatusPendingVerification {
		t.Fatalf("sin verificar => pending_verification, obtuvo %s", u.Status)
	}
	if len(audit.actions) != 2 || audit.meta[0]["reason"] != "falso positivo de fraude" {
		t.Fatalf("auditoría incompleta: %v %v", audit.actions, audit.meta)
	}
}

func TestManualVerifyEmail(t *testing.T) {
	users, audit := newAcctUsers(), &acctAudit{}
	r := adminRouter(users, audit)
	body := gin.H{"reason": "SMTP caído, identidad confirmada por soporte"}

	pending := adminUser(users, domain.UserStatusPendingVerification, false)
	if w := acctPost(r, "/users/"+pending.String()+"/verify-email", gin.H{}); w.Code != http.StatusBadRequest {
		t.Fatalf("sin motivo: %d", w.Code)
	}
	if w := acctPost(r, "/users/"+pending.String()+"/verify-email", body); w.Code != http.StatusOK {
		t.Fatalf("pending: %d %s", w.Code, w.Body.String())
	}
	if u, _ := users.FindByID(context.Background(), pending); !u.EmailVerified || u.Status != domain.UserStatusActive {
		t.Fatalf("tras verificar: %+v", u)
	}
	if len(audit.actions) != 1 || audit.actions[0] != "email_manually_verified" {
		t.Fatalf("auditoría: %v", audit.actions)
	}

	for name, status := range map[string]domain.UserStatus{"bloqueada": domain.UserStatusBlocked, "borrada": domain.UserStatusDeleted} {
		id := adminUser(users, status, false)
		if w := acctPost(r, "/users/"+id.String()+"/verify-email", body); w.Code != http.StatusConflict {
			t.Errorf("%s: %d, esperaba 409", name, w.Code)
		}
	}
	already := adminUser(users, domain.UserStatusActive, true)
	if w := acctPost(r, "/users/"+already.String()+"/verify-email", body); w.Code != http.StatusConflict || errCode(w) != "already_verified" {
		t.Fatalf("ya verificada: %d %s", w.Code, w.Body.String())
	}
}
