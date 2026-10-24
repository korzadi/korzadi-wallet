// Package recovery implements Bloque 7.6: Recovery / Exceptions.
//
// This package is deliberately NOT a second transaction engine. Every
// subsystem it watches (Gas Station 7.2, Sweeps 7.3, Treasury/Rebalancing
// 7.4) already has its own complete, spec-compliant state machine: a
// crashed worker's lease simply expires (locked_until < now()) and the
// SAME subsystem's own ClaimDue query picks the row back up on the next
// tick — "worker reiniciado" / "proceso reiniciado" are already recovered
// by that mechanism alone, in every one of 7.2/7.3/7.4, with zero code in
// this package. Ambiguous broadcasts, reorgs, stuck nonces and lost
// receipts are already handled, per subsystem, by the exact discipline
// spec 7.6.1 asks for ("Antes de volver a transmitir: receipt,
// mempool/estado, nonce, evidencia") — see sweep.broadcastStep /
// treasury.broadcastStep, both written that way already.
//
// What is genuinely missing, and what THIS package adds, is a
// cross-cutting SUPERVISOR: something that watches all three subsystems
// at once and catches the cases where a row is not just "retrying with
// backoff" but truly stuck — no worker has touched it in a long time, or
// it has failed/needs_review and nobody has looked — and gives an
// operator ONE place to see all of it, subsystem by subsystem. It never
// signs, never broadcasts, and never re-derives an amount. Its only two
// write actions are:
//
//   - Unstick: clear a row's OWN lock columns (locked_by/locked_until) so
//     that subsystem's OWN worker claims and re-evaluates it on the very
//     next tick — through that subsystem's own receipt/mempool/nonce
//     checks, never bypassing them.
//   - EscalateToReview: flip needs_review=true (and status to a terminal
//     state if not already) using that subsystem's OWN compare-and-set
//     update path — the exact same write gasstation/sweep/treasury already
//     make from inside their own failNeedsReview, just triggered from
//     outside when a row has been stuck for too long instead of from
//     inside their own state machine.
//
// Nothing here ever performs "blind resend" (spec 7.6.1) — Unstick clears
// a LOCK, never a signed transaction's bytes, so the owning subsystem's
// own broadcastStep still runs its full pre-send verification before
// anything is ever transmitted again.
package recovery

import (
	"time"

	"github.com/google/uuid"
)

// Subsystem names one of the tracked operational engines. Bitcoin/TRON/6B
// blockchain_withdrawals are out of scope for this package purely because
// their exact table shape is not visible in this block's context slice —
// see docs/RECOVERY_7_6.md "known limitations" — the pattern below extends
// to them identically once their schema is confirmed.
type Subsystem string

const (
	SubsystemSweep             Subsystem = "sweep"
	SubsystemTreasuryRebalance Subsystem = "treasury_rebalance"
	SubsystemGasStationRefill  Subsystem = "gas_station_refill"
)

// Kind classifies WHY an incident was raised — spec 7.6's own vocabulary,
// mapped onto what is actually observable from each subsystem's existing
// columns (status, needs_review, error_detail, locked_until, attempts,
// updated_at). Nothing here is inferred beyond what those columns already
// record.
type Kind string

const (
	// KindStuckNoWorker: the row is ACTIVE (non-terminal), its lease is
	// not currently held (locked_until is null or in the past), and it has
	// not been updated in longer than the staleness window — i.e., no
	// worker instance appears to be claiming it at all. This is the
	// generic form of "worker reiniciado"/"proceso reiniciado" that
	// ClaimDue's own expiring-lease mechanism does NOT already cover: a
	// single missed tick recovers on its own; this only fires once that
	// has clearly failed to happen for a while.
	KindStuckNoWorker Kind = "stuck_no_worker"
	// KindStuckProcessing: ACTIVE but attempts has climbed past the
	// subsystem's own retry ceiling without resolving — the row is
	// looping, not merely retrying.
	KindStuckProcessing Kind = "stuck_processing"
	// KindBroadcastAmbiguous / KindNonceStuck mirror the exact
	// failure-code vocabulary sweep/treasury already write into
	// error_detail (see sweep.classifyBroadcastError /
	// treasury.classifyBroadcastError) — Recovery surfaces them centrally,
	// it does not invent a new classification of its own.
	KindBroadcastAmbiguous Kind = "broadcast_ambiguous"
	KindNonceStuck         Kind = "nonce_stuck"
	KindReorg              Kind = "reorg"
	KindRPCTimeout         Kind = "rpc_timeout"
	// KindNeedsReview / KindFailed: the row already carries needs_review or
	// a terminal 'failed' status — surfaced centrally for operator
	// visibility and escalation-if-unresolved, never auto-cleared.
	KindNeedsReview Kind = "needs_review"
	KindFailed      Kind = "failed"
)

// IncidentStatus is the lifecycle of one recovery_incidents row — separate
// from the underlying operation's OWN status (a sweep/rebalance/refill's
// status never changes just because an incident about it exists, except
// via the narrow EscalateToReview write documented above).
type IncidentStatus string

const (
	IncidentOpen         IncidentStatus = "open"
	IncidentAcknowledged IncidentStatus = "acknowledged"
	IncidentResolved     IncidentStatus = "resolved"
)

// Item is the minimal, subsystem-agnostic projection Recovery needs to
// decide whether something is stuck — never the full domain row (this
// package does not need a sweep's signed bytes to decide it looks
// abandoned).
type Item struct {
	Subsystem   Subsystem
	ID          uuid.UUID
	Status      string
	NeedsReview bool
	ErrorDetail string
	Attempts    int
	LockedBy    string
	LockedUntil *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// FailureCode returns the short machine code before the first ':' in
// ErrorDetail — same convention as sweep.Sweep.FailureCode /
// treasury.Rebalance.FailureCode.
func (it Item) FailureCode() string {
	for i := 0; i < len(it.ErrorDetail); i++ {
		if it.ErrorDetail[i] == ':' {
			return it.ErrorDetail[:i]
		}
	}
	return it.ErrorDetail
}

// Incident is one recovery_incidents row.
type Incident struct {
	ID         uuid.UUID
	Subsystem  Subsystem
	ItemID     uuid.UUID
	Kind       Kind
	Status     IncidentStatus
	Detail     map[string]any
	DetectedAt time.Time

	ResolvedAt     *time.Time
	ResolvedBy     string
	ResolutionNote string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Event is one append-only audit record (recovery_events), using spec
// 7.6.3's own vocabulary: recovery_detected, recovery_attempted,
// recovery_completed, recovery_failed, recovery_needs_review.
type Event struct {
	Type   string
	Detail map[string]any
}

// RunSummary is what one detection pass returns.
type RunSummary struct {
	Scanned   int
	Detected  int // new incidents opened this pass
	Unstuck   int // Unstick actions performed (gated by Config.Enabled)
	Escalated int // EscalateToReview actions performed (gated by Config.Enabled)
	Errors    int
}
