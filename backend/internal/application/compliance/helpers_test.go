package compliance_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/compliance"
	"korzadi-wallet/backend/internal/application/compliance/compliancetest"
)

const (
	testSecret   = "0123456789abcdef0123456789abcdef" // test-only value
	provName     = "fake"
	screenerName = "fake-screener"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	t     *testing.T
	svc   *compliance.Service
	eng   *compliance.Engine
	store *compliancetest.MemStore
	prov  *compliancetest.FakeKYCProvider
	scr   *compliancetest.FakeScreener
	audit *compliancetest.FakeAudit
	clock *fakeClock
	cfg   compliance.Config
}

func newEnv(t *testing.T, mut ...func(*compliance.Config)) *env {
	t.Helper()
	cfg := compliance.Config{
		KYCEnabled: true, KYCProvider: provName, KYCWebhookSecret: testSecret,
		WebhookTolerance: 5 * time.Minute, AMLEnabled: true, SanctionsEnabled: true, SanctionsProvider: screenerName,
		WithdrawalRequiresKYC: true, AutoHoldOnCritical: true, SanctionsAutoHold: true,
		ScreeningMaxAttempts: 3, ScreeningRetryBackoff: time.Second,
	}
	for _, m := range mut {
		m(&cfg)
	}
	e := &env{t: t, cfg: cfg, store: compliancetest.NewMemStore(), prov: compliancetest.NewFakeKYCProvider(provName),
		scr: &compliancetest.FakeScreener{NameValue: screenerName, Outcome: compliance.MatchNone}, audit: &compliancetest.FakeAudit{},
		clock: &fakeClock{t: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}}
	reg := compliance.NewRegistry()
	reg.RegisterKYC(e.prov)
	reg.RegisterScreener(compliance.ScreenSanctions, e.scr)
	reg.RegisterScreener(compliance.ScreenPEP, e.scr)
	e.svc = compliance.NewService(e.store, cfg, reg, e.audit, nil)
	e.svc.SetClock(e.clock.Now)
	e.eng = compliance.NewEngine(e.store, cfg)
	e.eng.SetClock(e.clock.Now)
	return e
}

func key() string { return uuid.NewString() }

func adminActor(roles ...string) compliance.Actor {
	id := uuid.New()
	return compliance.Actor{Type: compliance.ActorAdmin, UserID: &id, Roles: roles, RequestID: "req-" + id.String()[:8]}
}
func reviewer() compliance.Actor { return adminActor(compliance.RoleReviewer) }
func cAdmin() compliance.Actor   { return adminActor(compliance.RoleAdmin) }

func (e *env) start(user uuid.UUID) *compliance.KYCCase {
	e.t.Helper()
	r, err := e.svc.StartVerification(context.Background(), compliance.StartInput{UserID: user, IdempotencyKey: key(), Country: "AR",
		Actor: compliance.Actor{Type: compliance.ActorUser, RequestID: "req-start"}})
	if err != nil {
		e.t.Fatalf("start: %v", err)
	}
	return r.Case
}

func (e *env) hook(id, typ, ref string, occurred time.Time, reason string, exp *time.Time) (*compliance.WebhookResult, error) {
	body := compliancetest.Body(id, typ, ref, occurred, reason, exp)
	return e.svc.ProcessWebhook(context.Background(), provName, compliancetest.Sign(testSecret, e.clock.Now(), body), body)
}

func (e *env) status(user uuid.UUID) compliance.KYCStatus {
	e.t.Helper()
	v, err := e.svc.GetStatus(context.Background(), user)
	if err != nil {
		e.t.Fatalf("status: %v", err)
	}
	return v.Status
}

// verified drives a fresh user to VERIFIED through the manual path.
func (e *env) verified(user uuid.UUID) *compliance.KYCCase {
	e.t.Helper()
	e.start(user)
	if _, err := e.svc.Review(context.Background(), reviewer(), user, nil, "DOC_CHECK", "revisión", key()); err != nil {
		e.t.Fatalf("review: %v", err)
	}
	c, err := e.svc.Approve(context.Background(), cAdmin(), user, nil, "OK", "aprobado", key())
	if err != nil {
		e.t.Fatalf("approve: %v", err)
	}
	return c
}

func mustErr(t *testing.T, got, want error) {
	t.Helper()
	if !isErr(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func isErr(got, want error) bool { return errors.Is(got, want) }
