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

func TestIdempotency_Transfer_Replay_NoDuplicate(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()

	sender := createTestAccount(t, pool)
	receiver := createTestAccount(t, pool)
	ledger := postgres.NewLedgerRepository(pool)
	testAdjustment(t, pool, ledger, sender, 100)

	wallets := postgres.NewWalletRepository(pool)
	assets := postgres.NewAssetRepository(pool)
	transfers := postgres.NewTransferRepository(pool)
	// Find wallet for sender
	var senderWalletID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT wallet_id FROM wallet_accounts WHERE id=$1`, sender).Scan(&senderWalletID); err != nil {
		t.Fatal(err)
	}
	var senderUserID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM wallets WHERE id=$1`, senderWalletID).Scan(&senderUserID); err != nil {
		t.Fatal(err)
	}
	var receiverWalletID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT wallet_id FROM wallet_accounts WHERE id=$1`, receiver).Scan(&receiverWalletID); err != nil {
		t.Fatal(err)
	}
	var receiverUserID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM wallets WHERE id=$1`, receiverWalletID).Scan(&receiverUserID); err != nil {
		t.Fatal(err)
	}

	svc := apptransfer.NewService(wallets, ledger, transfers, assets, postgres.NewUserRepository(pool), nil, nil)
	key := "idem-" + uuid.NewString()
	in := apptransfer.CreateTransferInput{SenderUserID: senderUserID, RecipientUserID: receiverUserID, AssetCode: "USD", Amount: decimal.NewFromInt(10), IdempotencyKey: key}
	r1, err := svc.Execute(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := svc.Execute(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if r1.ID != r2.ID {
		t.Fatal("idempotency failed")
	}
	b, _ := ledger.GetBalance(ctx, sender)
	if !b.Equal(decimal.NewFromInt(90)) {
		t.Fatalf("balance wrong: %s", b.String())
	}
}

func TestIDOR_TransferLookup_CrossUserDenied(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()
	sender := createTestAccount(t, pool)
	receiver := createTestAccount(t, pool)
	stranger := createTestAccount(t, pool)
	ledger := postgres.NewLedgerRepository(pool)
	testAdjustment(t, pool, ledger, sender, 50)

	wallets := postgres.NewWalletRepository(pool)
	assets := postgres.NewAssetRepository(pool)
	transfers := postgres.NewTransferRepository(pool)
	var senderWalletID uuid.UUID
	pool.QueryRow(ctx, `SELECT wallet_id FROM wallet_accounts WHERE id=$1`, sender).Scan(&senderWalletID)
	var senderUserID uuid.UUID
	pool.QueryRow(ctx, `SELECT user_id FROM wallets WHERE id=$1`, senderWalletID).Scan(&senderUserID)
	var receiverWalletID uuid.UUID
	pool.QueryRow(ctx, `SELECT wallet_id FROM wallet_accounts WHERE id=$1`, receiver).Scan(&receiverWalletID)
	var receiverUserID uuid.UUID
	pool.QueryRow(ctx, `SELECT user_id FROM wallets WHERE id=$1`, receiverWalletID).Scan(&receiverUserID)
	var strangerWalletID uuid.UUID
	pool.QueryRow(ctx, `SELECT wallet_id FROM wallet_accounts WHERE id=$1`, stranger).Scan(&strangerWalletID)
	var strangerUserID uuid.UUID
	pool.QueryRow(ctx, `SELECT user_id FROM wallets WHERE id=$1`, strangerWalletID).Scan(&strangerUserID)

	svc := apptransfer.NewService(wallets, ledger, transfers, assets, postgres.NewUserRepository(pool), nil, nil)
	in := apptransfer.CreateTransferInput{SenderUserID: senderUserID, RecipientUserID: receiverUserID, AssetCode: "USD", Amount: decimal.NewFromInt(5), IdempotencyKey: "idor-" + uuid.NewString()}
	res, err := svc.Execute(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	strangerTransfer, err := transfers.FindByIDForUser(ctx, res.ID, strangerUserID)
	if err != nil {
		t.Fatalf("cross-user lookup error: %v", err)
	}
	if strangerTransfer != nil {
		t.Fatal("cross-user access allowed")
	}
	if _, err := svc.GetForUser(ctx, res.ID, strangerUserID); !errors.Is(err, apptransfer.ErrTransferNotFound) {
		t.Fatalf("cross-user service lookup error=%v, want ErrTransferNotFound", err)
	}
	if own, err := svc.GetForUser(ctx, res.ID, senderUserID); err != nil || own.ID != res.ID {
		t.Fatalf("owner service lookup id=%v err=%v", own, err)
	}
}

func TestReorg_IncidentHandling(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()
	incidents := postgres.NewDepositIncidentRepository(pool)

	tx := "0xreorg-" + uuid.NewString()
	defer pool.Exec(ctx, `DELETE FROM deposit_incidents WHERE tx_hash = $1`, tx)
	defer pool.Exec(ctx, `DELETE FROM blockchain_transfers WHERE tx_hash = $1`, tx)

	var transferID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO blockchain_transfers
			(network, asset_code, contract_address, tx_hash, log_index, block_number, block_hash,
			 from_address, to_address, amount, decimals, confirmations, status)
		VALUES ('BSC','USDT','0xContractTest',$1,0,100,'0xblock100','0xfrom','0xto',5,18,30,'credited')
		RETURNING id
	`, tx).Scan(&transferID)
	if err != nil {
		t.Fatal(err)
	}

	inc := domain.DepositIncident{
		Network: domain.NetworkBSC, Kind: domain.IncidentReorgAfterCredit, TransferID: transferID,
		Transfer: domain.BlockchainTransfer{
			TxHash: tx, LogIndex: 0, AssetCode: "USDT", ToAddress: "0xto", Amount: decimal.NewFromInt(5),
			BlockHash: "0xblock100", BlockNumber: 100, FromAddress: "0xfrom", ContractAddress: "0xContractTest", Network: domain.NetworkBSC,
		},
		Details: "reorg detected",
	}
	if err := incidents.Report(ctx, inc); err != nil {
		t.Fatal(err)
	}
	// Idempotent
	if err := incidents.Report(ctx, inc); err != nil {
		t.Fatal(err)
	}
	var count int
	pool.QueryRow(ctx, `SELECT count(*) FROM deposit_incidents WHERE tx_hash = $1 AND status='open'`, tx).Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 open incident, got %d", count)
	}
}

func TestBroadcastAmbiguous_SettlementFailed(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()

	userID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,first_name,last_name,status) VALUES ($1,$2,'x','A','B','active')`, userID, userID.String()+"@amb.test"); err != nil {
		t.Fatal(err)
	}
	wid := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO blockchain_withdrawals(id,user_id,network,asset_code,to_address,amount,status,idempotency_key) VALUES ($1,$2,'BSC','USDT',$3,1,'processing',$4)`,
		wid, userID, "0x0000000000000000000000000000000000000001", "amb-"+wid.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO withdrawal_events(withdrawal_id,event_type,from_status,to_status,detail) VALUES ($1,'broadcast_ambiguous','processing','processing',$2)`,
		wid, []byte(`{"error":"timeout"}`)); err != nil {
		t.Fatal(err)
	}
	var c int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM withdrawal_events WHERE withdrawal_id=$1 AND event_type='broadcast_ambiguous' AND from_status='processing' AND to_status='processing' AND detail->>'error'='timeout'`, wid).Scan(&c); err != nil {
		t.Fatal(err)
	}
	if c != 1 {
		t.Fatal("event not recorded")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM blockchain_withdrawals WHERE id=$1`, wid).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "processing" {
		t.Fatalf("ambiguous broadcast must stay processing, got %q", status)
	}
}
