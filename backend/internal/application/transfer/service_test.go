package transfer

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ---- fakes ----

// fakeWallets is a minimal in-memory domain.WalletRepository. One wallet
// and one account (per asset) per user, created on demand.
type fakeWallets struct {
	mu       sync.Mutex
	wallets  map[uuid.UUID]*domain.Wallet                   // userID -> wallet
	accounts map[uuid.UUID]map[string]*domain.WalletAccount // walletID -> assetCode -> account
}

func newFakeWallets() *fakeWallets {
	return &fakeWallets{wallets: map[uuid.UUID]*domain.Wallet{}, accounts: map[uuid.UUID]map[string]*domain.WalletAccount{}}
}

func (f *fakeWallets) seedUser(userID uuid.UUID) uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := &domain.Wallet{ID: uuid.New(), UserID: userID, Status: domain.WalletStatus("active")}
	f.wallets[userID] = w
	f.accounts[w.ID] = map[string]*domain.WalletAccount{}
	return w.ID
}

func (f *fakeWallets) CreateWallet(ctx context.Context, w *domain.Wallet) error { return nil }

func (f *fakeWallets) FindWalletByUserID(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wallets[userID], nil // nil, nil for "not found" — mirrors the real repository exactly
}

func (f *fakeWallets) GetOrCreateAccount(ctx context.Context, walletID uuid.UUID, assetCode string) (*domain.WalletAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	accts, ok := f.accounts[walletID]
	if !ok {
		return nil, fmt.Errorf("wallet %s no existe", walletID)
	}
	if acc, ok := accts[assetCode]; ok {
		return acc, nil
	}
	acc := &domain.WalletAccount{ID: uuid.New(), WalletID: walletID, AssetCode: assetCode, AccountType: "user"}
	accts[assetCode] = acc
	return acc, nil
}

func (f *fakeWallets) ListAccounts(ctx context.Context, walletID uuid.UUID) ([]*domain.WalletAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.WalletAccount
	for _, a := range f.accounts[walletID] {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeWallets) setBalance(walletID uuid.UUID, assetCode string, amount decimal.Decimal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts[walletID][assetCode].BalanceCache = amount
}

// fakeLedger mimics the real consolidated engine closely enough for these
// unit tests: idempotent replay (same key -> same tx, nil error), balanced
// double-entry enforcement, and non-negative balance protection for
// account_type='user' — the exact same rule real
// postgres.LedgerRepository.ExecuteTransaction enforces (Bloque 2).
type fakeLedger struct {
	mu       sync.Mutex
	balances map[uuid.UUID]decimal.Decimal // walletAccountID -> balance
	posted   map[string]*domain.LedgerTransaction
	postCall int
}

func newFakeLedger() *fakeLedger {
	return &fakeLedger{balances: map[uuid.UUID]decimal.Decimal{}, posted: map[string]*domain.LedgerTransaction{}}
}

func (f *fakeLedger) ExecuteTransaction(ctx context.Context, txType domain.TransactionType, idempotencyKey string,
	referenceID *string, description string, createdBy *uuid.UUID, entries []domain.LedgerEntryInput) (*domain.LedgerTransaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.postCall++

	if tx, ok := f.posted[idempotencyKey]; ok {
		return tx, nil
	}

	sum := decimal.Zero
	for _, e := range entries {
		sum = sum.Add(e.Amount)
	}
	if !sum.IsZero() {
		return nil, fmt.Errorf("ledger desbalanceado: %s", sum)
	}

	// Non-negative check, only meaningful for the sender's debit (negative
	// entry) — mirrors the real engine's account-type-aware rule. Every
	// account in these tests is account_type='user'.
	for _, e := range entries {
		if e.Amount.IsNegative() {
			resulting := f.balances[e.WalletAccountID].Add(e.Amount)
			if resulting.IsNegative() {
				return nil, domain.ErrInsufficientFunds
			}
		}
	}
	for _, e := range entries {
		f.balances[e.WalletAccountID] = f.balances[e.WalletAccountID].Add(e.Amount)
	}

	tx := &domain.LedgerTransaction{ID: uuid.New(), Type: txType, Status: domain.TransactionStatus("completed"), IdempotencyKey: idempotencyKey}
	f.posted[idempotencyKey] = tx
	return tx, nil
}

func (f *fakeLedger) GetBalance(ctx context.Context, walletAccountID uuid.UUID) (decimal.Decimal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.balances[walletAccountID], nil
}
func (f *fakeLedger) FindByIdempotencyKey(ctx context.Context, key string) (*domain.LedgerTransaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posted[key], nil
}
func (f *fakeLedger) ListEntriesByAccount(ctx context.Context, walletAccountID uuid.UUID, limit, offset int) ([]*domain.LedgerEntry, error) {
	return nil, nil
}

// fakeTransfers mimics the real repository's idempotency_key UNIQUE
// constraint: a second concurrent Create with the same key returns
// domain.ErrIdempotencyReplay, exactly like the real Postgres repository
// now does after the Bloque 5 fix.
type fakeTransfers struct {
	mu      sync.Mutex
	byID    map[uuid.UUID]*domain.Transfer
	byKey   map[string]uuid.UUID
	creates int
}

func newFakeTransfers() *fakeTransfers {
	return &fakeTransfers{byID: map[uuid.UUID]*domain.Transfer{}, byKey: map[string]uuid.UUID{}}
}

func (f *fakeTransfers) Create(ctx context.Context, t *domain.Transfer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	if _, exists := f.byKey[t.IdempotencyKey]; exists {
		return domain.ErrIdempotencyReplay
	}
	cp := *t
	f.byID[t.ID] = &cp
	f.byKey[t.IdempotencyKey] = t.ID
	return nil
}

func (f *fakeTransfers) FindByIdempotencyKey(ctx context.Context, key string) (*domain.Transfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byKey[key]
	if !ok {
		return nil, nil
	}
	cp := *f.byID[id]
	return &cp, nil
}

func (f *fakeTransfers) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.Transfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Transfer
	for _, t := range f.byID {
		if t.SenderUserID == userID || t.ReceiverUserID == userID {
			cp := *t
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeTransfers) FindByIDForUser(ctx context.Context, id, userID uuid.UUID) (*domain.Transfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.byID[id]
	if !ok || (t.SenderUserID != userID && t.ReceiverUserID != userID) {
		return nil, nil
	}
	cp := *t
	return &cp, nil
}

func (f *fakeTransfers) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.TransactionStatus, ledgerTxID *uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.byID[id]
	if !ok {
		return fmt.Errorf("transfer %s no existe", id)
	}
	t.Status = status
	t.LedgerTransactionID = ledgerTxID
	return nil
}

type fakeAssets struct{ registered map[string]LedgerAsset }

func newFakeAssets() *fakeAssets {
	return &fakeAssets{registered: map[string]LedgerAsset{
		"USDT": {Code: "USDT", Name: "Tether", Decimals: 6, IsEnabled: true},
		"USD":  {Code: "USD", Name: "US Dollar", Decimals: 2, IsEnabled: true},
	}}
}

func (f *fakeAssets) Find(ctx context.Context, code string) (LedgerAsset, bool, error) {
	a, ok := f.registered[code]
	return a, ok, nil
}

type fakeUsers struct{ byEmail map[string]*domain.User }

func newFakeUsers() *fakeUsers { return &fakeUsers{byEmail: map[string]*domain.User{}} }

func (f *fakeUsers) FindByEmail(ctx context.Context, email string) (*domain.User, error) {
	return f.byEmail[email], nil
}

// ---- test harness ----

type harness struct {
	svc      *Service
	wallets  *fakeWallets
	ledger   *fakeLedger
	transfer *fakeTransfers
	assets   *fakeAssets
	users    *fakeUsers
}

func newHarness() *harness {
	h := &harness{
		wallets:  newFakeWallets(),
		ledger:   newFakeLedger(),
		transfer: newFakeTransfers(),
		assets:   newFakeAssets(),
		users:    newFakeUsers(),
	}
	h.svc = NewService(h.wallets, h.ledger, h.transfer, h.assets, h.users, nil, nil)
	return h
}

func (h *harness) fundUser(userID uuid.UUID, assetCode string, amount decimal.Decimal) uuid.UUID {
	walletID := h.wallets.seedUser(userID)
	acc, _ := h.wallets.GetOrCreateAccount(context.Background(), walletID, assetCode)
	h.wallets.setBalance(walletID, assetCode, amount)
	h.ledger.balances[acc.ID] = amount
	return walletID
}

// ---- tests ----

func TestExecute_NormalTransfer_DebitsSenderCreditsReceiver(t *testing.T) {
	h := newHarness()
	userA, userB := uuid.New(), uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))
	h.wallets.seedUser(userB)

	result, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientUserID: userB, AssetCode: "USDT",
		Amount: decimal.NewFromInt(30), IdempotencyKey: "key1",
	})
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if result.Status != domain.StatusCompleted {
		t.Errorf("status = %s, esperaba completed", result.Status)
	}

	walletA, _ := h.wallets.FindWalletByUserID(context.Background(), userA)
	walletB, _ := h.wallets.FindWalletByUserID(context.Background(), userB)
	accA, _ := h.wallets.GetOrCreateAccount(context.Background(), walletA.ID, "USDT")
	accB, _ := h.wallets.GetOrCreateAccount(context.Background(), walletB.ID, "USDT")

	balA, _ := h.ledger.GetBalance(context.Background(), accA.ID)
	balB, _ := h.ledger.GetBalance(context.Background(), accB.ID)
	if !balA.Equal(decimal.NewFromInt(70)) {
		t.Errorf("balance A = %s, esperaba 70", balA)
	}
	if !balB.Equal(decimal.NewFromInt(30)) {
		t.Errorf("balance B = %s, esperaba 30", balB)
	}
}

func TestExecute_InsufficientFunds_RejectsAndLeavesBalancesUnchanged(t *testing.T) {
	h := newHarness()
	userA, userB := uuid.New(), uuid.New()
	walletA := h.fundUser(userA, "USDT", decimal.NewFromInt(50))
	h.wallets.seedUser(userB)

	_, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientUserID: userB, AssetCode: "USDT",
		Amount: decimal.NewFromInt(100), IdempotencyKey: "key2",
	})
	if err != domain.ErrInsufficientFunds {
		t.Fatalf("esperaba ErrInsufficientFunds, obtuve: %v", err)
	}

	accA, _ := h.wallets.GetOrCreateAccount(context.Background(), walletA, "USDT")
	balA, _ := h.ledger.GetBalance(context.Background(), accA.ID)
	if !balA.Equal(decimal.NewFromInt(50)) {
		t.Errorf("balance A cambió a %s pese al rechazo — no debía tocarse", balA)
	}
}

func TestExecute_SelfTransfer_Rejected(t *testing.T) {
	h := newHarness()
	userA := uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))

	_, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientUserID: userA, AssetCode: "USDT",
		Amount: decimal.NewFromInt(10), IdempotencyKey: "key3",
	})
	if err != domain.ErrSelfTransfer {
		t.Fatalf("esperaba ErrSelfTransfer, obtuve: %v", err)
	}
	if h.ledger.postCall != 0 {
		t.Errorf("el Ledger no debía ser tocado, ExecuteTransaction se llamó %d veces", h.ledger.postCall)
	}
}

func TestExecute_RecipientDoesNotExist_RejectedWithoutTouchingLedger(t *testing.T) {
	h := newHarness()
	userA := uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))

	_, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientUserID: uuid.New(), AssetCode: "USDT",
		Amount: decimal.NewFromInt(10), IdempotencyKey: "key4",
	})
	if err != domain.ErrUserNotFound {
		t.Fatalf("esperaba ErrUserNotFound, obtuve: %v", err)
	}
	if h.ledger.postCall != 0 {
		t.Errorf("el Ledger no debía ser tocado para un destinatario inexistente")
	}
	if h.transfer.creates != 0 {
		t.Errorf("no debía crearse ningún registro de transferencia, se crearon %d", h.transfer.creates)
	}
}

func TestExecute_RecipientByUnverifiedEmail_TreatedAsNotFound(t *testing.T) {
	h := newHarness()
	userA := uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))
	h.users.byEmail["unverified@example.com"] = &domain.User{ID: uuid.New(), Email: "unverified@example.com", EmailVerified: false}

	_, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientEmail: "unverified@example.com", AssetCode: "USDT",
		Amount: decimal.NewFromInt(10), IdempotencyKey: "key5",
	})
	if err != domain.ErrUserNotFound {
		t.Fatalf("esperaba ErrUserNotFound para un email no verificado, obtuve: %v", err)
	}
}

func TestExecute_RecipientByVerifiedEmail_Succeeds(t *testing.T) {
	h := newHarness()
	userA := uuid.New()
	userB := uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))
	h.wallets.seedUser(userB)
	h.users.byEmail["b@example.com"] = &domain.User{ID: userB, Email: "b@example.com", EmailVerified: true}

	result, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientEmail: "B@Example.com", AssetCode: "USDT", // case-insensitive
		Amount: decimal.NewFromInt(10), IdempotencyKey: "key6",
	})
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if result.ReceiverUserID != userB {
		t.Errorf("receiver = %s, esperaba %s", result.ReceiverUserID, userB)
	}
}

func TestExecute_UnregisteredAsset_Rejected(t *testing.T) {
	h := newHarness()
	userA, userB := uuid.New(), uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))
	h.wallets.seedUser(userB)

	_, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientUserID: userB, AssetCode: "NOTREAL",
		Amount: decimal.NewFromInt(10), IdempotencyKey: "key7",
	})
	if err != domain.ErrAssetDisabled {
		t.Fatalf("esperaba ErrAssetDisabled para un asset no registrado, obtuve: %v", err)
	}
}

func TestExecute_DisabledAsset_Rejected(t *testing.T) {
	h := newHarness()
	h.assets.registered["DEAD"] = LedgerAsset{Code: "DEAD", Decimals: 2, IsEnabled: false}
	userA, userB := uuid.New(), uuid.New()
	h.fundUser(userA, "DEAD", decimal.NewFromInt(100))
	h.wallets.seedUser(userB)

	_, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientUserID: userB, AssetCode: "DEAD",
		Amount: decimal.NewFromInt(10), IdempotencyKey: "key8",
	})
	if err != domain.ErrAssetDisabled {
		t.Fatalf("esperaba ErrAssetDisabled para un asset deshabilitado, obtuve: %v", err)
	}
}

func TestExecute_ExcessDecimalsForAsset_Rejected(t *testing.T) {
	h := newHarness()
	userA, userB := uuid.New(), uuid.New()
	h.fundUser(userA, "USD", decimal.NewFromInt(100)) // USD registered with 2 decimals
	h.wallets.seedUser(userB)

	amount, _ := decimal.NewFromString("10.123") // 3 decimals > USD's 2
	_, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientUserID: userB, AssetCode: "USD",
		Amount: amount, IdempotencyKey: "key9",
	})
	if err == nil {
		t.Fatal("esperaba error por exceso de decimales para USD (2 decimales)")
	}
}

func TestExecute_ZeroOrNegativeAmount_Rejected(t *testing.T) {
	h := newHarness()
	userA, userB := uuid.New(), uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))
	h.wallets.seedUser(userB)

	for _, amt := range []decimal.Decimal{decimal.Zero, decimal.NewFromInt(-5)} {
		_, err := h.svc.Execute(context.Background(), CreateTransferInput{
			SenderUserID: userA, RecipientUserID: userB, AssetCode: "USDT",
			Amount: amt, IdempotencyKey: "key-" + amt.String(),
		})
		if err != domain.ErrInvalidAmount {
			t.Errorf("amount=%s: esperaba ErrInvalidAmount, obtuve: %v", amt, err)
		}
	}
}

func TestExecute_Idempotency_SameKeyTwice_OnlyOneCredit(t *testing.T) {
	h := newHarness()
	userA, userB := uuid.New(), uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))
	h.wallets.seedUser(userB)

	in := CreateTransferInput{SenderUserID: userA, RecipientUserID: userB, AssetCode: "USDT", Amount: decimal.NewFromInt(40), IdempotencyKey: "same-key"}

	first, err := h.svc.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("primera llamada: %v", err)
	}
	second, err := h.svc.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("segunda llamada (misma idempotency_key): %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("ids distintos entre llamadas: %s vs %s — debía ser la misma transferencia", first.ID, second.ID)
	}
	if h.ledger.postCall != 1 {
		// Once a transfer reaches 'completed', reconcileExisting returns
		// immediately without touching the ledger again at all — the
		// strongest possible idempotency guarantee (a fully-completed
		// retry doesn't even re-query the ledger, let alone post again).
		t.Errorf("ExecuteTransaction llamadas = %d, esperaba 1 (una transferencia ya completada no debe volver a tocar el ledger)", h.ledger.postCall)
	}
	if len(h.ledger.posted) != 1 {
		t.Errorf("transacciones de ledger únicas = %d, esperaba 1", len(h.ledger.posted))
	}

	walletA, _ := h.wallets.FindWalletByUserID(context.Background(), userA)
	accA, _ := h.wallets.GetOrCreateAccount(context.Background(), walletA.ID, "USDT")
	balA, _ := h.ledger.GetBalance(context.Background(), accA.ID)
	if !balA.Equal(decimal.NewFromInt(60)) {
		t.Errorf("balance de A tras dos solicitudes idénticas = %s, esperaba 60 (un solo débito de 40)", balA)
	}
}

func TestExecute_ConcurrentTransfers_NoDoubleSpend(t *testing.T) {
	h := newHarness()
	userA, userB, userC := uuid.New(), uuid.New(), uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))
	h.wallets.seedUser(userB)
	h.wallets.seedUser(userC)

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, results[0] = h.svc.Execute(context.Background(), CreateTransferInput{
			SenderUserID: userA, RecipientUserID: userB, AssetCode: "USDT", Amount: decimal.NewFromInt(100), IdempotencyKey: "race-a-b",
		})
	}()
	go func() {
		defer wg.Done()
		_, results[1] = h.svc.Execute(context.Background(), CreateTransferInput{
			SenderUserID: userA, RecipientUserID: userC, AssetCode: "USDT", Amount: decimal.NewFromInt(100), IdempotencyKey: "race-a-c",
		})
	}()
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else if err != domain.ErrInsufficientFunds {
			t.Errorf("error inesperado: %v", err)
		}
	}
	if successes != 1 {
		t.Errorf("transferencias exitosas = %d, esperaba exactamente 1 (A solo tenía fondos para una de las dos)", successes)
	}

	walletA, _ := h.wallets.FindWalletByUserID(context.Background(), userA)
	accA, _ := h.wallets.GetOrCreateAccount(context.Background(), walletA.ID, "USDT")
	balA, _ := h.ledger.GetBalance(context.Background(), accA.ID)
	if balA.IsNegative() {
		t.Errorf("balance de A quedó negativo: %s", balA)
	}
	if !balA.Equal(decimal.Zero) {
		t.Errorf("balance de A = %s, esperaba 0 (se gastaron los 100 exactamente una vez)", balA)
	}
}

func TestGetForUser_OwnershipEnforced(t *testing.T) {
	h := newHarness()
	userA, userB, attacker := uuid.New(), uuid.New(), uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))
	h.wallets.seedUser(userB)

	result, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientUserID: userB, AssetCode: "USDT", Amount: decimal.NewFromInt(10), IdempotencyKey: "keyown",
	})
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}

	if _, err := h.svc.GetForUser(context.Background(), result.ID, attacker); err != ErrTransferNotFound {
		t.Fatalf("esperaba ErrTransferNotFound para un usuario ajeno, obtuve: %v", err)
	}
	if view, err := h.svc.GetForUser(context.Background(), result.ID, userA); err != nil || view.Direction != DirectionSent {
		t.Errorf("userA debía ver la transferencia como 'sent'; err=%v view=%+v", err, view)
	}
	if view, err := h.svc.GetForUser(context.Background(), result.ID, userB); err != nil || view.Direction != DirectionReceived {
		t.Errorf("userB debía ver la transferencia como 'received'; err=%v view=%+v", err, view)
	}
}

func TestExecute_LedgerFailureMidFlight_MarksFailedNoPartialState(t *testing.T) {
	h := newHarness()
	userA, userB := uuid.New(), uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))
	h.wallets.seedUser(userB)

	// Force the ledger to reject this specific transaction type to
	// simulate a mid-flight failure without needing real DB fault
	// injection.
	h.assets.registered["USDT"] = LedgerAsset{Code: "USDT", Decimals: 6, IsEnabled: true}
	badLedger := &failingLedger{fakeLedger: h.ledger}
	h.svc = NewService(h.wallets, badLedger, h.transfer, h.assets, h.users, nil, nil)

	_, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientUserID: userB, AssetCode: "USDT", Amount: decimal.NewFromInt(10), IdempotencyKey: "keyfail",
	})
	if err == nil {
		t.Fatal("esperaba un error simulado del ledger")
	}

	stored, _ := h.transfer.FindByIdempotencyKey(context.Background(), "keyfail")
	if stored == nil {
		t.Fatal("el registro de transferencia debía existir (creado antes del intento de posteo)")
	}
	if stored.Status != domain.StatusFailed {
		t.Errorf("status = %s, esperaba failed tras el error simulado del ledger", stored.Status)
	}
	if stored.LedgerTransactionID != nil {
		t.Error("no debía quedar un ledger_transaction_id asociado a una transferencia fallida")
	}
}

type failingLedger struct{ *fakeLedger }

func (f *failingLedger) ExecuteTransaction(ctx context.Context, txType domain.TransactionType, idempotencyKey string,
	referenceID *string, description string, createdBy *uuid.UUID, entries []domain.LedgerEntryInput) (*domain.LedgerTransaction, error) {
	return nil, fmt.Errorf("fallo simulado de infraestructura")
}

// AUDIT: an Idempotency-Key must be bound to its sender and payload.
func TestExecute_IdempotencyKey_OtherUserOrOtherPayload_Rejected(t *testing.T) {
	h := newHarness()
	userA, userB, userC := uuid.New(), uuid.New(), uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))
	h.fundUser(userC, "USDT", decimal.NewFromInt(100))
	h.wallets.seedUser(userB)

	in := CreateTransferInput{SenderUserID: userA, RecipientUserID: userB, AssetCode: "USDT", Amount: decimal.NewFromInt(40), IdempotencyKey: "bound-key"}
	if _, err := h.svc.Execute(context.Background(), in); err != nil {
		t.Fatalf("primera llamada: %v", err)
	}

	// Another user reusing A's key must not receive A's transfer.
	other := CreateTransferInput{SenderUserID: userC, RecipientUserID: userB, AssetCode: "USDT", Amount: decimal.NewFromInt(40), IdempotencyKey: "bound-key"}
	if got, err := h.svc.Execute(context.Background(), other); err == nil || got != nil {
		t.Fatalf("otro usuario con la misma key debía ser rechazado, obtuve %v / %v", got, err)
	}

	// Same sender, different amount: conflict, not a silent replay.
	diff := in
	diff.Amount = decimal.NewFromInt(90)
	if got, err := h.svc.Execute(context.Background(), diff); err == nil || got != nil {
		t.Fatalf("misma key con otro monto debía ser rechazada, obtuve %v / %v", got, err)
	}

	if h.ledger.postCall != 1 {
		t.Errorf("ExecuteTransaction llamadas = %d, esperaba 1", h.ledger.postCall)
	}
}

// ---- Fase A: namespacing de idempotency keys en el ledger ----

// Ataque de colisión: un usuario manda como Idempotency-Key la clave interna
// de un depósito on-chain. La transferencia NUNCA debe postearse en el ledger
// con esa clave cruda.
func TestExecute_LedgerKey_IsNamespaced_CannotCollideWithInternalDepositKey(t *testing.T) {
	h := newHarness()
	userA, userB := uuid.New(), uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))
	h.wallets.seedUser(userB)

	depositKey := "blockchain-deposit:BSC:USDT:0x55d398326f99059fF775485246999027B3197955:0xdeadbeef:1"
	if _, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientUserID: userB, AssetCode: "USDT",
		Amount: decimal.NewFromInt(1), IdempotencyKey: depositKey,
	}); err != nil {
		t.Fatalf("transferencia: %v", err)
	}

	if _, raw := h.ledger.posted[depositKey]; raw {
		t.Fatal("la transferencia se posteó en el ledger con la clave cruda del usuario (colisión posible)")
	}
	want := domain.NamespacedIdempotencyKey(domain.IdemPrefixTransfer, userA, depositKey)
	if _, ok := h.ledger.posted[want]; !ok {
		t.Fatalf("esperaba la clave namespaceada %q en el ledger", want)
	}
}

// Una transferencia pendiente cuyo ledger ya se posteó con la clave CRUDA
// (versión anterior al cambio) debe completarse sin postear de nuevo.
func TestExecute_LegacyRawLedgerKey_IsNotReposted(t *testing.T) {
	h := newHarness()
	userA, userB := uuid.New(), uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))
	h.wallets.seedUser(userB)

	rawKey := "legacy-key-0001"
	pending := &domain.Transfer{
		ID: uuid.New(), SenderUserID: userA, ReceiverUserID: userB, AssetCode: "USDT",
		Amount: decimal.NewFromInt(5), Status: domain.StatusPending, IdempotencyKey: rawKey,
	}
	if err := h.transfer.Create(context.Background(), pending); err != nil {
		t.Fatalf("seed transfer: %v", err)
	}
	ref := pending.ID.String()
	h.ledger.posted[rawKey] = &domain.LedgerTransaction{
		ID: uuid.New(), Type: domain.TxTypeTransfer, IdempotencyKey: rawKey, ReferenceID: &ref,
	}

	res, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientUserID: userB, AssetCode: "USDT",
		Amount: decimal.NewFromInt(5), IdempotencyKey: rawKey,
	})
	if err != nil {
		t.Fatalf("reintento: %v", err)
	}
	if res.Status != domain.StatusCompleted {
		t.Fatalf("estado = %s, esperaba completed", res.Status)
	}
	if h.ledger.postCall != 0 {
		t.Fatalf("ExecuteTransaction llamado %d veces: se habría pagado dos veces", h.ledger.postCall)
	}
}

// Una tx legacy con la misma clave cruda pero de OTRA operación (otro
// reference_id) NO debe darse por buena.
func TestExecute_LegacyRawLedgerKey_ForeignTx_IsIgnored(t *testing.T) {
	h := newHarness()
	userA, userB := uuid.New(), uuid.New()
	h.fundUser(userA, "USDT", decimal.NewFromInt(100))
	h.wallets.seedUser(userB)

	rawKey := "legacy-key-0002"
	pending := &domain.Transfer{
		ID: uuid.New(), SenderUserID: userA, ReceiverUserID: userB, AssetCode: "USDT",
		Amount: decimal.NewFromInt(5), Status: domain.StatusPending, IdempotencyKey: rawKey,
	}
	if err := h.transfer.Create(context.Background(), pending); err != nil {
		t.Fatalf("seed transfer: %v", err)
	}
	other := uuid.NewString()
	h.ledger.posted[rawKey] = &domain.LedgerTransaction{
		ID: uuid.New(), Type: domain.TxTypeDeposit, IdempotencyKey: rawKey, ReferenceID: &other,
	}

	if _, err := h.svc.Execute(context.Background(), CreateTransferInput{
		SenderUserID: userA, RecipientUserID: userB, AssetCode: "USDT",
		Amount: decimal.NewFromInt(5), IdempotencyKey: rawKey,
	}); err != nil {
		t.Fatalf("reintento: %v", err)
	}
	if h.ledger.postCall != 1 {
		t.Fatalf("ExecuteTransaction llamado %d veces, esperaba 1 (la tx ajena no es de esta transferencia)", h.ledger.postCall)
	}
}
