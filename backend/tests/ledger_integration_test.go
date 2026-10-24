//go:build integration

package tests

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"korzadi-wallet/backend/internal/infrastructure/migrate"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

var (
	testSchemaOnce sync.Once
	testSchemaErr  error
)

func testPool(t *testing.T) *pgxpool.Pool {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL no definido, saltando test de integración")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parsear DATABASE_URL de integración: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("por seguridad, los integration tests requieren una base terminada en _test; recibida %q", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("conectar db: %v", err)
	}
	testSchemaOnce.Do(func() {
		testSchemaErr = migrate.Run(context.Background(), pool, "../migrations")
	})
	if testSchemaErr != nil {
		pool.Close()
		t.Fatalf("preparar esquema efímero de integración: %v", testSchemaErr)
	}
	return pool
}

func createTestAccount(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	ctx := context.Background()
	userID := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, first_name, last_name)
		VALUES ($1,$2,'x','Test','User')`, userID, userID.String()+"@test.local")
	if err != nil {
		t.Fatalf("crear usuario: %v", err)
	}
	walletID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO wallets (id, user_id) VALUES ($1,$2)`, walletID, userID)
	if err != nil {
		t.Fatalf("crear wallet: %v", err)
	}
	accountID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO wallet_accounts (id, wallet_id, asset_code) VALUES ($1,$2,'USD')`, accountID, walletID)
	if err != nil {
		t.Fatalf("crear cuenta: %v", err)
	}
	return accountID
}

// TestLedgerIdempotency verifica que reintentar la misma operación con la misma
// idempotency_key NO duplica el movimiento contable.
func TestLedgerIdempotency(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	ctx := context.Background()

	accA := createTestAccount(t, pool)
	accB := createTestAccount(t, pool)

	// Fondear cuenta A con un adjustment inicial.
	testAdjustment(t, pool, ledger, accA, 100)

	idemKey := uuid.NewString()
	entries := []domain.LedgerEntryInput{
		{WalletAccountID: accA, AssetCode: "USD", Amount: decimal.NewFromInt(-30)},
		{WalletAccountID: accB, AssetCode: "USD", Amount: decimal.NewFromInt(30)},
	}

	tx1, err := ledger.ExecuteTransaction(ctx, domain.TxTypeTransfer, idemKey, nil, "transferencia test", nil, entries)
	if err != nil {
		t.Fatalf("primera ejecución: %v", err)
	}
	tx2, err := ledger.ExecuteTransaction(ctx, domain.TxTypeTransfer, idemKey, nil, "transferencia test", nil, entries)
	if err != nil {
		t.Fatalf("segunda ejecución (reintento): %v", err)
	}
	if tx1.ID != tx2.ID {
		t.Fatalf("se esperaba el mismo ledger_transaction_id, se obtuvieron %s y %s", tx1.ID, tx2.ID)
	}

	balA, _ := ledger.GetBalance(ctx, accA)
	balB, _ := ledger.GetBalance(ctx, accB)
	if !balA.Equal(decimal.NewFromInt(70)) {
		t.Fatalf("saldo A esperado 70, obtenido %s (la operación se duplicó)", balA.String())
	}
	if !balB.Equal(decimal.NewFromInt(30)) {
		t.Fatalf("saldo B esperado 30, obtenido %s (la operación se duplicó)", balB.String())
	}
}

// TestConcurrentTransfersSameBalance verifica que dos gastos concurrentes del mismo saldo
// no puedan sobre-gastar: solo uno debe tener éxito.
func TestConcurrentTransfersSameBalance(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	ctx := context.Background()

	accA := createTestAccount(t, pool)
	accB := createTestAccount(t, pool)
	accC := createTestAccount(t, pool)

	testAdjustment(t, pool, ledger, accA, 100)

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, err := ledger.ExecuteTransaction(ctx, domain.TxTypeTransfer, uuid.NewString(), nil, "gasto 1", nil,
			[]domain.LedgerEntryInput{
				{WalletAccountID: accA, AssetCode: "USD", Amount: decimal.NewFromInt(-100)},
				{WalletAccountID: accB, AssetCode: "USD", Amount: decimal.NewFromInt(100)},
			})
		results[0] = err
	}()
	go func() {
		defer wg.Done()
		_, err := ledger.ExecuteTransaction(ctx, domain.TxTypeTransfer, uuid.NewString(), nil, "gasto 2", nil,
			[]domain.LedgerEntryInput{
				{WalletAccountID: accA, AssetCode: "USD", Amount: decimal.NewFromInt(-100)},
				{WalletAccountID: accC, AssetCode: "USD", Amount: decimal.NewFromInt(100)},
			})
		results[1] = err
	}()
	wg.Wait()

	successes := 0
	for _, e := range results {
		if e == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("se esperaba exactamente 1 operación exitosa, se obtuvieron %d (posible doble gasto)", successes)
	}

	balA, _ := ledger.GetBalance(ctx, accA)
	if balA.IsNegative() {
		t.Fatalf("saldo de A quedó negativo: %s — doble gasto confirmado", balA.String())
	}
}

// Postgres-backed race: two separately identified withdrawal requests debit
// the same funded account at once. Exactly one may reserve the available
// balance through ledger execution; the other must fail without overdrawing.
func TestConcurrentWithdrawalsSameBalance_ExactlyOneDebit(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	ctx := context.Background()
	source := createTestAccount(t, pool)
	destinationA := createTestAccount(t, pool)
	destinationB := createTestAccount(t, pool)
	testAdjustment(t, pool, ledger, source, 100)

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, destination := range []uuid.UUID{destinationA, destinationB} {
		wg.Add(1)
		go func(destination uuid.UUID) {
			defer wg.Done()
			_, err := ledger.ExecuteTransaction(ctx, domain.TxTypeWithdrawal, uuid.NewString(), nil, "concurrent integration withdrawal", nil, []domain.LedgerEntryInput{
				{WalletAccountID: source, AssetCode: "USD", Amount: decimal.NewFromInt(-60)},
				{WalletAccountID: destination, AssetCode: "USD", Amount: decimal.NewFromInt(60)},
			})
			results <- err
		}(destination)
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("successful concurrent withdrawals=%d, want exactly one", wins)
	}
	balance, err := ledger.GetBalance(ctx, source)
	if err != nil || !balance.Equal(decimal.NewFromInt(40)) {
		t.Fatalf("source balance=%s err=%v, want 40", balance, err)
	}
}

// TestUnbalancedLedgerRejected verifica que un intento de entries que no suman 0 se rechace.
func TestUnbalancedLedgerRejected(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	ctx := context.Background()

	accA := createTestAccount(t, pool)

	_, err := ledger.ExecuteTransaction(ctx, domain.TxTypeTransfer, uuid.NewString(), nil, "desbalanceado", nil,
		[]domain.LedgerEntryInput{{WalletAccountID: accA, AssetCode: "USD", Amount: decimal.NewFromInt(50)}})
	// Un solo entry con monto != 0 nunca suma 0 -> debe rechazarse, EXCEPTO si se documenta
	// como ajuste unilateral administrativo. En este diseño, todo entry debe balancear,
	// así que se espera un error explícito.
	if err == nil {
		t.Fatalf("se esperaba error por ledger desbalanceado, no se obtuvo ninguno")
	}
}

// ---- Bloque 2: motor consolidado + reservas ----

func createTestClearingAccount(t *testing.T, pool *pgxpool.Pool, assetCode string) uuid.UUID {
	ctx := context.Background()
	userID := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, first_name, last_name)
		VALUES ($1,$2,'x','System','Clearing')`, userID, userID.String()+"@test.local")
	if err != nil {
		t.Fatalf("crear usuario: %v", err)
	}
	walletID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO wallets (id, user_id) VALUES ($1,$2)`, walletID, userID)
	if err != nil {
		t.Fatalf("crear wallet: %v", err)
	}
	accountID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO wallet_accounts (id, wallet_id, asset_code, account_type) VALUES ($1,$2,$3,'system_clearing')`,
		accountID, walletID, assetCode)
	if err != nil {
		t.Fatalf("crear cuenta de clearing: %v", err)
	}
	return accountID
}

// TestLedger_UserAccountCannotGoNegative_ClearingCan is the regression test
// for the Bloque 2 consolidation: this is the ONE distinction the single
// posting engine makes between account types, and it must hold in both
// directions on the same call.
func TestLedger_UserAccountCannotGoNegative_ClearingCan(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	ctx := context.Background()

	userAcc := createTestAccount(t, pool) // account_type = 'user' (default)
	clearingAcc := createTestClearingAccount(t, pool, "USD")

	// Crediting the user while debiting clearing must succeed even though
	// it takes clearing negative — this is exactly the deposit shape.
	_, err := ledger.ExecuteTransaction(ctx, domain.TxTypeDeposit, uuid.NewString(), nil, "depósito simulado", nil,
		[]domain.LedgerEntryInput{
			{WalletAccountID: userAcc, AssetCode: "USD", Amount: decimal.NewFromInt(100)},
			{WalletAccountID: clearingAcc, AssetCode: "USD", Amount: decimal.NewFromInt(-100)},
		})
	if err != nil {
		t.Fatalf("depósito (clearing va a negativo) no debía fallar: %v", err)
	}

	// Now try to debit the user MORE than they have — must be rejected,
	// even though the offsetting entry is a credit to clearing (which
	// would happily accept it).
	_, err = ledger.ExecuteTransaction(ctx, domain.TxTypeWithdrawal, uuid.NewString(), nil, "retiro excesivo", nil,
		[]domain.LedgerEntryInput{
			{WalletAccountID: userAcc, AssetCode: "USD", Amount: decimal.NewFromInt(-1000)},
			{WalletAccountID: clearingAcc, AssetCode: "USD", Amount: decimal.NewFromInt(1000)},
		})
	if err == nil {
		t.Fatal("se esperaba ErrInsufficientFunds: una cuenta 'user' nunca debe poder ir a saldo negativo")
	}

	// The user's balance must be exactly 100 still (the withdrawal attempt
	// above must have rolled back completely, including clearing's side).
	balance, err := ledger.GetBalance(ctx, userAcc)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if !balance.Equal(decimal.NewFromInt(100)) {
		t.Errorf("balance del usuario = %s, esperaba 100 (el intento de retiro fallido no debía dejar rastro)", balance.String())
	}
}

// TestReservation_LockReleaseConsume exercises the full reservation
// lifecycle against a real Postgres instance.
func TestReservation_LockReleaseConsume(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	reservations := postgres.NewReservationRepository(pool)
	ctx := context.Background()

	acc := createTestAccount(t, pool)

	// Fund the account first: available must come from somewhere real.
	testAdjustment(t, pool, ledger, acc, 100)

	balance, err := reservations.GetBalance(ctx, acc)
	if err != nil {
		t.Fatalf("GetBalance inicial: %v", err)
	}
	if !balance.Available.Equal(decimal.NewFromInt(100)) || !balance.Locked.IsZero() {
		t.Fatalf("balance inicial inesperado: %+v", balance)
	}

	// Reserve 40 -> available 60, locked 40.
	res, err := reservations.Reserve(ctx, acc, "USD", decimal.NewFromInt(40), "test de reserva", nil)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	balance, _ = reservations.GetBalance(ctx, acc)
	if !balance.Available.Equal(decimal.NewFromInt(60)) || !balance.Locked.Equal(decimal.NewFromInt(40)) {
		t.Fatalf("balance tras reservar 40 = %+v, esperaba available=60 locked=40", balance)
	}

	// Reserving more than what's now available must fail.
	if _, err := reservations.Reserve(ctx, acc, "USD", decimal.NewFromInt(61), "excede lo disponible", nil); err == nil {
		t.Error("se esperaba ErrInsufficientFunds al reservar más de lo disponible")
	}

	// Release -> funds return to available.
	if err := reservations.Release(ctx, res.ID); err != nil {
		t.Fatalf("Release: %v", err)
	}
	balance, _ = reservations.GetBalance(ctx, acc)
	if !balance.Available.Equal(decimal.NewFromInt(100)) || !balance.Locked.IsZero() {
		t.Fatalf("balance tras liberar = %+v, esperaba available=100 locked=0", balance)
	}

	// Releasing an already-released reservation must be rejected, not a
	// silent no-op (spec: no modificaciones silenciosas del historial).
	if err := reservations.Release(ctx, res.ID); err == nil {
		t.Error("se esperaba error al liberar una reserva ya resuelta")
	}

	// Reserve again and Consume this time.
	res2, err := reservations.Reserve(ctx, acc, "USD", decimal.NewFromInt(25), "reserva a consumir", nil)
	if err != nil {
		t.Fatalf("Reserve #2: %v", err)
	}
	if err := reservations.Consume(ctx, res2.ID); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	balance, _ = reservations.GetBalance(ctx, acc)
	// Consume only clears the hold — it never touches balance_cache/total
	// on its own (see domain.ReservationRepository's doc comment), so
	// Total is still 100 and Locked is back to 0; a real caller would also
	// have posted a matching ledger debit in the same business operation.
	if !balance.Locked.IsZero() {
		t.Errorf("locked tras consumir = %s, esperaba 0", balance.Locked.String())
	}
	if !balance.Total.Equal(decimal.NewFromInt(100)) {
		t.Errorf("total tras consumir (sin débito de ledger explícito) = %s, esperaba 100", balance.Total.String())
	}
}
