//go:build integration

package tests

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	appdeposit "korzadi-wallet/backend/internal/application/deposit"
	"korzadi-wallet/backend/internal/application/watcher"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

// fakeRPCProvider deliberately implements only deterministic local behavior;
// no test in this package can reach a public RPC endpoint.
type fakeRPCProvider struct {
	network domain.Network
	event   domain.OnChainTransferEvent
	receipt domain.TransactionReceipt
}

func (f *fakeRPCProvider) Network() domain.Network { return f.network }
func (f *fakeRPCProvider) HealthCheck(context.Context, string) (domain.ProviderHealth, error) {
	return domain.ProviderHealth{}, nil
}
func (f *fakeRPCProvider) LatestBlock(context.Context) (uint64, error) {
	return f.event.BlockNumber, nil
}
func (f *fakeRPCProvider) NativeBalance(context.Context, string) (decimal.Decimal, error) {
	return decimal.Zero, nil
}
func (f *fakeRPCProvider) TokenBalance(context.Context, string, string, int32) (decimal.Decimal, error) {
	return decimal.Zero, nil
}
func (f *fakeRPCProvider) TransferEventsInRange(_ context.Context, contract string, _ int32, from, to uint64) ([]domain.OnChainTransferEvent, error) {
	if contract == f.event.ContractAddress && from <= f.event.BlockNumber && to >= f.event.BlockNumber {
		return []domain.OnChainTransferEvent{f.event}, nil
	}
	return nil, nil
}
func (f *fakeRPCProvider) NativeTransfersInRange(context.Context, uint64, uint64, map[string]bool) ([]domain.OnChainNativeTransferEvent, error) {
	return nil, nil
}
func (f *fakeRPCProvider) GetReceipt(context.Context, string) (*domain.TransactionReceipt, error) {
	r := f.receipt
	return &r, nil
}
func (f *fakeRPCProvider) EstimateFee(context.Context, string, string, decimal.Decimal) (decimal.Decimal, error) {
	return decimal.Zero, nil
}
func (f *fakeRPCProvider) BroadcastTransaction(context.Context, []byte) (string, error) {
	return "", fmt.Errorf("fake RPC: broadcast not enabled in deposit tests")
}
func (f *fakeRPCProvider) GetTransaction(context.Context, string) (*domain.TransactionReceipt, error) {
	return nil, nil
}

func provisionDepositAddress(t *testing.T, ctx context.Context, pool *pgxpool.Pool, network domain.Network) (uuid.UUID, string) {
	t.Helper()
	userID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,first_name,last_name,status,email_verified) VALUES ($1,$2,'test','Integration','User','active',true)`, userID, userID.String()+"@integration.test"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	walletID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO wallets(id,user_id,status) VALUES ($1,$2,'active')`, walletID, userID); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}
	address := "deposit-" + string(network) + "-" + uuid.NewString()
	addressID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO crypto_addresses(id,user_id,network,asset_code,address,derivation_index,encrypted_private_key,status) VALUES ($1,$2,$3,'USDT',$4,0,'test-only','active')`, addressID, userID, network, address); err != nil {
		t.Fatalf("insert deposit address: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID) })
	return userID, address
}

func TestBlockchainDeposit_FullFlowOnBSCETHAndTRON(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()

	ledger := postgres.NewLedgerRepository(pool)
	confirm := appdeposit.NewBlockchainDepositService(
		ledger,
		postgres.NewClearingAccountRepository(pool),
		postgres.NewUserWalletAccountRepository(pool),
		postgres.NewBlockchainDepositRecorderRepository(pool),
	)
	for _, network := range []domain.Network{domain.NetworkBSC, domain.NetworkETHEREUM, domain.NetworkTRON} {
		t.Run(string(network), func(t *testing.T) {
			userID, to := provisionDepositAddress(t, ctx, pool, network)
			contract := "Tether-test-contract-" + string(network)
			txHash := "fake-tx-" + uuid.NewString()
			event := domain.OnChainTransferEvent{ContractAddress: contract, TxHash: txHash, LogIndex: 0, BlockNumber: 100, BlockHash: "fake-block-100", From: "sender-test", To: to, RawAmount: decimal.RequireFromString("12.5")}
			provider := &fakeRPCProvider{network: network, event: event, receipt: domain.TransactionReceipt{TxHash: txHash, BlockNumber: 100, BlockHash: event.BlockHash, Status: true, Confirmations: 2}}
			w := watcher.NewEVMDepositWatcher(provider, postgres.NewAddressToUserResolver(pool), postgres.NewAddressToUserResolver(pool), postgres.NewBlockchainTransferRepository(pool), postgres.NewBlockchainSyncStateRepository(pool), confirm,
				watcher.Config{Network: network, Assets: []watcher.EVMAsset{{AssetCode: "USDT", Kind: domain.AssetKindToken, ContractAddress: contract, Decimals: 6}}, ConfirmationsRequired: 1, PollInterval: 10 * time.Millisecond, MaxBlocksPerScan: 10})
			watchCtx, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() { w.Run(watchCtx); close(done) }()
			defer func() { cancel(); <-done }()

			accountID, err := (postgres.NewUserWalletAccountRepository(pool)).FindOrCreateUserAccount(ctx, userID, "USDT")
			if err != nil {
				t.Fatalf("resolve account: %v", err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				balance, berr := ledger.GetBalance(ctx, accountID)
				if berr != nil {
					t.Fatalf("read ledger balance: %v", berr)
				}
				if balance.Equal(decimal.RequireFromString("12.5")) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			balance, err := ledger.GetBalance(ctx, accountID)
			if err != nil || !balance.Equal(decimal.RequireFromString("12.5")) {
				t.Fatalf("final credited balance = %s, err=%v", balance, err)
			}
			var status string
			if err := pool.QueryRow(ctx, `SELECT status FROM blockchain_transfers WHERE network=$1 AND tx_hash=$2`, network, txHash).Scan(&status); err != nil || status != string(domain.TransferStatusCredited) {
				t.Fatalf("transfer status=%q err=%v", status, err)
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM blockchain_deposits WHERE network=$1 AND tx_hash=$2`, network, txHash).Scan(&count); err != nil || count != 1 {
				t.Fatalf("deposit rows=%d err=%v", count, err)
			}
			otherUserID, _ := provisionDepositAddress(t, ctx, pool, network)
			recorder := postgres.NewBlockchainDepositRecorderRepository(pool)
			ownRows, err := recorder.ListByUser(ctx, userID, 20, 0)
			if err != nil || len(ownRows) != 1 {
				t.Fatalf("owner deposit history rows=%d err=%v", len(ownRows), err)
			}
			otherRows, err := recorder.ListByUser(ctx, otherUserID, 20, 0)
			if err != nil || len(otherRows) != 0 {
				t.Fatalf("cross-user deposit history rows=%d err=%v", len(otherRows), err)
			}
			confirmed := domain.BlockchainTransfer{Network: network, AssetCode: "USDT", AssetKind: domain.AssetKindToken, ContractAddress: contract,
				TxHash: txHash, LogIndex: 0, BlockNumber: 100, BlockHash: event.BlockHash, FromAddress: event.From, ToAddress: to,
				Amount: event.RawAmount, Decimals: 6, Status: domain.TransferStatusConfirmed}
			var wg sync.WaitGroup
			errCh := make(chan error, 8)
			for i := 0; i < cap(errCh); i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := confirm.ConfirmBlockchainDeposit(ctx, confirmed, userID)
					errCh <- err
				}()
			}
			wg.Wait()
			close(errCh)
			for err := range errCh {
				if err != nil {
					t.Errorf("idempotent concurrent credit: %v", err)
				}
			}
			balance, err = ledger.GetBalance(ctx, accountID)
			if err != nil || !balance.Equal(decimal.RequireFromString("12.5")) {
				t.Fatalf("balance after duplicate credits=%s err=%v", balance, err)
			}
		})
	}
}
