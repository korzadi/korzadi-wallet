//go:build integration

package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

func newUserForTokens(t *testing.T, ctx context.Context) (uuid.UUID, func()) {
	t.Helper()
	pool := testPool(t)
	users := postgres.NewUserRepository(pool)
	u := &domain.User{
		ID: uuid.New(), Email: "tok-" + uuid.NewString() + "@example.com", PasswordHash: "x",
		FirstName: "T", LastName: "T", Status: domain.UserStatusPendingVerification,
	}
	if err := users.Create(ctx, u); err != nil {
		t.Fatalf("crear usuario: %v", err)
	}
	return u.ID, func() {
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID)
		pool.Close()
	}
}

func TestAccountTokens_CreateConsume_SingleUse_Expiry(t *testing.T) {
	ctx := context.Background()
	userID, cleanup := newUserForTokens(t, ctx)
	defer cleanup()
	pool := testPool(t)
	defer pool.Close()
	repo := postgres.NewAccountTokenRepository(pool)

	h1 := "1111111111111111111111111111111111111111111111111111111111111111"
	if err := repo.Create(ctx, userID, domain.TokenPurposeVerifyEmail, h1, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Propósito equivocado => no consume.
	if _, ok, _ := repo.Consume(ctx, domain.TokenPurposeResetPassword, h1); ok {
		t.Fatal("un token de verificación no debe servir para reset")
	}
	got, ok, err := repo.Consume(ctx, domain.TokenPurposeVerifyEmail, h1)
	if err != nil || !ok || got != userID {
		t.Fatalf("Consume = (%s, %v, %v)", got, ok, err)
	}
	if _, ok, _ := repo.Consume(ctx, domain.TokenPurposeVerifyEmail, h1); ok {
		t.Fatal("un token no puede consumirse dos veces")
	}

	// Expirado.
	h2 := "2222222222222222222222222222222222222222222222222222222222222222"
	_ = repo.Create(ctx, userID, domain.TokenPurposeResetPassword, h2, time.Now().Add(-time.Minute))
	if _, ok, _ := repo.Consume(ctx, domain.TokenPurposeResetPassword, h2); ok {
		t.Fatal("un token expirado no puede consumirse")
	}
}

func TestAccountTokens_NewTokenInvalidatesPrevious_AndLastCreatedAt(t *testing.T) {
	ctx := context.Background()
	userID, cleanup := newUserForTokens(t, ctx)
	defer cleanup()
	pool := testPool(t)
	defer pool.Close()
	repo := postgres.NewAccountTokenRepository(pool)

	if _, found, _ := repo.LastCreatedAt(ctx, userID, domain.TokenPurposeResetPassword); found {
		t.Fatal("aún no hay tokens")
	}
	old := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newer := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	_ = repo.Create(ctx, userID, domain.TokenPurposeResetPassword, old, time.Now().Add(time.Hour))
	_ = repo.Create(ctx, userID, domain.TokenPurposeResetPassword, newer, time.Now().Add(time.Hour))

	if _, ok, _ := repo.Consume(ctx, domain.TokenPurposeResetPassword, old); ok {
		t.Fatal("el enlace anterior debía quedar invalidado al emitir uno nuevo")
	}
	if _, ok, _ := repo.Consume(ctx, domain.TokenPurposeResetPassword, newer); !ok {
		t.Fatal("el enlace más reciente debía funcionar")
	}
	if ts, found, err := repo.LastCreatedAt(ctx, userID, domain.TokenPurposeResetPassword); err != nil || !found || time.Since(ts) > time.Minute {
		t.Fatalf("LastCreatedAt = (%v, %v, %v)", ts, found, err)
	}

	// InvalidateUnused.
	third := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	_ = repo.Create(ctx, userID, domain.TokenPurposeResetPassword, third, time.Now().Add(time.Hour))
	if err := repo.InvalidateUnused(ctx, userID, domain.TokenPurposeResetPassword); err != nil {
		t.Fatalf("InvalidateUnused: %v", err)
	}
	if _, ok, _ := repo.Consume(ctx, domain.TokenPurposeResetPassword, third); ok {
		t.Fatal("el token invalidado no debe consumirse")
	}
}

// 20 consumos concurrentes del mismo token: exactamente uno gana.
func TestAccountTokens_ConcurrentConsume_ExactlyOneWins(t *testing.T) {
	ctx := context.Background()
	userID, cleanup := newUserForTokens(t, ctx)
	defer cleanup()
	pool := testPool(t)
	defer pool.Close()
	repo := postgres.NewAccountTokenRepository(pool)

	h := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	_ = repo.Create(ctx, userID, domain.TokenPurposeVerifyEmail, h, time.Now().Add(time.Hour))

	const n = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, err := repo.Consume(ctx, domain.TokenPurposeVerifyEmail, h); err == nil && ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d consumos exitosos del mismo token, esperaba exactamente 1", wins)
	}
}

func TestUserRepo_MarkEmailVerified_UpdatePassword_FindFold_RevokeAll(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()
	users := postgres.NewUserRepository(pool)
	sessions := postgres.NewSessionRepository(pool)

	mk := func(status domain.UserStatus) *domain.User {
		u := &domain.User{ID: uuid.New(), Email: "Mix-" + uuid.NewString() + "@Example.com", PasswordHash: "old",
			FirstName: "A", LastName: "B", Status: status}
		if err := users.Create(ctx, u); err != nil {
			t.Fatalf("crear: %v", err)
		}
		t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID) })
		return u
	}
	pending, blocked, deleted := mk(domain.UserStatusPendingVerification), mk(domain.UserStatusBlocked), mk(domain.UserStatusDeleted)

	// MarkEmailVerified: activa pending; NO reactiva bloqueadas; ignora borradas.
	if err := users.MarkEmailVerified(ctx, pending.ID); err != nil {
		t.Fatalf("pending: %v", err)
	}
	if u, _ := users.FindByID(ctx, pending.ID); !u.EmailVerified || u.Status != domain.UserStatusActive {
		t.Fatalf("pending tras verificar: %+v", u)
	}
	if err := users.MarkEmailVerified(ctx, blocked.ID); err != nil {
		t.Fatalf("blocked: %v", err)
	}
	if u, _ := users.FindByID(ctx, blocked.ID); u.Status != domain.UserStatusBlocked {
		t.Fatalf("una cuenta bloqueada no debe activarse: %s", u.Status)
	}
	if err := users.MarkEmailVerified(ctx, deleted.ID); err == nil {
		t.Fatal("una cuenta borrada no debe poder verificarse")
	}

	// FindByEmailFold ignora mayúsculas y espacios.
	if u, err := users.FindByEmailFold(ctx, "  "+pending.Email+"  "); err != nil || u == nil || u.ID != pending.ID {
		t.Fatalf("FindByEmailFold exacto: %v %v", u, err)
	}
	lower := []byte(pending.Email)
	for i, b := range lower {
		if b >= 'A' && b <= 'Z' {
			lower[i] = b + 32
		}
	}
	if u, _ := users.FindByEmailFold(ctx, string(lower)); u == nil || u.ID != pending.ID {
		t.Fatal("FindByEmailFold debe ignorar mayúsculas")
	}

	// UpdatePassword limpia bloqueo e intentos.
	pool.Exec(ctx, `UPDATE users SET failed_login_attempts = 5, locked_until = now() + interval '1 hour' WHERE id = $1`, pending.ID)
	if err := users.UpdatePassword(ctx, pending.ID, "nuevo-hash"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	u, _ := users.FindByID(ctx, pending.ID)
	if u.PasswordHash != "nuevo-hash" || u.FailedLoginAttempts != 0 || u.LockedUntil != nil {
		t.Fatalf("tras UpdatePassword: %+v", u)
	}
	if err := users.UpdatePassword(ctx, uuid.New(), "x"); err == nil {
		t.Fatal("usuario inexistente debía fallar")
	}

	// RevokeAllForUser revoca todas sus sesiones y solo las suyas.
	other := mk(domain.UserStatusActive)
	mkSession := func(uid uuid.UUID) {
		_ = sessions.Create(ctx, &domain.Session{ID: uuid.New(), UserID: uid, RefreshTokenHash: uuid.NewString(),
			FamilyID: uuid.New(), IPAddress: "127.0.0.1", ExpiresAt: time.Now().Add(time.Hour)})
	}
	mkSession(pending.ID)
	mkSession(pending.ID)
	mkSession(other.ID)
	if err := sessions.RevokeAllForUser(ctx, pending.ID); err != nil {
		t.Fatalf("RevokeAllForUser: %v", err)
	}
	var activeMine, activeOther int
	pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked = FALSE`, pending.ID).Scan(&activeMine)
	pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked = FALSE`, other.ID).Scan(&activeOther)
	if activeMine != 0 || activeOther != 1 {
		t.Fatalf("sesiones activas: propias=%d (esperaba 0), ajenas=%d (esperaba 1)", activeMine, activeOther)
	}
}
