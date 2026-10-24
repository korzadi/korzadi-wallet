package compliance

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ---------------- flags ----------------

// raiseFlagTx inserts an OPEN flag, deduplicating on (user,type,reason).
// Flags are internal review markers, never accusations.
func (s *Service) raiseFlagTx(ctx context.Context, tx Tx, f *Flag, now time.Time) (*Flag, bool, error) {
	if !f.Type.Valid() || !f.Severity.Valid() || !reCode.MatchString(f.ReasonCode) || len(f.Description) > maxAuditString {
		return nil, false, ErrInvalidArgument
	}
	f.ID = uuid.New()
	f.Status = FlagOpen
	f.CreatedAt = now
	f.Version = 1
	return tx.InsertFlag(ctx, f)
}

type ManualFlagInput struct {
	Actor       Actor
	UserID      uuid.UUID
	Type        FlagType
	Severity    RiskLevel
	ReasonCode  string
	Description string
}

// CreateManualFlag: reviewer or admin. Idempotent by dedupe of the open flag.
func (s *Service) CreateManualFlag(ctx context.Context, in ManualFlagInput) (*Flag, error) {
	if !in.Actor.IsReviewer() || in.Actor.UserID == nil {
		return nil, ErrForbidden
	}
	if !s.cfg.AMLEnabled {
		return nil, ErrAMLDisabled
	}
	var out *Flag
	var created bool
	err := s.store.WithTx(ctx, func(tx Tx) error {
		if err := tx.LockUser(ctx, in.UserID); err != nil {
			return err
		}
		f, c, err := s.raiseFlagTx(ctx, tx, &Flag{
			UserID: in.UserID, Type: in.Type, Severity: in.Severity, Source: SourceManual,
			ReasonCode: in.ReasonCode, Description: strings.TrimSpace(in.Description), CreatedBy: in.Actor.UserID,
		}, s.now())
		out, created = f, c
		return err
	})
	if err != nil {
		return nil, err
	}
	if created {
		s.audit(ctx, auditEvent{Actor: in.Actor, Action: "AML_FLAG_CREATED", Resource: "compliance_flag", ResourceID: &out.ID,
			UserID: in.UserID, NewState: string(FlagOpen), Reason: in.ReasonCode, Extra: map[string]any{"type": string(in.Type), "severity": string(in.Severity)}})
	}
	return out, nil
}

// RaiseAutomatedFlag is for internal detectors (velocity, high value, ...).
// It records a flag for REVIEW only; it takes no action against the user.
func (s *Service) RaiseAutomatedFlag(ctx context.Context, userID uuid.UUID, t FlagType, sev RiskLevel, reasonCode, desc string) (*Flag, error) {
	if !s.cfg.AMLEnabled {
		return nil, ErrAMLDisabled
	}
	var out *Flag
	var created bool
	err := s.store.WithTx(ctx, func(tx Tx) error {
		f, c, err := s.raiseFlagTx(ctx, tx, &Flag{UserID: userID, Type: t, Severity: sev, Source: SourceAutomated, ReasonCode: reasonCode, Description: desc}, s.now())
		out, created = f, c
		return err
	})
	if err != nil {
		return nil, err
	}
	if created {
		s.audit(ctx, auditEvent{Actor: Actor{Type: ActorSystem, Ref: "aml-detector"}, Action: "AML_FLAG_CREATED", Resource: "compliance_flag",
			ResourceID: &out.ID, UserID: userID, NewState: string(FlagOpen), Reason: reasonCode})
	}
	return out, nil
}

// ResolveFlag closes an OPEN flag (compliance_admin). dismiss=true marks it
// DISMISSED (not applicable) instead of RESOLVED.
func (s *Service) ResolveFlag(ctx context.Context, actor Actor, flagID uuid.UUID, dismiss bool, reason, key string) (*Flag, error) {
	if !actor.IsComplianceAdmin() || actor.UserID == nil {
		return nil, ErrForbidden
	}
	reason, err := validReason(reason)
	if err != nil {
		return nil, err
	}
	var out *Flag
	replayed := false
	err = s.store.WithTx(ctx, func(tx Tx) error {
		resID, replay, err := claim(ctx, tx, "flag.resolve", *actor.UserID, key, flagID.String(), boolStr(dismiss), reason)
		if err != nil {
			return err
		}
		f, err := tx.GetFlagForUpdate(ctx, flagID)
		if err != nil {
			return err
		}
		if replay && resID != nil {
			out, replayed = f, true
			return nil
		}
		if f.Status != FlagOpen {
			return ErrFlagNotOpen
		}
		if f.UserID == *actor.UserID {
			return ErrSelfAction
		}
		now := s.now()
		f.Status = FlagResolved
		if dismiss {
			f.Status = FlagDismissed
		}
		f.ResolvedAt, f.ResolvedBy, f.ResolutionReason = &now, actor.UserID, reason
		if err := tx.UpdateFlag(ctx, f); err != nil {
			return err
		}
		out = f
		return tx.SetIdempotencyResource(ctx, "flag.resolve", *actor.UserID, strings.TrimSpace(key), f.ID)
	})
	if err != nil {
		return nil, err
	}
	if !replayed {
		s.audit(ctx, auditEvent{Actor: actor, Action: "AML_FLAG_" + string(out.Status), Resource: "compliance_flag", ResourceID: &out.ID,
			UserID: out.UserID, OldState: string(FlagOpen), NewState: string(out.Status), Reason: reason})
	}
	return out, nil
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// ---------------- holds ----------------

// placeHoldTx creates an ACTIVE hold unless one of that type already exists
// (unique index backs this). A hold restricts authorization only; it never
// moves money or touches the Ledger.
func (s *Service) placeHoldTx(ctx context.Context, tx Tx, userID uuid.UUID, t HoldType, by *uuid.UUID, reasonCode, reason string, now time.Time) (*Hold, bool, error) {
	if !t.Valid() || !reCode.MatchString(reasonCode) {
		return nil, false, ErrInvalidArgument
	}
	if ex, err := tx.ActiveHoldForUpdate(ctx, userID, t); err != nil {
		return nil, false, err
	} else if ex != nil {
		return ex, false, nil
	}
	h := &Hold{ID: uuid.New(), UserID: userID, Type: t, Status: HoldActive, ReasonCode: reasonCode, Reason: reason, PlacedBy: by, PlacedAt: now, Version: 1}
	if err := tx.InsertHold(ctx, h); err != nil {
		return nil, false, err
	}
	return h, true, nil
}

type PlaceHoldInput struct {
	Actor          Actor
	UserID         uuid.UUID
	Type           HoldType
	ReasonCode     string
	Reason         string
	IdempotencyKey string
}

// PlaceHold: compliance_admin.
func (s *Service) PlaceHold(ctx context.Context, in PlaceHoldInput) (*Hold, error) {
	if !in.Actor.IsComplianceAdmin() || in.Actor.UserID == nil {
		return nil, ErrForbidden
	}
	if !s.cfg.AMLEnabled {
		return nil, ErrAMLDisabled
	}
	if *in.Actor.UserID == in.UserID {
		return nil, ErrSelfAction
	}
	reason, err := validReason(in.Reason)
	if err != nil {
		return nil, err
	}
	var out *Hold
	created := false
	err = s.store.WithTx(ctx, func(tx Tx) error {
		if err := tx.LockUser(ctx, in.UserID); err != nil {
			return err
		}
		resID, replay, err := claim(ctx, tx, "hold.place", *in.Actor.UserID, in.IdempotencyKey, in.UserID.String(), string(in.Type), in.ReasonCode, reason)
		if err != nil {
			return err
		}
		if replay && resID != nil { // same key: return what it produced, even if released since
			h, err := tx.GetHoldForUpdate(ctx, *resID)
			out = h
			return err
		}
		h, c, err := s.placeHoldTx(ctx, tx, in.UserID, in.Type, in.Actor.UserID, in.ReasonCode, reason, s.now())
		out, created = h, c
		if err != nil {
			return err
		}
		return tx.SetIdempotencyResource(ctx, "hold.place", *in.Actor.UserID, strings.TrimSpace(in.IdempotencyKey), h.ID)
	})
	if err != nil {
		return nil, err
	}
	if created {
		s.audit(ctx, auditEvent{Actor: in.Actor, Action: "COMPLIANCE_HOLD_PLACED", Resource: "compliance_hold", ResourceID: &out.ID,
			UserID: in.UserID, NewState: string(HoldActive), Reason: reason, Extra: map[string]any{"hold_type": string(in.Type), "reason_code": in.ReasonCode}})
	}
	return out, nil
}

// ReleaseHold: compliance_admin. Never on one's own account.
func (s *Service) ReleaseHold(ctx context.Context, actor Actor, holdID uuid.UUID, reason, key string) (*Hold, error) {
	if !actor.IsComplianceAdmin() || actor.UserID == nil {
		return nil, ErrForbidden
	}
	reason, err := validReason(reason)
	if err != nil {
		return nil, err
	}
	var out *Hold
	replayed := false
	err = s.store.WithTx(ctx, func(tx Tx) error {
		resID, replay, err := claim(ctx, tx, "hold.release", *actor.UserID, key, holdID.String(), reason)
		if err != nil {
			return err
		}
		h, err := tx.GetHoldForUpdate(ctx, holdID)
		if err != nil {
			return err
		}
		if replay && resID != nil {
			out, replayed = h, true
			return nil
		}
		if h.Status != HoldActive {
			return ErrHoldAlreadyReleased
		}
		if h.UserID == *actor.UserID {
			return ErrSelfAction
		}
		now := s.now()
		h.Status = HoldReleased
		h.ReleasedAt, h.ReleasedBy, h.ReleaseReason = &now, actor.UserID, reason
		if err := tx.UpdateHold(ctx, h); err != nil {
			return err
		}
		out = h
		return tx.SetIdempotencyResource(ctx, "hold.release", *actor.UserID, strings.TrimSpace(key), h.ID)
	})
	if err != nil {
		return nil, err
	}
	if !replayed {
		s.audit(ctx, auditEvent{Actor: actor, Action: "COMPLIANCE_HOLD_RELEASED", Resource: "compliance_hold", ResourceID: &out.ID,
			UserID: out.UserID, OldState: string(HoldActive), NewState: string(HoldReleased), Reason: reason, Extra: map[string]any{"hold_type": string(out.Type)}})
	}
	return out, nil
}

// ---------------- risk ----------------

type SetRiskInput struct {
	Actor          Actor // Type SYSTEM/PROVIDER = automated; ADMIN = manual
	UserID         uuid.UUID
	Level          RiskLevel
	ReasonCodes    []string
	Reason         string
	IdempotencyKey string // required for manual changes
}

// SetRiskLevel records an internal risk level. Rules:
//   - automated sources may only raise (or keep) the level;
//   - a reviewer may raise; lowering requires compliance_admin;
//   - reaching CRITICAL places a HIGH_RISK_REVIEW hold when
//     AML_AUTO_HOLD_ON_CRITICAL is on (an internal control, not a finding).
func (s *Service) SetRiskLevel(ctx context.Context, in SetRiskInput) (*RiskProfile, error) {
	if !s.cfg.AMLEnabled {
		return nil, ErrAMLDisabled
	}
	if !in.Level.Valid() {
		return nil, ErrInvalidArgument
	}
	for _, c := range in.ReasonCodes {
		if !reCode.MatchString(c) {
			return nil, ErrInvalidArgument
		}
	}
	manual := in.Actor.Type == ActorAdmin
	source := "AUTOMATED"
	if in.Actor.Type == ActorProvider {
		source = "PROVIDER"
	}
	if manual {
		source = "MANUAL"
		if !in.Actor.IsReviewer() || in.Actor.UserID == nil {
			return nil, ErrForbidden
		}
		if *in.Actor.UserID == in.UserID {
			return nil, ErrSelfAction
		}
		if _, err := validReason(in.Reason); err != nil {
			return nil, err
		}
	} else if in.Actor.Type != ActorSystem && in.Actor.Type != ActorProvider {
		return nil, ErrForbidden
	}
	var out *RiskProfile
	var old RiskLevel
	changed := false
	err := s.store.WithTx(ctx, func(tx Tx) error {
		if err := tx.LockUser(ctx, in.UserID); err != nil {
			return err
		}
		if manual {
			if _, _, err := claim(ctx, tx, "risk.set", *in.Actor.UserID, in.IdempotencyKey, in.UserID.String(), string(in.Level), strings.Join(in.ReasonCodes, ",")); err != nil {
				return err
			}
		}
		cur, err := tx.RiskProfileForUpdate(ctx, in.UserID)
		if err != nil {
			return err
		}
		old = RiskLow
		if cur != nil {
			old = cur.Level
		}
		if in.Level.rank() < old.rank() {
			if !manual || !in.Actor.IsComplianceAdmin() {
				return ErrRiskDowngradeManualOnly
			}
		}
		if cur != nil && cur.Level == in.Level {
			out = cur
			return nil
		}
		np := &RiskProfile{UserID: in.UserID, Level: in.Level, ReasonCodes: in.ReasonCodes, Version: 1}
		if cur != nil {
			np.Version = cur.Version
		}
		if err := tx.UpsertRiskProfile(ctx, np); err != nil {
			return err
		}
		if err := tx.InsertRiskHistory(ctx, &RiskHistoryEntry{UserID: in.UserID, OldLevel: old, NewLevel: in.Level,
			ReasonCodes: in.ReasonCodes, Source: source, ActorUserID: in.Actor.UserID, Reason: in.Reason, RequestID: in.Actor.RequestID}); err != nil {
			return err
		}
		// Mirror onto the user's current KYC cases (kyc_cases.risk_level).
		cases, err := tx.CurrentCasesForUpdate(ctx, in.UserID)
		if err != nil {
			return err
		}
		for _, c := range cases {
			c.RiskLevel = in.Level
			if err := tx.UpdateCase(ctx, c); err != nil {
				return err
			}
		}
		if in.Level == RiskCritical && s.cfg.AutoHoldOnCritical {
			if _, _, err := s.placeHoldTx(ctx, tx, in.UserID, HoldHighRisk, nil, "AUTO_CRITICAL_RISK", "nivel de riesgo interno CRITICAL: requiere revisión", s.now()); err != nil {
				return err
			}
		}
		out, changed = np, true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if changed {
		s.audit(ctx, auditEvent{Actor: in.Actor, Action: "AML_RISK_LEVEL_CHANGED", Resource: "risk_profile", UserID: in.UserID,
			OldState: string(old), NewState: string(in.Level), Reason: in.Reason, Extra: map[string]any{"source": source, "reason_codes": strings.Join(in.ReasonCodes, ",")}})
	}
	return out, nil
}
