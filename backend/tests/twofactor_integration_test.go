//go:build integration

package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

func newTwoFAUser(t *testing.T, ctx context.Context) (uuid.UUID, *postgres.TwoFactorRepository) {
	t.Helper()
	pool := testPool(t)
	t.Cleanup(pool.Close)
	id := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, first_name, last_name, status)
		VALUES ($1, $2, 'x', 'T', 'T', 'active')
	`, id, "2fa-"+id.String()+"@example.com"); err != nil {
		t.Fatalf("crear usuario: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) })

	repo := postgres.NewTwoFactorRepository(pool)
	if err := repo.Create(ctx, id, "secreto-cifrado", []string{"h1", "h2", "h3"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Enable(ctx, id); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	return id, repo
}

func TestTwoFARepo_GetState(t *testing.T) {
	ctx := context.Background()
	id, repo := newTwoFAUser(t, ctx)

	st, err := repo.GetState(ctx, id)
	if err != nil || !st.Exists || !st.Enabled || st.RecoveryCodesRemaining != 3 || st.LastUsedStep != 0 || st.LockedUntil != nil {
		t.Fatalf("GetState = %+v err=%v", st, err)
	}
	if st, _ := repo.GetState(ctx, uuid.New()); st.Exists {
		t.Fatal("un usuario sin 2FA no debe existir")
	}
}

// 20 reclamos concurrentes del mismo intervalo: exactamente uno gana; uno anterior nunca.
func TestTwoFARepo_ClaimStep_AtomicAndMonotonic(t *testing.T) {
	ctx := context.Background()
	id, repo := newTwoFAUser(t, ctx)

	const n = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := repo.ClaimStep(ctx, id, 1000); err == nil && ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d reclamos exitosos del mismo intervalo, esperaba exactamente 1", wins)
	}
	if ok, _ := repo.ClaimStep(ctx, id, 999); ok {
		t.Fatal("un intervalo anterior al último usado no debe reclamarse")
	}
	if ok, _ := repo.ClaimStep(ctx, id, 1001); !ok {
		t.Fatal("un intervalo posterior debe reclamarse")
	}
}

func TestTwoFARepo_RegisterFailure_LocksAtThreshold_AndReset(t *testing.T) {
	ctx := context.Background()
	id, repo := newTwoFAUser(t, ctx)

	for i := 1; i < 5; i++ {
		until, err := repo.RegisterFailure(ctx, id, 5, 15*time.Minute)
		if err != nil || until != nil {
			t.Fatalf("fallo %d: until=%v err=%v (no debía bloquear aún)", i, until, err)
		}
	}
	until, err := repo.RegisterFailure(ctx, id, 5, 15*time.Minute)
	if err != nil || until == nil {
		t.Fatalf("el 5.º fallo debía bloquear: until=%v err=%v", until, err)
	}
	if d := time.Until(*until); d < 14*time.Minute || d > 15*time.Minute+5*time.Second {
		t.Fatalf("bloqueo de %v, esperaba ~15 min", d)
	}
	st, _ := repo.GetState(ctx, id)
	if st.LockedUntil == nil {
		t.Fatal("el estado debe reflejar el bloqueo")
	}

	// ResetFailures pone el contador a cero (el bloqueo vigente expira por tiempo).
	repo.RegisterFailure(ctx, id, 5, 15*time.Minute)
	if err := repo.ResetFailures(ctx, id); err != nil {
		t.Fatalf("ResetFailures: %v", err)
	}
	pool := testPool(t)
	defer pool.Close()
	var failed int
	pool.QueryRow(ctx, `SELECT failed_attempts FROM two_factor_credentials WHERE user_id = $1`, id).Scan(&failed)
	if failed != 0 {
		t.Fatalf("failed_attempts = %d, esperaba 0", failed)
	}
}

// Un código de recuperación solo puede consumirse una vez aunque lleguen 20 solicitudes a la vez.
func TestTwoFARepo_ConsumeRecoveryCode_SingleUseAndConcurrent(t *testing.T) {
	ctx := context.Background()
	id, repo := newTwoFAUser(t, ctx)

	const n = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, err := repo.ConsumeRecoveryCode(ctx, id, "h1"); err == nil && ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d consumos exitosos del mismo código, esperaba exactamente 1", wins)
	}
	remaining, ok, _ := repo.ConsumeRecoveryCode(ctx, id, "h2")
	if !ok || remaining != 1 {
		t.Fatalf("consumir h2: ok=%v remaining=%d, esperaba ok y 1 restante", ok, remaining)
	}
	if _, ok, _ := repo.ConsumeRecoveryCode(ctx, id, "no-existe"); ok {
		t.Fatal("un hash inexistente no debe consumirse")
	}
}

func TestTwoFARepo_ReplaceDelete_AndSetupResetsState(t *testing.T) {
	ctx := context.Background()
	id, repo := newTwoFAUser(t, ctx)

	if err := repo.ReplaceRecoveryCodes(ctx, id, []string{"n1", "n2"}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if st, _ := repo.GetState(ctx, id); st.RecoveryCodesRemaining != 2 {
		t.Fatalf("tras reemplazar quedan %d", st.RecoveryCodesRemaining)
	}
	if _, ok, _ := repo.ConsumeRecoveryCode(ctx, id, "h3"); ok {
		t.Fatal("los códigos anteriores no deben valer tras reemplazar")
	}

	repo.ClaimStep(ctx, id, 5000)
	repo.RegisterFailure(ctx, id, 1, time.Hour) // bloquea
	// Re-configurar (Create) reinicia anti-replay, contador y bloqueo para el secreto nuevo.
	if err := repo.Create(ctx, id, "otro-secreto", []string{"x"}); err != nil {
		t.Fatalf("Create (re-setup): %v", err)
	}
	st, _ := repo.GetState(ctx, id)
	if st.Enabled || st.LastUsedStep != 0 || st.LockedUntil != nil {
		t.Fatalf("el re-setup debe reiniciar el estado: %+v", st)
	}

	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if st, _ := repo.GetState(ctx, id); st.Exists {
		t.Fatal("tras Delete no debe existir")
	}
}
