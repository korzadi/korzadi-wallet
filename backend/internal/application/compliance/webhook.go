package compliance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
)

// WebhookResult reports how an authenticated webhook was handled.
type WebhookResult struct {
	Duplicate  bool
	Processing WebhookProcessing
}

var eventTarget = map[string]KYCStatus{
	EvtCreated:   KYCPending,
	EvtPending:   KYCPending,
	EvtInReview:  KYCInReview,
	EvtApproved:  KYCVerified,
	EvtRejected:  KYCRejected,
	EvtExpired:   KYCExpired,
	EvtSuspended: KYCSuspended,
}

// ProcessWebhook authenticates and applies a provider event (spec §5).
//
// Order matters: provider lookup -> signature -> replay window -> parse ->
// persist-once (UNIQUE provider,event_id) -> locked, ordered, validated
// state change. Nothing from the body is trusted before the signature
// passes, and nothing is applied twice.
func (s *Service) ProcessWebhook(ctx context.Context, providerName string, headers map[string]string, body []byte) (*WebhookResult, error) {
	if !s.cfg.KYCEnabled {
		return nil, ErrKYCDisabled
	}
	prov, ok := s.reg.KYC(providerName)
	if !ok || providerName != s.cfg.KYCProvider {
		return nil, ErrUnknownProvider
	}
	signedAt, err := prov.VerifyWebhookSignature(headers, body, s.cfg.KYCWebhookSecret)
	if err != nil {
		return nil, ErrWebhookSignature
	}
	now := s.now()
	if !signedAt.IsZero() {
		d := now.Sub(signedAt)
		if d < 0 {
			d = -d
		}
		if d > s.cfg.WebhookTolerance {
			return nil, ErrWebhookReplay
		}
	}
	ev, err := prov.HandleWebhook(ctx, body)
	if err != nil || ev == nil || ev.EventID == "" || ev.ProviderReference == "" || len(ev.EventID) > 255 || len(ev.ProviderReference) > 255 {
		return nil, ErrWebhookMalformed
	}
	sum := sha256.Sum256(body)
	we := &WebhookEvent{
		ID: uuid.New(), Provider: providerName, EventID: ev.EventID, EventType: ev.Type,
		ProviderReference: ev.ProviderReference, PayloadHash: hex.EncodeToString(sum[:]),
	}
	if !ev.OccurredAt.IsZero() {
		t := ev.OccurredAt.UTC()
		we.OccurredAt = &t
	}

	var res WebhookResult
	var audited *auditEvent
	err = s.store.WithTx(ctx, func(tx Tx) error {
		inserted, err := tx.InsertWebhookEvent(ctx, we)
		if err != nil {
			return err
		}
		if !inserted { // at-least-once delivery: already seen
			res = WebhookResult{Duplicate: true}
			return nil
		}
		c, err := tx.FindCaseByProviderRef(ctx, providerName, ev.ProviderReference)
		if err != nil {
			return err
		}
		finish := func(p WebhookProcessing, caseID *uuid.UUID) error {
			res = WebhookResult{Processing: p}
			return tx.SetWebhookOutcome(ctx, providerName, ev.EventID, p, caseID)
		}
		if c == nil {
			// Possibly an event that raced ahead of our own commit of the
			// provider reference. Do NOT persist it (the tx rolls back) and
			// answer non-2xx so the provider retries later instead of the
			// event being deduplicated away forever.
			return ErrCaseNotFound
		}
		if err := tx.LockUser(ctx, c.UserID); err != nil {
			return err
		}
		// Re-read under the user lock so a concurrent manual review that
		// committed first is visible.
		c, err = tx.GetCaseForUpdate(ctx, c.ID)
		if err != nil {
			return err
		}
		target, known := eventTarget[ev.Type]
		if !known || target == c.Status {
			return finish(WebhookIgnoredNoop, &c.ID)
		}
		if c.LastProviderEventAt != nil && !ev.OccurredAt.IsZero() && ev.OccurredAt.Before(*c.LastProviderEventAt) {
			return finish(WebhookIgnoredStale, &c.ID) // out-of-order delivery
		}
		actor := Actor{Type: ActorProvider, Ref: providerName}
		old := c.Status
		if !ev.OccurredAt.IsZero() {
			t := ev.OccurredAt.UTC()
			c.LastProviderEventAt = &t
		}
		if verr := s.applyTransitionSafe(ctx, tx, c, target, actor, ev, now); verr != nil {
			if errors.Is(verr, ErrInvalidTransition) {
				// Provider outcome contradicts our state (e.g. approved after a
				// manual rejection): keep our state, raise an internal flag.
				if _, _, ferr := s.raiseFlagTx(ctx, tx, &Flag{
					UserID: c.UserID, Type: FlagKYCMismatch, Severity: RiskMedium, Source: SourceProvider,
					ReasonCode: "PROVIDER_STATE_CONFLICT", Description: "El proveedor reportó un estado incompatible con el actual; requiere revisión.",
				}, now); ferr != nil {
					return ferr
				}
				return finish(WebhookIgnoredInvalidTransition, &c.ID)
			}
			return verr
		}
		audited = &auditEvent{Actor: actor, Action: "KYC_PROVIDER_EVENT", Resource: "kyc_case", ResourceID: &c.ID,
			UserID: c.UserID, OldState: string(old), NewState: string(c.Status), Reason: ev.Type,
			Extra: map[string]any{"event_id": ev.EventID}}
		return finish(WebhookApplied, &c.ID)
	})
	if err != nil {
		return nil, err
	}
	if audited != nil {
		s.audit(ctx, *audited)
	}
	return &res, nil
}

func (s *Service) applyTransitionSafe(ctx context.Context, tx Tx, c *KYCCase, target KYCStatus, actor Actor, ev *ProviderEvent, now time.Time) error {
	if err := ValidateTransition(c.Status, target); err != nil {
		return err
	}
	x := &transitionExtras{ExpiresAt: ev.ExpiresAt, Profile: ev}
	return s.applyTransition(ctx, tx, c, target, actor, ev.ReasonCode, "evento del proveedor: "+ev.Type, now, x)
}
