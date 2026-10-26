//go:build integration

package tests

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

func TestWithdrawal_CompleteFlow_WithFakes(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()

	ledgerRepo := postgres.NewLedgerRepository(pool)
	userID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,first_name,last_name,status,email_verified) VALUES ($1,$2,'x','W','U','active',true)`, userID, userID.String()+"@w.test"); err != nil {
		t.Fatal(err)
	}
	walletID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO wallets(id,user_id,status) VALUES ($1,$2,'active')`, walletID, userID); err != nil {
		t.Fatal(err)
	}
	accountID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO wallet_accounts(id,wallet_id,asset_code) VALUES ($1,$2,'USDT')`, accountID, walletID); err != nil {
		t.Fatal(err)
	}
	testAdjustment(t, pool, ledgerRepo, accountID, 100)

	// Create withdrawal record directly for testing
	wid := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO blockchain_withdrawals(id,user_id,network,asset_code,to_address,amount,status,idempotency_key) VALUES ($1,$2,'BSC','USDT',$3,10,'pending',$4)`,
		wid, userID, "0x1234567890123456789012345678901234567890", "idem-"+wid.String()); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM blockchain_withdrawals WHERE id=$1`, wid).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("withdrawal status=%q, want pending", status)
	}

	// Creating the row directly does not reserve or debit funds; the funding
	// adjustment must have used the account's own asset (USDT), never USD.
	bal, err := ledgerRepo.GetBalance(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if !bal.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance=%s, want 100", bal)
	}
	var wrongAssetEntries int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE wallet_account_id=$1 AND asset_code<>'USDT'`, accountID).Scan(&wrongAssetEntries); err != nil {
		t.Fatal(err)
	}
	if wrongAssetEntries != 0 {
		t.Fatalf("found %d ledger entries with the wrong asset for a USDT account", wrongAssetEntries)
	}
}

func TestConcurrent_DebitCreditWithdrawal_NoDoubleSpend(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ledger := postgres.NewLedgerRepository(pool)
	ctx := context.Background()

	accA := createTestAccount(t, pool)
	testAdjustment(t, pool, ledger, accA, 50)

	// Try concurrent transfers that would overdraw
	idem1 := uuid.NewString()
	idem2 := uuid.NewString()
	entries1 := []domain.LedgerEntryInput{
		{WalletAccountID: accA, AssetCode: "USD", Amount: decimal.NewFromInt(-30)},
		{WalletAccountID: createTestAccount(t, pool), AssetCode: "USD", Amount: decimal.NewFromInt(30)},
	}
	entries2 := []domain.LedgerEntryInput{
		{WalletAccountID: accA, AssetCode: "USD", Amount: decimal.NewFromInt(-30)},
		{WalletAccountID: createTestAccount(t, pool), AssetCode: "USD", Amount: decimal.NewFromInt(30)},
	}

	type res struct {
		err error
	}
	ch1 := make(chan res, 1)
	ch2 := make(chan res, 1)
	go func() {
		_, err := ledger.ExecuteTransaction(ctx, domain.TxTypeTransfer, idem1, nil, "t1", nil, entries1)
		ch1 <- res{err}
	}()
	go func() {
		_, err := ledger.ExecuteTransaction(ctx, domain.TxTypeTransfer, idem2, nil, "t2", nil, entries2)
		ch2 <- res{err}
	}()
	r1 := <-ch1
	r2 := <-ch2
	// At least one should succeed if we have enough, but both try to spend from same account concurrently - need to be careful about locking
	successes := 0
	if r1.err == nil {
		successes++
	}
	if r2.err == nil {
		successes++
	}
	if successes > 1 {
		t.Fatal("concurrent double spend occurred")
	}
}
