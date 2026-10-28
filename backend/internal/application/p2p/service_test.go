package p2p

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	p2pd "korzadi-wallet/backend/internal/domain/p2p"
)

func TestBuildReleaseEntries_BalancedWithCommission(t *testing.T) {
	seller, buyer, clearing := uuid.New(), uuid.New(), uuid.New()
	o := &p2pd.Order{
		AssetCode:        "USDT",
		Amount:           decimal.NewFromInt(10),
		CommissionAmount: decimal.RequireFromString("0.1"),
	}

	entries, buyerCredit, err := buildReleaseEntries(o, seller, buyer, clearing)
	if err != nil {
		t.Fatalf("buildReleaseEntries: %v", err)
	}
	if !buyerCredit.Equal(decimal.RequireFromString("9.9")) {
		t.Fatalf("buyer credit = %s, expected 9.9", buyerCredit.String())
	}

	sum := decimal.Zero
	for _, e := range entries {
		sum = sum.Add(e.Amount)
	}
	if !sum.IsZero() {
		t.Fatalf("entries sum = %s, expected 0", sum.String())
	}
}

func TestBuildReleaseEntries_ZeroCommissionCreditsFullAmount(t *testing.T) {
	seller, buyer := uuid.New(), uuid.New()
	o := &p2pd.Order{
		AssetCode: "USDT",
		Amount:    decimal.NewFromInt(10),
	}

	entries, buyerCredit, err := buildReleaseEntries(o, seller, buyer, uuid.Nil)
	if err != nil {
		t.Fatalf("buildReleaseEntries: %v", err)
	}
	if !buyerCredit.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("buyer credit = %s, expected 10", buyerCredit.String())
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, expected 2", len(entries))
	}
}

func TestBuildReleaseEntries_RequiresClearingAccountForCommission(t *testing.T) {
	seller, buyer := uuid.New(), uuid.New()
	o := &p2pd.Order{
		AssetCode:        "USDT",
		Amount:           decimal.NewFromInt(10),
		CommissionAmount: decimal.RequireFromString("0.1"),
	}

	if _, _, err := buildReleaseEntries(o, seller, buyer, uuid.Nil); !errors.Is(err, ErrCommissionCalc) {
		t.Fatalf("err = %v, expected ErrCommissionCalc", err)
	}
}

func TestBuildReleaseEntries_RejectsCommissionAboveAmount(t *testing.T) {
	seller, buyer, clearing := uuid.New(), uuid.New(), uuid.New()
	o := &p2pd.Order{
		AssetCode:        "USDT",
		Amount:           decimal.NewFromInt(1),
		CommissionAmount: decimal.NewFromInt(2),
	}

	if _, _, err := buildReleaseEntries(o, seller, buyer, clearing); !errors.Is(err, ErrCommissionCalc) {
		t.Fatalf("err = %v, expected ErrCommissionCalc", err)
	}
}
