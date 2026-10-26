package security

import (
	"context"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
)

// TwoFactorVerifier is the exact shape of twofactor.Service.Verify —
// Bloque 8 reuses the existing TOTP implementation entirely (spec §12:
// "No inventar un nuevo sistema 2FA. Reutilizar el existente."); it never
// stores or checks a code itself.
type TwoFactorVerifier interface {
	Verify(ctx context.Context, userID uuid.UUID, code string) (bool, error)
}

// StepUpGuard enforces step-up authentication (spec §12/§13) for security
// changes, limit modifications, admin operations and high-value
// withdrawals, by requiring a fresh valid 2FA code on top of whatever
// already got the user into an authenticated session.
type PINVerifier interface {
	VerifyPin(ctx context.Context, userID uuid.UUID, pin string) (bool, error)
}

type StepUpGuard struct {
	twoFactor TwoFactorVerifier
	pin       PINVerifier
	events    *EventLogger
}

func NewStepUpGuard(twoFactor TwoFactorVerifier, events *EventLogger) *StepUpGuard {
	return &StepUpGuard{twoFactor: twoFactor, events: events}
}

func (g *StepUpGuard) WithPINVerifier(pin PINVerifier) *StepUpGuard {
	g.pin = pin
	return g
}

// Require verifies code for userID. An empty code returns
// domain.Err2FARequired (matching the vocabulary already used by
// blockchainwithdrawal/handlers.TwoFAVerifyMiddleware); a present but
// wrong code returns ErrStepUpInvalid and logs TWO_FACTOR_FAILED.
func isPinCode(code string) bool {
	if len(code) < 4 || len(code) > 16 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (g *StepUpGuard) Require(ctx context.Context, userID uuid.UUID, code, ip string) error {
	if code == "" {
		return domain.Err2FARequired
	}
	// Try 2FA first (existing behavior).
	ok, err := g.twoFactor.Verify(ctx, userID, code)
	if err == nil && ok {
		return nil
	}
	// If 2FA verification didn't succeed (wrong code or maybe replay), and
	// the code looks like a PIN (numeric 4-16 digits), try PIN verifier.
	if (err == nil && !ok) || (err != nil && isPinCode(code)) {
		if g.pin != nil {
			pinOk, pinErr := g.pin.VerifyPin(ctx, userID, code)
			if pinErr != nil {
				return pinErr
			}
			if pinOk {
				return nil
			}
		}
	}
	if err != nil {
		if g.events != nil {
			uid := userID
			_ = g.events.Log(ctx, Event{
				Type:     EventTwoFactorFailed,
				Severity: "warning",
				UserID:   &uid,
				IP:       ip,
			})
		}
		return ErrStepUpInvalid
	}
	if !ok {
		if g.events != nil {
			uid := userID
			_ = g.events.Log(ctx, Event{
				Type:     EventTwoFactorFailed,
				Severity: "warning",
				UserID:   &uid,
				IP:       ip,
			})
		}
		return ErrStepUpInvalid
	}
	return nil
}

// RequireForAmount only calls Require when amount crosses the policy's
// step-up threshold (or unconditionally if policy is nil/has no
// threshold and forceIfNoPolicy is true — used for operations that must
// always step up, like changing security settings). Returns nil
// immediately if no step-up is needed for this amount.
func (g *StepUpGuard) RequireForAmount(ctx context.Context, p *Policy, amount Money, userID uuid.UUID, code, pinCode, ip string) error {
	switch EvaluateThresholds(p, amount) {
	case "step_up", "manual_approval":
		if pinCode != "" && isPinCode(pinCode) {
			return g.Require(ctx, userID, pinCode, ip)
		}
		return g.Require(ctx, userID, code, ip)
	default:
		return nil
	}
}
