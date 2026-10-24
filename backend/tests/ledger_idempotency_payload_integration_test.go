//go:build integration

package tests

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

func fund(t *testing.T, l *postgres.LedgerRepository, acc uuid.UUID, amount int64) {
	t.Helper()
	pool := testPool(t)
	defer pool.Close()
	testAdjustment(t, pool, l, acc, amount)
}

func transferEntries(from, to uuid.UUID, amt int64) []domain.LedgerEntryInput {
	return []domain.LedgerEntryInput{
		{WalletAccountID: from, AssetCode: "USD", Amount: decimal.NewFromInt(-amt)},
		{WalletAccountID: to, AssetCode: "USD", Amount: decimal.NewFromInt(amt)},
	}
}

func countTxByKey(t *testing.T, key string) int {
	t.Helper()
	pool := testPool(t)
	defer pool.Close()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM ledger_transactions WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("contar: %v", err)
	}
	return n
}

// Colisión: la clave ya la usó una operación de OTRO tipo => mismatch, no replay.
func TestLedgerIdempotency_SameKeyDifferentType_Rejected(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	l := postgres.NewLedgerRepository(pool)
	ctx := context.Background()
	a, b := createTestAccount(t, pool), createTestAccount(t, pool)
	fund(t, l, a, 100)

	key := "blockchain-deposit:TEST:" + uuid.NewString()
	if _, err := l.ExecuteTransaction(ctx, domain.TxTypeDeposit, key, nil, "deposito", nil, transferEntries(a, b, 10)); err != nil {
		t.Fatalf("primera: %v", err)
	}
	_, err := l.ExecuteTransaction(ctx, domain.TxTypeTransfer, key, nil, "ataque", nil, transferEntries(a, b, 10))
	if !errors.Is(err, domain.ErrIdempotencyPayloadMismatch) {
		t.Fatalf("esperaba ErrIdempotencyPayloadMismatch, obtuve: %v", err)
	}
	if n := countTxByKey(t, key); n != 1 {
		t.Fatalf("tx con la clave = %d, esperaba 1", n)
	}
}

func TestLedgerIdempotency_SameKeyDifferentAmountOrAccount_Rejected(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	l := postgres.NewLedgerRepository(pool)
	ctx := context.Background()
	a, b, c := createTestAccount(t, pool), createTestAccount(t, pool), createTestAccount(t, pool)
	fund(t, l, a, 100)

	key := uuid.NewString()
	if _, err := l.ExecuteTransaction(ctx, domain.TxTypeTransfer, key, nil, "t", nil, transferEntries(a, b, 10)); err != nil {
		t.Fatalf("primera: %v", err)
	}
	for name, entries := range map[string][]domain.LedgerEntryInput{
		"otro monto":  transferEntries(a, b, 11),
		"otra cuenta": transferEntries(a, c, 10),
	} {
		_, err := l.ExecuteTransaction(ctx, domain.TxTypeTransfer, key, nil, "t", nil, entries)
		if !errors.Is(err, domain.ErrIdempotencyPayloadMismatch) {
			t.Errorf("%s: esperaba ErrIdempotencyPayloadMismatch, obtuve: %v", name, err)
		}
	}
	// El saldo solo se movió una vez.
	if bal, _ := l.GetBalance(ctx, b); !bal.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("saldo de b = %s, esperaba 10", bal)
	}
}

func TestLedgerIdempotency_LegitimateReplay_SameTx_EntryOrderIrrelevant(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	l := postgres.NewLedgerRepository(pool)
	ctx := context.Background()
	a, b := createTestAccount(t, pool), createTestAccount(t, pool)
	fund(t, l, a, 100)

	key := uuid.NewString()
	e := transferEntries(a, b, 25)
	tx1, err := l.ExecuteTransaction(ctx, domain.TxTypeTransfer, key, nil, "t", nil, e)
	if err != nil {
		t.Fatalf("primera: %v", err)
	}
	reversed := []domain.LedgerEntryInput{e[1], e[0]}
	tx2, err := l.ExecuteTransaction(ctx, domain.TxTypeTransfer, key, nil, "t", nil, reversed)
	if err != nil {
		t.Fatalf("replay legítimo: %v", err)
	}
	if tx1.ID != tx2.ID {
		t.Fatalf("ids distintos: %s vs %s", tx1.ID, tx2.ID)
	}
}

// Concurrencia: misma clave y mismo payload => una sola tx, todos reciben el mismo ID.
func TestLedgerIdempotency_Concurrent_SamePayload_OneTx(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	l := postgres.NewLedgerRepository(pool)
	ctx := context.Background()
	a, b := createTestAccount(t, pool), createTestAccount(t, pool)
	fund(t, l, a, 100)

	key := uuid.NewString()
	const n = 20
	ids := make([]uuid.UUID, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tx, err := l.ExecuteTransaction(ctx, domain.TxTypeTransfer, key, nil, "t", nil, transferEntries(a, b, 10))
			errs[i] = err
			if tx != nil {
				ids[i] = tx.ID
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("ids distintos: %s vs %s", ids[i], ids[0])
		}
	}
	if bal, _ := l.GetBalance(ctx, b); !bal.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("saldo de b = %s, esperaba 10 (doble crédito)", bal)
	}
	if c := countTxByKey(t, key); c != 1 {
		t.Fatalf("tx con la clave = %d, esperaba 1", c)
	}
}

// Concurrencia hostil: dos payloads distintos compiten por la misma clave.
// Gana exactamente uno; el otro recibe mismatch; el dinero se mueve una vez.
func TestLedgerIdempotency_Concurrent_DifferentPayloads_ExactlyOneWins(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	l := postgres.NewLedgerRepository(pool)
	ctx := context.Background()
	a, b := createTestAccount(t, pool), createTestAccount(t, pool)
	fund(t, l, a, 1000)

	key := uuid.NewString()
	const n = 20
	type res struct {
		id  uuid.UUID
		amt int64
		err error
	}
	out := make([]res, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			amt := int64(10)
			if i%2 == 1 {
				amt = 20
			}
			tx, err := l.ExecuteTransaction(ctx, domain.TxTypeTransfer, key, nil, "t", nil, transferEntries(a, b, amt))
			out[i] = res{amt: amt, err: err}
			if tx != nil {
				out[i].id = tx.ID
			}
		}(i)
	}
	wg.Wait()

	var winnerID uuid.UUID
	var winnerAmt int64
	for _, r := range out {
		if r.err == nil {
			if winnerID == uuid.Nil {
				winnerID, winnerAmt = r.id, r.amt
			}
			if r.id != winnerID || r.amt != winnerAmt {
				t.Fatalf("dos payloads distintos obtuvieron éxito con la misma clave")
			}
		} else if !errors.Is(r.err, domain.ErrIdempotencyPayloadMismatch) {
			t.Fatalf("error inesperado: %v", r.err)
		}
	}
	if winnerID == uuid.Nil {
		t.Fatal("ninguna operación tuvo éxito")
	}
	if bal, _ := l.GetBalance(ctx, b); !bal.Equal(decimal.NewFromInt(winnerAmt)) {
		t.Fatalf("saldo de b = %s, esperaba %d", bal, winnerAmt)
	}
	if c := countTxByKey(t, key); c != 1 {
		t.Fatalf("tx con la clave = %d, esperaba 1", c)
	}
}
