package compliance_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/compliance"
	"korzadi-wallet/backend/internal/application/security"
)

func TestFlag_CreateDedupeResolve(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	rev, adm := reviewer(), cAdmin()
	in := compliance.ManualFlagInput{Actor: rev, UserID: u, Type: compliance.FlagVelocity, Severity: compliance.RiskMedium, ReasonCode: "HIGH_FREQUENCY", Description: "frecuencia inusual; requiere revisión"}
	f, err := e.svc.CreateManualFlag(context.Background(), in)
	if err != nil || f.Status != compliance.FlagOpen || f.Source != compliance.SourceManual {
		t.Fatalf("%v %+v", err, f)
	}
	f2, err := e.svc.CreateManualFlag(context.Background(), in) // retry: deduped
	if err != nil || f2.ID != f.ID || len(e.store.AllFlags(u)) != 1 {
		t.Fatal("el flag abierto debe deduplicarse")
	}
	// validation
	in.Type = "BOGUS"
	_, err = e.svc.CreateManualFlag(context.Background(), in)
	mustErr(t, err, compliance.ErrInvalidArgument)

	// a reviewer cannot resolve; only compliance_admin
	_, err = e.svc.ResolveFlag(context.Background(), rev, f.ID, false, "ok", key())
	mustErr(t, err, compliance.ErrForbidden)

	k := key()
	r, err := e.svc.ResolveFlag(context.Background(), adm, f.ID, false, "revisado, sin hallazgos", k)
	if err != nil || r.Status != compliance.FlagResolved || r.ResolvedAt == nil || r.ResolvedBy == nil || r.ResolutionReason == "" {
		t.Fatalf("%v %+v", err, r)
	}
	r2, err := e.svc.ResolveFlag(context.Background(), adm, f.ID, false, "revisado, sin hallazgos", k) // replay
	if err != nil || r2.ID != r.ID {
		t.Fatalf("replay: %v", err)
	}
	_, err = e.svc.ResolveFlag(context.Background(), adm, f.ID, false, "otra vez", key())
	mustErr(t, err, compliance.ErrFlagNotOpen)
	_, err = e.svc.ResolveFlag(context.Background(), adm, uuid.New(), false, "x", key())
	mustErr(t, err, compliance.ErrFlagNotFound)
	_, err = e.svc.ResolveFlag(context.Background(), adm, f.ID, false, "", key())
	mustErr(t, err, compliance.ErrReasonRequired)
}

func TestFlag_AutomatedAndAMLDisabled(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	f, err := e.svc.RaiseAutomatedFlag(context.Background(), u, compliance.FlagHighValue, compliance.RiskHigh, "OVER_THRESHOLD", "monto sobre el umbral interno")
	if err != nil || f.Source != compliance.SourceAutomated {
		t.Fatal(err)
	}
	e2 := newEnv(t, func(c *compliance.Config) { c.AMLEnabled = false })
	_, err = e2.svc.RaiseAutomatedFlag(context.Background(), u, compliance.FlagHighValue, compliance.RiskHigh, "X", "")
	mustErr(t, err, compliance.ErrAMLDisabled)
}

func TestHold_PlaceReleaseAffectsAuthorizationOnly(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.verified(u)
	ctx := context.Background()
	if err := e.eng.CheckOperation(ctx, security.OpWithdrawal, u, nil); err != nil {
		t.Fatalf("usuario VERIFIED sin hold debe poder retirar: %v", err)
	}
	rev, adm := reviewer(), cAdmin()
	_, err := e.svc.PlaceHold(ctx, compliance.PlaceHoldInput{Actor: rev, UserID: u, Type: compliance.HoldCompliance, ReasonCode: "REVIEW", Reason: "x", IdempotencyKey: key()})
	mustErr(t, err, compliance.ErrForbidden)

	h, err := e.svc.PlaceHold(ctx, compliance.PlaceHoldInput{Actor: adm, UserID: u, Type: compliance.HoldCompliance, ReasonCode: "REVIEW", Reason: "revisión interna", IdempotencyKey: key()})
	if err != nil || h.Status != compliance.HoldActive {
		t.Fatal(err)
	}
	h2, err := e.svc.PlaceHold(ctx, compliance.PlaceHoldInput{Actor: adm, UserID: u, Type: compliance.HoldCompliance, ReasonCode: "REVIEW", Reason: "revisión interna", IdempotencyKey: key()})
	if err != nil || h2.ID != h.ID || len(e.store.AllHolds(u)) != 1 {
		t.Fatal("no debe duplicarse un hold activo del mismo tipo")
	}
	for _, op := range []security.OperationType{security.OpWithdrawal, security.OpTransfer} {
		err := e.eng.CheckOperation(ctx, op, u, nil)
		mustErr(t, err, security.ErrComplianceRestricted)
	}
	// operations that are not user outflows (deposits/sweeps/gas) are untouched
	for _, op := range []security.OperationType{security.OpSweep, security.OpGasRefill, security.OpAdminOperation, "DEPOSIT"} {
		if err := e.eng.CheckOperation(ctx, op, u, nil); err != nil {
			t.Fatalf("%s no debe bloquearse por un hold: %v", op, err)
		}
	}
	// the hold never changes KYC state
	if e.status(u) != compliance.KYCVerified {
		t.Fatal("el hold no debe cambiar el estado KYC")
	}

	k := key()
	rel, err := e.svc.ReleaseHold(ctx, adm, h.ID, "revisión concluida", k)
	if err != nil || rel.Status != compliance.HoldReleased || rel.ReleasedAt == nil || rel.ReleasedBy == nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ReleaseHold(ctx, adm, h.ID, "revisión concluida", k); err != nil {
		t.Fatalf("replay idempotente: %v", err)
	}
	_, err = e.svc.ReleaseHold(ctx, adm, h.ID, "otra", key())
	mustErr(t, err, compliance.ErrHoldAlreadyReleased)
	_, err = e.svc.ReleaseHold(ctx, rev, h.ID, "x", key())
	mustErr(t, err, compliance.ErrForbidden)
	if err := e.eng.CheckOperation(ctx, security.OpWithdrawal, u, nil); err != nil {
		t.Fatalf("liberado el hold debe poder retirar: %v", err)
	}
}

func TestHold_NotOnSelfAndInvalidType(t *testing.T) {
	e := newEnv(t)
	adm := cAdmin()
	_, err := e.svc.PlaceHold(context.Background(), compliance.PlaceHoldInput{Actor: adm, UserID: *adm.UserID, Type: compliance.HoldCompliance, ReasonCode: "R", Reason: "x", IdempotencyKey: key()})
	mustErr(t, err, compliance.ErrSelfAction)
	_, err = e.svc.PlaceHold(context.Background(), compliance.PlaceHoldInput{Actor: adm, UserID: uuid.New(), Type: "BOGUS", ReasonCode: "R", Reason: "x", IdempotencyKey: key()})
	mustErr(t, err, compliance.ErrInvalidArgument)
}

func TestRisk_Transitions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := uuid.New()
	sys := compliance.Actor{Type: compliance.ActorSystem, Ref: "aml-detector"}

	p, err := e.svc.SetRiskLevel(ctx, compliance.SetRiskInput{Actor: sys, UserID: u, Level: compliance.RiskHigh, ReasonCodes: []string{"VELOCITY"}})
	if err != nil || p.Level != compliance.RiskHigh {
		t.Fatal(err)
	}
	// automated sources can never lower
	_, err = e.svc.SetRiskLevel(ctx, compliance.SetRiskInput{Actor: sys, UserID: u, Level: compliance.RiskLow})
	mustErr(t, err, compliance.ErrRiskDowngradeManualOnly)
	// a reviewer cannot lower either
	_, err = e.svc.SetRiskLevel(ctx, compliance.SetRiskInput{Actor: reviewer(), UserID: u, Level: compliance.RiskLow, Reason: "x", IdempotencyKey: key()})
	mustErr(t, err, compliance.ErrRiskDowngradeManualOnly)
	// compliance_admin can
	p, err = e.svc.SetRiskLevel(ctx, compliance.SetRiskInput{Actor: cAdmin(), UserID: u, Level: compliance.RiskLow, ReasonCodes: []string{"REVIEWED"}, Reason: "revisado", IdempotencyKey: key()})
	if err != nil || p.Level != compliance.RiskLow {
		t.Fatal(err)
	}
	// invalid inputs
	_, err = e.svc.SetRiskLevel(ctx, compliance.SetRiskInput{Actor: sys, UserID: u, Level: "EXTREME"})
	mustErr(t, err, compliance.ErrInvalidArgument)
	_, err = e.svc.SetRiskLevel(ctx, compliance.SetRiskInput{Actor: reviewer(), UserID: u, Level: compliance.RiskMedium, IdempotencyKey: key()})
	mustErr(t, err, compliance.ErrReasonRequired)
	_, err = e.svc.SetRiskLevel(ctx, compliance.SetRiskInput{Actor: compliance.Actor{Type: compliance.ActorUser}, UserID: u, Level: compliance.RiskLow})
	mustErr(t, err, compliance.ErrForbidden)
	if n := len(e.store.RiskHistory(u)); n != 2 {
		t.Fatalf("historial de riesgo=%d", n)
	}
}

func TestRisk_CriticalPlacesInternalHold(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.verified(u)
	sys := compliance.Actor{Type: compliance.ActorSystem, Ref: "aml-detector"}
	if _, err := e.svc.SetRiskLevel(context.Background(), compliance.SetRiskInput{Actor: sys, UserID: u, Level: compliance.RiskCritical, ReasonCodes: []string{"PATTERN"}}); err != nil {
		t.Fatal(err)
	}
	hs := e.store.AllHolds(u)
	if len(hs) != 1 || hs[0].Type != compliance.HoldHighRisk || hs[0].PlacedBy != nil {
		t.Fatalf("%+v", hs)
	}
	mustErr(t, e.eng.CheckOperation(context.Background(), security.OpWithdrawal, u, nil), security.ErrComplianceRestricted)

	e2 := newEnv(t, func(c *compliance.Config) { c.AutoHoldOnCritical = false })
	u2 := uuid.New()
	_, _ = e2.svc.SetRiskLevel(context.Background(), compliance.SetRiskInput{Actor: sys, UserID: u2, Level: compliance.RiskCritical})
	if len(e2.store.AllHolds(u2)) != 0 {
		t.Fatal("con AML_AUTO_HOLD_ON_CRITICAL=false no debe crearse hold")
	}
}
