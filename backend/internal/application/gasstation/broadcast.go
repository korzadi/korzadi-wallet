package gasstation

import "strings"

type broadcastClass int

const (
	// broadcastAmbiguous: the node's response does not prove the
	// transaction was rejected — treat it as possibly accepted. NEVER
	// auto-retried.
	broadcastAmbiguous broadcastClass = iota
	// broadcastAlreadyKnown: the node already has this exact transaction
	// (e.g. a retry of an earlier ambiguous send) — proceed exactly as if
	// Broadcast had returned success.
	broadcastAlreadyKnown
	// broadcastDefinitiveRejection: the node proves the transaction was
	// NOT accepted (bad chain id, oversized data, gas above the block
	// limit, ...). Safe to mark the refill failed and release its nonce.
	broadcastDefinitiveRejection
)

// classifyBroadcastError mirrors blockchainwithdrawal.classifyBroadcastError's
// rules exactly (same substring checks), collapsing its 4-way split
// (ambiguous / alreadyKnown / reprice / rejected) into 3, since this
// package has no repricing/re-sign loop of its own: a "reprice" signal
// (underpriced / insufficient funds) is NOT proof of rejection by itself —
// it is folded into "ambiguous" here rather than "rejected", so it is never
// auto-marked failed.
func classifyBroadcastError(err error) broadcastClass {
	if err == nil {
		return broadcastAmbiguous
	}
	msg := strings.ToLower(err.Error())
	has := func(subs ...string) bool {
		for _, sub := range subs {
			if strings.Contains(msg, sub) {
				return true
			}
		}
		return false
	}
	switch {
	case has("already known", "known transaction", "same hash was already imported", "already in the mempool"):
		return broadcastAlreadyKnown
	case has("intrinsic gas too low", "exceeds block gas limit", "gas limit reached", "invalid sender", "invalid chain id", "oversized data"):
		return broadcastDefinitiveRejection
	default:
		return broadcastAmbiguous
	}
}
