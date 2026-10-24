package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ---- fakes ----

type fakeRepo struct {
	posted    map[string]*domain.LedgerTransaction
	failWith  error
	callCount int
}

type fakeAdjustmentRepo struct {
	*fakeRepo
	adjustmentCalls int
}

func (f *fakeAdjustmentRepo) ExecuteAdjustment(ctx context.Context, key, reason string, actor uuid.UUID, ip string, entries []domain.LedgerEntryInput) (*domain.LedgerTransaction, error) {
	f.adjustmentCalls++
	tx := &domain.LedgerTransaction{ID: uuid.New(), Type: domain.TxTypeAdjustment, IdempotencyKey: key, Description: reason, CreatedBy: &actor}
	return tx, nil
}

type fakeAdjustmentRoles []string

func (f fakeAdjustmentRoles) GetRolesForUser(context.Context, uuid.UUID) ([]string, error) {
	return f, nil
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{posted: map[string]*domain.LedgerTransaction{}}
}

func (f *fakeRepo) ExecuteTransaction(ctx context.Context, txType domain.TransactionType, idempotencyKey string,
	referenceID *string, description string, createdBy *uuid.UUID,
	entries []domain.LedgerEntryInput) (*domain.LedgerTransaction, error) {
	f.callCount++
	if f.failWith != nil {
		return nil, f.failWith
	}
	if tx, ok := f.posted[idempotencyKey]; ok {
		return tx, nil
	}
	tx := &domain.LedgerTransaction{ID: uuid.New(), Type: txType, IdempotencyKey: idempotencyKey, Status: domain.TransactionStatus("completed")}
	f.posted[idempotencyKey] = tx
	return tx, nil
}

func (f *fakeRepo) GetBalance(ctx context.Context, walletAccountID uuid.UUID) (decimal.Decimal, error) {
	return decimal.Zero, nil
}

func (f *fakeRepo) FindByIdempotencyKey(ctx context.Context, key string) (*domain.LedgerTransaction, error) {
	return f.posted[key], nil
}

func (f *fakeRepo) ListEntriesByAccount(ctx context.Context, walletAccountID uuid.UUID, limit, offset int) ([]*domain.LedgerEntry, error) {
	return nil, nil
}

type fakeReservations struct {
	reserveCalls int
	releaseCalls int
	consumeCalls int
	byID         map[uuid.UUID]*domain.Reservation
	failWith     error
}

func newFakeReservations() *fakeReservations {
	return &fakeReservations{byID: map[uuid.UUID]*domain.Reservation{}}
}

func (f *fakeReservations) Reserve(ctx context.Context, walletAccountID uuid.UUID, assetCode string, amount decimal.Decimal, reason string, referenceID *string) (*domain.Reservation, error) {
	f.reserveCalls++
	if f.failWith != nil {
		return nil, f.failWith
	}
	r := &domain.Reservation{ID: uuid.New(), WalletAccountID: walletAccountID, AssetCode: assetCode, Amount: amount, Status: domain.ReservationActive, Reason: reason, ReferenceID: referenceID}
	f.byID[r.ID] = r
	return r, nil
}

func (f *fakeReservations) Release(ctx context.Context, reservationID uuid.UUID) error {
	f.releaseCalls++
	r, ok := f.byID[reservationID]
	if !ok {
		return domain.ErrReservationNotFound
	}
	if r.Status != domain.ReservationActive {
		return domain.ErrReservationNotActive
	}
	r.Status = domain.ReservationReleased
	return nil
}

func (f *fakeReservations) Consume(ctx context.Context, reservationID uuid.UUID) error {
	f.consumeCalls++
	r, ok := f.byID[reservationID]
	if !ok {
		return domain.ErrReservationNotFound
	}
	if r.Status != domain.ReservationActive {
		return domain.ErrReservationNotActive
	}
	r.Status = domain.ReservationConsumed
	return nil
}

func (f *fakeReservations) FindByID(ctx context.Context, reservationID uuid.UUID) (*domain.Reservation, error) {
	r, ok := f.byID[reservationID]
	if !ok {
		return nil, domain.ErrReservationNotFound
	}
	return r, nil
}

func (f *fakeReservations) GetBalance(ctx context.Context, walletAccountID uuid.UUID) (domain.Balance, error) {
	return domain.Balance{WalletAccountID: walletAccountID}, nil
}

// ---- tests ----

func TestExecuteTransaction_RejectsEmptyIdempotencyKey(t *testing.T) {
	svc := NewService(newFakeRepo(), newFakeReservations())
	_, err := svc.ExecuteTransaction(context.Background(), domain.TxTypeDeposit, "", nil, "desc", nil,
		[]domain.LedgerEntryInput{{WalletAccountID: uuid.New(), AssetCode: "USD", Amount: decimal.NewFromInt(10)}})
	if err == nil {
		t.Fatal("esperaba error: idempotency_key vacío nunca debe llegar al repositorio")
	}
}

func TestExecuteTransaction_RejectsNoEntries(t *testing.T) {
	svc := NewService(newFakeRepo(), newFakeReservations())
	_, err := svc.ExecuteTransaction(context.Background(), domain.TxTypeDeposit, "key1", nil, "desc", nil, nil)
	if err == nil {
		t.Fatal("esperaba error: no se puede postear una transacción sin entries")
	}
}

func TestExecuteTransaction_RejectsZeroAmountEntry(t *testing.T) {
	svc := NewService(newFakeRepo(), newFakeReservations())
	_, err := svc.ExecuteTransaction(context.Background(), domain.TxTypeDeposit, "key1", nil, "desc", nil,
		[]domain.LedgerEntryInput{{WalletAccountID: uuid.New(), AssetCode: "USD", Amount: decimal.Zero}})
	if err == nil {
		t.Fatal("esperaba error: un entry con monto cero no aporta nada y probablemente es un bug del caller")
	}
}

func TestExecuteTransaction_RejectsMissingWalletAccountID(t *testing.T) {
	svc := NewService(newFakeRepo(), newFakeReservations())
	_, err := svc.ExecuteTransaction(context.Background(), domain.TxTypeDeposit, "key1", nil, "desc", nil,
		[]domain.LedgerEntryInput{{WalletAccountID: uuid.Nil, AssetCode: "USD", Amount: decimal.NewFromInt(10)}})
	if err == nil {
		t.Fatal("esperaba error: wallet_account_id nulo")
	}
}

func TestExecuteTransaction_RejectsAdjustment(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, newFakeReservations())
	_, err := svc.ExecuteTransaction(context.Background(), domain.TxTypeAdjustment, "adjustment:key", nil, "reason", nil,
		[]domain.LedgerEntryInput{{WalletAccountID: uuid.New(), AssetCode: "USD", Amount: decimal.NewFromInt(1)}})
	if !errors.Is(err, domain.ErrAdjustmentRequiresService) {
		t.Fatalf("expected service-only rejection, got %v", err)
	}
	if repo.callCount != 0 {
		t.Fatal("generic repository path must not be called")
	}
}

func TestExecuteAdjustment_RequiresRoleAndDelegates(t *testing.T) {
	actor := uuid.New()
	repo := &fakeAdjustmentRepo{fakeRepo: newFakeRepo()}
	entries := []domain.LedgerEntryInput{{WalletAccountID: uuid.New(), AssetCode: "USD", Amount: decimal.NewFromInt(2)}}
	denied := NewService(repo, newFakeReservations()).WithAdjustmentAuthorization(fakeAdjustmentRoles{"user"}, nil)
	if _, err := denied.ExecuteAdjustment(context.Background(), actor, "", "adjustment:denied", "reason", entries); !errors.Is(err, ErrAdjustmentForbidden) {
		t.Fatalf("expected forbidden, got %v", err)
	}
	if repo.adjustmentCalls != 0 {
		t.Fatal("unauthorized adjustment reached repository")
	}
	allowed := NewService(repo, newFakeReservations()).WithAdjustmentAuthorization(fakeAdjustmentRoles{"super_admin"}, nil)
	if _, err := allowed.ExecuteAdjustment(context.Background(), actor, "", "adjustment:allowed", "reason", entries); err != nil {
		t.Fatal(err)
	}
	if repo.adjustmentCalls != 1 {
		t.Fatalf("expected one privileged repository call, got %d", repo.adjustmentCalls)
	}
}

func TestExecuteTransaction_DelegatesToRepository(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, newFakeReservations())

	entries := []domain.LedgerEntryInput{
		{WalletAccountID: uuid.New(), AssetCode: "USD", Amount: decimal.NewFromInt(10)},
		{WalletAccountID: uuid.New(), AssetCode: "USD", Amount: decimal.NewFromInt(-10)},
	}
	tx, err := svc.ExecuteTransaction(context.Background(), domain.TxTypeTransfer, "key1", nil, "desc", nil, entries)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if tx == nil {
		t.Fatal("esperaba una transacción no nula")
	}
	if repo.callCount != 1 {
		t.Errorf("el repositorio debe llamarse exactamente 1 vez, se llamó %d", repo.callCount)
	}
}

func TestExecuteTransaction_PropagatesRepositoryError(t *testing.T) {
	repo := newFakeRepo()
	repo.failWith = domain.ErrInsufficientFunds
	svc := NewService(repo, newFakeReservations())

	_, err := svc.ExecuteTransaction(context.Background(), domain.TxTypeWithdrawal, "key1", nil, "desc", nil,
		[]domain.LedgerEntryInput{
			{WalletAccountID: uuid.New(), AssetCode: "USD", Amount: decimal.NewFromInt(-10)},
			{WalletAccountID: uuid.New(), AssetCode: "USD", Amount: decimal.NewFromInt(10)},
		})
	if err != domain.ErrInsufficientFunds {
		t.Fatalf("esperaba que el error del repositorio se propague sin envolver, obtuve: %v", err)
	}
}

func TestReserveReleaseConsume_DelegateAndLog(t *testing.T) {
	reservations := newFakeReservations()
	svc := NewService(newFakeRepo(), reservations)
	accountID := uuid.New()

	res, err := svc.Reserve(context.Background(), accountID, "USD", decimal.NewFromInt(40), "prueba", nil)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if reservations.reserveCalls != 1 {
		t.Errorf("Reserve debe delegar exactamente una vez al repositorio")
	}

	if err := svc.Release(context.Background(), res.ID); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if reservations.releaseCalls != 1 {
		t.Errorf("Release debe delegar exactamente una vez")
	}

	// Releasing again must surface the error, not swallow it.
	if err := svc.Release(context.Background(), res.ID); err != domain.ErrReservationNotActive {
		t.Errorf("esperaba ErrReservationNotActive al liberar dos veces, obtuve: %v", err)
	}
}

func TestReserve_RejectsNonPositiveAmount(t *testing.T) {
	svc := NewService(newFakeRepo(), newFakeReservations())
	_, err := svc.Reserve(context.Background(), uuid.New(), "USD", decimal.Zero, "prueba", nil)
	if err == nil {
		t.Fatal("esperaba error: una reserva de monto cero o negativo no tiene sentido")
	}
}

func TestConsume_OnUnknownReservation_ReturnsNotFound(t *testing.T) {
	svc := NewService(newFakeRepo(), newFakeReservations())
	err := svc.Consume(context.Background(), uuid.New())
	if err != domain.ErrReservationNotFound {
		t.Errorf("esperaba ErrReservationNotFound, obtuve: %v", err)
	}
}
