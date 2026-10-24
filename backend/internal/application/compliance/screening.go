package compliance

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Screening (sanctions / PEP / adverse media) is abstracted behind Screener.
// A POTENTIAL_MATCH is only ever "requires manual review": it never becomes
// a conclusion about the user. Only a human review can mark CONFIRMED.

func (s *Service) screener(kind ScreeningKind) (Screener, error) {
	if !s.cfg.SanctionsEnabled {
		return nil, ErrScreeningUnavailable
	}
	sc, ok := s.reg.Screener(kind)
	if !ok {
		return nil, ErrScreeningUnavailable // fail closed: nothing is simulated
	}
	return sc, nil
}

type ScreeningOutcomeView struct {
	Request *ScreeningRequest
	Result  *ScreeningResult // nil while pending/failed
}

// RequestScreening runs (or resumes) one screening. Idempotent on
// (user, kind, key): a retry returns the stored result and never asks the
// provider twice for a completed request.
func (s *Service) RequestScreening(ctx context.Context, actor Actor, userID uuid.UUID, kind ScreeningKind, key string) (*ScreeningOutcomeView, error) {
	if !actor.IsReviewer() && actor.Type != ActorSystem {
		return nil, ErrForbidden
	}
	if !kind.Valid() {
		return nil, ErrInvalidArgument
	}
	if _, err := s.screener(kind); err != nil {
		return nil, err
	}
	var req *ScreeningRequest
	err := s.store.WithTx(ctx, func(tx Tx) error {
		if strings.TrimSpace(key) == "" {
			return ErrIdempotencyKeyRequired
		}
		r, _, err := tx.InsertScreeningRequest(ctx, &ScreeningRequest{
			ID: uuid.New(), UserID: userID, Kind: kind, Status: ScreenReqPending,
			IdempotencyKey: strings.TrimSpace(key), RequestedBy: actor.UserID, CreatedAt: s.now(),
		})
		req = r
		return err
	})
	if err != nil {
		return nil, err
	}
	if req.Status == ScreenReqCompleted || req.Status == ScreenReqExhausted {
		return s.screeningView(ctx, req)
	}
	if err := s.runScreening(ctx, req.ID, actor); err != nil {
		return nil, err
	}
	var final *ScreeningRequest
	if err := s.store.WithTx(ctx, func(tx Tx) error {
		r, err := tx.GetScreeningRequestForUpdate(ctx, req.ID)
		final = r
		return err
	}); err != nil {
		return nil, err
	}
	return s.screeningView(ctx, final)
}

func (s *Service) screeningView(ctx context.Context, r *ScreeningRequest) (*ScreeningOutcomeView, error) {
	v := &ScreeningOutcomeView{Request: r}
	err := s.store.WithTx(ctx, func(tx Tx) error {
		res, err := tx.ScreeningResultByRequest(ctx, r.ID)
		v.Result = res
		return err
	})
	return v, err
}

// runScreening performs one provider attempt for request id. Provider I/O is
// outside DB transactions; the result insert is guarded by the request's
// row lock + UNIQUE(request_id), so concurrent attempts collapse to one result.
func (s *Service) runScreening(ctx context.Context, id uuid.UUID, actor Actor) error {
	var (
		req  *ScreeningRequest
		skip bool
	)
	if err := s.store.WithTx(ctx, func(tx Tx) error {
		r, err := tx.GetScreeningRequestForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if r.Status == ScreenReqCompleted || r.Status == ScreenReqExhausted {
			skip = true
			return nil
		}
		r.AttemptCount++
		if err := tx.UpdateScreeningRequest(ctx, r); err != nil {
			return err
		}
		req = r
		return nil
	}); err != nil {
		return err
	}
	if skip {
		return nil
	}
	sc, err := s.screener(req.Kind)
	if err != nil {
		return s.failScreening(ctx, id, "screener_unavailable")
	}
	subj := ScreeningSubject{UserID: req.UserID, RequestID: req.ID}
	if s.users != nil {
		if u, uerr := s.users.FindByID(ctx, req.UserID); uerr == nil && u != nil {
			subj.FullName = strings.TrimSpace(u.FirstName + " " + u.LastName)
		}
	}
	if cs, cerr := s.store.CurrentCases(ctx, req.UserID); cerr == nil {
		sortCasesNewestFirst(cs)
		for _, c := range cs {
			if c.Country != "" {
				subj.Country = c.Country
				break
			}
		}
	}
	out, perr := sc.Screen(ctx, subj)
	if perr != nil || out == nil || !out.Match.Valid() {
		return s.failScreening(ctx, id, "provider_error")
	}

	var view *ScreeningResult
	created := false
	err = s.store.WithTx(ctx, func(tx Tx) error {
		if err := tx.LockUser(ctx, req.UserID); err != nil {
			return err
		}
		r, err := tx.GetScreeningRequestForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if r.Status == ScreenReqCompleted {
			return nil // another attempt already stored the result
		}
		now := s.now()
		res := &ScreeningResult{ID: uuid.New(), RequestID: r.ID, UserID: r.UserID, Kind: r.Kind, Match: out.Match,
			Review: ReviewNotRequired, ProviderReference: out.ProviderReference, ScreenedAt: now, Version: 1}
		if out.Match != MatchNone {
			res.Review = ReviewRequired
		}
		if err := tx.InsertScreeningResult(ctx, res); err != nil {
			return err
		}
		r.Status, r.Provider, r.ProviderReference, r.NextRetryAt, r.LastErrorCode = ScreenReqCompleted, sc.Name(), out.ProviderReference, nil, ""
		if err := tx.UpdateScreeningRequest(ctx, r); err != nil {
			return err
		}
		if res.Review == ReviewRequired {
			ft := FlagManualReview
			if r.Kind == ScreenSanctions {
				ft = FlagSanctionsReview
			}
			if _, _, err := s.raiseFlagTx(ctx, tx, &Flag{UserID: r.UserID, Type: ft, Severity: RiskHigh, Source: SourceScreening,
				ReasonCode: "SCREENING_" + string(out.Match), Description: "Resultado de screening que requiere revisión manual."}, now); err != nil {
				return err
			}
			if r.Kind == ScreenSanctions && s.cfg.SanctionsAutoHold && s.cfg.AMLEnabled {
				if _, _, err := s.placeHoldTx(ctx, tx, r.UserID, HoldSanctionReview, nil, "SCREENING_REVIEW", "screening pendiente de revisión manual", now); err != nil {
					return err
				}
			}
		}
		view, created = res, true
		return nil
	})
	if err != nil {
		return err
	}
	if created {
		s.audit(ctx, auditEvent{Actor: actor, Action: "SCREENING_COMPLETED", Resource: "screening_result", ResourceID: &view.ID, UserID: req.UserID,
			NewState: string(view.Match), Extra: map[string]any{"kind": string(view.Kind), "review_status": string(view.Review)}})
	}
	return nil
}

func (s *Service) failScreening(ctx context.Context, id uuid.UUID, code string) error {
	var exhausted bool
	var userID uuid.UUID
	err := s.store.WithTx(ctx, func(tx Tx) error {
		r, err := tx.GetScreeningRequestForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if r.Status == ScreenReqCompleted {
			return nil
		}
		now := s.now()
		r.LastErrorCode = code
		userID = r.UserID
		if r.AttemptCount >= s.cfg.ScreeningMaxAttempts {
			r.Status, r.NextRetryAt, exhausted = ScreenReqExhausted, nil, true
			if _, _, err := s.raiseFlagTx(ctx, tx, &Flag{UserID: r.UserID, Type: FlagProviderAlert, Severity: RiskMedium, Source: SourceAutomated,
				ReasonCode: "SCREENING_UNAVAILABLE", Description: "El screening no pudo completarse tras varios intentos."}, now); err != nil {
				return err
			}
		} else {
			r.Status = ScreenReqFailed
			backoff := s.cfg.ScreeningRetryBackoff << uint(min(r.AttemptCount-1, 10))
			t := now.Add(backoff)
			r.NextRetryAt = &t
		}
		return tx.UpdateScreeningRequest(ctx, r)
	})
	if err != nil {
		return err
	}
	if exhausted {
		s.audit(ctx, auditEvent{Actor: Actor{Type: ActorSystem, Ref: "screening"}, Action: "SCREENING_EXHAUSTED", Resource: "screening_request", UserID: userID, Reason: code})
	}
	return ErrProviderUnavailable
}

// RetryFailedScreenings is the worker entry point. Requests are leased
// (next_retry_at pushed forward) inside the same transaction that selects
// them with SKIP LOCKED, so two workers never take the same request.
func (s *Service) RetryFailedScreenings(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 50
	}
	var ids []uuid.UUID
	err := s.store.WithTx(ctx, func(tx Tx) error {
		now := s.now()
		rs, err := tx.RetryableScreeningForUpdate(ctx, now, limit)
		if err != nil {
			return err
		}
		lease := now.Add(s.cfg.ScreeningRetryBackoff)
		for _, r := range rs {
			l := lease
			r.NextRetryAt = &l
			if err := tx.UpdateScreeningRequest(ctx, r); err != nil {
				return err
			}
			ids = append(ids, r.ID)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		if err := s.runScreening(ctx, id, Actor{Type: ActorSystem, Ref: "screening-retry-worker"}); err == nil {
			n++
		}
	}
	return n, nil
}

// ReviewScreening records the HUMAN outcome of a REVIEW_REQUIRED result
// (compliance_admin). It does not release holds automatically: releasing is
// a separate, audited decision.
func (s *Service) ReviewScreening(ctx context.Context, actor Actor, resultID uuid.UUID, confirmed bool, reason, key string) (*ScreeningResult, error) {
	if !actor.IsComplianceAdmin() || actor.UserID == nil {
		return nil, ErrForbidden
	}
	reason, err := validReason(reason)
	if err != nil {
		return nil, err
	}
	outcome := ReviewCleared
	if confirmed {
		outcome = ReviewConfirmed
	}
	var out *ScreeningResult
	replayed := false
	err = s.store.WithTx(ctx, func(tx Tx) error {
		resID, replay, err := claim(ctx, tx, "screening.review", *actor.UserID, key, resultID.String(), string(outcome), reason)
		if err != nil {
			return err
		}
		r, err := tx.GetScreeningResultForUpdate(ctx, resultID)
		if err != nil {
			return err
		}
		if replay && resID != nil {
			out, replayed = r, true
			return nil
		}
		if r.Review != ReviewRequired {
			return ErrScreeningReviewed
		}
		if r.UserID == *actor.UserID {
			return ErrSelfAction
		}
		now := s.now()
		r.Review, r.ReviewedBy, r.ReviewedAt, r.ReviewReason = outcome, actor.UserID, &now, reason
		if err := tx.UpdateScreeningResult(ctx, r); err != nil {
			return err
		}
		out = r
		return tx.SetIdempotencyResource(ctx, "screening.review", *actor.UserID, strings.TrimSpace(key), r.ID)
	})
	if err != nil {
		return nil, err
	}
	if !replayed {
		s.audit(ctx, auditEvent{Actor: actor, Action: "SCREENING_REVIEWED", Resource: "screening_result", ResourceID: &out.ID, UserID: out.UserID,
			OldState: string(ReviewRequired), NewState: string(outcome), Reason: reason})
	}
	return out, nil
}
