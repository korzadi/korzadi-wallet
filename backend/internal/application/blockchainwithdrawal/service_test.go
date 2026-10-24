package blockchainwithdrawal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	appfee "korzadi-wallet/backend/internal/application/fee"
	"korzadi-wallet/backend/internal/application/locking"
	"korzadi-wallet/backend/internal/application/transfer"
	"korzadi-wallet/backend/internal/domain"
)

const testNetwork = domain.NetworkETHEREUM

type harness struct {
	svc      *Service
	store    *fakeStore
	nonces   *fakeNonces
	ledger   *fakeLedger
	accounts *fakeAccounts
	clearing *fakeClearing
	fees     *fakeFeeEngine
	quotes   *fakeQuotes
	users    *fakeUsers
	wallets  *fakeWallets
	registry *fakeRegistry
	addrs    *fakeAddresses
	signer   *fakeSigner
	chain    *fakeChain
	userID   uuid.UUID
	feeAcc   uuid.UUID
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		store: newFakeStore(), nonces: newFakeNonces(), ledger: newFakeLedger(),
		accounts: newFakeAccounts(), clearing: newFakeClearing(), fees: newFakeFeeEngine(),
		users: newFakeUsers(), wallets: newFakeWallets(), registry: newFakeRegistry(),
		addrs: newFakeAddresses(), signer: &fakeSigner{}, chain: newFakeChain(testNetwork),
		userID: uuid.New(),
	}
	h.quotes = &fakeQuotes{engine: h.fees}
	h.feeAcc, _ = h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, FeeAssetCode)
	h.ledger.fund(h.feeAcc, decimal.NewFromInt(1000))
	feeReserver := &fakeFeeReserver{ledger: h.ledger, engine: h.fees, feeAcc: h.feeAcc}

	h.users.users[h.userID] = &domain.User{ID: h.userID, Status: domain.UserStatusActive}
	h.wallets.wallets[h.userID] = &domain.Wallet{ID: uuid.New(), Status: domain.WalletStatusActive}
	h.registry.add(domain.NetworkAsset{Network: testNetwork, AssetCode: "ETH", Kind: domain.AssetKindNative, Decimals: 18, Enabled: true})
	h.addrs.addresses[string(testNetwork)+"|"+h.userID.String()] = "0x1111111111111111111111111111111111111111"

	svc, err := NewService(Deps{
		Store: h.store, Nonces: h.nonces, Ledger: h.ledger, UserAccounts: h.accounts, Clearing: h.clearing,
		Fees: h.fees, FeeReserver: feeReserver, Quotes: h.quotes, Users: h.users, Wallets: h.wallets,
		LedgerAssets: newFakeLedgerAssetsAdapter("ETH"), Registry: h.registry, Addresses: h.addrs,
		Signer: h.signer, Networks: map[domain.Network]NetworkRuntime{testNetwork: {Chain: h.chain}},
		Locks: locking.NewMemory(),
		Config: Config{
			Enabled: true, MainnetEnabled: true,
			Limits:        map[string]AssetLimits{"ETH": {Max: decimal.NewFromInt(10), DailyMax: decimal.NewFromInt(100)}},
			LeaseDuration: time.Minute, PollInterval: time.Second, BatchSize: 10,
			MaxReserveTries: 5, MaxPrepareTries: 5, BroadcastGrace: time.Hour, MaxNetworkFeeOverrunBps: 5000,
		},
		Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	h.svc = svc
	return h
}

func (h *harness) createInput(amount string) CreateInput {
	return CreateInput{
		UserID: h.userID, AssetCode: "ETH", Network: string(testNetwork),
		Amount: decimal.RequireFromString(amount), DestinationAddress: "0x2222222222222222222222222222222222222222",
		IdempotencyKey: "wd-" + uuid.NewString(), TwoFAVerified: true,
	}
}

// ---- Create ----

func TestCreate_HappyPath_ReservesAmountAndFee(t *testing.T) {
	h := newHarness(t)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))

	result, err := h.svc.Create(context.Background(), h.createInput("1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !result.Created {
		t.Fatal("esperaba created=true")
	}
	w := result.Withdrawal
	if w.Status != StatusReserved {
		t.Fatalf("esperaba reserved, obtuve %s", w.Status)
	}
	if w.AmountReservationID == nil || w.FeeReservationID == nil {
		t.Fatal("faltan reservas")
	}
	// Fondeamos 5 ETH y retiramos 1 ETH: debe quedar disponible 4 (el otro
	// se movió a locked_balance vía la reserva, no desaparece del todo).
	if !h.ledger.balances[userAcc].Equal(decimal.NewFromInt(4)) {
		t.Fatalf("el saldo disponible tras reservar 1 de 5 debe ser 4, está %s", h.ledger.balances[userAcc])
	}
}

func TestCreate_InsufficientFunds_NoReservationLeftBehind(t *testing.T) {
	h := newHarness(t)
	// sin fondear la cuenta de ETH del usuario

	result, err := h.svc.Create(context.Background(), h.createInput("1"))
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("esperaba ErrInsufficientFunds, obtuve %v", err)
	}
	if result.Withdrawal.Status != StatusFailed {
		t.Fatalf("esperaba failed, obtuve %s", result.Withdrawal.Status)
	}
	if result.Withdrawal.AmountReservationID != nil {
		t.Fatal("no debe quedar una reserva de monto")
	}
}

func TestCreate_BitcoinAlwaysRefused_NoSideEffects(t *testing.T) {
	h := newHarness(t)
	in := h.createInput("0.01")
	in.Network = string(domain.NetworkBITCOIN)
	in.DestinationAddress = "bc1qxyz"

	_, err := h.svc.Create(context.Background(), in)
	if !errors.Is(err, ErrBitcoinDisabled) {
		t.Fatalf("esperaba ErrBitcoinDisabled, obtuve %v", err)
	}
	if len(h.store.byID) != 0 {
		t.Fatal("bitcoin no debe crear ninguna fila de withdrawal")
	}
	if len(h.ledger.reservations) != 0 {
		t.Fatal("bitcoin no debe crear ninguna reserva")
	}
}

func TestCreate_IdempotentReplay_SameResultNoNewReservation(t *testing.T) {
	h := newHarness(t)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))

	in := h.createInput("1")
	first, err := h.svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("primer create: %v", err)
	}
	second, err := h.svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("segundo create: %v", err)
	}
	if second.Created {
		t.Fatal("el segundo debe ser un replay (created=false)")
	}
	if first.Withdrawal.ID != second.Withdrawal.ID {
		t.Fatal("el replay debe devolver el mismo withdrawal")
	}
	if len(h.ledger.reservations) != 2 { // exactamente una de monto + una de fee
		t.Fatalf("no debe crear reservas adicionales en el replay, hay %d", len(h.ledger.reservations))
	}
}

func TestCreate_ExpiredQuote_Rejected(t *testing.T) {
	h := newHarness(t)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))

	quote, err := h.fees.EstimateFee(context.Background(), appfee.EstimateFeeInput{
		UserID: h.userID, OperationType: appfee.OperationWithdrawal, Network: testNetwork,
		OperationAssetCode: "ETH", OperationAmount: decimal.NewFromInt(1),
	})
	if err != nil {
		t.Fatalf("estimate fee: %v", err)
	}
	quote.ExpiresAt = time.Now().Add(-time.Minute) // ya expirada

	in := h.createInput("1")
	in.FeeQuoteID = &quote.ID
	_, err = h.svc.Create(context.Background(), in)
	if !errors.Is(err, appfee.ErrQuoteExpired) {
		t.Fatalf("esperaba ErrQuoteExpired, obtuve %v", err)
	}
}

func TestCreate_ConcurrentSameBalance_OnlyOneSucceeds(t *testing.T) {
	h := newHarness(t)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(1)) // alcanza para UN retiro de 1 ETH, no para dos

	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			in := h.createInput("1") // cada goroutine con su propia idempotency key: son dos solicitudes DISTINTAS
			_, err := h.svc.Create(context.Background(), in)
			results <- err
		}()
	}
	ok, failed := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrInsufficientFunds):
			failed++
		default:
			t.Fatalf("error inesperado: %v", err)
		}
	}
	if ok != 1 || failed != 1 {
		t.Fatalf("esperaba exactamente 1 éxito y 1 fallo por fondos insuficientes, obtuve ok=%d failed=%d", ok, failed)
	}
	if h.ledger.balances[userAcc].Sign() != 0 {
		t.Fatalf("no debe quedar saldo disponible negativo ni positivo inesperado: %s", h.ledger.balances[userAcc])
	}
}

func TestCreate_InvalidDestinationAddress_RejectedBeforeReservation(t *testing.T) {
	h := newHarness(t)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))

	in := h.createInput("1")
	in.DestinationAddress = "not-an-address"
	_, err := h.svc.Create(context.Background(), in)
	if !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("esperaba ErrInvalidAddress, obtuve %v", err)
	}
	if len(h.ledger.reservations) != 0 {
		t.Fatal("una dirección inválida no debe reservar nada")
	}
}

func TestCreate_AssetNetworkMismatch_Rejected(t *testing.T) {
	h := newHarness(t)
	in := h.createInput("1")
	in.AssetCode = "BTC" // BTC no está registrado para testNetwork (ETHEREUM)
	_, err := h.svc.Create(context.Background(), in)
	if !errors.Is(err, ErrAssetNotEnabled) {
		t.Fatalf("esperaba ErrAssetNotEnabled, obtuve %v", err)
	}
}

// ---- Ownership ----

func TestGet_OwnershipEnforced(t *testing.T) {
	h := newHarness(t)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))
	result, err := h.svc.Create(context.Background(), h.createInput("1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	other := uuid.New()
	_, err = h.svc.Get(context.Background(), other, result.Withdrawal.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("un usuario ajeno no debe poder leer el retiro de otro (esperaba ErrNotFound, obtuve %v)", err)
	}

	got, err := h.svc.Get(context.Background(), h.userID, result.Withdrawal.ID)
	if err != nil || got.ID != result.Withdrawal.ID {
		t.Fatalf("el dueño sí debe poder leerlo: %v", err)
	}
}

// fakeLedgerAssetsAdapter adapts a set of enabled codes to bw.LedgerAssets.
type fakeLedgerAssetsAdapter map[string]bool

func newFakeLedgerAssetsAdapter(codes ...string) fakeLedgerAssetsAdapter {
	m := fakeLedgerAssetsAdapter{}
	for _, c := range codes {
		m[c] = true
	}
	m["USDT"] = true
	return m
}

func (m fakeLedgerAssetsAdapter) Find(ctx context.Context, code string) (transfer.LedgerAsset, bool, error) {
	if !m[code] {
		return transfer.LedgerAsset{}, false, nil
	}
	return transfer.LedgerAsset{Code: code, Decimals: 18, IsEnabled: true}, true, nil
}
