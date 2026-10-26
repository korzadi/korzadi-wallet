//go:build integration

package tests

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	apptransfer "korzadi-wallet/backend/internal/application/transfer"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

func TestTransferService_PostsBalancedLedgerAndEnforcesIDOR(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()
	makeWallet := func() uuid.UUID {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,first_name,last_name,status,email_verified) VALUES ($1,$2,'test','Transfer','User','active',true)`, id, id.String()+"@transfer.test"); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		walletID := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO wallets(id,user_id,status) VALUES ($1,$2,'active')`, walletID, id); err != nil {
			t.Fatalf("insert wallet: %v", err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) })
		return id
	}
	senderID, receiverID, strangerID := makeWallet(), makeWallet(), makeWallet()
	wallets := postgres.NewWalletRepository(pool)
	ledger := postgres.NewLedgerRepository(pool)
	assets := postgres.NewAssetRepository(pool)
	transfers := postgres.NewTransferRepository(pool)
	senderWallet, err := wallets.FindWalletByUserID(ctx, senderID)
	if err != nil || senderWallet == nil {
		t.Fatalf("sender wallet: %v", err)
	}
	senderAccount, err := wallets.GetOrCreateAccount(ctx, senderWallet.ID, "USD")
	if err != nil {
		t.Fatal(err)
	}
	testAdjustment(t, pool, ledger, senderAccount.ID, 100)

	svc := apptransfer.NewService(wallets, ledger, transfers, assets, postgres.NewUserRepository(pool), nil, nil)
	in := apptransfer.CreateTransferInput{SenderUserID: senderID, RecipientUserID: receiverID, AssetCode: "USD", Amount: decimal.NewFromInt(25), IdempotencyKey: "integration-transfer-" + uuid.NewString()}
	result, err := svc.Execute(ctx, in)
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if result.Status != domain.StatusCompleted {
		t.Fatalf("transfer status=%s", result.Status)
	}
	replay, err := svc.Execute(ctx, in)
	if err != nil || replay.ID != result.ID {
		t.Fatalf("idempotent replay id=%v err=%v", replay, err)
	}

	receiverWallet, err := wallets.FindWalletByUserID(ctx, receiverID)
	if err != nil || receiverWallet == nil {
		t.Fatalf("receiver wallet: %v", err)
	}
	receiverAccount, err := wallets.GetOrCreateAccount(ctx, receiverWallet.ID, "USD")
	if err != nil {
		t.Fatal(err)
	}
	fromBalance, err := ledger.GetBalance(ctx, senderAccount.ID)
	if err != nil {
		t.Fatal(err)
	}
	toBalance, err := ledger.GetBalance(ctx, receiverAccount.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !fromBalance.Equal(decimal.NewFromInt(75)) || !toBalance.Equal(decimal.NewFromInt(25)) {
		t.Fatalf("balances after transfer sender=%s receiver=%s", fromBalance, toBalance)
	}

	// The repository lookup is scoped to its owner in SQL; a different user
	// cannot retrieve the successful transfer by its internal UUID.
	strangerTransfer, err := transfers.FindByIDForUser(ctx, result.ID, strangerID)
	if err != nil {
		t.Fatalf("cross-user lookup error: %v", err)
	}
	if strangerTransfer != nil {
		t.Fatal("cross-user transfer lookup unexpectedly succeeded")
	}
	if _, err := svc.GetForUser(ctx, result.ID, strangerID); !errors.Is(err, apptransfer.ErrTransferNotFound) {
		t.Fatalf("cross-user service lookup error=%v, want ErrTransferNotFound", err)
	}
	if own, err := svc.GetForUser(ctx, result.ID, senderID); err != nil || own.ID != result.ID {
		t.Fatalf("owner service lookup id=%v err=%v", own, err)
	}
}
