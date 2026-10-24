package recovery

import (
	"testing"
	"time"
)

func TestItem_FailureCode(t *testing.T) {
	cases := map[string]string{
		"nonce_stuck: la tx es desconocida": "nonce_stuck",
		"broadcast_ambiguous: timeout":      "broadcast_ambiguous",
		"sin_dos_puntos":                    "sin_dos_puntos",
		"":                                  "",
	}
	for detail, want := range cases {
		it := Item{ErrorDetail: detail}
		if got := it.FailureCode(); got != want {
			t.Errorf("FailureCode(%q) = %q, want %q", detail, got, want)
		}
	}
}

func TestIsLockExpired(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if !isLockExpired(Item{LockedUntil: nil}, now) {
		t.Error("a nil LockedUntil must be treated as expired (never locked)")
	}

	past := now.Add(-time.Minute)
	if !isLockExpired(Item{LockedUntil: &past}, now) {
		t.Error("a LockedUntil in the past must be expired")
	}

	future := now.Add(time.Minute)
	if isLockExpired(Item{LockedUntil: &future}, now) {
		t.Error("a LockedUntil in the future must NOT be expired")
	}
}
