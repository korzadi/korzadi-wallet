package treasury

import (
	"errors"
	"testing"
	"time"
)

func TestClassifyBroadcastError(t *testing.T) {
	cases := []struct {
		msg  string
		want broadcastClass
	}{
		{"already known", broadcastAlreadyKnown},
		{"replacement transaction underpriced", broadcastAmbiguous},
		{"transaction underpriced", broadcastReprice},
		{"insufficient funds for gas * price + value", broadcastReprice},
		{"intrinsic gas too low", broadcastRejected},
		{"invalid chain id for signer", broadcastRejected},
		{"connection refused", broadcastAmbiguous},
	}
	for _, tc := range cases {
		got := classifyBroadcastError(errors.New(tc.msg))
		if got != tc.want {
			t.Errorf("classifyBroadcastError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func TestClassifyBroadcastError_Nil(t *testing.T) {
	if classifyBroadcastError(nil) != broadcastAmbiguous {
		t.Fatal("nil error should classify as ambiguous (never treated as a definite failure)")
	}
}

func TestIsRevertError(t *testing.T) {
	if !isRevertError(errors.New("execution reverted: insufficient balance")) {
		t.Fatal("expected revert to be detected")
	}
	if isRevertError(errors.New("connection timeout")) {
		t.Fatal("did not expect a timeout to classify as revert")
	}
}

func TestBackoff_Monotonic(t *testing.T) {
	prev := time.Duration(0)
	for attempt := 1; attempt <= 10; attempt++ {
		d := Backoff(attempt)
		if d < prev {
			t.Fatalf("backoff decreased at attempt %d: %s < %s", attempt, d, prev)
		}
		if d > maxBackoff {
			t.Fatalf("backoff exceeded cap at attempt %d: %s", attempt, d)
		}
		prev = d
	}
}

func TestBackoff_ClampsLowAttempts(t *testing.T) {
	if Backoff(0) != Backoff(1) {
		t.Fatal("attempts < 1 should clamp to the same value as attempt 1")
	}
}
