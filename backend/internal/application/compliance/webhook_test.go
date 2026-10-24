package compliance_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/compliance"
	"korzadi-wallet/backend/internal/application/compliance/compliancetest"
)

func TestWebhook_ValidSignatureApplies(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c := e.start(u)
	res, err := e.hook("e1", compliance.EvtInReview, c.ProviderReference, e.clock.Now(), "", nil)
	if err != nil || res.Processing != compliance.WebhookApplied || res.Duplicate {
		t.Fatalf("%v %+v", err, res)
	}
	if e.status(u) != compliance.KYCInReview {
		t.Fatal("estado")
	}
	if e.store.WebhookOutcome(provName, "e1") != compliance.WebhookApplied {
		t.Fatal("el evento debe persistirse con su resultado")
	}
}

func TestWebhook_InvalidSignature(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c := e.start(u)
	body := compliancetest.Body("e1", compliance.EvtApproved, c.ProviderReference, e.clock.Now(), "", nil)

	h := compliancetest.Sign("otro-secreto-otro-secreto-1234", e.clock.Now(), body) // wrong secret
	_, err := e.svc.ProcessWebhook(context.Background(), provName, h, body)
	mustErr(t, err, compliance.ErrWebhookSignature)

	h = compliancetest.Sign(testSecret, e.clock.Now(), body) // tampered body
	tampered := compliancetest.Body("e1", compliance.EvtApproved, c.ProviderReference, e.clock.Now(), "X", nil)
	_, err = e.svc.ProcessWebhook(context.Background(), provName, h, tampered)
	mustErr(t, err, compliance.ErrWebhookSignature)

	_, err = e.svc.ProcessWebhook(context.Background(), provName, map[string]string{}, body) // no signature
	mustErr(t, err, compliance.ErrWebhookSignature)

	if e.status(u) != compliance.KYCPending || e.store.WebhookCount() != 0 {
		t.Fatal("un webhook no autenticado no debe cambiar estado ni persistirse")
	}
}

func TestWebhook_ReplayWindow(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c := e.start(u)
	body := compliancetest.Body("e1", compliance.EvtApproved, c.ProviderReference, e.clock.Now(), "", nil)
	old := compliancetest.Sign(testSecret, e.clock.Now().Add(-10*time.Minute), body) // validly signed, but too old
	_, err := e.svc.ProcessWebhook(context.Background(), provName, old, body)
	mustErr(t, err, compliance.ErrWebhookReplay)
	future := compliancetest.Sign(testSecret, e.clock.Now().Add(10*time.Minute), body)
	_, err = e.svc.ProcessWebhook(context.Background(), provName, future, body)
	mustErr(t, err, compliance.ErrWebhookReplay)
	if e.status(u) != compliance.KYCPending {
		t.Fatal("un replay no debe aplicarse")
	}
}

func TestWebhook_DuplicateEvent(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c := e.start(u)
	if _, err := e.hook("e1", compliance.EvtInReview, c.ProviderReference, e.clock.Now(), "", nil); err != nil {
		t.Fatal(err)
	}
	n := e.store.HistoryCount(c.ID)
	res, err := e.hook("e1", compliance.EvtInReview, c.ProviderReference, e.clock.Now(), "", nil)
	if err != nil || !res.Duplicate {
		t.Fatalf("%v %+v", err, res)
	}
	if e.store.HistoryCount(c.ID) != n || e.store.WebhookCount() != 1 {
		t.Fatal("un duplicado no debe reaplicarse")
	}
}

func TestWebhook_OutOfOrder(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c := e.start(u)
	t0 := e.clock.Now()
	if _, err := e.hook("e2", compliance.EvtInReview, c.ProviderReference, t0.Add(2*time.Second), "", nil); err != nil {
		t.Fatal(err)
	}
	// an OLDER event arrives late: must not be applied
	res, err := e.hook("e1", compliance.EvtRejected, c.ProviderReference, t0.Add(1*time.Second), "DOC", nil)
	if err != nil || res.Processing != compliance.WebhookIgnoredStale {
		t.Fatalf("%v %+v", err, res)
	}
	if e.status(u) != compliance.KYCInReview {
		t.Fatal("el evento viejo no debe pisar el estado nuevo")
	}
}

func TestWebhook_ConflictWithManualDecisionRaisesFlag(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c := e.start(u)
	_, _ = e.svc.Review(context.Background(), reviewer(), u, nil, "DOC", "rev", key())
	if _, err := e.svc.Reject(context.Background(), cAdmin(), u, nil, "MANUAL", "rechazo manual", key()); err != nil {
		t.Fatal(err)
	}
	res, err := e.hook("e1", compliance.EvtApproved, c.ProviderReference, e.clock.Now().Add(time.Minute), "", nil)
	if err != nil || res.Processing != compliance.WebhookIgnoredInvalidTransition {
		t.Fatalf("%v %+v", err, res)
	}
	if e.status(u) != compliance.KYCRejected {
		t.Fatal("la decisión manual no debe revertirse por un evento del proveedor")
	}
	fl := e.store.AllFlags(u)
	if len(fl) != 1 || fl[0].Type != compliance.FlagKYCMismatch || fl[0].Status != compliance.FlagOpen {
		t.Fatalf("debe existir un flag KYC_MISMATCH para revisión: %+v", fl)
	}
}

func TestWebhook_UnknownProviderAndDisabled(t *testing.T) {
	e := newEnv(t)
	body := compliancetest.Body("e1", compliance.EvtApproved, "x", e.clock.Now(), "", nil)
	h := compliancetest.Sign(testSecret, e.clock.Now(), body)
	_, err := e.svc.ProcessWebhook(context.Background(), "desconocido", h, body)
	mustErr(t, err, compliance.ErrUnknownProvider)

	e2 := newEnv(t, func(c *compliance.Config) { c.KYCEnabled = false })
	_, err = e2.svc.ProcessWebhook(context.Background(), provName, h, body)
	mustErr(t, err, compliance.ErrKYCDisabled)
}

func TestWebhook_UnknownReferenceIsNotPersisted(t *testing.T) {
	e := newEnv(t)
	_, err := e.hook("e1", compliance.EvtApproved, "no-existe", e.clock.Now(), "", nil)
	mustErr(t, err, compliance.ErrCaseNotFound) // non-2xx: provider retries
	if e.store.WebhookCount() != 0 {
		t.Fatal("no debe quedar deduplicado un evento que no pudimos aplicar")
	}
}

func TestWebhook_MalformedBody(t *testing.T) {
	e := newEnv(t)
	body := []byte("{no es json")
	_, err := e.svc.ProcessWebhook(context.Background(), provName, compliancetest.Sign(testSecret, e.clock.Now(), body), body)
	mustErr(t, err, compliance.ErrWebhookMalformed)
	body = compliancetest.Body("", compliance.EvtApproved, "ref", e.clock.Now(), "", nil) // no event id
	_, err = e.svc.ProcessWebhook(context.Background(), provName, compliancetest.Sign(testSecret, e.clock.Now(), body), body)
	mustErr(t, err, compliance.ErrWebhookMalformed)
}

func TestWebhook_ApprovedSetsExpiryAndProviderActor(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c := e.start(u)
	exp := e.clock.Now().Add(24 * time.Hour)
	if _, err := e.hook("e1", compliance.EvtApproved, c.ProviderReference, e.clock.Now(), "", &exp); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.store.CurrentCases(context.Background(), u)
	if cur[0].ExpiresAt == nil || !cur[0].ExpiresAt.Equal(exp) || cur[0].VerifiedAt == nil {
		t.Fatalf("%+v", cur[0])
	}
	h, _ := e.store.History(context.Background(), u, 10)
	if h[0].ActorType != compliance.ActorProvider || h[0].ActorRef != provName {
		t.Fatalf("el historial debe registrar al proveedor como actor: %+v", h[0])
	}
}
