package postgres

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

func d(n int64) decimal.Decimal { return decimal.NewFromInt(n) }

func TestCheckSettlementCoverage(t *testing.T) {
	user := uuid.New()
	sys := uuid.New()
	other := uuid.New()

	res := func(acct uuid.UUID, asset string, amt int64) settlementReservation {
		return settlementReservation{ID: uuid.New(), AccountID: acct, AssetCode: asset, Amount: d(amt)}
	}
	entry := func(acct uuid.UUID, asset string, amt int64) domain.LedgerEntryInput {
		return domain.LedgerEntryInput{WalletAccountID: acct, AssetCode: asset, Amount: d(amt)}
	}

	tests := []struct {
		name         string
		reservations []settlementReservation
		entries      []domain.LedgerEntryInput
		wantErr      bool
	}{
		{"débito exacto", []settlementReservation{res(user, "USDT", 10)},
			[]domain.LedgerEntryInput{entry(user, "USDT", -10), entry(sys, "USDT", 10)}, false},
		{"débito mayor que la reserva (lo cubre el CHECK de DB)", []settlementReservation{res(user, "USDT", 10)},
			[]domain.LedgerEntryInput{entry(user, "USDT", -12), entry(sys, "USDT", 12)}, false},
		{"débito menor: liberaría fondos sin cobrar", []settlementReservation{res(user, "USDT", 10)},
			[]domain.LedgerEntryInput{entry(user, "USDT", -9), entry(sys, "USDT", 9)}, true},
		{"sin débito a la cuenta reservada", []settlementReservation{res(user, "USDT", 10)},
			[]domain.LedgerEntryInput{entry(other, "USDT", -10), entry(sys, "USDT", 10)}, true},
		{"asset distinto al reservado", []settlementReservation{res(user, "ETH", 1)},
			[]domain.LedgerEntryInput{entry(user, "USDT", -1), entry(sys, "USDT", 1)}, true},
		{"crédito a la misma cuenta reduce el débito neto", []settlementReservation{res(user, "USDT", 10)},
			[]domain.LedgerEntryInput{entry(user, "USDT", -10), entry(user, "USDT", 5), entry(sys, "USDT", 5)}, true},
		{"dos reservas, mismo (cuenta, asset): se suman", []settlementReservation{res(user, "USDT", 6), res(user, "USDT", 4)},
			[]domain.LedgerEntryInput{entry(user, "USDT", -10), entry(sys, "USDT", 10)}, false},
		{"dos reservas, mismo (cuenta, asset): débito insuficiente", []settlementReservation{res(user, "USDT", 6), res(user, "USDT", 4)},
			[]domain.LedgerEntryInput{entry(user, "USDT", -9), entry(sys, "USDT", 9)}, true},
		{"retiro: monto ETH + fee USDT, ambos cubiertos",
			[]settlementReservation{res(user, "ETH", 1), res(user, "USDT", 2)},
			[]domain.LedgerEntryInput{
				entry(user, "ETH", -1), entry(sys, "ETH", 1),
				entry(user, "USDT", -2), entry(sys, "USDT", 2)}, false},
		{"retiro: fee sin cubrir", []settlementReservation{res(user, "ETH", 1), res(user, "USDT", 2)},
			[]domain.LedgerEntryInput{entry(user, "ETH", -1), entry(sys, "ETH", 1)}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSettlementCoverage(tc.reservations, tc.entries)
			if tc.wantErr {
				if !errors.Is(err, domain.ErrSettlementMismatch) {
					t.Fatalf("esperaba ErrSettlementMismatch, obtuve: %v", err)
				}
			} else if err != nil {
				t.Fatalf("no esperaba error: %v", err)
			}
		})
	}
}
