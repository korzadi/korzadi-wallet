package treasury

import "testing"

func TestCanTransition_AllowedPaths(t *testing.T) {
	allowed := [][2]Status{
		{StatusPending, StatusProcessing},
		{StatusPending, StatusFailed},
		{StatusProcessing, StatusBroadcasted},
		{StatusProcessing, StatusFailed},
		{StatusBroadcasted, StatusConfirming},
		{StatusBroadcasted, StatusFailed},
		{StatusConfirming, StatusCompleted},
		{StatusConfirming, StatusBroadcasted}, // reorg dropped the receipt
		{StatusConfirming, StatusFailed},
	}
	for _, pair := range allowed {
		if !CanTransition(pair[0], pair[1]) {
			t.Errorf("expected %s -> %s to be allowed", pair[0], pair[1])
		}
	}
}

func TestCanTransition_DisallowedPaths(t *testing.T) {
	disallowed := [][2]Status{
		{StatusPending, StatusCompleted},
		{StatusPending, StatusConfirming},
		{StatusCompleted, StatusPending},
		{StatusCompleted, StatusFailed},
		{StatusFailed, StatusPending},
		{StatusFailed, StatusCompleted},
		{StatusBroadcasted, StatusPending},
	}
	for _, pair := range disallowed {
		if CanTransition(pair[0], pair[1]) {
			t.Errorf("expected %s -> %s to be disallowed", pair[0], pair[1])
		}
	}
}

func TestStatus_IsTerminal(t *testing.T) {
	if !StatusCompleted.IsTerminal() || !StatusFailed.IsTerminal() {
		t.Fatal("completed/failed must be terminal")
	}
	for _, s := range []Status{StatusPending, StatusProcessing, StatusBroadcasted, StatusConfirming} {
		if s.IsTerminal() {
			t.Errorf("%s must NOT be terminal", s)
		}
	}
}

func TestRebalance_FailureCode(t *testing.T) {
	cases := map[string]string{
		"insufficient_treasury: treasury=10 requerido=20": "insufficient_treasury",
		"nonce_stuck: some cause":                         "nonce_stuck",
		"no_colon_here":                                   "no_colon_here",
		"":                                                "",
	}
	for detail, want := range cases {
		r := &Rebalance{ErrorDetail: detail}
		if got := r.FailureCode(); got != want {
			t.Errorf("FailureCode(%q) = %q, want %q", detail, got, want)
		}
	}
}
