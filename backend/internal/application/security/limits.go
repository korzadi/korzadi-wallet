package security

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Policy mirrors one row of security_policies (spec §3/§4): the
// configurable limits and thresholds for one (operation, asset, network)
// scope. Pointer fields are nil when that particular limit is not
// configured ("no limit"), never confused with a configured limit of
// zero.
type Policy struct {
	Operation OperationType
	AssetCode string // AnyAsset ("*") when the policy applies to every asset
	Network   string // "" when the policy applies to every network

	MinAmount decimal.Decimal // always set (defaults to 0)

	MaxAmount           *decimal.Decimal
	DailyLimit          *decimal.Decimal
	DailyCountLimit     *int
	MonthlyLimit        *decimal.Decimal
	MonthlyCountLimit   *int
	StepUpThreshold     *decimal.Decimal
	ManualApprovalAbove *decimal.Decimal

	Enabled bool
}

// PolicyRepository resolves the applicable security_policies row for an
// operation, preferring the most specific (asset+network) match and
// falling back to a wildcard. Returns (nil, nil) when no policy at all is
// configured for that operation — meaning it is simply not managed by
// this table (e.g. a system operation whose own subsystem already
// decides amounts), which LimitService treats as "no additional limits
// beyond what already exists elsewhere", not as an error.
type PolicyRepository interface {
	Get(ctx context.Context, op OperationType, assetCode, network string) (*Policy, error)
	List(ctx context.Context) ([]Policy, error)
	Upsert(ctx context.Context, p Policy) error
}

// ReserveInput is what a caller (transfer.Service, a withdrawal handler,
// etc.) provides to ask "may this amount proceed, and hold it against the
// user's limits while the operation is in flight".
type ReserveInput struct {
	UserID    uuid.UUID
	Operation OperationType
	AssetCode string // "" is normalized to AnyAsset
	Network   string
	Amount    decimal.Decimal

	// ReferenceID, when set, makes Reserve idempotent/replay-safe (spec
	// §9): calling Reserve twice with the same (Operation, ReferenceID)
	// while the first reservation is still active returns
	// ErrReplay instead of double-counting the amount.
	ReferenceID string
}

// Reservation is the handle returned by Reserve; Commit/Release take its
// ID.
type Reservation struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Operation OperationType
	AssetCode string
	Amount    decimal.Decimal
}

// Limiter implements the reserve -> commit | release primitive of spec
// §5, backed by durable, atomic storage (Postgres row locks — see
// postgres.SecurityLimitRepository) so that two concurrent requests can
// never both be approved against the same limit (spec's own example:
// daily_limit=1000, two simultaneous 700s must not both succeed).
type Limiter interface {
	// Reserve atomically checks in.Amount against the applicable Policy
	// (min/max/daily/monthly, count and amount) and, if it fits, holds it
	// against the user's daily and monthly usage. It must run inside a
	// single database transaction with row-level locking; a
	// read-then-write implementation without locking would reintroduce
	// exactly the race this method exists to prevent.
	Reserve(ctx context.Context, in ReserveInput) (*Reservation, error)
	// Commit finalizes a reservation: the amount stays counted against
	// the period's usage permanently (it does not get added again — it
	// was already counted at Reserve time).
	Commit(ctx context.Context, reservationID uuid.UUID) error
	// Release cancels an active reservation, returning its amount to the
	// user's available daily/monthly allowance.
	Release(ctx context.Context, reservationID uuid.UUID) error
}

// LimitService is the application-facing entry point: it wraps a Limiter
// with security-event logging, so every rejection is both returned to the
// caller AND recorded (spec §16: WITHDRAWAL_LIMIT_REJECTED /
// TRANSFER_LIMIT_REJECTED).
type LimitService struct {
	limiter Limiter
	events  *EventLogger
}

func NewLimitService(limiter Limiter, events *EventLogger) *LimitService {
	return &LimitService{limiter: limiter, events: events}
}

// Reserve behaves exactly like Limiter.Reserve, additionally logging a
// security event when the reservation is rejected for a limit reason.
func (s *LimitService) Reserve(ctx context.Context, in ReserveInput, ip string) (*Reservation, error) {
	if in.AssetCode == "" {
		in.AssetCode = AnyAsset
	}
	res, err := s.limiter.Reserve(ctx, in)
	if err != nil {
		s.logRejection(ctx, in, ip, err)
		return nil, err
	}
	return res, nil
}

func (s *LimitService) Commit(ctx context.Context, reservationID uuid.UUID) error {
	return s.limiter.Commit(ctx, reservationID)
}

func (s *LimitService) Release(ctx context.Context, reservationID uuid.UUID) error {
	return s.limiter.Release(ctx, reservationID)
}

func (s *LimitService) logRejection(ctx context.Context, in ReserveInput, ip string, err error) {
	if s.events == nil {
		return
	}
	if !isLimitRejection(err) {
		return
	}
	eventType := EventTransferLimitRejected
	if in.Operation == OpWithdrawal {
		eventType = EventWithdrawalLimitRejected
	}
	userID := in.UserID
	_ = s.events.Log(ctx, Event{
		Type:      eventType,
		Severity:  "warning",
		UserID:    &userID,
		Operation: in.Operation,
		IP:        ip,
		Metadata: map[string]any{
			"asset_code": in.AssetCode,
			"amount":     in.Amount.String(),
			"reason":     err.Error(),
		},
	})
}

func isLimitRejection(err error) bool {
	switch {
	case errors.Is(err, ErrBelowMinimum), errors.Is(err, ErrAboveMaximum),
		errors.Is(err, ErrDailyLimitExceeded), errors.Is(err, ErrDailyCountExceeded),
		errors.Is(err, ErrMonthlyLimitExceeded), errors.Is(err, ErrMonthlyCountExceeded),
		errors.Is(err, ErrOperationDisabled), errors.Is(err, ErrReplay):
		return true
	default:
		return false
	}
}

// EvaluateThresholds classifies amount against a policy's step-up and
// manual-approval thresholds (spec §13): "normal" -> "" (no extra
// control), "step_up", or "manual_approval". A nil policy or nil
// threshold means that tier is not required.
func EvaluateThresholds(p *Policy, amount decimal.Decimal) string {
	if p == nil {
		return ""
	}
	if p.ManualApprovalAbove != nil && amount.GreaterThan(*p.ManualApprovalAbove) {
		return "manual_approval"
	}
	if p.StepUpThreshold != nil && amount.GreaterThan(*p.StepUpThreshold) {
		return "step_up"
	}
	return ""
}

// ValidatePolicyShape performs the same invariant checks the migration's
// CHECK constraints enforce, so an admin API can reject a bad policy
// before ever reaching Postgres with a clear, specific error instead of a
// raw constraint-violation message.
func ValidatePolicyShape(p Policy) error {
	if !p.Operation.Valid() {
		return ErrOperationUnknown
	}
	if p.MinAmount.IsNegative() {
		return fmt.Errorf("security: min_amount no puede ser negativo")
	}
	if p.MaxAmount != nil {
		if !p.MaxAmount.IsPositive() {
			return fmt.Errorf("security: max_amount debe ser positivo")
		}
		if p.MinAmount.GreaterThan(*p.MaxAmount) {
			return fmt.Errorf("security: min_amount no puede exceder max_amount")
		}
	}
	if p.DailyLimit != nil {
		if !p.DailyLimit.IsPositive() {
			return fmt.Errorf("security: daily_limit debe ser positivo")
		}
		if p.MaxAmount != nil && p.MaxAmount.GreaterThan(*p.DailyLimit) {
			return fmt.Errorf("security: max_amount no puede exceder daily_limit")
		}
	}
	if p.MonthlyLimit != nil && !p.MonthlyLimit.IsPositive() {
		return fmt.Errorf("security: monthly_limit debe ser positivo")
	}
	if p.DailyCountLimit != nil && *p.DailyCountLimit <= 0 {
		return fmt.Errorf("security: daily_count_limit debe ser positivo")
	}
	if p.MonthlyCountLimit != nil && *p.MonthlyCountLimit <= 0 {
		return fmt.Errorf("security: monthly_count_limit debe ser positivo")
	}
	return nil
}
