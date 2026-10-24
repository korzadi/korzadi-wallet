package fee

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// fakeLedgerReservations mimics application/ledger.Service's Reserve
// closely enough for these tests: each call creates a genuinely new
// Reservation (Reserve itself has no built-in dedup key — that's exactly
// why ReservationService's quote-level claim exists), Release/Consume
// flip its status.
type fakeLedgerReservations struct {
	mu           sync.Mutex
	byID         map[uuid.UUID]*domain.Reservation
	reserveCalls int
}

func newFakeLedgerReservations() *fakeLedgerReservations {
	return &fakeLedgerReservations{byID: map[uuid.UUID]*domain.Reservation{}}
}

func (f *fakeLedgerReservations) Reserve(ctx context.Context, walletAccountID uuid.UUID, assetCode string, amount decimal.Decimal, reason string, referenceID *string) (*domain.Reservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reserveCalls++
	r := &domain.Reservation{ID: uuid.New(), WalletAccountID: walletAccountID, AssetCode: assetCode, Amount: amount, Status: domain.ReservationActive, Reason: reason, ReferenceID: referenceID}
	f.byID[r.ID] = r
	return r, nil
}

func (f *fakeLedgerReservations) Release(ctx context.Context, reservationID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.byID[reservationID]
	if !ok {
		return domain.ErrReservationNotFound
	}
	r.Status = domain.ReservationReleased
	return nil
}

func (f *fakeLedgerReservations) Consume(ctx context.Context, reservationID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.byID[reservationID]
	if !ok {
		return domain.ErrReservationNotFound
	}
	r.Status = domain.ReservationConsumed
	return nil
}

func (f *fakeLedgerReservations) FindReservation(ctx context.Context, reservationID uuid.UUID) (*domain.Reservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.byID[reservationID]
	if !ok {
		return nil, domain.ErrReservationNotFound
	}
	cp := *r
	return &cp, nil
}

func (f *fakeLedgerReservations) activeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.byID {
		if r.Status == domain.ReservationActive {
			n++
		}
	}
	return n
}

type fakeWalletResolver struct {
	wallets  map[uuid.UUID]*domain.Wallet
	accounts map[uuid.UUID]*domain.WalletAccount // walletID -> account (single asset, enough for these tests)
}

func newFakeWalletResolver() *fakeWalletResolver {
	return &fakeWalletResolver{wallets: map[uuid.UUID]*domain.Wallet{}, accounts: map[uuid.UUID]*domain.WalletAccount{}}
}

func (f *fakeWalletResolver) seed(userID uuid.UUID) uuid.UUID {
	w := &domain.Wallet{ID: uuid.New(), UserID: userID}
	f.wallets[userID] = w
	f.accounts[w.ID] = &domain.WalletAccount{ID: uuid.New(), WalletID: w.ID, AssetCode: "USDT"}
	return w.ID
}

func (f *fakeWalletResolver) FindWalletByUserID(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error) {
	return f.wallets[userID], nil
}

func (f *fakeWalletResolver) GetOrCreateAccount(ctx context.Context, walletID uuid.UUID, assetCode string) (*domain.WalletAccount, error) {
	return f.accounts[walletID], nil
}

func activeQuote(userID uuid.UUID) *FeeQuote {
	return &FeeQuote{
		ID: uuid.New(), UserID: userID, OperationType: OperationWithdrawal,
		Network: domain.NetworkETHEREUM, OperationAssetCode: "ETH", FeeAssetCode: "USDT",
		ServiceFee: decimal.NewFromInt(1), NetworkFee: decimal.NewFromFloat(0.5), TotalFee: decimal.NewFromFloat(1.5),
		Status: QuoteStatusActive, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(2 * time.Minute),
	}
}

// ---- tests ----

func TestReserveFee_CreatesReservationAndUpdatesQuote(t *testing.T) {
	ledger := newFakeLedgerReservations()
	wallets := newFakeWalletResolver()
	quotes := newFakeQuoteStore()
	svc := NewReservationService(ledger, wallets, quotes)

	userID := uuid.New()
	wallets.seed(userID)
	quote := activeQuote(userID)
	quotes.byID[quote.ID] = quote

	res, err := svc.ReserveFee(context.Background(), quote.ID)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if !res.Amount.Equal(quote.TotalFee) {
		t.Errorf("monto reservado = %s, esperaba TotalFee = %s", res.Amount, quote.TotalFee)
	}

	updated, _ := quotes.FindByID(context.Background(), quote.ID)
	if updated.Status != QuoteStatusReserved {
		t.Errorf("status de la cotización = %s, esperaba reserved", updated.Status)
	}
	if updated.ReservationID == nil || *updated.ReservationID != res.ID {
		t.Error("la cotización debía quedar asociada a la reserva creada")
	}
}

func TestReserveFee_Idempotent_SecondCallReturnsSameReservation(t *testing.T) {
	ledger := newFakeLedgerReservations()
	wallets := newFakeWalletResolver()
	quotes := newFakeQuoteStore()
	svc := NewReservationService(ledger, wallets, quotes)

	userID := uuid.New()
	wallets.seed(userID)
	quote := activeQuote(userID)
	quotes.byID[quote.ID] = quote

	first, err := svc.ReserveFee(context.Background(), quote.ID)
	if err != nil {
		t.Fatalf("primera llamada: %v", err)
	}
	second, err := svc.ReserveFee(context.Background(), quote.ID)
	if err != nil {
		t.Fatalf("segunda llamada: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("ids de reserva distintos entre llamadas: %s vs %s — debía ser la misma", first.ID, second.ID)
	}
	if ledger.reserveCalls != 1 {
		t.Errorf("Reserve debía llamarse una sola vez (la segunda es un replay detectado por el quote), se llamó %d veces", ledger.reserveCalls)
	}
}

func TestReserveFee_ConcurrentCalls_OnlyOneReservationSurvives(t *testing.T) {
	ledger := newFakeLedgerReservations()
	wallets := newFakeWalletResolver()
	quotes := newFakeQuoteStore()
	svc := NewReservationService(ledger, wallets, quotes)

	userID := uuid.New()
	wallets.seed(userID)
	quote := activeQuote(userID)
	quotes.byID[quote.ID] = quote

	const workers = 15
	var wg sync.WaitGroup
	reservationIDs := make([]uuid.UUID, workers)
	errs := make([]error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := svc.ReserveFee(context.Background(), quote.ID)
			errs[idx] = err
			if err == nil {
				reservationIDs[idx] = res.ID
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: no esperaba error: %v", i, err)
		}
	}
	first := reservationIDs[0]
	for i, id := range reservationIDs {
		if id != first {
			t.Errorf("worker %d obtuvo una reserva distinta (%s) a la del worker 0 (%s)", i, id, first)
		}
	}
	if ledger.activeCount() != 1 {
		t.Errorf("reservas activas = %d, esperaba exactamente 1 pese a %d llamadas concurrentes (spec §11, §22)", ledger.activeCount(), workers)
	}
}

func TestReserveFee_QuoteNotFound(t *testing.T) {
	svc := NewReservationService(newFakeLedgerReservations(), newFakeWalletResolver(), newFakeQuoteStore())
	if _, err := svc.ReserveFee(context.Background(), uuid.New()); err != ErrQuoteNotFound {
		t.Fatalf("esperaba ErrQuoteNotFound, obtuve: %v", err)
	}
}

func TestReserveFee_ExpiredQuote_Rejected(t *testing.T) {
	ledger := newFakeLedgerReservations()
	wallets := newFakeWalletResolver()
	quotes := newFakeQuoteStore()
	svc := NewReservationService(ledger, wallets, quotes)

	userID := uuid.New()
	wallets.seed(userID)
	quote := activeQuote(userID)
	quote.ExpiresAt = time.Now().Add(-1 * time.Minute)
	quotes.byID[quote.ID] = quote

	if _, err := svc.ReserveFee(context.Background(), quote.ID); err != ErrQuoteExpired {
		t.Fatalf("esperaba ErrQuoteExpired, obtuve: %v", err)
	}
	if ledger.reserveCalls != 0 {
		t.Error("no debía crearse ninguna reserva para una cotización expirada")
	}
}

func TestReleaseFee_ReturnsToAvailableAndCancelsQuote(t *testing.T) {
	ledger := newFakeLedgerReservations()
	wallets := newFakeWalletResolver()
	quotes := newFakeQuoteStore()
	svc := NewReservationService(ledger, wallets, quotes)

	userID := uuid.New()
	wallets.seed(userID)
	quote := activeQuote(userID)
	quotes.byID[quote.ID] = quote

	res, err := svc.ReserveFee(context.Background(), quote.ID)
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	if err := svc.ReleaseFee(context.Background(), quote.ID); err != nil {
		t.Fatalf("liberar: %v", err)
	}

	released, _ := ledger.FindReservation(context.Background(), res.ID)
	if released.Status != domain.ReservationReleased {
		t.Errorf("status de la reserva = %s, esperaba released", released.Status)
	}
	updated, _ := quotes.FindByID(context.Background(), quote.ID)
	if updated.Status != QuoteStatusCancelled {
		t.Errorf("status de la cotización = %s, esperaba cancelled", updated.Status)
	}
}

func TestConsumeFee_MarksConsumed(t *testing.T) {
	ledger := newFakeLedgerReservations()
	wallets := newFakeWalletResolver()
	quotes := newFakeQuoteStore()
	svc := NewReservationService(ledger, wallets, quotes)

	userID := uuid.New()
	wallets.seed(userID)
	quote := activeQuote(userID)
	quotes.byID[quote.ID] = quote

	res, err := svc.ReserveFee(context.Background(), quote.ID)
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	if err := svc.ConsumeFee(context.Background(), quote.ID); err != nil {
		t.Fatalf("consumir: %v", err)
	}

	consumed, _ := ledger.FindReservation(context.Background(), res.ID)
	if consumed.Status != domain.ReservationConsumed {
		t.Errorf("status de la reserva = %s, esperaba consumed", consumed.Status)
	}
	updated, _ := quotes.FindByID(context.Background(), quote.ID)
	if updated.Status != QuoteStatusConsumed {
		t.Errorf("status de la cotización = %s, esperaba consumed", updated.Status)
	}
}

func TestConsumeFee_WithoutReservation_Rejected(t *testing.T) {
	quotes := newFakeQuoteStore()
	userID := uuid.New()
	quote := activeQuote(userID)
	quotes.byID[quote.ID] = quote

	svc := NewReservationService(newFakeLedgerReservations(), newFakeWalletResolver(), quotes)
	if err := svc.ConsumeFee(context.Background(), quote.ID); err == nil {
		t.Fatal("esperaba error: no se puede consumir una reserva que nunca se creó")
	}
}
