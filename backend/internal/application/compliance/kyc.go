package compliance

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	reCountry = regexp.MustCompile(`^[A-Z]{2}$`)
	reDocType = regexp.MustCompile(`^[A-Za-z0-9_]{1,30}$`)
	reHex64   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	reCode    = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,50}$`)
)

func cleanCountry(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	if reCountry.MatchString(c) {
		return c
	}
	return ""
}

// ---------------- user-facing ----------------

// UserKYCView is everything a normal user may see about their own KYC.
// Risk level, flags and holds are internal and never exposed here.
type UserKYCView struct {
	Status    KYCStatus  `json:"status"`
	CaseID    *uuid.UUID `json:"case_id,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CanStart  bool       `json:"can_start"`
}

type StartInput struct {
	UserID           uuid.UUID
	IdempotencyKey   string
	Country          string
	ResidenceCountry string
	Actor            Actor
}

type StartResult struct {
	Case        *KYCCase
	RedirectURL string
	Replayed    bool
}

func (s *Service) provider() (KYCProvider, error) {
	if !s.cfg.KYCEnabled {
		return nil, ErrKYCDisabled
	}
	p, ok := s.reg.KYC(s.cfg.KYCProvider)
	if !ok {
		return nil, ErrProviderUnavailable // fail closed: configured but not registered
	}
	return p, nil
}

// GetStatus returns the caller's OWN status. The user id always comes from
// the authenticated session, never from a path/body parameter (no IDOR).
func (s *Service) GetStatus(ctx context.Context, userID uuid.UUID) (*UserKYCView, error) {
	cases, err := s.store.CurrentCases(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("compliance: leer casos: %w", err)
	}
	now := s.now()
	st := EffectiveStatus(cases, now)
	v := &UserKYCView{Status: st}
	sortCasesNewestFirst(cases)
	if len(cases) > 0 {
		v.CaseID = &cases[0].ID
		for _, c := range cases {
			if c.Status == KYCVerified {
				v.ExpiresAt = c.ExpiresAt
			}
		}
	}
	v.CanStart = s.cfg.KYCEnabled && s.canStartFrom(st, cases, now)
	return v, nil
}

func (s *Service) canStartFrom(st KYCStatus, cases []*KYCCase, now time.Time) bool {
	switch st {
	case KYCNotStarted, KYCPending, KYCInReview, KYCRejected, KYCExpired:
		return true
	case KYCVerified:
		return s.inRenewalWindow(cases, now)
	}
	return false // SUSPENDED: only compliance can move it
}

func (s *Service) inRenewalWindow(cases []*KYCCase, now time.Time) bool {
	if s.cfg.KYCRenewalWindow <= 0 {
		return false
	}
	for _, c := range cases {
		if c.Status == KYCVerified && c.ExpiresAt != nil && c.ExpiresAt.Sub(now) <= s.cfg.KYCRenewalWindow {
			return true
		}
	}
	return false
}

// StartVerification creates (or resumes) the user's KYC case and asks the
// provider to open a verification. Safe to retry: the case is unique per
// user in flight, the provider call is keyed by the case id, and the
// Idempotency-Key makes replays return the same case (spec §21).
func (s *Service) StartVerification(ctx context.Context, in StartInput) (*StartResult, error) {
	prov, err := s.provider()
	if err != nil {
		return nil, err
	}
	country, residence := cleanCountry(in.Country), cleanCountry(in.ResidenceCountry)
	var (
		kc       *KYCCase
		replayed bool
		created  bool
	)

	// ---- tx 1: create/find the case ----
	err = s.store.WithTx(ctx, func(tx Tx) error {
		if err := tx.LockUser(ctx, in.UserID); err != nil {
			return err
		}
		resID, replay, err := claim(ctx, tx, "kyc.start", in.UserID, in.IdempotencyKey, in.UserID.String(), country, residence)
		if err != nil {
			return err
		}
		if replay && resID != nil {
			c, err := tx.GetCaseForUpdate(ctx, *resID)
			if err != nil {
				return err
			}
			kc, replayed = c, true
			return nil
		}
		now := s.now()
		cases, err := tx.CurrentCasesForUpdate(ctx, in.UserID)
		if err != nil {
			return err
		}
		for _, c := range cases {
			if c.Status.InFlight() {
				kc = c // resume the in-flight case (double start / retry)
				return tx.SetIdempotencyResource(ctx, "kyc.start", in.UserID, strings.TrimSpace(in.IdempotencyKey), c.ID)
			}
		}
		st := EffectiveStatus(cases, now)
		if st == KYCSuspended || (st == KYCVerified && !s.inRenewalWindow(cases, now)) {
			return ErrRenewalNotAllowed
		}
		// Close out stale terminal cases; expire a lapsed VERIFIED first.
		for _, c := range cases {
			if c.Status == KYCVerified && c.ExpiresAt != nil && !c.ExpiresAt.After(now) {
				if err := s.applyTransition(ctx, tx, c, KYCExpired, Actor{Type: ActorSystem, Ref: "kyc-start", RequestID: in.Actor.RequestID}, "EXPIRED_AT_RENEWAL", "vencido al iniciar renovación", now, nil); err != nil {
					return err
				}
			}
			if c.Status == KYCRejected || c.Status == KYCExpired {
				t := now
				c.SupersededAt = &t
				if err := tx.UpdateCase(ctx, c); err != nil {
					return err
				}
			}
		}
		nc := &KYCCase{
			ID: uuid.New(), UserID: in.UserID, Status: KYCNotStarted, Provider: prov.Name(),
			Country: country, ResidenceCountry: residence, RiskLevel: RiskLow, Version: 1,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.InsertCase(ctx, nc); err != nil {
			return err
		}
		if err := tx.InsertHistory(ctx, &StatusHistory{
			ID: uuid.New(), CaseID: nc.ID, UserID: in.UserID, NewStatus: KYCNotStarted,
			ActorType: ActorUser, ActorUserID: &in.UserID, ReasonCode: "KYC_START",
			RequestID: in.Actor.RequestID, CreatedAt: now,
		}); err != nil {
			return err
		}
		kc, created = nc, true
		return tx.SetIdempotencyResource(ctx, "kyc.start", in.UserID, strings.TrimSpace(in.IdempotencyKey), nc.ID)
	})
	if err != nil {
		if errors.Is(err, ErrActiveCaseExists) {
			return nil, ErrConcurrentUpdate
		}
		return nil, err
	}
	if kc.Status != KYCNotStarted {
		return &StartResult{Case: kc, Replayed: replayed || !created}, nil
	}

	// ---- provider call (outside any DB transaction) ----
	res, perr := prov.CreateVerification(ctx, CreateVerificationInput{
		UserID: in.UserID, CaseID: kc.ID, IdempotencyKey: kc.ID.String(),
		Country: kc.Country, ResidenceCountry: kc.ResidenceCountry,
	})
	if perr != nil || res == nil || strings.TrimSpace(res.ProviderReference) == "" {
		// The case stays NOT_STARTED: a retry resumes it. Provider error
		// details are deliberately not propagated (no info leak).
		return nil, ErrProviderUnavailable
	}

	// ---- tx 2: NOT_STARTED -> PENDING ----
	err = s.store.WithTx(ctx, func(tx Tx) error {
		c, err := tx.GetCaseForUpdate(ctx, kc.ID)
		if err != nil {
			return err
		}
		if c.Status != KYCNotStarted { // a concurrent start already advanced it
			kc = c
			return nil
		}
		now := s.now()
		c.Provider = prov.Name()
		c.ProviderReference = res.ProviderReference
		c.StartedAt = &now
		if err := s.applyTransition(ctx, tx, c, KYCPending, Actor{Type: ActorUser, UserID: &in.UserID, RequestID: in.Actor.RequestID}, "PROVIDER_VERIFICATION_CREATED", "", now, nil); err != nil {
			return err
		}
		kc = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.audit(ctx, auditEvent{Actor: Actor{Type: ActorUser, UserID: &in.UserID, IP: in.Actor.IP, RequestID: in.Actor.RequestID},
		Action: "KYC_STARTED", Resource: "kyc_case", ResourceID: &kc.ID, UserID: in.UserID,
		OldState: string(KYCNotStarted), NewState: string(kc.Status), Extra: map[string]any{"provider": prov.Name()}})
	return &StartResult{Case: kc, RedirectURL: res.RedirectURL, Replayed: replayed}, nil
}

// ---------------- transitions (shared by user/admin/provider/worker) ----------------

// transitionExtras carries optional data that accompanies a transition.
type transitionExtras struct {
	ExpiresAt *time.Time
	Profile   *ProviderEvent // country/document metadata (already sanitized on apply)
}

// applyTransition is the ONLY place a case status changes. It validates the
// state machine, mutates the case with optimistic-version protection, writes
// the immutable history row, and supersedes prior settled cases when needed.
// The caller MUST hold the case's row lock (Get*ForUpdate) inside tx.
func (s *Service) applyTransition(ctx context.Context, tx Tx, c *KYCCase, to KYCStatus, actor Actor,
	reasonCode, reason string, now time.Time, x *transitionExtras) error {
	from := c.Status
	if err := ValidateTransition(from, to); err != nil {
		return err
	}
	if reasonCode != "" && !reCode.MatchString(reasonCode) {
		reasonCode = "INVALID_CODE"
	}

	// Two settled (VERIFIED/SUSPENDED) cases can't coexist: supersede the
	// older VERIFIED one when this case becomes VERIFIED or SUSPENDED.
	if to == KYCVerified || to == KYCSuspended {
		others, err := tx.CurrentCasesForUpdate(ctx, c.UserID)
		if err != nil {
			return err
		}
		for _, o := range others {
			if o.ID != c.ID && o.Status == KYCVerified {
				t := now
				o.SupersededAt = &t
				if err := tx.UpdateCase(ctx, o); err != nil {
					return err
				}
			}
		}
	}

	switch to {
	case KYCPending:
		if c.SubmittedAt == nil {
			t := now
			c.SubmittedAt = &t
		}
	case KYCInReview, KYCSuspended:
		c.ReviewReasonCode = reasonCode
	case KYCVerified:
		t := now
		c.VerifiedAt = &t
		c.RejectionReasonCode = ""
		c.ReviewReasonCode = reasonCode
		if x != nil && x.ExpiresAt != nil {
			c.ExpiresAt = x.ExpiresAt
		} else if s.cfg.KYCDefaultValidity > 0 {
			e := now.Add(s.cfg.KYCDefaultValidity)
			c.ExpiresAt = &e
		}
	case KYCRejected:
		t := now
		c.RejectedAt = &t
		c.RejectionReasonCode = reasonCode
	}
	if x != nil && x.Profile != nil {
		p := x.Profile
		if v := cleanCountry(p.Country); v != "" {
			c.Country = v
		}
		if v := cleanCountry(p.ResidenceCountry); v != "" {
			c.ResidenceCountry = v
		}
		if v := cleanCountry(p.DocumentCountry); v != "" {
			c.DocumentCountry = v
		}
		if reDocType.MatchString(p.DocumentType) {
			c.DocumentType = p.DocumentType
		}
		if reHex64.MatchString(p.DocumentHash) {
			c.DocumentHash = p.DocumentHash
		}
	}
	c.Status = to
	if err := tx.UpdateCase(ctx, c); err != nil {
		return err
	}
	h := &StatusHistory{
		ID: uuid.New(), CaseID: c.ID, UserID: c.UserID, OldStatus: from, NewStatus: to,
		ActorType: actor.Type, ActorUserID: actor.UserID, ActorRef: actor.Ref,
		ReasonCode: reasonCode, Reason: reason, RequestID: actor.RequestID, CreatedAt: now,
	}
	return tx.InsertHistory(ctx, h)
}

// ---------------- admin ----------------

type AdminKYCView struct {
	UserID          uuid.UUID
	EffectiveStatus KYCStatus
	Cases           []*KYCCase
	History         []*StatusHistory
	Risk            *RiskProfile
	ActiveHolds     []*Hold
	OpenFlags       []*Flag
}

// AdminGet is read-only compliance visibility (reviewer or admin).
func (s *Service) AdminGet(ctx context.Context, actor Actor, userID uuid.UUID) (*AdminKYCView, error) {
	if !actor.IsReviewer() {
		return nil, ErrForbidden
	}
	cases, err := s.store.AllCases(ctx, userID)
	if err != nil {
		return nil, err
	}
	current, err := s.store.CurrentCases(ctx, userID)
	if err != nil {
		return nil, err
	}
	hist, err := s.store.History(ctx, userID, 200)
	if err != nil {
		return nil, err
	}
	risk, err := s.store.RiskProfileOf(ctx, userID)
	if err != nil {
		return nil, err
	}
	holds, err := s.store.ActiveHolds(ctx, userID)
	if err != nil {
		return nil, err
	}
	flags, err := s.store.OpenFlags(ctx, userID, 100)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, auditEvent{Actor: actor, Action: "KYC_ADMIN_VIEWED", Resource: "kyc_case", UserID: userID})
	return &AdminKYCView{UserID: userID, EffectiveStatus: EffectiveStatus(current, s.now()),
		Cases: cases, History: hist, Risk: risk, ActiveHolds: holds, OpenFlags: flags}, nil
}

type TransitionInput struct {
	Actor          Actor
	UserID         uuid.UUID
	CaseID         *uuid.UUID // optional; otherwise the case for which the move is legal
	To             KYCStatus
	ReasonCode     string
	Reason         string
	IdempotencyKey string
}

// Review moves PENDING -> IN_REVIEW (reviewer or admin).
func (s *Service) Review(ctx context.Context, actor Actor, userID uuid.UUID, caseID *uuid.UUID, reasonCode, reason, key string) (*KYCCase, error) {
	return s.Transition(ctx, TransitionInput{Actor: actor, UserID: userID, CaseID: caseID, To: KYCInReview, ReasonCode: reasonCode, Reason: reason, IdempotencyKey: key})
}
func (s *Service) Approve(ctx context.Context, actor Actor, userID uuid.UUID, caseID *uuid.UUID, reasonCode, reason, key string) (*KYCCase, error) {
	return s.Transition(ctx, TransitionInput{Actor: actor, UserID: userID, CaseID: caseID, To: KYCVerified, ReasonCode: reasonCode, Reason: reason, IdempotencyKey: key})
}
func (s *Service) Reject(ctx context.Context, actor Actor, userID uuid.UUID, caseID *uuid.UUID, reasonCode, reason, key string) (*KYCCase, error) {
	return s.Transition(ctx, TransitionInput{Actor: actor, UserID: userID, CaseID: caseID, To: KYCRejected, ReasonCode: reasonCode, Reason: reason, IdempotencyKey: key})
}
func (s *Service) Suspend(ctx context.Context, actor Actor, userID uuid.UUID, caseID *uuid.UUID, reasonCode, reason, key string) (*KYCCase, error) {
	return s.Transition(ctx, TransitionInput{Actor: actor, UserID: userID, CaseID: caseID, To: KYCSuspended, ReasonCode: reasonCode, Reason: reason, IdempotencyKey: key})
}

func (s *Service) authorizeTransition(actor Actor, from, to KYCStatus) error {
	// Reviewers may only move PENDING <-> IN_REVIEW; every decision
	// (approve/reject/suspend/reinstate) is compliance_admin.
	if (from == KYCPending && to == KYCInReview) || (from == KYCInReview && to == KYCPending) {
		if actor.IsReviewer() {
			return nil
		}
		return ErrForbidden
	}
	if actor.IsComplianceAdmin() {
		return nil
	}
	return ErrForbidden
}

// Transition is the single manual entry point. It requires actor, reason,
// idempotency key and a legal transition; it locks the case row, so two
// admins (or admin + webhook + worker) can never both win.
func (s *Service) Transition(ctx context.Context, in TransitionInput) (*KYCCase, error) {
	if !in.Actor.IsReviewer() {
		return nil, ErrForbidden
	}
	if in.Actor.UserID == nil {
		return nil, ErrForbidden
	}
	if *in.Actor.UserID == in.UserID {
		return nil, ErrSelfAction // nobody edits their own KYC
	}
	reason, err := validReason(in.Reason)
	if err != nil {
		return nil, err
	}
	if in.To == KYCRejected && in.ReasonCode == "" {
		return nil, ErrInvalidArgument
	}
	if in.ReasonCode != "" && !reCode.MatchString(in.ReasonCode) {
		return nil, ErrInvalidArgument
	}

	var out *KYCCase
	var oldState KYCStatus
	replayed := false
	err = s.store.WithTx(ctx, func(tx Tx) error {
		if err := tx.LockUser(ctx, in.UserID); err != nil {
			return err
		}
		caseStr := ""
		if in.CaseID != nil {
			caseStr = in.CaseID.String()
		}
		resID, replay, err := claim(ctx, tx, "kyc.transition", *in.Actor.UserID, in.IdempotencyKey,
			in.UserID.String(), caseStr, string(in.To), in.ReasonCode, reason)
		if err != nil {
			return err
		}
		if replay && resID != nil {
			c, err := tx.GetCaseForUpdate(ctx, *resID)
			if err != nil {
				return err
			}
			out, replayed = c, true
			return nil
		}
		cases, err := tx.CurrentCasesForUpdate(ctx, in.UserID)
		if err != nil {
			return err
		}
		var target *KYCCase
		for _, c := range cases {
			if in.CaseID != nil && c.ID != *in.CaseID {
				continue
			}
			if validateAdminTransition(c.Status, in.To) == nil {
				target = c
				break
			}
		}
		if target == nil {
			if in.CaseID != nil {
				// Distinguish "not this user's case" (IDOR) from "bad state".
				found := false
				for _, c := range cases {
					if c.ID == *in.CaseID {
						found = true
					}
				}
				if !found {
					return ErrCaseNotFound
				}
			}
			return ErrInvalidTransition
		}
		if err := s.authorizeTransition(in.Actor, target.Status, in.To); err != nil {
			return err
		}
		oldState = target.Status
		if err := s.applyTransition(ctx, tx, target, in.To, in.Actor, in.ReasonCode, reason, s.now(), nil); err != nil {
			return err
		}
		out = target
		return tx.SetIdempotencyResource(ctx, "kyc.transition", *in.Actor.UserID, strings.TrimSpace(in.IdempotencyKey), target.ID)
	})
	if err != nil {
		return nil, err
	}
	if !replayed {
		s.audit(ctx, auditEvent{Actor: in.Actor, Action: "KYC_" + string(in.To), Resource: "kyc_case", ResourceID: &out.ID,
			UserID: in.UserID, OldState: string(oldState), NewState: string(in.To), Reason: reason,
			Extra: map[string]any{"reason_code": in.ReasonCode}})
	}
	return out, nil
}

// ---------------- expiration ----------------

// ExpireDue moves VERIFIED cases whose expires_at passed to EXPIRED.
// Idempotent and duplicate-safe: rows are taken with FOR UPDATE SKIP
// LOCKED and re-validated by the state machine, so two workers or a
// concurrent manual review can never double-apply.
func (s *Service) ExpireDue(ctx context.Context, batch int) (int, error) {
	if batch <= 0 {
		batch = 100
	}
	type done struct{ id, user uuid.UUID }
	var applied []done
	err := s.store.WithTx(ctx, func(tx Tx) error {
		now := s.now()
		due, err := tx.DueForExpiryForUpdate(ctx, now, batch)
		if err != nil {
			return err
		}
		for _, c := range due {
			if c.Status != KYCVerified || c.SupersededAt != nil {
				continue
			}
			if err := s.applyTransition(ctx, tx, c, KYCExpired, Actor{Type: ActorSystem, Ref: "kyc-expiry-worker"}, "KYC_EXPIRED", "vigencia vencida", now, nil); err != nil {
				return err
			}
			applied = append(applied, done{c.ID, c.UserID})
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, d := range applied {
		id := d.id
		s.audit(ctx, auditEvent{Actor: Actor{Type: ActorSystem, Ref: "kyc-expiry-worker"}, Action: "KYC_EXPIRED",
			Resource: "kyc_case", ResourceID: &id, UserID: d.user, OldState: string(KYCVerified), NewState: string(KYCExpired), Reason: "vigencia vencida"})
	}
	return len(applied), nil
}
