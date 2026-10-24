package security

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ComplianceChecker is Bloque 9's optional hook (implemented by
// compliance.Engine). Defined here — not imported — so security never
// depends on the compliance package. It only AUTHORIZES; it never moves
// balances. Nil means "compliance not wired": behavior is unchanged.
type ComplianceChecker interface {
	// CheckOperation is called with amount=nil from PreAuthorize (status and
	// holds) and with the amount from Reserve (reduced-tier cap).
	CheckOperation(ctx context.Context, op OperationType, userID uuid.UUID, amount *decimal.Decimal) error
	// CheckInbound gates the recipient of an internal transfer.
	CheckInbound(ctx context.Context, op OperationType, userID uuid.UUID) error
}

// Service is the single entry point application-layer code (transfer,
// withdrawal, admin handlers, ...) is expected to depend on, composing
// every Bloque 8 control. Every field is independently optional (nil-safe)
// so a caller that only needs, say, limits, can construct a Service with
// just a Limiter and leave the rest nil — matching the existing codebase's
// convention of optional collaborators (domain.AuditLogger, Notifier).
type Service struct {
	Policies    PolicyRepository
	Limits      *LimitService
	Freeze      *FreezeGuard
	Guard       *Guard
	StepUp      *StepUpGuard
	Velocity    *VelocityTracker
	Destination *DestinationGuard
	Events      *EventLogger

	// Freezes and EventsRepo expose the raw repositories behind Freeze
	// and Events for admin/read endpoints (list every freeze, list/filter
	// every security event) — FreezeGuard and EventLogger deliberately
	// only expose what the hot authorize/log path needs, not full CRUD.
	Freezes    FreezeRepository
	EventsRepo EventRepository

	// Compliance is Bloque 9's KYC/AML hook; nil = not wired (no change).
	Compliance ComplianceChecker
}

// SetCompliance wires Bloque 9. Called once at startup; never required.
func (s *Service) SetCompliance(c ComplianceChecker) { s.Compliance = c }

// CheckRecipient applies compliance rules to the RECEIVING side of an
// internal transfer. No-op when compliance is not wired.
func (s *Service) CheckRecipient(ctx context.Context, op OperationType, recipientID uuid.UUID) error {
	if s.Compliance == nil {
		return nil
	}
	return s.Compliance.CheckInbound(ctx, op, recipientID)
}

// NewService wires the sub-components together. Any argument may be nil;
// callers only pay for what they use.
func NewService(policies PolicyRepository, limits *LimitService, freeze *FreezeGuard, guard *Guard,
	stepUp *StepUpGuard, velocity *VelocityTracker, destination *DestinationGuard, events *EventLogger,
	freezes FreezeRepository, eventsRepo EventRepository) *Service {
	return &Service{
		Policies: policies, Limits: limits, Freeze: freeze, Guard: guard,
		StepUp: stepUp, Velocity: velocity, Destination: destination, Events: events,
		Freezes: freezes, EventsRepo: eventsRepo,
	}
}

// PreAuthorize runs the checks that must pass BEFORE any reservation or
// ledger movement is attempted for a sensitive operation (spec §6/§7):
// account active, kill switch / emergency freeze. It does not check
// amount limits (see Reserve) or step-up (see StepUp) — those need the
// amount and, for step-up, a 2FA code the caller collects separately.
func (s *Service) PreAuthorize(ctx context.Context, op OperationType, userID uuid.UUID, ip string) error {
	if s.Guard != nil {
		if err := s.Guard.RequireActiveAccount(ctx, userID); err != nil {
			return err
		}
	}
	if s.Freeze != nil {
		uid := userID
		if err := s.Freeze.CheckEnabled(ctx, op, &uid, ip); err != nil {
			return err
		}
	}
	if s.Compliance != nil {
		if err := s.Compliance.CheckOperation(ctx, op, userID, nil); err != nil {
			return err
		}
	}
	return nil
}

// Reserve resolves the applicable Policy (if a PolicyRepository is wired)
// and asks Limits to reserve the amount, returning both the reservation
// and the policy so the caller can evaluate step-up/manual-approval
// thresholds (EvaluateThresholds) without a second round trip.
func (s *Service) Reserve(ctx context.Context, in ReserveInput, ip string) (*Reservation, *Policy, error) {
	if s.Compliance != nil {
		amt := in.Amount
		if err := s.Compliance.CheckOperation(ctx, in.Operation, in.UserID, &amt); err != nil {
			return nil, nil, err
		}
	}
	var policy *Policy
	if s.Policies != nil {
		p, err := s.Policies.Get(ctx, in.Operation, in.AssetCode, in.Network)
		if err != nil {
			return nil, nil, fmt.Errorf("security: cargar política de %s: %w", in.Operation, err)
		}
		policy = p
	}
	if s.Limits == nil {
		// No limiter wired: nothing to reserve, but still return the
		// policy so callers can evaluate thresholds.
		return nil, policy, nil
	}
	res, err := s.Limits.Reserve(ctx, in, ip)
	if err != nil {
		return nil, policy, err
	}
	return res, policy, nil
}

// Commit/Release proxy straight to Limits, guarded against a nil Limits
// (e.g. in tests or for operations that don't use amount limits) so
// callers don't need their own nil check on every call site.
func (s *Service) Commit(ctx context.Context, reservationID uuid.UUID) error {
	if s.Limits == nil {
		return nil
	}
	return s.Limits.Commit(ctx, reservationID)
}

func (s *Service) Release(ctx context.Context, reservationID uuid.UUID) error {
	if s.Limits == nil {
		return nil
	}
	return s.Limits.Release(ctx, reservationID)
}
