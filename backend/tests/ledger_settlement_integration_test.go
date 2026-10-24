//go:build integration

package tests

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

func fundAccount(t *testing.T, ledger *postgres.LedgerRepository, acct uuid.UUID, amount int64) {
	t.Helper()
	pool := testPool(t)
	defer pool.Close()
	testAdjustment(t, pool, ledger, acct, amount)
}

func settleInput(key string, resID, from, to uuid.UUID, amount int64) domain.ReservationSettlementInput {
	return domain.ReservationSettlementInput{
		ReservationIDs: []uuid.UUID{resID},
		TxType:         domain.TxTypeWithdrawal,
		IdempotencyKey: key,
		Description:    "settlement test",
		Entries: []domain.LedgerEntryInput{
			{WalletAccountID: from, AssetCode: "USD", Amount: decimal.NewFromInt(-amount)},
			{WalletAccountID: to, AssetCode: "USD", Amount: decimal.NewFromInt(amount)},
		},
	}
}

func TestSettleReservations_HappyPath(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	resv := postgres.NewReservationRepository(pool)
	ctx := context.Background()

	from, to := createTestAccount(t, pool), createTestAccount(t, pool)
	fundAccount(t, ledger, from, 100)
	r, err := resv.Reserve(ctx, from, "USD", decimal.NewFromInt(40), "test", nil)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	if _, err := ledger.SettleReservations(ctx, settleInput(uuid.NewString(), r.ID, from, to, 40)); err != nil {
		t.Fatalf("settle: %v", err)
	}

	bal, _ := resv.GetBalance(ctx, from)
	if !bal.Total.Equal(decimal.NewFromInt(60)) || !bal.Locked.IsZero() || !bal.Available.Equal(decimal.NewFromInt(60)) {
		t.Fatalf("esperaba total=60 locked=0 available=60, obtuve %+v", bal)
	}
	got, _ := resv.FindByID(ctx, r.ID)
	if got.Status != domain.ReservationConsumed {
		t.Fatalf("la reserva debe quedar consumed, está %s", got.Status)
	}
}

func TestSettleReservations_IdempotentReplay(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	resv := postgres.NewReservationRepository(pool)
	ctx := context.Background()

	from, to := createTestAccount(t, pool), createTestAccount(t, pool)
	fundAccount(t, ledger, from, 100)
	r, _ := resv.Reserve(ctx, from, "USD", decimal.NewFromInt(40), "test", nil)

	key := uuid.NewString()
	tx1, err := ledger.SettleReservations(ctx, settleInput(key, r.ID, from, to, 40))
	if err != nil {
		t.Fatalf("primera: %v", err)
	}
	tx2, err := ledger.SettleReservations(ctx, settleInput(key, r.ID, from, to, 40))
	if err != nil {
		t.Fatalf("replay debe ser éxito idempotente, obtuve: %v", err)
	}
	if tx1.ID != tx2.ID {
		t.Fatalf("replay debe devolver el mismo ledger tx (%s vs %s)", tx1.ID, tx2.ID)
	}
	bal, _ := resv.GetBalance(ctx, from)
	if !bal.Total.Equal(decimal.NewFromInt(60)) {
		t.Fatalf("se debitó más de una vez: total=%s", bal.Total)
	}
}

func TestSettleReservations_ConcurrentSameKey_SingleDebit(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	resv := postgres.NewReservationRepository(pool)
	ctx := context.Background()

	from, to := createTestAccount(t, pool), createTestAccount(t, pool)
	fundAccount(t, ledger, from, 100)
	r, _ := resv.Reserve(ctx, from, "USD", decimal.NewFromInt(40), "test", nil)

	key := uuid.NewString()
	const n = 8
	ids := make([]uuid.UUID, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tx, err := ledger.SettleReservations(ctx, settleInput(key, r.ID, from, to, 40))
			errs[i] = err
			if tx != nil {
				ids[i] = tx.ID
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: misma key debe ser replay, no error: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("todas deben ver el mismo ledger tx")
		}
	}
	bal, _ := resv.GetBalance(ctx, from)
	if !bal.Total.Equal(decimal.NewFromInt(60)) {
		t.Fatalf("esperaba un único débito (total=60), obtuve %s", bal.Total)
	}
}

func TestSettleReservations_ConcurrentDifferentKeys_OnlyOneWins(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	resv := postgres.NewReservationRepository(pool)
	ctx := context.Background()

	from, to := createTestAccount(t, pool), createTestAccount(t, pool)
	fundAccount(t, ledger, from, 100)
	r, _ := resv.Reserve(ctx, from, "USD", decimal.NewFromInt(40), "test", nil)

	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = ledger.SettleReservations(ctx, settleInput(uuid.NewString(), r.ID, from, to, 40))
		}(i)
	}
	wg.Wait()

	ok := 0
	for _, e := range errs {
		if e == nil {
			ok++
		} else if e != domain.ErrReservationNotActive {
			t.Fatalf("los perdedores deben recibir ErrReservationNotActive, obtuve: %v", e)
		}
	}
	if ok != 1 {
		t.Fatalf("exactamente una liquidación debe ganar, ganaron %d", ok)
	}
	bal, _ := resv.GetBalance(ctx, from)
	if !bal.Total.Equal(decimal.NewFromInt(60)) {
		t.Fatalf("double spend: total=%s (esperaba 60)", bal.Total)
	}
}

func TestSettleReservations_CoverageMismatch_RollsBack(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	resv := postgres.NewReservationRepository(pool)
	ctx := context.Background()

	from, to := createTestAccount(t, pool), createTestAccount(t, pool)
	fundAccount(t, ledger, from, 100)
	r, _ := resv.Reserve(ctx, from, "USD", decimal.NewFromInt(40), "test", nil)

	_, err := ledger.SettleReservations(ctx, settleInput(uuid.NewString(), r.ID, from, to, 30)) // débito 30 < reserva 40
	if err == nil {
		t.Fatal("esperaba ErrSettlementMismatch")
	}
	assertReservationUntouched(t, resv, r.ID, from, 100, 40)
}

func TestSettleReservations_FailureMidTx_RollsBackEverything(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	resv := postgres.NewReservationRepository(pool)
	ctx := context.Background()

	from := createTestAccount(t, pool)
	fundAccount(t, ledger, from, 100)
	r, _ := resv.Reserve(ctx, from, "USD", decimal.NewFromInt(40), "test", nil)

	// Contraparte inexistente: falla dentro de la tx, antes de confirmar.
	_, err := ledger.SettleReservations(ctx, settleInput(uuid.NewString(), r.ID, from, uuid.New(), 40))
	if err == nil {
		t.Fatal("esperaba error por cuenta inexistente")
	}
	assertReservationUntouched(t, resv, r.ID, from, 100, 40)
}

func TestSettleReservations_DoesNotWeakenAvailableCheck(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	resv := postgres.NewReservationRepository(pool)
	ctx := context.Background()

	from, to := createTestAccount(t, pool), createTestAccount(t, pool)
	fundAccount(t, ledger, from, 100)
	if _, err := resv.Reserve(ctx, from, "USD", decimal.NewFromInt(100), "test", nil); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// Un débito ordinario NO puede gastar fondos reservados (CHECK de 0005).
	_, err := ledger.ExecuteTransaction(ctx, domain.TxTypeTransfer, uuid.NewString(), nil, "gasto de fondos reservados", nil,
		[]domain.LedgerEntryInput{
			{WalletAccountID: from, AssetCode: "USD", Amount: decimal.NewFromInt(-10)},
			{WalletAccountID: to, AssetCode: "USD", Amount: decimal.NewFromInt(10)},
		})
	if err == nil {
		t.Fatal("un débito ordinario no debe poder gastar fondos reservados")
	}
	bal, _ := resv.GetBalance(ctx, from)
	if !bal.Total.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("saldo alterado: %s", bal.Total)
	}
}

func assertReservationUntouched(t *testing.T, resv *postgres.ReservationRepository, id, acct uuid.UUID, wantTotal, wantLocked int64) {
	t.Helper()
	ctx := context.Background()
	got, err := resv.FindByID(ctx, id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Status != domain.ReservationActive {
		t.Fatalf("la reserva debe seguir active tras rollback, está %s", got.Status)
	}
	bal, _ := resv.GetBalance(ctx, acct)
	if !bal.Total.Equal(decimal.NewFromInt(wantTotal)) || !bal.Locked.Equal(decimal.NewFromInt(wantLocked)) {
		t.Fatalf("saldo alterado tras rollback: %+v (esperaba total=%d locked=%d)", bal, wantTotal, wantLocked)
	}
}
