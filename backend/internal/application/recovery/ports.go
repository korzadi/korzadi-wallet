package recovery

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// RecoverableStore is the read/limited-write surface Recovery needs from
// ONE subsystem's own table. It is intentionally narrow: no amount, no
// address, no signed bytes — this package classifies and, when safe,
// unsticks or escalates, but never reconstructs or re-executes an
// operation.
type RecoverableStore interface {
	Subsystem() Subsystem

	// ScanActive returns every row in a non-terminal status, regardless of
	// lock state — Recovery itself decides staleness (Item.UpdatedAt /
	// LockedUntil) rather than pushing that policy into each adapter.
	ScanActive(ctx context.Context, limit int) ([]Item, error)
	// ScanNeedsReview returns every row with needs_review = true.
	ScanNeedsReview(ctx context.Context, limit int) ([]Item, error)
	// ScanFailed returns every row with a terminal 'failed' status updated
	// after `since`.
	ScanFailed(ctx context.Context, since time.Time, limit int) ([]Item, error)

	// Unstick clears ONLY locked_by/locked_until on the row — never
	// signed_raw_tx, tx_hash, status, or amount. A no-op (not an error) if
	// the row is not currently locked or is no longer active. This is the
	// ONLY write this interface makes that is not gated by needs_review
	// semantics; it hands the row back to that subsystem's OWN worker,
	// which re-verifies everything itself before doing anything further.
	Unstick(ctx context.Context, id uuid.UUID) error

	// EscalateToReview sets needs_review = true (and moves status to that
	// subsystem's own terminal 'failed'-equivalent value if it is not
	// already terminal) via a compare-and-set against the row's CURRENT
	// status — same CAS discipline every Bloque 7 write already uses.
	// Applies only if the row is not already needs_review; a no-op
	// otherwise.
	EscalateToReview(ctx context.Context, id uuid.UUID, reason string) error
}

// IncidentStore is the persistence port for recovery_incidents /
// recovery_events, implemented by postgres.RecoveryRepository.
type IncidentStore interface {
	// Open creates a new OPEN incident, or is a no-op returning the
	// existing one if an OPEN incident of the same (subsystem, item_id,
	// kind) already exists — see
	// uq_recovery_incidents_open_subject_kind (0014): idempotent
	// detection, never a duplicate.
	Open(ctx context.Context, in *Incident, ev Event) (*Incident, error)
	Get(ctx context.Context, id uuid.UUID) (*Incident, error)
	List(ctx context.Context, f Filter) ([]*Incident, error)
	// Resolve performs a compare-and-set: applies only if the incident is
	// currently open or acknowledged.
	Resolve(ctx context.Context, id uuid.UUID, resolvedBy, note string) (*Incident, error)
	AddEvent(ctx context.Context, incidentID uuid.UUID, eventType string, detail map[string]any) error
}

// Filter narrows IncidentStore.List(); every non-nil field is ANDed.
type Filter struct {
	Subsystem *Subsystem
	Status    *IncidentStatus
	Kind      *Kind
}

// AuditLogger is the same shape every Bloque 7 package already declares.
type AuditLogger interface {
	LogCustodyWallet(ctx context.Context, custodyWalletID *uuid.UUID, action, actor string, metadata map[string]any) error
}
