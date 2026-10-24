package accountaccess

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
)

// ---- fakes -------------------------------------------------------------------

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type fakeUsers struct {
	mu    sync.Mutex
	users map[uuid.UUID]*domain.User
}

func newFakeUsers() *fakeUsers { return &fakeUsers{users: map[uuid.UUID]*domain.User{}} }

func (f *fakeUsers) add(u *domain.User) { f.mu.Lock(); f.users[u.ID] = u; f.mu.Unlock() }

func (f *fakeUsers) FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[id]; ok {
		c := *u
		return &c, nil
	}
	return nil, nil
}
func (f *fakeUsers) FindByEmailFold(ctx context.Context, email string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if strings.EqualFold(u.Email, strings.TrimSpace(email)) {
			c := *u
			return &c, nil
		}
	}
	return nil, nil
}
func (f *fakeUsers) Update(ctx context.Context, u *domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := *u
	f.users[u.ID] = &c
	return nil
}
func (f *fakeUsers) IncrementFailedLogin(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[id].FailedLoginAttempts++
	return nil
}
func (f *fakeUsers) ResetFailedLogin(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[id].FailedLoginAttempts = 0
	f.users[id].LockedUntil = nil
	return nil
}
func (f *fakeUsers) UpdatePassword(ctx context.Context, id uuid.UUID, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return domain.ErrUserNotFound
	}
	u.PasswordHash, u.FailedLoginAttempts, u.LockedUntil = hash, 0, nil
	return nil
}

// MarkEmailVerified imita el SQL real: nunca activa bloqueadas; no toca borradas.
func (f *fakeUsers) MarkEmailVerified(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok || u.Status == domain.UserStatusDeleted {
		return domain.ErrUserNotFound
	}
	u.EmailVerified = true
	if u.Status == domain.UserStatusPendingVerification {
		u.Status = domain.UserStatusActive
	}
	return nil
}
func (f *fakeUsers) get(id uuid.UUID) *domain.User {
	u, _ := f.FindByID(context.Background(), id)
	return u
}

type tokenRow struct {
	userID  uuid.UUID
	purpose domain.AccountTokenPurpose
	hash    string
	expires time.Time
	used    bool
	created time.Time
}

type fakeTokens struct {
	mu   sync.Mutex
	rows []*tokenRow
	clk  *clock
}

func (f *fakeTokens) Create(ctx context.Context, userID uuid.UUID, p domain.AccountTokenPurpose, hash string, exp time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.userID == userID && r.purpose == p {
			r.used = true
		}
	}
	f.rows = append(f.rows, &tokenRow{userID: userID, purpose: p, hash: hash, expires: exp, created: f.clk.Now()})
	return nil
}
func (f *fakeTokens) Consume(ctx context.Context, p domain.AccountTokenPurpose, hash string) (uuid.UUID, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.hash == hash && r.purpose == p && !r.used && r.expires.After(f.clk.Now()) {
			r.used = true
			return r.userID, true, nil
		}
	}
	return uuid.Nil, false, nil
}
func (f *fakeTokens) LastCreatedAt(ctx context.Context, userID uuid.UUID, p domain.AccountTokenPurpose) (time.Time, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var last time.Time
	found := false
	for _, r := range f.rows {
		if r.userID == userID && r.purpose == p && (!found || r.created.After(last)) {
			last, found = r.created, true
		}
	}
	return last, found, nil
}
func (f *fakeTokens) InvalidateUnused(ctx context.Context, userID uuid.UUID, p domain.AccountTokenPurpose) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.userID == userID && r.purpose == p {
			r.used = true
		}
	}
	return nil
}
func (f *fakeTokens) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.rows) }

type fakeSessions struct {
	mu      sync.Mutex
	revoked map[uuid.UUID]int
}

func (f *fakeSessions) RevokeAllForUser(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[id]++
	return nil
}
func (f *fakeSessions) times(id uuid.UUID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.revoked[id]
}

type sentMail struct{ to, subject, body string }

type recSender struct {
	mu   sync.Mutex
	sent []sentMail
	err  error
}

func (r *recSender) Send(ctx context.Context, to, subject, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.sent = append(r.sent, sentMail{to, subject, body})
	return nil
}
func (r *recSender) all() []sentMail {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sentMail(nil), r.sent...)
}

type auditRec struct {
	mu      sync.Mutex
	actions []string
}

func (a *auditRec) Log(ctx context.Context, actor *uuid.UUID, role, action, et string, eid *uuid.UUID, md map[string]any, ip string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, action)
	return nil
}
func (a *auditRec) has(action string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, x := range a.actions {
		if x == action {
			return true
		}
	}
	return false
}

type notifRec struct {
	mu    sync.Mutex
	types []string
}

func (n *notifRec) Notify(ctx context.Context, id uuid.UUID, et, title, body string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.types = append(n.types, et)
	return nil
}

type fake2FA struct {
	enabled bool
	code    string
}

func (f *fake2FA) Verify(ctx context.Context, id uuid.UUID, code string) (bool, error) {
	if !f.enabled {
		return false, &domain.DomainError{Code: "2fa_not_enabled", Message: "no"}
	}
	return code == f.code, nil
}

type env struct {
	svc      *Service
	users    *fakeUsers
	tokens   *fakeTokens
	sessions *fakeSessions
	sender   *recSender
	audit    *auditRec
	notif    *notifRec
	clk      *clock
}

const oldPassword = "ContraseñaVieja-123"

func newEnv(t *testing.T, twofa TwoFAChecker) *env {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	e := &env{
		users: newFakeUsers(), sessions: &fakeSessions{revoked: map[uuid.UUID]int{}}, sender: &recSender{},
		audit: &auditRec{}, notif: &notifRec{}, clk: clk, tokens: &fakeTokens{clk: clk},
	}
	e.svc = NewService(Deps{
		Users: e.users, Tokens: e.tokens, Sessions: e.sessions, Sender: e.sender,
		Audit: e.audit, Notifier: e.notif, TwoFA: twofa, BaseURL: "https://wallet.example.com/", Now: clk.Now,
	})
	return e
}

func (e *env) addUser(t *testing.T, email string, status domain.UserStatus, verified bool) *domain.User {
	t.Helper()
	hash, err := authjwt.HashPassword(oldPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u := &domain.User{ID: uuid.New(), Email: email, FirstName: "Ana", PasswordHash: hash, Status: status, EmailVerified: verified}
	e.users.add(u)
	return u
}

// tokenFrom extrae el token del último correo enviado.
func tokenFrom(t *testing.T, m sentMail, path string) string {
	t.Helper()
	idx := strings.Index(m.body, path+"?token=")
	if idx < 0 {
		t.Fatalf("el correo no contiene %s?token=\n%s", path, m.body)
	}
	rest := m.body[idx+len(path+"?token="):]
	end := strings.IndexAny(rest, " \n\r")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

// ---- verificación de email ---------------------------------------------------------

func TestVerifyEmail_FullFlow_ActivatesPendingUser(t *testing.T) {
	e := newEnv(t, nil)
	u := e.addUser(t, "ana@example.com", domain.UserStatusPendingVerification, false)
	ctx := context.Background()

	if err := e.svc.RequestEmailVerification(ctx, u.ID, "1.1.1.1"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	mails := e.sender.all()
	if len(mails) != 1 || mails[0].to != "ana@example.com" {
		t.Fatalf("correos enviados: %+v", mails)
	}
	if !strings.Contains(mails[0].body, "https://wallet.example.com/verify-email?token=") {
		t.Fatalf("enlace mal formado (¿doble barra?):\n%s", mails[0].body)
	}
	token := tokenFrom(t, mails[0], "/verify-email")

	// Solo se guarda el hash, nunca el token.
	for _, r := range e.tokens.rows {
		if r.hash == token || len(r.hash) != 64 {
			t.Fatal("el token en claro no debe almacenarse; se espera un hash SHA-256 hex")
		}
	}

	if err := e.svc.VerifyEmail(ctx, token, "1.1.1.1"); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	got := e.users.get(u.ID)
	if !got.EmailVerified || got.Status != domain.UserStatusActive {
		t.Fatalf("usuario tras verificar: verified=%v status=%s", got.EmailVerified, got.Status)
	}
	if !e.audit.has("email_verified") {
		t.Fatal("falta el evento de auditoría email_verified")
	}

	// Un solo uso.
	if err := e.svc.VerifyEmail(ctx, token, "1.1.1.1"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("reuso del token: %v, esperaba ErrInvalidToken", err)
	}
}

func TestVerifyEmail_ExpiredAndUnknownTokens(t *testing.T) {
	e := newEnv(t, nil)
	u := e.addUser(t, "ana@example.com", domain.UserStatusPendingVerification, false)
	ctx := context.Background()
	_ = e.svc.RequestEmailVerification(ctx, u.ID, "ip")
	token := tokenFrom(t, e.sender.all()[0], "/verify-email")

	e.clk.Advance(VerifyEmailTTL + time.Second)
	if err := e.svc.VerifyEmail(ctx, token, "ip"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("token expirado: %v", err)
	}
	if err := e.svc.VerifyEmail(ctx, "no-existe", "ip"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("token inexistente: %v", err)
	}
	if got := e.users.get(u.ID); got.EmailVerified {
		t.Fatal("no debía quedar verificado")
	}
}

func TestVerifyEmail_NeverReactivatesBlockedUser(t *testing.T) {
	e := newEnv(t, nil)
	u := e.addUser(t, "ana@example.com", domain.UserStatusPendingVerification, false)
	ctx := context.Background()
	_ = e.svc.RequestEmailVerification(ctx, u.ID, "ip")
	token := tokenFrom(t, e.sender.all()[0], "/verify-email")

	// Un admin la bloquea entre el envío y el clic.
	blocked := e.users.get(u.ID)
	blocked.Status = domain.UserStatusBlocked
	_ = e.users.Update(ctx, blocked)

	_ = e.svc.VerifyEmail(ctx, token, "ip")
	if got := e.users.get(u.ID); got.Status != domain.UserStatusBlocked {
		t.Fatalf("una cuenta bloqueada no puede activarse por verificación: status=%s", got.Status)
	}
}

func TestResend_CooldownAndOldTokenInvalidated(t *testing.T) {
	e := newEnv(t, nil)
	u := e.addUser(t, "ana@example.com", domain.UserStatusPendingVerification, false)
	ctx := context.Background()

	_ = e.svc.RequestEmailVerification(ctx, u.ID, "ip")
	first := tokenFrom(t, e.sender.all()[0], "/verify-email")

	e.svc.ResendVerification(ctx, "ANA@example.com", "ip") // dentro del cooldown => nada
	if n := len(e.sender.all()); n != 1 {
		t.Fatalf("correos = %d dentro del cooldown, esperaba 1", n)
	}

	e.clk.Advance(ResendCooldown + time.Second)
	e.svc.ResendVerification(ctx, "ana@example.com", "ip")
	mails := e.sender.all()
	if len(mails) != 2 {
		t.Fatalf("correos = %d tras el cooldown, esperaba 2", len(mails))
	}
	second := tokenFrom(t, mails[1], "/verify-email")
	if first == second {
		t.Fatal("cada envío debe generar un token distinto")
	}
	if err := e.svc.VerifyEmail(ctx, first, "ip"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("el enlace anterior debía quedar invalidado: %v", err)
	}
	if err := e.svc.VerifyEmail(ctx, second, "ip"); err != nil {
		t.Fatalf("el último enlace debe funcionar: %v", err)
	}
}

func TestResend_NoEnumeration_NoMailForUnknownVerifiedOrBlocked(t *testing.T) {
	e := newEnv(t, nil)
	e.addUser(t, "verificada@example.com", domain.UserStatusActive, true)
	e.addUser(t, "bloqueada@example.com", domain.UserStatusBlocked, false)
	ctx := context.Background()

	for _, mail := range []string{"nadie@example.com", "verificada@example.com", "bloqueada@example.com"} {
		e.svc.ResendVerification(ctx, mail, "ip")
	}
	if n := len(e.sender.all()); n != 0 {
		t.Fatalf("se enviaron %d correos; no debía enviarse ninguno", n)
	}
	if e.tokens.count() != 0 {
		t.Fatal("no debía crearse ningún token")
	}
}

func TestRequest_WithoutBaseURL_FailsAndSendsNothing(t *testing.T) {
	e := newEnv(t, nil)
	e.svc = NewService(Deps{Users: e.users, Tokens: e.tokens, Sessions: e.sessions, Sender: e.sender, Now: e.clk.Now})
	u := e.addUser(t, "ana@example.com", domain.UserStatusPendingVerification, false)

	if err := e.svc.RequestEmailVerification(context.Background(), u.ID, "ip"); err == nil {
		t.Fatal("sin APP_BASE_URL debía fallar")
	}
	if len(e.sender.all()) != 0 || e.tokens.count() != 0 {
		t.Fatal("no debía enviarse correo ni crearse token")
	}
}

func TestVerifyEmail_ConcurrentConsume_ExactlyOneWins(t *testing.T) {
	e := newEnv(t, nil)
	u := e.addUser(t, "ana@example.com", domain.UserStatusPendingVerification, false)
	_ = e.svc.RequestEmailVerification(context.Background(), u.ID, "ip")
	token := tokenFrom(t, e.sender.all()[0], "/verify-email")

	const n = 20
	results := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i] = e.svc.VerifyEmail(context.Background(), token, "ip") }(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, domain.ErrInvalidToken) {
			t.Fatalf("error inesperado: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d verificaciones exitosas con el mismo token, esperaba exactamente 1", wins)
	}
}

func TestTokens_AreRandomAndLongEnough(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		tok, err := newToken()
		if err != nil || len(tok) != 43 || seen[tok] {
			t.Fatalf("token inválido o repetido: %q err=%v", tok, err)
		}
		seen[tok] = true
	}
}

// ---- restablecimiento de contraseña -------------------------------------------------

func TestForgotAndReset_FullFlow(t *testing.T) {
	e := newEnv(t, nil)
	u := e.addUser(t, "ana@example.com", domain.UserStatusPendingVerification, false)
	// cuenta con bloqueo por intentos fallidos previos
	locked := e.users.get(u.ID)
	until := e.clk.Now().Add(10 * time.Minute)
	locked.LockedUntil, locked.FailedLoginAttempts = &until, 5
	_ = e.users.Update(context.Background(), locked)
	ctx := context.Background()

	e.svc.ForgotPassword(ctx, "Ana@Example.com", "ip")
	mails := e.sender.all()
	if len(mails) != 1 || !strings.Contains(mails[0].body, "/reset-password?token=") {
		t.Fatalf("correo de reset: %+v", mails)
	}
	token := tokenFrom(t, mails[0], "/reset-password")

	if err := e.svc.ResetPassword(ctx, token, "NuevaContraseña-456", "ip"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	got := e.users.get(u.ID)
	if ok, _ := authjwt.VerifyPassword("NuevaContraseña-456", got.PasswordHash); !ok {
		t.Fatal("la nueva contraseña debía quedar activa")
	}
	if ok, _ := authjwt.VerifyPassword(oldPassword, got.PasswordHash); ok {
		t.Fatal("la contraseña vieja no debía seguir funcionando")
	}
	if got.LockedUntil != nil || got.FailedLoginAttempts != 0 {
		t.Fatal("el reset debía limpiar el bloqueo por intentos")
	}
	if !got.EmailVerified || got.Status != domain.UserStatusActive {
		t.Fatalf("probar el control del correo debe verificarlo: verified=%v status=%s", got.EmailVerified, got.Status)
	}
	if e.sessions.times(u.ID) != 1 {
		t.Fatal("el reset debe revocar TODAS las sesiones")
	}
	if !e.audit.has("password_reset_completed") {
		t.Fatal("falta auditoría password_reset_completed")
	}
	// Aviso por correo del cambio (sin enlaces).
	all := e.sender.all()
	if len(all) != 2 || strings.Contains(all[1].body, "token=") {
		t.Fatalf("el aviso de cambio no debe contener enlaces: %+v", all)
	}
	// Un solo uso.
	if err := e.svc.ResetPassword(ctx, token, "OtraContraseña-789", "ip"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("reuso: %v", err)
	}
}

func TestReset_WeakPassword_DoesNotBurnToken(t *testing.T) {
	e := newEnv(t, nil)
	u := e.addUser(t, "ana@example.com", domain.UserStatusActive, true)
	ctx := context.Background()
	e.svc.ForgotPassword(ctx, u.Email, "ip")
	token := tokenFrom(t, e.sender.all()[0], "/reset-password")

	if err := e.svc.ResetPassword(ctx, token, "corta", "ip"); !errors.Is(err, domain.ErrWeakPassword) {
		t.Fatalf("contraseña débil: %v", err)
	}
	if err := e.svc.ResetPassword(ctx, token, strings.Repeat("a", 129), "ip"); !errors.Is(err, domain.ErrWeakPassword) {
		t.Fatalf("contraseña demasiado larga: %v", err)
	}
	if err := e.svc.ResetPassword(ctx, token, "ContraseñaFuerte-1", "ip"); err != nil {
		t.Fatalf("tras un intento débil el token debía seguir vivo: %v", err)
	}
}

func TestForgot_NoMailForUnknownBlockedOrDeleted_AndCooldown(t *testing.T) {
	e := newEnv(t, nil)
	e.addUser(t, "bloqueada@example.com", domain.UserStatusBlocked, true)
	e.addUser(t, "borrada@example.com", domain.UserStatusDeleted, true)
	active := e.addUser(t, "ana@example.com", domain.UserStatusActive, true)
	ctx := context.Background()

	for _, m := range []string{"nadie@example.com", "bloqueada@example.com", "borrada@example.com"} {
		e.svc.ForgotPassword(ctx, m, "ip")
	}
	if len(e.sender.all()) != 0 {
		t.Fatal("no debía enviarse correo a cuentas inexistentes/bloqueadas/borradas")
	}

	e.svc.ForgotPassword(ctx, active.Email, "ip")
	e.svc.ForgotPassword(ctx, active.Email, "ip") // cooldown
	if n := len(e.sender.all()); n != 1 {
		t.Fatalf("correos = %d, esperaba 1 (cooldown)", n)
	}
}

func TestReset_BlockedAfterRequest_IsRejected(t *testing.T) {
	e := newEnv(t, nil)
	u := e.addUser(t, "ana@example.com", domain.UserStatusActive, true)
	ctx := context.Background()
	e.svc.ForgotPassword(ctx, u.Email, "ip")
	token := tokenFrom(t, e.sender.all()[0], "/reset-password")

	b := e.users.get(u.ID)
	b.Status = domain.UserStatusBlocked
	_ = e.users.Update(ctx, b)

	if err := e.svc.ResetPassword(ctx, token, "ContraseñaFuerte-1", "ip"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("cuenta bloqueada: %v", err)
	}
	if ok, _ := authjwt.VerifyPassword(oldPassword, e.users.get(u.ID).PasswordHash); !ok {
		t.Fatal("la contraseña no debía cambiar")
	}
}

func TestReset_TokenIsPerPurpose(t *testing.T) {
	e := newEnv(t, nil)
	u := e.addUser(t, "ana@example.com", domain.UserStatusPendingVerification, false)
	ctx := context.Background()
	_ = e.svc.RequestEmailVerification(ctx, u.ID, "ip")
	verifyToken := tokenFrom(t, e.sender.all()[0], "/verify-email")

	// Un token de verificación NO sirve para restablecer la contraseña.
	if err := e.svc.ResetPassword(ctx, verifyToken, "ContraseñaFuerte-1", "ip"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("token de otro propósito: %v", err)
	}
}

func TestSenderFailure_DoesNotLeakOrPanic(t *testing.T) {
	e := newEnv(t, nil)
	u := e.addUser(t, "ana@example.com", domain.UserStatusActive, true)
	e.sender.err = errors.New("smtp caído")
	// Las operaciones públicas no devuelven nada: el llamador no puede distinguir.
	e.svc.ForgotPassword(context.Background(), u.Email, "ip")
	e.svc.ForgotPassword(context.Background(), "nadie@example.com", "ip")
}

// ---- cambio de contraseña autenticado -------------------------------------------------

func TestChangePassword_Success_RevokesSessionsAndNotifies(t *testing.T) {
	e := newEnv(t, &fake2FA{enabled: false})
	u := e.addUser(t, "ana@example.com", domain.UserStatusActive, true)

	if err := e.svc.ChangePassword(context.Background(), u.ID, oldPassword, "NuevaContraseña-456", "", "ip"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	got := e.users.get(u.ID)
	if ok, _ := authjwt.VerifyPassword("NuevaContraseña-456", got.PasswordHash); !ok {
		t.Fatal("la nueva contraseña no quedó activa")
	}
	if e.sessions.times(u.ID) != 1 {
		t.Fatal("debe revocar todas las sesiones")
	}
	if !e.audit.has("password_changed") || len(e.notif.types) == 0 {
		t.Fatal("falta auditoría o notificación")
	}
}

func TestChangePassword_Validation(t *testing.T) {
	e := newEnv(t, nil)
	u := e.addUser(t, "ana@example.com", domain.UserStatusActive, true)
	ctx := context.Background()

	if err := e.svc.ChangePassword(ctx, u.ID, oldPassword, "corta", "", "ip"); !errors.Is(err, domain.ErrWeakPassword) {
		t.Fatalf("débil: %v", err)
	}
	if err := e.svc.ChangePassword(ctx, u.ID, oldPassword, "ana@example.com", "", "ip"); !errors.Is(err, domain.ErrWeakPassword) {
		t.Fatalf("igual al correo: %v", err)
	}
	if err := e.svc.ChangePassword(ctx, u.ID, oldPassword, oldPassword, "", "ip"); !errors.Is(err, domain.ErrSamePassword) {
		t.Fatalf("igual a la actual: %v", err)
	}
	if e.sessions.times(u.ID) != 0 {
		t.Fatal("una validación fallida no debe revocar sesiones")
	}
}

func TestChangePassword_WrongCurrent_LocksAfterFiveAttempts(t *testing.T) {
	e := newEnv(t, nil)
	u := e.addUser(t, "ana@example.com", domain.UserStatusActive, true)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := e.svc.ChangePassword(ctx, u.ID, "incorrecta-xxxx", "NuevaContraseña-456", "", "ip"); !errors.Is(err, domain.ErrInvalidCurrentPassword) {
			t.Fatalf("intento %d: %v", i+1, err)
		}
	}
	if err := e.svc.ChangePassword(ctx, u.ID, oldPassword, "NuevaContraseña-456", "", "ip"); !errors.Is(err, domain.ErrAccountLocked) {
		t.Fatalf("tras 5 fallos la cuenta debía bloquearse temporalmente: %v", err)
	}
	// Pasado el bloqueo vuelve a funcionar.
	e.clk.Advance(changePasswordLock + time.Second)
	if err := e.svc.ChangePassword(ctx, u.ID, oldPassword, "NuevaContraseña-456", "", "ip"); err != nil {
		t.Fatalf("tras el bloqueo: %v", err)
	}
}

func TestChangePassword_Requires2FAWhenEnabled(t *testing.T) {
	e := newEnv(t, &fake2FA{enabled: true, code: "123456"})
	u := e.addUser(t, "ana@example.com", domain.UserStatusActive, true)
	ctx := context.Background()

	if err := e.svc.ChangePassword(ctx, u.ID, oldPassword, "NuevaContraseña-456", "", "ip"); !errors.Is(err, domain.ErrInvalid2FACode) {
		t.Fatalf("sin código: %v", err)
	}
	if err := e.svc.ChangePassword(ctx, u.ID, oldPassword, "NuevaContraseña-456", "000000", "ip"); !errors.Is(err, domain.ErrInvalid2FACode) {
		t.Fatalf("código incorrecto: %v", err)
	}
	if e.sessions.times(u.ID) != 0 {
		t.Fatal("sin 2FA válido no debe cambiar nada")
	}
	if err := e.svc.ChangePassword(ctx, u.ID, oldPassword, "NuevaContraseña-456", "123456", "ip"); err != nil {
		t.Fatalf("con código correcto: %v", err)
	}
}

func TestChangePassword_BlockedUserRejected(t *testing.T) {
	e := newEnv(t, nil)
	u := e.addUser(t, "ana@example.com", domain.UserStatusBlocked, true)
	if err := e.svc.ChangePassword(context.Background(), u.ID, oldPassword, "NuevaContraseña-456", "", "ip"); !errors.Is(err, domain.ErrAccountLocked) {
		t.Fatalf("bloqueada: %v", err)
	}
}

func TestValidatePassword(t *testing.T) {
	good := []string{"abcdefghij", "contraseña-larga-123", strings.Repeat("ñ", 128)}
	for _, p := range good {
		if err := ValidatePassword(p, "x@y.com"); err != nil {
			t.Errorf("%q debía aceptarse: %v", p, err)
		}
	}
	for _, p := range []string{"", "123456789", strings.Repeat("a", 129), "x@y.com   "} {
		if err := ValidatePassword(p, "x@y.com"); err == nil {
			t.Errorf("%q debía rechazarse", p)
		}
	}
}
