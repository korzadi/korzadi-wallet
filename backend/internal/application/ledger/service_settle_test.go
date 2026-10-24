package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// settlingRepo embebe fakeRepo (service_test.go) y añade SettleReservations.
type settlingRepo struct {
	*fakeRepo
	settleCalls int
	lastInput   domain.ReservationSettlementInput
	failWith    error
}

func (s *settlingRepo) SettleReservations(ctx context.Context, in domain.ReservationSettlementInput) (*domain.LedgerTransaction, error) {
	s.settleCalls++
	s.lastInput = in
	if s.failWith != nil {
		return nil, s.failWith
	}
	return &domain.LedgerTransaction{ID: uuid.New(), Type: in.TxType, IdempotencyKey: in.IdempotencyKey}, nil
}

func validSettlement() domain.ReservationSettlementInput {
	acct := uuid.New()
	sys := uuid.New()
	return domain.ReservationSettlementInput{
		ReservationIDs: []uuid.UUID{uuid.New()},
		TxType:         domain.TxTypeWithdrawal,
		IdempotencyKey: "wd:1",
		Description:    "test",
		Entries: []domain.LedgerEntryInput{
			{WalletAccountID: acct, AssetCode: "USDT", Amount: decimal.NewFromInt(-10)},
			{WalletAccountID: sys, AssetCode: "USDT", Amount: decimal.NewFromInt(10)},
		},
	}
}

func TestSettleReservations_DelegatesToSettler(t *testing.T) {
	repo := &settlingRepo{fakeRepo: newFakeRepo()}
	svc := NewService(repo, newFakeReservations())

	tx, err := svc.SettleReservations(context.Background(), validSettlement())
	if err != nil || tx == nil {
		t.Fatalf("esperaba éxito, obtuve tx=%v err=%v", tx, err)
	}
	if repo.settleCalls != 1 {
		t.Errorf("debe delegar exactamente una vez, se llamó %d", repo.settleCalls)
	}
}

func TestSettleReservations_FailsClosedWithoutSettler(t *testing.T) {
	// fakeRepo NO implementa ReservationSettler: nunca debe degradarse a
	// Consume + ExecuteTransaction en dos pasos.
	repo := newFakeRepo()
	reservations := newFakeReservations()
	svc := NewService(repo, reservations)

	_, err := svc.SettleReservations(context.Background(), validSettlement())
	if !errors.Is(err, ErrSettlementNotSupported) {
		t.Fatalf("esperaba ErrSettlementNotSupported, obtuve: %v", err)
	}
	if reservations.consumeCalls != 0 || repo.callCount != 0 {
		t.Fatalf("no debe tocar Consume (%d) ni ExecuteTransaction (%d)", reservations.consumeCalls, repo.callCount)
	}
}

func TestSettleReservations_RejectsInvalidInputBeforeRepository(t *testing.T) {
	cases := map[string]func(*domain.ReservationSettlementInput){
		"sin idempotency_key": func(in *domain.ReservationSettlementInput) { in.IdempotencyKey = "" },
		"sin reservas":        func(in *domain.ReservationSettlementInput) { in.ReservationIDs = nil },
		"sin entries":         func(in *domain.ReservationSettlementInput) { in.Entries = nil },
		"entry monto cero":    func(in *domain.ReservationSettlementInput) { in.Entries[0].Amount = decimal.Zero },
		"entry sin cuenta":    func(in *domain.ReservationSettlementInput) { in.Entries[0].WalletAccountID = uuid.Nil },
		"entry sin asset":     func(in *domain.ReservationSettlementInput) { in.Entries[0].AssetCode = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			repo := &settlingRepo{fakeRepo: newFakeRepo()}
			svc := NewService(repo, newFakeReservations())
			in := validSettlement()
			mutate(&in)
			if _, err := svc.SettleReservations(context.Background(), in); err == nil {
				t.Fatal("esperaba error de validación")
			}
			if repo.settleCalls != 0 {
				t.Fatal("una entrada inválida nunca debe llegar al repositorio")
			}
		})
	}
}

func TestSettleReservations_PropagatesRepositoryErrorUnwrapped(t *testing.T) {
	repo := &settlingRepo{fakeRepo: newFakeRepo(), failWith: domain.ErrReservationNotActive}
	svc := NewService(repo, newFakeReservations())
	_, err := svc.SettleReservations(context.Background(), validSettlement())
	if err != domain.ErrReservationNotActive {
		t.Fatalf("esperaba ErrReservationNotActive sin envolver, obtuve: %v", err)
	}
}
