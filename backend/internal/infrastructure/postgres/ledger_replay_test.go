package postgres

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

func TestNetEntries_OrderAndSplitInsensitive(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	d := decimal.NewFromInt

	x := []domain.LedgerEntryInput{
		{WalletAccountID: a, AssetCode: "USDT", Amount: d(-30)},
		{WalletAccountID: b, AssetCode: "USDT", Amount: d(30)},
	}
	// mismo resultado, otro orden y entry partido en dos
	y := []domain.LedgerEntryInput{
		{WalletAccountID: b, AssetCode: "USDT", Amount: d(10)},
		{WalletAccountID: b, AssetCode: "USDT", Amount: d(20)},
		{WalletAccountID: a, AssetCode: "USDT", Amount: d(-30)},
	}
	if !sameNetEntries(netEntries(x), netEntries(y)) {
		t.Fatal("entries equivalentes debían compararse como iguales")
	}
}

func TestNetEntries_DetectsDifferentPayloads(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	d := decimal.NewFromInt
	base := []domain.LedgerEntryInput{
		{WalletAccountID: a, AssetCode: "USDT", Amount: d(-30)},
		{WalletAccountID: b, AssetCode: "USDT", Amount: d(30)},
	}
	cases := map[string][]domain.LedgerEntryInput{
		"otro monto": {
			{WalletAccountID: a, AssetCode: "USDT", Amount: d(-31)},
			{WalletAccountID: b, AssetCode: "USDT", Amount: d(31)},
		},
		"otra cuenta destino": {
			{WalletAccountID: a, AssetCode: "USDT", Amount: d(-30)},
			{WalletAccountID: c, AssetCode: "USDT", Amount: d(30)},
		},
		"otro asset": {
			{WalletAccountID: a, AssetCode: "USDC", Amount: d(-30)},
			{WalletAccountID: b, AssetCode: "USDC", Amount: d(30)},
		},
		"sentido invertido": {
			{WalletAccountID: a, AssetCode: "USDT", Amount: d(30)},
			{WalletAccountID: b, AssetCode: "USDT", Amount: d(-30)},
		},
		"entry extra": {
			{WalletAccountID: a, AssetCode: "USDT", Amount: d(-30)},
			{WalletAccountID: b, AssetCode: "USDT", Amount: d(30)},
			{WalletAccountID: c, AssetCode: "USDT", Amount: d(5)},
			{WalletAccountID: a, AssetCode: "USDT", Amount: d(-5)},
		},
	}
	for name, other := range cases {
		if sameNetEntries(netEntries(base), netEntries(other)) {
			t.Errorf("%s: debía detectarse como payload distinto", name)
		}
	}
}

func TestNetEntries_DecimalScaleInsensitive(t *testing.T) {
	a := uuid.New()
	x := []domain.LedgerEntryInput{{WalletAccountID: a, AssetCode: "USDT", Amount: decimal.RequireFromString("1.50")}}
	y := []domain.LedgerEntryInput{{WalletAccountID: a, AssetCode: "USDT", Amount: decimal.RequireFromString("1.5000000000")}}
	if !sameNetEntries(netEntries(x), netEntries(y)) {
		t.Fatal("1.50 y 1.5000000000 deben ser iguales (NUMERIC(36,18) normaliza la escala)")
	}
}
