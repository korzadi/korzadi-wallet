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
type StepUpGuard struct {
	twoFactor TwoFactorVerifier
	events    *EventLogger
}

func NewStepUpGuard(twoFactor TwoFactorVerifier, events *EventLogger) *StepUpGuard {
	return &StepUpGuard{twoFactor: twoFactor, events: events}
}

// Require verifies code for userID. An empty code returns
// domain.Err2FARequired (matching the vocabulary already used by
// blockchainwithdrawal/handlers.TwoFAVerifyMiddleware); a present but
// wrong code returns ErrStepUpInvalid and logs TWO_FACTOR_FAILED.
func (g *StepUpGuard) Require(ctx context.Context, userID uuid.UUID, code, ip string) error {
	if code == "" {
		return domain.Err2FARequired
	}
	ok, err := g.twoFactor.Verify(ctx, userID, code)
	if err != nil {
		return err
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
func (g *StepUpGuard) RequireForAmount(ctx context.Context, p *Policy, amount Money, userID uuid.UUID, code, ip string) error {
	switch EvaluateThresholds(p, amount) {
	case "step_up", "manual_approval":
		return g.Require(ctx, userID, code, ip)
	default:
		return nil
	}
}
