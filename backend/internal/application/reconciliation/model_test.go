package reconciliation

import "testing"

func TestFundCategoryForWalletType(t *testing.T) {
	cases := map[string]FundCategory{
		"HOT":      FundCategoryOperational,
		"TREASURY": FundCategoryTreasury,
		"GAS":      FundCategoryGas,
		"WEIRD":    FundCategory("WEIRD"),
	}
	for walletType, want := range cases {
		if got := FundCategoryForWalletType(walletType); got != want {
			t.Errorf("FundCategoryForWalletType(%q) = %q, want %q", walletType, got, want)
		}
	}
}

func TestNextStatus_FreshMismatchThenEscalation(t *testing.T) {
	svc := &Service{}

	// No previous check, currently mismatched -> MISMATCH (first detection).
	got := svc.nextStatus(nil, false)
	if got != StatusMismatch {
		t.Fatalf("first mismatch: got %s, want %s", got, StatusMismatch)
	}

	// Previous was MISMATCH, still mismatched on this pass -> escalate to PENDING_REVIEW.
	prevMismatch := &Check{Status: StatusMismatch}
	got = svc.nextStatus(prevMismatch, false)
	if got != StatusPendingReview {
		t.Fatalf("persisting mismatch: got %s, want %s", got, StatusPendingReview)
	}

	// Previous was PENDING_REVIEW, still mismatched -> stays PENDING_REVIEW.
	prevReview := &Check{Status: StatusPendingReview}
	got = svc.nextStatus(prevReview, false)
	if got != StatusPendingReview {
		t.Fatalf("persisting review: got %s, want %s", got, StatusPendingReview)
	}
}

func TestNextStatus_MatchAlwaysWins(t *testing.T) {
	svc := &Service{}
	for _, prev := range []*Check{nil, {Status: StatusMismatch}, {Status: StatusPendingReview}, {Status: StatusMatched}} {
		if got := svc.nextStatus(prev, true); got != StatusMatched {
			t.Errorf("a real match should always yield MATCHED regardless of history, got %s", got)
		}
	}
}
