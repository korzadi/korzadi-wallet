//go:build integration

package tests

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	appp2p "korzadi-wallet/backend/internal/application/p2p"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
	p2prepo "korzadi-wallet/backend/internal/infrastructure/postgres/p2p"
)

func createTestUserWithBalance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, asset string, amount decimal.Decimal) uuid.UUID {
	userID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,first_name,last_name,status,email_verified) VALUES ($1,$2,'x','T','U','active',true)`, userID, userID.String()+"@test.local"); err != nil {
		t.Fatalf("user: %v", err)
	}
	walletID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO wallets(id,user_id,status) VALUES ($1,$2,'active')`, walletID, userID); err != nil {
		t.Fatalf("wallet: %v", err)
	}
	accountID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO wallet_accounts(id,wallet_id,asset_code,account_type) VALUES ($1,$2,$3,'user')`, accountID, walletID, asset); err != nil {
		t.Fatalf("account: %v", err)
	}
	if amount.IsPositive() {
		if !amount.IsInteger() {
			t.Fatalf("test balance %s must be an integer amount", amount)
		}
		testAdjustment(t, pool, postgres.NewLedgerRepository(pool), accountID, amount.IntPart())
	}
	t.Cleanup(func() {
		p := testPool(t)
		p.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
	})
	return userID
}

func TestP2P_CreateOffer_Sell(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()

	seller := createTestUserWithBalance(t, ctx, pool, "USDT", decimal.NewFromInt(1000))

	repo := p2prepo.New(pool)
	ledger := postgres.NewLedgerRepository(pool)
	resv := postgres.NewReservationRepository(pool)
	accounts := postgres.NewUserWalletAccountRepository(pool)
	wallets := postgres.NewWalletRepository(pool)

	svc := appp2p.New(repo, ledger, resv, accounts, wallets, nil, decimal.NewFromInt(100))

	offer, err := svc.CreateOffer(ctx, seller, appp2p.CreateOfferInput{
		AssetCode: "USDT", FiatCurrency: "ARS", PricePerUnit: decimal.NewFromInt(1000),
		MinAmount: decimal.NewFromInt(10), MaxAmount: decimal.NewFromInt(100),
		TotalAmount: decimal.NewFromInt(100), Side: "sell",
		PaymentMethods: []string{"bank_transfer"}, CountryRestrictions: []string{"AR"},
		PaymentTimeLimitMins: 30, Instructions: "Transfer to CBU...", Terms: "Standard terms",
	})
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if offer.AvailableAmount.Equal(decimal.NewFromInt(100)) && offer.Side == "sell" {
		t.Logf("offer created: %s", offer.ID)
	}
}

func TestP2P_CreateOffer_RejectsInsufficientBalance(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()

	seller := createTestUserWithBalance(t, ctx, pool, "USDT", decimal.NewFromInt(50))

	repo := p2prepo.New(pool)
	ledger := postgres.NewLedgerRepository(pool)
	resv := postgres.NewReservationRepository(pool)
	accounts := postgres.NewUserWalletAccountRepository(pool)
	wallets := postgres.NewWalletRepository(pool)

	svc := appp2p.New(repo, ledger, resv, accounts, wallets, nil, decimal.NewFromInt(100))

	_, err := svc.CreateOffer(ctx, seller, appp2p.CreateOfferInput{
		AssetCode: "USDT", FiatCurrency: "ARS", PricePerUnit: decimal.NewFromInt(1000),
		MinAmount: decimal.NewFromInt(10), MaxAmount: decimal.NewFromInt(100),
		TotalAmount: decimal.NewFromInt(100), Side: "sell",
		PaymentMethods: []string{"bank_transfer"}, CountryRestrictions: []string{"AR"},
		PaymentTimeLimitMins: 30,
	})
	if err == nil {
		t.Fatal("expected insufficient balance error")
	}
	t.Logf("correctly rejected: %v", err)
}

func TestP2P_CreateOrder_ReservesEscrow(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()

	seller := createTestUserWithBalance(t, ctx, pool, "USDT", decimal.NewFromInt(1000))
	buyer := createTestUserWithBalance(t, ctx, pool, "USD", decimal.NewFromInt(0))

	repo := p2prepo.New(pool)
	ledger := postgres.NewLedgerRepository(pool)
	resv := postgres.NewReservationRepository(pool)
	accounts := postgres.NewUserWalletAccountRepository(pool)
	wallets := postgres.NewWalletRepository(pool)

	svc := appp2p.New(repo, ledger, resv, accounts, wallets, nil, decimal.NewFromInt(100))

	offer, err := svc.CreateOffer(ctx, seller, appp2p.CreateOfferInput{
		AssetCode: "USDT", FiatCurrency: "ARS", PricePerUnit: decimal.NewFromInt(1000),
		MinAmount: decimal.NewFromInt(10), MaxAmount: decimal.NewFromInt(100),
		TotalAmount: decimal.NewFromInt(100), Side: "sell",
		PaymentMethods: []string{"bank_transfer"}, CountryRestrictions: []string{"AR"},
		PaymentTimeLimitMins: 30, Instructions: "Transfer to CBU...",
	})
	if err != nil {
		t.Fatal(err)
	}

	order, err := svc.CreateOrder(ctx, buyer, appp2p.CreateOrderInput{
		OfferID: offer.ID, Amount: decimal.NewFromInt(10), PaymentMethod: "bank_transfer",
		IdempotencyKey: "test-idem-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != "escrow_locked" {
		t.Fatalf("order status: %s", order.Status)
	}
	if order.EscrowReservationID == nil {
		t.Fatal("missing escrow reservation")
	}
	t.Logf("order created: %s, escrow: %s", order.ID, *order.EscrowReservationID)
}

func TestP2P_Release_TransfersFunds(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()

	seller := createTestUserWithBalance(t, ctx, pool, "USDT", decimal.NewFromInt(1000))
	buyer := createTestUserWithBalance(t, ctx, pool, "USD", decimal.NewFromInt(0))

	repo := p2prepo.New(pool)
	ledger := postgres.NewLedgerRepository(pool)
	resv := postgres.NewReservationRepository(pool)
	accounts := postgres.NewUserWalletAccountRepository(pool)
	wallets := postgres.NewWalletRepository(pool)

	svc := appp2p.New(repo, ledger, resv, accounts, wallets, nil, decimal.NewFromInt(100))

	offer, _ := svc.CreateOffer(ctx, seller, appp2p.CreateOfferInput{
		AssetCode: "USDT", FiatCurrency: "ARS", PricePerUnit: decimal.NewFromInt(1000),
		MinAmount: decimal.NewFromInt(10), MaxAmount: decimal.NewFromInt(100),
		TotalAmount: decimal.NewFromInt(100), Side: "sell",
		PaymentMethods: []string{"bank_transfer"}, CountryRestrictions: []string{"AR"},
		PaymentTimeLimitMins: 30, Instructions: "Transfer to CBU...",
	})
	order, _ := svc.CreateOrder(ctx, buyer, appp2p.CreateOrderInput{
		OfferID: offer.ID, Amount: decimal.NewFromInt(10), PaymentMethod: "bank_transfer",
		IdempotencyKey: "test-idem-" + uuid.NewString(),
	})

	// Mark paid by buyer
	if err := svc.MarkPaid(ctx, buyer, order.ID); err != nil {
		t.Fatalf("mark paid: %v", err)
	}

	// Release by seller
	if err := svc.Release(ctx, seller, order.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Check buyer balance
	buyerAcc, _ := accounts.FindOrCreateUserAccount(ctx, buyer, "USDT")
	bal, _ := ledger.GetBalance(ctx, buyerAcc)
	if !bal.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("buyer balance: %s, expected 10", bal.String())
	}
	t.Logf("buyer received: %s", bal.String())
}

func TestP2P_Cancel_ReturnsEscrow(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	defer pool.Close()

	seller := createTestUserWithBalance(t, ctx, pool, "USDT", decimal.NewFromInt(1000))
	buyer := createTestUserWithBalance(t, ctx, pool, "USD", decimal.NewFromInt(0))

	repo := p2prepo.New(pool)
	ledger := postgres.NewLedgerRepository(pool)
	resv := postgres.NewReservationRepository(pool)
	accounts := postgres.NewUserWalletAccountRepository(pool)
	wallets := postgres.NewWalletRepository(pool)

	svc := appp2p.New(repo, ledger, resv, accounts, wallets, nil, decimal.NewFromInt(100))

	offer, _ := svc.CreateOffer(ctx, seller, appp2p.CreateOfferInput{
		AssetCode: "USDT", FiatCurrency: "ARS", PricePerUnit: decimal.NewFromInt(1000),
		MinAmount: decimal.NewFromInt(10), MaxAmount: decimal.NewFromInt(100),
		TotalAmount: decimal.NewFromInt(100), Side: "sell",
		PaymentMethods: []string{"bank_transfer"}, CountryRestrictions: []string{"AR"},
		PaymentTimeLimitMins: 30, Instructions: "Transfer to CBU...",
	})
	order, _ := svc.CreateOrder(ctx, buyer, appp2p.CreateOrderInput{
		OfferID: offer.ID, Amount: decimal.NewFromInt(10), PaymentMethod: "bank_transfer",
		IdempotencyKey: "test-idem-" + uuid.NewString(),
	})

	if err := svc.Cancel(ctx, seller, order.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	// Check seller balance unchanged
	sellerAcc, _ := accounts.FindOrCreateUserAccount(ctx, seller, "USDT")
	bal, _ := ledger.GetBalance(ctx, sellerAcc)
	if !bal.Equal(decimal.NewFromInt(1000)) {
		t.Fatalf("seller balance: %s, expected 1000", bal.String())
	}
	t.Logf("seller balance preserved: %s", bal.String())
}
