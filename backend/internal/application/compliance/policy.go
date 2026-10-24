package compliance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/application/security"
)

// RestrictionError is returned when compliance state blocks an operation.
// It matches security.ErrComplianceRestricted (so HTTPStatusFor maps it to
// 403) and carries only a coarse, non-sensitive code. Users never see hold
// types, flags, screening or risk information.
type RestrictionError struct{ Code string }

func (e *RestrictionError) Error() string {
	return "compliance: operación no permitida (" + e.Code + ")"
}
func (e *RestrictionError) Is(target error) bool { return target == security.ErrComplianceRestricted }

// Decision is the pure outcome of the policy for one (user, operation).
type Decision struct {
	Allowed bool
	Code    string // set when !Allowed
	// Limited is true when the user is in the reduced tier; Cap is the
	// per-operation maximum applied (asset units). The daily/monthly totals
	// remain governed by Bloque 8's security_policies — not duplicated here.
	Limited bool
	Cap     *decimal.Decimal
}

// Decide is the centralized, side-effect-free policy (spec §6). Deposits are
// never evaluated here: blockchain reception and Ledger crediting are
// independent of KYC and holds (see docs/KYC_AML.md).
func Decide(cfg Config, op security.OperationType, status KYCStatus, holds []*Hold, amount *decimal.Decimal) Decision {
	if op != security.OpWithdrawal && op != security.OpTransfer {
		return Decision{Allowed: true}
	}
	if cfg.AMLEnabled && len(holds) > 0 {
		return Decision{Code: "compliance_hold"}
	}
	if !cfg.KYCEnabled {
		return Decision{Allowed: true}
	}
	deny := func() Decision { return Decision{Code: "kyc_" + strings.ToLower(string(status))} }
	switch op {
	case security.OpWithdrawal:
		if status == KYCSuspended {
			return deny()
		}
		if cfg.WithdrawalRequiresKYC && status != KYCVerified {
			return deny()
		}
		return Decision{Allowed: true}
	case security.OpTransfer:
		switch status {
		case KYCVerified:
			return Decision{Allowed: true}
		case KYCNotStarted, KYCPending, KYCInReview:
			if !cfg.LimitedTransferMax.IsPositive() {
				return deny() // no reduced tier configured: nothing is invented
			}
			limit := cfg.LimitedTransferMax
			if amount != nil && amount.GreaterThan(limit) {
				return Decision{Code: "kyc_limit_exceeded", Limited: true, Cap: &limit}
			}
			return Decision{Allowed: true, Limited: true, Cap: &limit}
		default: // REJECTED, EXPIRED, SUSPENDED
			return deny()
		}
	}
	return Decision{Allowed: true}
}

// Engine implements security.ComplianceChecker on top of the Store.
type Engine struct {
	store Store
	cfg   Config
	now   func() time.Time
}

func NewEngine(store Store, cfg Config) *Engine {
	return &Engine{store: store, cfg: cfg, now: func() time.Time { return time.Now().UTC() }}
}

func (e *Engine) SetClock(f func() time.Time) { e.now = f }

func (e *Engine) load(ctx context.Context, userID uuid.UUID) (KYCStatus, []*Hold, error) {
	var status KYCStatus = KYCNotStarted
	if e.cfg.KYCEnabled {
		cs, err := e.store.CurrentCases(ctx, userID)
		if err != nil {
			return "", nil, err
		}
		status = EffectiveStatus(cs, e.now())
	}
	var holds []*Hold
	if e.cfg.AMLEnabled {
		h, err := e.store.ActiveHolds(ctx, userID)
		if err != nil {
			return "", nil, err
		}
		holds = h
	}
	return status, holds, nil
}

// CheckOperation is called by security.Service (PreAuthorize with amount=nil,
// Reserve with the amount). If the state cannot be read it FAILS CLOSED with
// a generic error; it never allows on error.
func (e *Engine) CheckOperation(ctx context.Context, op security.OperationType, userID uuid.UUID, amount *decimal.Decimal) error {
	if (!e.cfg.KYCEnabled && !e.cfg.AMLEnabled) || (op != security.OpWithdrawal && op != security.OpTransfer) {
		return nil
	}
	status, holds, err := e.load(ctx, userID)
	if err != nil {
		return fmt.Errorf("compliance: no se pudo evaluar la política: %w", err)
	}
	if d := Decide(e.cfg, op, status, holds, amount); !d.Allowed {
		return &RestrictionError{Code: d.Code}
	}
	return nil
}

// CheckInbound gates the RECIPIENT of an internal transfer. Only a KYC
// SUSPENDED status or an active hold blocks receiving; the transfer is
// simply refused (the sender keeps their funds). Blockchain deposits are
// never routed through this.
func (e *Engine) CheckInbound(ctx context.Context, op security.OperationType, userID uuid.UUID) error {
	if (!e.cfg.KYCEnabled && !e.cfg.AMLEnabled) || op != security.OpTransfer {
		return nil
	}
	status, holds, err := e.load(ctx, userID)
	if err != nil {
		return fmt.Errorf("compliance: no se pudo evaluar la política: %w", err)
	}
	if (e.cfg.AMLEnabled && len(holds) > 0) || (e.cfg.KYCEnabled && status == KYCSuspended) {
		return &RestrictionError{Code: "recipient_restricted"}
	}
	return nil
}
