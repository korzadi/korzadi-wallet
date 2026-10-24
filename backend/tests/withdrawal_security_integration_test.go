//go:build integration

package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/security"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

func newTestUser(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	pool := testPool(t)
	defer pool.Close()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, first_name, last_name, status)
		VALUES ($1, $2, 'x', 'T', 'T', 'active')
	`, id, "dest-"+id.String()+"@example.com"); err != nil {
		t.Fatalf("crear usuario: %v", err)
	}
	t.Cleanup(func() {
		p := testPool(t)
		defer p.Close()
		p.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

// El hash SQL del backfill de la migración 0019 debe ser idéntico a security.HashDestination.
func TestMigration0019_BackfillHash_MatchesGoHash(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()

	for _, addr := range []string{
		"0x2222222222222222222222222222222222222222",
		"0xAbCdEf0123456789aBcDeF0123456789AbCdEf01", // mayúsculas/minúsculas (checksum)
		"  0xAbC  ",
	} {
		var sqlHash string
		if err := pool.QueryRow(ctx, `SELECT encode(sha256(convert_to(lower(btrim($1::text)), 'UTF8')), 'hex')`, addr).Scan(&sqlHash); err != nil {
			t.Fatalf("hash SQL: %v", err)
		}
		if sqlHash != security.HashDestination(addr) {
			t.Fatalf("hash SQL %s != hash Go %s para %q", sqlHash, security.HashDestination(addr), addr)
		}
	}
}

func TestDestinationRepo_PendingCooldownLifecycle(t *testing.T) {
	ctx := context.Background()
	userID := newTestUser(t, ctx)
	pool := testPool(t)
	defer pool.Close()
	repo := postgres.NewSecurityDestinationRepository(pool)
	guard := security.NewDestinationGuard(repo, nil)
	addr := "0xAbCdEf0123456789aBcDeF0123456789AbCdEf01"

	// Primer intento: se registra como pendiente y espera.
	_, err := guard.CheckWithCooldown(ctx, userID, "USDT", "BSC", addr, "ip", time.Hour)
	var cd *security.DestinationCooldownError
	if !asCooldown(err, &cd) || !cd.FirstRequest {
		t.Fatalf("primer intento: %v", err)
	}
	// Segundo intento (otra capitalización): mismo registro, ya no es el primero.
	_, err = guard.CheckWithCooldown(ctx, userID, "USDT", "BSC", "0xabcdef0123456789abcdef0123456789abcdef01", "ip", time.Hour)
	if !asCooldown(err, &cd) || cd.FirstRequest {
		t.Fatalf("segundo intento: %v", err)
	}
	// Lookup trata 'pending' como nueva.
	if st, _ := repo.Lookup(ctx, userID, "USDT", "BSC", security.HashDestination(addr)); st != security.DestinationNew {
		t.Fatalf("pending debe verse como new, es %s", st)
	}

	// Pasado el tiempo (envejecemos first_seen_at) ya no espera.
	pool.Exec(ctx, `UPDATE known_destinations SET first_seen_at = now() - interval '2 hours' WHERE user_id = $1`, userID)
	if _, err := guard.CheckWithCooldown(ctx, userID, "USDT", "BSC", addr, "ip", time.Hour); err != nil {
		t.Fatalf("tras la espera: %v", err)
	}

	// Un retiro completado la convierte en known; known nunca vuelve a esperar ni se degrada.
	if err := guard.RecordSuccessfulUse(ctx, userID, "USDT", "BSC", addr); err != nil {
		t.Fatalf("RecordSuccessfulUse: %v", err)
	}
	if st, _ := repo.Lookup(ctx, userID, "USDT", "BSC", security.HashDestination(addr)); st != security.DestinationKnown {
		t.Fatalf("tras el uso debe ser known, es %s", st)
	}
	if _, created, _ := repo.RegisterPending(ctx, userID, "USDT", "BSC", security.HashDestination(addr)); created {
		t.Fatal("RegisterPending no debe recrear ni degradar una fila existente")
	}
	if st, _ := repo.Lookup(ctx, userID, "USDT", "BSC", security.HashDestination(addr)); st != security.DestinationKnown {
		t.Fatalf("RegisterPending degradó el estado: %s", st)
	}
}

func TestDestinationRepo_AdminAllowlistAndBlock(t *testing.T) {
	ctx := context.Background()
	userID := newTestUser(t, ctx)
	pool := testPool(t)
	defer pool.Close()
	guard := security.NewDestinationGuard(postgres.NewSecurityDestinationRepository(pool), nil)
	addr := "0x1111111111111111111111111111111111111111"

	if _, err := guard.CheckWithCooldown(ctx, userID, "USDT", "BSC", addr, "ip", time.Hour); err == nil {
		t.Fatal("debía quedar en espera")
	}
	if err := guard.SetStatus(ctx, userID, "USDT", "BSC", addr, security.DestinationAllowlisted); err != nil {
		t.Fatalf("allowlist: %v", err)
	}
	if _, err := guard.CheckWithCooldown(ctx, userID, "USDT", "BSC", addr, "ip", time.Hour); err != nil {
		t.Fatalf("allowlisted no espera: %v", err)
	}
	if err := guard.SetStatus(ctx, userID, "USDT", "BSC", addr, security.DestinationBlocked); err != nil {
		t.Fatalf("block: %v", err)
	}
	if _, err := guard.CheckWithCooldown(ctx, userID, "USDT", "BSC", addr, "ip", time.Hour); err == nil {
		t.Fatal("blocked debe rechazarse")
	}
}

func TestWithdrawalRepo_LimitReservationRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()
	repo := postgres.NewBlockchainWithdrawalRepository(pool)

	// Retiro inexistente.
	if _, found, err := repo.GetLimitReservation(ctx, uuid.New()); err != nil || found {
		t.Fatalf("inexistente: found=%v err=%v", found, err)
	}

	userID := newTestUser(t, ctx)
	wid := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO blockchain_withdrawals (id, user_id, network, asset_code, to_address, amount, status, idempotency_key)
		VALUES ($1, $2, 'BSC', 'USDT', '0xto', 1, 'pending', $3)
	`, wid, userID, "k-"+wid.String()); err != nil {
		t.Fatalf("insertar retiro mínimo: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM blockchain_withdrawals WHERE id = $1`, wid)

	if _, found, _ := repo.GetLimitReservation(ctx, wid); found {
		t.Fatal("aún no tiene reserva")
	}
	r1 := uuid.New()
	if err := repo.SetLimitReservation(ctx, wid, r1); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Solo se escribe una vez.
	_ = repo.SetLimitReservation(ctx, wid, uuid.New())
	got, found, err := repo.GetLimitReservation(ctx, wid)
	if err != nil || !found || got != r1 {
		t.Fatalf("Get = (%s, %v, %v), esperaba %s", got, found, err, r1)
	}
}

func asCooldown(err error, target **security.DestinationCooldownError) bool {
	return errors.As(err, target)
}
