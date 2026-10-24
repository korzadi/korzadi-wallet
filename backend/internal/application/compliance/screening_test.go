package compliance_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/compliance"
)

func TestScreening_NoMatch(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	v, err := e.svc.RequestScreening(context.Background(), reviewer(), u, compliance.ScreenSanctions, key())
	if err != nil || v.Result == nil || v.Result.Match != compliance.MatchNone || v.Result.Review != compliance.ReviewNotRequired {
		t.Fatalf("%v %+v", err, v)
	}
	if len(e.store.AllFlags(u)) != 0 || len(e.store.AllHolds(u)) != 0 {
		t.Fatal("NO_MATCH no debe generar flags ni holds")
	}
}

func TestScreening_PotentialMatchRequiresReviewNotConclusion(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.scr.Outcome = compliance.MatchPotential
	v, err := e.svc.RequestScreening(context.Background(), reviewer(), u, compliance.ScreenSanctions, key())
	if err != nil {
		t.Fatal(err)
	}
	if v.Result.Match != compliance.MatchPotential || v.Result.Review != compliance.ReviewRequired {
		t.Fatalf("%+v", v.Result)
	}
	if v.Result.Match == compliance.MatchConfirmed || v.Result.Review == compliance.ReviewConfirmed {
		t.Fatal("un POTENTIAL_MATCH jamás se convierte en confirmado automáticamente")
	}
	fl, hs := e.store.AllFlags(u), e.store.AllHolds(u)
	if len(fl) != 1 || fl[0].Type != compliance.FlagSanctionsReview || len(hs) != 1 || hs[0].Type != compliance.HoldSanctionReview {
		t.Fatalf("flags=%+v holds=%+v", fl, hs)
	}
	// only a human review decides; a reviewer cannot
	_, err = e.svc.ReviewScreening(context.Background(), reviewer(), v.Result.ID, false, "x", key())
	mustErr(t, err, compliance.ErrForbidden)
	r, err := e.svc.ReviewScreening(context.Background(), cAdmin(), v.Result.ID, false, "falso positivo", key())
	if err != nil || r.Review != compliance.ReviewCleared || r.ReviewedBy == nil {
		t.Fatal(err)
	}
	_, err = e.svc.ReviewScreening(context.Background(), cAdmin(), v.Result.ID, true, "otra", key())
	mustErr(t, err, compliance.ErrScreeningReviewed)
	// clearing does not silently release the hold: that is a separate audited decision
	if len(mustActive(t, e, u)) != 1 {
		t.Fatal("el hold debe seguir activo hasta su liberación explícita")
	}
}

func mustActive(t *testing.T, e *env, u uuid.UUID) []*compliance.Hold {
	t.Helper()
	h, err := e.store.ActiveHolds(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestScreening_ReviewRequiredAndPEP(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.scr.Outcome = compliance.MatchReviewRequired
	v, err := e.svc.RequestScreening(context.Background(), cAdmin(), u, compliance.ScreenPEP, key())
	if err != nil || v.Result.Review != compliance.ReviewRequired {
		t.Fatalf("%v", err)
	}
	fl := e.store.AllFlags(u)
	if len(fl) != 1 || fl[0].Type != compliance.FlagManualReview {
		t.Fatalf("PEP debe generar revisión manual: %+v", fl)
	}
	if len(e.store.AllHolds(u)) != 0 {
		t.Fatal("PEP no coloca hold automático")
	}
}

func TestScreening_AutoHoldCanBeDisabled(t *testing.T) {
	e := newEnv(t, func(c *compliance.Config) { c.SanctionsAutoHold = false })
	u := uuid.New()
	e.scr.Outcome = compliance.MatchPotential
	if _, err := e.svc.RequestScreening(context.Background(), reviewer(), u, compliance.ScreenSanctions, key()); err != nil {
		t.Fatal(err)
	}
	if len(e.store.AllHolds(u)) != 0 || len(e.store.AllFlags(u)) != 1 {
		t.Fatal("solo flag, sin hold")
	}
}

func TestScreening_ProviderFailureAndRetry(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.scr.FailTimes = 1
	k := key()
	_, err := e.svc.RequestScreening(context.Background(), reviewer(), u, compliance.ScreenSanctions, k)
	mustErr(t, err, compliance.ErrProviderUnavailable)
	if n, _ := e.svc.RetryFailedScreenings(context.Background(), 10); n != 0 {
		t.Fatal("todavía dentro del backoff")
	}
	e.clock.Advance(time.Hour)
	if n, err := e.svc.RetryFailedScreenings(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("retry: %d %v", n, err)
	}
	res, _ := e.store.ScreeningResults(context.Background(), u, 10)
	if len(res) != 1 || res[0].Match != compliance.MatchNone {
		t.Fatal("el retry debe completar el screening")
	}
	calls := e.scr.Calls
	// same idempotency key afterwards: stored result, no new provider call
	v, err := e.svc.RequestScreening(context.Background(), reviewer(), u, compliance.ScreenSanctions, k)
	if err != nil || v.Result == nil || e.scr.Calls != calls || e.store.ScreeningRequestCount() != 1 {
		t.Fatalf("idempotencia: %v calls=%d", err, e.scr.Calls)
	}
	// a second worker pass finds nothing (idempotent)
	e.clock.Advance(time.Hour)
	if n, _ := e.svc.RetryFailedScreenings(context.Background(), 10); n != 0 {
		t.Fatal("no debe reintentar lo ya completado")
	}
}

func TestScreening_ExhaustedRaisesProviderAlert(t *testing.T) {
	e := newEnv(t) // max attempts = 3
	u := uuid.New()
	e.scr.FailTimes = 100
	_, err := e.svc.RequestScreening(context.Background(), reviewer(), u, compliance.ScreenSanctions, key())
	mustErr(t, err, compliance.ErrProviderUnavailable)
	for i := 0; i < 2; i++ {
		e.clock.Advance(time.Hour)
		_, _ = e.svc.RetryFailedScreenings(context.Background(), 10)
	}
	e.clock.Advance(time.Hour)
	if n, _ := e.svc.RetryFailedScreenings(context.Background(), 10); n != 0 {
		t.Fatal("agotado: no hay más reintentos")
	}
	fl := e.store.AllFlags(u)
	if len(fl) != 1 || fl[0].Type != compliance.FlagProviderAlert {
		t.Fatalf("%+v", fl)
	}
	if e.scr.Calls != 3 {
		t.Fatalf("intentos=%d", e.scr.Calls)
	}
}

func TestScreening_UnavailableAndForbidden(t *testing.T) {
	e := newEnv(t, func(c *compliance.Config) { c.SanctionsEnabled = false })
	_, err := e.svc.RequestScreening(context.Background(), reviewer(), uuid.New(), compliance.ScreenSanctions, key())
	mustErr(t, err, compliance.ErrScreeningUnavailable)

	e2 := newEnv(t)
	_, err = e2.svc.RequestScreening(context.Background(), compliance.Actor{Type: compliance.ActorUser}, uuid.New(), compliance.ScreenSanctions, key())
	mustErr(t, err, compliance.ErrForbidden)
	_, err = e2.svc.RequestScreening(context.Background(), reviewer(), uuid.New(), "BOGUS", key())
	mustErr(t, err, compliance.ErrInvalidArgument)
	_, err = e2.svc.RequestScreening(context.Background(), reviewer(), uuid.New(), compliance.ScreenAdverseMedia, key())
	mustErr(t, err, compliance.ErrScreeningUnavailable) // no adverse-media provider registered
}
