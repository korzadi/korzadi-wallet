package recovery

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Deps wires every collaborator. Stores is one RecoverableStore per
// watched subsystem — adding a fourth subsystem later is exactly "append
// one adapter to this slice", never a change to Service's logic.
type Deps struct {
	Stores    []RecoverableStore
	Incidents IncidentStore
	Audit     AuditLogger
	Config    Config
	Now       func() time.Time
}

func (d Deps) validate() error {
	if len(d.Stores) == 0 {
		return fmt.Errorf("recovery: Stores es requerido (al menos un subsistema)")
	}
	if d.Incidents == nil {
		return fmt.Errorf("recovery: Incidents es requerido")
	}
	return nil
}

// Service is the application-layer entry point for Bloque 7.6.
type Service struct {
	deps Deps
	now  func() time.Time
}

func NewService(deps Deps) (*Service, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Config.PollInterval <= 0 {
		deps.Config.PollInterval = DefaultConfig().PollInterval
	}
	if deps.Config.StaleAfter <= 0 {
		deps.Config.StaleAfter = DefaultConfig().StaleAfter
	}
	if deps.Config.MaxAttemptsBeforeReview <= 0 {
		deps.Config.MaxAttemptsBeforeReview = DefaultConfig().MaxAttemptsBeforeReview
	}
	if deps.Config.BatchSize <= 0 {
		deps.Config.BatchSize = DefaultConfig().BatchSize
	}
	return &Service{deps: deps, now: deps.Now}, nil
}

// RunDetection is 7.6.1's "detect + classify" pass, across every wired
// subsystem. It ALWAYS runs its detection and recording half regardless of
// Config.Enabled (spec 7.6.2) — only the two write actions inside
// handleItem (Unstick / EscalateToReview) are gated.
func (s *Service) RunDetection(ctx context.Context) (RunSummary, error) {
	var summary RunSummary
	for _, store := range s.deps.Stores {
		active, err := store.ScanActive(ctx, s.deps.Config.BatchSize)
		if err != nil {
			summary.Errors++
			log.Printf("recovery: escanear activos de %s: %v", store.Subsystem(), err)
			continue
		}
		for _, item := range active {
			summary.Scanned++
			if err := s.handleActiveItem(ctx, store, item, &summary); err != nil {
				summary.Errors++
				log.Printf("recovery: procesar %s/%s: %v", store.Subsystem(), item.ID, err)
			}
		}

		review, err := store.ScanNeedsReview(ctx, s.deps.Config.BatchSize)
		if err != nil {
			summary.Errors++
			log.Printf("recovery: escanear needs_review de %s: %v", store.Subsystem(), err)
			continue
		}
		for _, item := range review {
			summary.Scanned++
			if err := s.recordIncident(ctx, store.Subsystem(), item, KindNeedsReview, map[string]any{"error_detail": item.ErrorDetail}, &summary); err != nil {
				summary.Errors++
				log.Printf("recovery: registrar needs_review %s/%s: %v", store.Subsystem(), item.ID, err)
			}
		}

		failed, err := store.ScanFailed(ctx, s.now().Add(-24*time.Hour), s.deps.Config.BatchSize)
		if err != nil {
			summary.Errors++
			log.Printf("recovery: escanear failed de %s: %v", store.Subsystem(), err)
			continue
		}
		for _, item := range failed {
			summary.Scanned++
			if err := s.recordIncident(ctx, store.Subsystem(), item, KindFailed, map[string]any{"error_detail": item.ErrorDetail}, &summary); err != nil {
				summary.Errors++
				log.Printf("recovery: registrar failed %s/%s: %v", store.Subsystem(), item.ID, err)
			}
		}
	}
	return summary, nil
}

// handleActiveItem classifies ONE active (non-terminal) row and, if it
// looks genuinely stuck rather than merely retrying with normal backoff,
// records an incident and — only if Config.Enabled — takes the single safe
// action that classification warrants.
func (s *Service) handleActiveItem(ctx context.Context, store RecoverableStore, item Item, summary *RunSummary) error {
	now := s.now()
	code := item.FailureCode()

	switch {
	case code == "broadcast_ambiguous":
		return s.recordIncident(ctx, store.Subsystem(), item, KindBroadcastAmbiguous, map[string]any{"error_detail": item.ErrorDetail}, summary)

	case code == "nonce_stuck":
		return s.recordIncident(ctx, store.Subsystem(), item, KindNonceStuck, map[string]any{"error_detail": item.ErrorDetail}, summary)

	case item.Attempts >= s.deps.Config.MaxAttemptsBeforeReview:
		if err := s.recordIncident(ctx, store.Subsystem(), item, KindStuckProcessing, map[string]any{"attempts": item.Attempts, "status": item.Status}, summary); err != nil {
			return err
		}
		if !s.deps.Config.Enabled {
			return nil
		}
		if err := store.EscalateToReview(ctx, item.ID, "recovery: excedió el máximo de reintentos sin resolver"); err != nil {
			return fmt.Errorf("escalar a revisión: %w", err)
		}
		summary.Escalated++
		s.audit(ctx, "recovery_completed", store.Subsystem(), item.ID, map[string]any{"action": "escalate_to_review", "attempts": item.Attempts})
		return nil

	case isLockExpired(item, now) && now.Sub(item.UpdatedAt) > s.deps.Config.StaleAfter:
		if err := s.recordIncident(ctx, store.Subsystem(), item, KindStuckNoWorker, map[string]any{"status": item.Status, "locked_by": item.LockedBy, "idle_for": now.Sub(item.UpdatedAt).String()}, summary); err != nil {
			return err
		}
		if !s.deps.Config.Enabled {
			return nil
		}
		// Unstick: clear the lock so the OWNING subsystem's own worker
		// claims it on the next tick and re-runs ITS OWN receipt/mempool/
		// nonce checks before doing anything — never a blind resend from
		// here (spec 7.6.1).
		if err := store.Unstick(ctx, item.ID); err != nil {
			return fmt.Errorf("liberar lock: %w", err)
		}
		summary.Unstuck++
		s.audit(ctx, "recovery_attempted", store.Subsystem(), item.ID, map[string]any{"action": "unstick", "previous_locked_by": item.LockedBy})
		return nil
	}
	return nil
}

func isLockExpired(item Item, now time.Time) bool {
	return item.LockedUntil == nil || item.LockedUntil.Before(now)
}

func (s *Service) recordIncident(ctx context.Context, subsystem Subsystem, item Item, kind Kind, detail map[string]any, summary *RunSummary) error {
	full := map[string]any{"item_status": item.Status, "attempts": item.Attempts}
	for k, v := range detail {
		full[k] = v
	}
	in := &Incident{
		Subsystem:  subsystem,
		ItemID:     item.ID,
		Kind:       kind,
		Status:     IncidentOpen,
		Detail:     full,
		DetectedAt: s.now(),
	}
	saved, opened, err := s.openIncident(ctx, in)
	if err != nil {
		return fmt.Errorf("abrir incidente: %w", err)
	}
	if opened {
		summary.Detected++
		s.audit(ctx, "recovery_detected", subsystem, item.ID, map[string]any{"kind": string(kind), "incident_id": saved.ID.String()})
		log.Printf("recovery: incidente detectado subsystem=%s item=%s kind=%s", subsystem, item.ID, kind)
	}
	return nil
}

// openIncident wraps IncidentStore.Open and reports whether a NEW incident
// was created (vs. an already-open one of the same kind being returned
// idempotently) — Open's own contract (see ports.go) guarantees the latter
// never duplicates a row, so this only inspects CreatedAt vs DetectedAt to
// tell the two apart for logging/summary purposes.
func (s *Service) openIncident(ctx context.Context, in *Incident) (*Incident, bool, error) {
	ev := Event{Type: "recovery_detected", Detail: in.Detail}
	saved, err := s.deps.Incidents.Open(ctx, in, ev)
	if err != nil {
		return nil, false, err
	}
	return saved, saved.CreatedAt.Equal(in.DetectedAt) || saved.CreatedAt.After(in.DetectedAt.Add(-time.Second)), nil
}

// ---- operator resolution (never automatic) ----

// ResolveIncident marks an incident resolved. Recovery never verifies the
// underlying row itself here the way reconciliation.ResolveCheck
// re-derives a balance — an incident's "evidence" is whatever the operator
// did in response (redeployed a worker, manually confirmed a tx on an
// explorer, etc.), which this package cannot observe, so a note is
// mandatory instead.
func (s *Service) ResolveIncident(ctx context.Context, id uuid.UUID, actor, note string) (*Incident, error) {
	if strings.TrimSpace(note) == "" {
		return nil, ErrResolutionNote
	}
	resolved, err := s.deps.Incidents.Resolve(ctx, id, actor, note)
	if err != nil {
		return nil, fmt.Errorf("recovery: resolver incidente: %w", err)
	}
	if resolved == nil {
		return nil, ErrConcurrentUpdate
	}
	s.audit(ctx, "recovery_completed", resolved.Subsystem, resolved.ItemID, map[string]any{"action": "resolved", "actor": actor, "note": note, "incident_id": resolved.ID.String()})
	return resolved, nil
}

func (s *Service) audit(ctx context.Context, action string, subsystem Subsystem, itemID uuid.UUID, meta map[string]any) {
	if s.deps.Audit == nil {
		return
	}
	full := map[string]any{"subsystem": string(subsystem), "item_id": itemID.String()}
	for k, v := range meta {
		full[k] = v
	}
	// Recovery incidents are not always about a custody wallet directly
	// (e.g. a gas_station_refill IS tied to one, a sweep's item_id is a
	// sweep row, not a wallet) — LogCustodyWallet's custodyWalletID
	// parameter is passed nil here on purpose; the item/subsystem identify
	// the subject instead. Best-effort: a failed audit write never blocks
	// or reverses a recovery action.
	_ = s.deps.Audit.LogCustodyWallet(ctx, nil, action, "system:recovery", full)
}
