package treasury

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal {
	v, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return v
}

func validInput() CreatePolicyInput {
	return CreatePolicyInput{
		Network:                 "bsc",
		AssetCode:               "USDT",
		HotCustodyWalletID:      uuid.New(),
		TreasuryCustodyWalletID: uuid.New(),
		MinBalance:              d("100"),
		TargetBalance:           d("500"),
		MaxBalance:              d("1000"),
		RebalanceThreshold:      d("50"),
		MaxRebalanceAmount:      d("2000"),
	}
}

func TestValidatePolicyFields_Valid(t *testing.T) {
	if err := ValidatePolicyFields(validInput()); err != nil {
		t.Fatalf("expected valid input to pass, got: %v", err)
	}
}

func TestValidatePolicyFields_MinGreaterThanTarget(t *testing.T) {
	in := validInput()
	in.MinBalance = d("600")
	if err := ValidatePolicyFields(in); !errors.Is(err, ErrMinExceedsTarget) {
		t.Fatalf("expected ErrMinExceedsTarget, got: %v", err)
	}
}

func TestValidatePolicyFields_TargetGreaterThanMax(t *testing.T) {
	in := validInput()
	in.TargetBalance = d("2000")
	if err := ValidatePolicyFields(in); !errors.Is(err, ErrTargetExceedsMax) {
		t.Fatalf("expected ErrTargetExceedsMax, got: %v", err)
	}
}

func TestValidatePolicyFields_ThresholdNotPositive(t *testing.T) {
	in := validInput()
	in.RebalanceThreshold = d("0")
	if err := ValidatePolicyFields(in); !errors.Is(err, ErrThresholdNotPositive) {
		t.Fatalf("expected ErrThresholdNotPositive, got: %v", err)
	}
	in.RebalanceThreshold = d("-1")
	if err := ValidatePolicyFields(in); !errors.Is(err, ErrThresholdNotPositive) {
		t.Fatalf("expected ErrThresholdNotPositive for negative, got: %v", err)
	}
}

func TestValidatePolicyFields_MaxAmountNotPositive(t *testing.T) {
	in := validInput()
	in.MaxRebalanceAmount = d("0")
	if err := ValidatePolicyFields(in); !errors.Is(err, ErrMaxAmountNotPositive) {
		t.Fatalf("expected ErrMaxAmountNotPositive, got: %v", err)
	}
}

func TestValidatePolicyFields_HotEqualsTreasury(t *testing.T) {
	in := validInput()
	in.TreasuryCustodyWalletID = in.HotCustodyWalletID
	if err := ValidatePolicyFields(in); !errors.Is(err, ErrHotEqualsTreasury) {
		t.Fatalf("expected ErrHotEqualsTreasury, got: %v", err)
	}
}

func TestValidatePolicyFields_NegativeAmounts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(*CreatePolicyInput)
	}{
		{"min", func(in *CreatePolicyInput) { in.MinBalance = d("-1"); in.TargetBalance = d("-1") }},
	} {
		in := validInput()
		tc.break_(&in)
		if err := ValidatePolicyFields(in); !errors.Is(err, ErrNegativeAmount) {
			t.Fatalf("%s: expected ErrNegativeAmount, got: %v", tc.name, err)
		}
	}
}

func TestValidatePolicyFields_InvalidNetworkOrAsset(t *testing.T) {
	in := validInput()
	in.Network = ""
	if err := ValidatePolicyFields(in); !errors.Is(err, ErrInvalidNetwork) {
		t.Fatalf("expected ErrInvalidNetwork, got: %v", err)
	}

	in = validInput()
	in.AssetCode = "  "
	if err := ValidatePolicyFields(in); !errors.Is(err, ErrInvalidAsset) {
		t.Fatalf("expected ErrInvalidAsset, got: %v", err)
	}
}

// TestValidatePolicyFields_BoundaryEqualities exercises the "<=" edges
// explicitly: min == target and target == max must both PASS (spec says
// <=, not <).
func TestValidatePolicyFields_BoundaryEqualities(t *testing.T) {
	in := validInput()
	in.MinBalance = in.TargetBalance
	if err := ValidatePolicyFields(in); err != nil {
		t.Fatalf("min == target should be valid, got: %v", err)
	}

	in = validInput()
	in.TargetBalance = in.MaxBalance
	if err := ValidatePolicyFields(in); err != nil {
		t.Fatalf("target == max should be valid, got: %v", err)
	}
}
