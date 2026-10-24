package compliance_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/compliance"
)

func TestStateMachine_ValidAndInvalid(t *testing.T) {
	valid := [][2]compliance.KYCStatus{
		{compliance.KYCNotStarted, compliance.KYCPending},
		{compliance.KYCPending, compliance.KYCInReview}, {compliance.KYCPending, compliance.KYCVerified},
		{compliance.KYCPending, compliance.KYCRejected}, {compliance.KYCInReview, compliance.KYCVerified},
		{compliance.KYCInReview, compliance.KYCRejected}, {compliance.KYCInReview, compliance.KYCSuspended},
		{compliance.KYCVerified, compliance.KYCExpired}, {compliance.KYCVerified, compliance.KYCSuspended},
		{compliance.KYCSuspended, compliance.KYCInReview},
	}
	for _, p := range valid {
		if err := compliance.ValidateTransition(p[0], p[1]); err != nil {
			t.Errorf("%s -> %s debería ser válida: %v", p[0], p[1], err)
		}
	}
	invalid := [][2]compliance.KYCStatus{
		{compliance.KYCNotStarted, compliance.KYCVerified}, {compliance.KYCRejected, compliance.KYCVerified},
		{compliance.KYCRejected, compliance.KYCPending}, {compliance.KYCExpired, compliance.KYCVerified},
		{compliance.KYCVerified, compliance.KYCRejected}, {compliance.KYCVerified, compliance.KYCPending},
		{compliance.KYCPending, compliance.KYCPending}, {compliance.KYCSuspended, compliance.KYCVerified},
		{compliance.KYCStatus("BOGUS"), compliance.KYCPending}, {compliance.KYCPending, compliance.KYCStatus("")},
	}
	for _, p := range invalid {
		mustErr(t, compliance.ValidateTransition(p[0], p[1]), compliance.ErrInvalidTransition)
	}
	for _, s := range []compliance.KYCStatus{"NOT_STARTED", "PENDING", "IN_REVIEW", "VERIFIED", "REJECTED", "EXPIRED", "SUSPENDED"} {
		if !s.Valid() {
			t.Errorf("%s debería ser válido", s)
		}
	}
	if compliance.KYCStatus("verified").Valid() {
		t.Error("los estados son case-sensitive")
	}
}

func TestStart_CreatesPendingCase(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c := e.start(u)
	if c.Status != compliance.KYCPending || c.ProviderReference == "" || c.Provider != provName || c.StartedAt == nil {
		t.Fatalf("caso inesperado: %+v", c)
	}
	if e.prov.Created != 1 {
		t.Fatalf("Created=%d", e.prov.Created)
	}
	if n := e.store.HistoryCount(c.ID); n != 2 { // NOT_STARTED, PENDING
		t.Fatalf("history=%d", n)
	}
}

func TestStart_IdempotentReplayAndDoubleStart(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	k := key()
	in := compliance.StartInput{UserID: u, IdempotencyKey: k, Country: "AR"}
	r1, err := e.svc.StartVerification(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := e.svc.StartVerification(context.Background(), in)
	if err != nil || r2.Case.ID != r1.Case.ID || !r2.Replayed {
		t.Fatalf("replay: %v %+v", err, r2)
	}
	in.IdempotencyKey = key() // another request while one is in flight
	r3, err := e.svc.StartVerification(context.Background(), in)
	if err != nil || r3.Case.ID != r1.Case.ID {
		t.Fatalf("resume: %v", err)
	}
	if e.prov.Created != 1 {
		t.Fatalf("el proveedor recibió %d verificaciones", e.prov.Created)
	}
	all, _ := e.store.AllCases(context.Background(), u)
	if len(all) != 1 {
		t.Fatalf("casos=%d", len(all))
	}
	// same key, different payload
	in.IdempotencyKey, in.Country = k, "BR"
	_, err = e.svc.StartVerification(context.Background(), in)
	mustErr(t, err, compliance.ErrIdempotencyKeyReuse)
	// missing key
	_, err = e.svc.StartVerification(context.Background(), compliance.StartInput{UserID: uuid.New()})
	mustErr(t, err, compliance.ErrIdempotencyKeyRequired)
}

func TestStart_ProviderFailureThenResume(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.prov.Fail = true
	_, err := e.svc.StartVerification(context.Background(), compliance.StartInput{UserID: u, IdempotencyKey: key()})
	mustErr(t, err, compliance.ErrProviderUnavailable)
	if err.Error() != compliance.ErrProviderUnavailable.Error() {
		t.Fatal("el error no debe filtrar detalle del proveedor")
	}
	if e.status(u) != compliance.KYCNotStarted {
		t.Fatal("debe seguir NOT_STARTED")
	}
	e.prov.Fail = false
	c := e.start(u)
	if c.Status != compliance.KYCPending {
		t.Fatal("debe reanudar a PENDING")
	}
	all, _ := e.store.AllCases(context.Background(), u)
	if len(all) != 1 {
		t.Fatalf("se creó más de un caso: %d", len(all))
	}
}

func TestStart_FailsClosedWhenDisabledOrUnregistered(t *testing.T) {
	e := newEnv(t, func(c *compliance.Config) { c.KYCEnabled = false })
	_, err := e.svc.StartVerification(context.Background(), compliance.StartInput{UserID: uuid.New(), IdempotencyKey: key()})
	mustErr(t, err, compliance.ErrKYCDisabled)

	e2 := newEnv(t, func(c *compliance.Config) { c.KYCProvider = "no-registrado" })
	_, err = e2.svc.StartVerification(context.Background(), compliance.StartInput{UserID: uuid.New(), IdempotencyKey: key()})
	mustErr(t, err, compliance.ErrProviderUnavailable)
}

func TestManualReview_ApproveFlow(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c := e.verified(u)
	if c.Status != compliance.KYCVerified || c.VerifiedAt == nil {
		t.Fatalf("%+v", c)
	}
	if e.status(u) != compliance.KYCVerified {
		t.Fatal("estado efectivo")
	}
	if n := e.store.HistoryCount(c.ID); n != 4 { // NOT_STARTED, PENDING, IN_REVIEW, VERIFIED
		t.Fatalf("history=%d", n)
	}
	acts := e.audit.Actions()
	for _, want := range []string{"KYC_STARTED", "KYC_IN_REVIEW", "KYC_VERIFIED"} {
		found := false
		for _, a := range acts {
			found = found || a == want
		}
		if !found {
			t.Errorf("falta auditoría %s en %v", want, acts)
		}
	}
}

func TestManualReview_CannotApproveFromPending(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.start(u)
	_, err := e.svc.Approve(context.Background(), cAdmin(), u, nil, "OK", "x", key())
	mustErr(t, err, compliance.ErrInvalidTransition)
}

func TestManualReview_RejectThenRenew(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.start(u)
	_, _ = e.svc.Review(context.Background(), reviewer(), u, nil, "DOC", "rev", key())
	_, err := e.svc.Reject(context.Background(), cAdmin(), u, nil, "", "falta código", key())
	mustErr(t, err, compliance.ErrInvalidArgument)
	_, err = e.svc.Reject(context.Background(), cAdmin(), u, nil, "DOC_UNREADABLE", "", key())
	mustErr(t, err, compliance.ErrReasonRequired)
	c, err := e.svc.Reject(context.Background(), cAdmin(), u, nil, "DOC_UNREADABLE", "documento ilegible", key())
	if err != nil || c.Status != compliance.KYCRejected || c.RejectedAt == nil || c.RejectionReasonCode != "DOC_UNREADABLE" {
		t.Fatalf("%v %+v", err, c)
	}
	if e.status(u) != compliance.KYCRejected {
		t.Fatal("estado")
	}
	// renewal: a NEW case; the rejected one stays as history
	n := e.start(u)
	if n.ID == c.ID || n.Status != compliance.KYCPending {
		t.Fatal("debe crear un caso nuevo")
	}
	all, _ := e.store.AllCases(context.Background(), u)
	cur, _ := e.store.CurrentCases(context.Background(), u)
	if len(all) != 2 || len(cur) != 1 {
		t.Fatalf("all=%d current=%d", len(all), len(cur))
	}
	if e.store.HistoryCount(c.ID) < 4 {
		t.Fatal("el historial del caso anterior debe conservarse")
	}
}

func TestManualReview_SuspendAndNoRestart(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.start(u)
	_, _ = e.svc.Review(context.Background(), reviewer(), u, nil, "DOC", "rev", key())
	c, err := e.svc.Suspend(context.Background(), cAdmin(), u, nil, "COMPLIANCE", "suspensión por revisión", key())
	if err != nil || c.Status != compliance.KYCSuspended {
		t.Fatalf("%v", err)
	}
	if e.status(u) != compliance.KYCSuspended {
		t.Fatal("estado")
	}
	v, _ := e.svc.GetStatus(context.Background(), u)
	if v.CanStart {
		t.Fatal("un usuario suspendido no puede reiniciar KYC por su cuenta")
	}
	_, err = e.svc.StartVerification(context.Background(), compliance.StartInput{UserID: u, IdempotencyKey: key()})
	// an in-flight (IN_REVIEW->SUSPENDED is settled) case exists: start must not bypass suspension
	mustErr(t, err, compliance.ErrRenewalNotAllowed)
	// reinstatement goes through review again
	c, err = e.svc.Review(context.Background(), cAdmin(), u, nil, "REINSTATE", "reinstalar", key())
	if err != nil || c.Status != compliance.KYCInReview {
		t.Fatalf("%v", err)
	}
}

func TestManualReview_IdempotentApprove(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.start(u)
	_, _ = e.svc.Review(context.Background(), reviewer(), u, nil, "DOC", "rev", key())
	k := key()
	a := cAdmin()
	c1, err := e.svc.Transition(context.Background(), compliance.TransitionInput{Actor: a, UserID: u, To: compliance.KYCVerified, ReasonCode: "OK", Reason: "ok", IdempotencyKey: k})
	if err != nil {
		t.Fatal(err)
	}
	n := e.store.HistoryCount(c1.ID)
	c2, err := e.svc.Transition(context.Background(), compliance.TransitionInput{Actor: a, UserID: u, To: compliance.KYCVerified, ReasonCode: "OK", Reason: "ok", IdempotencyKey: k})
	if err != nil || c2.ID != c1.ID || e.store.HistoryCount(c1.ID) != n {
		t.Fatalf("replay no idempotente: %v", err)
	}
	_, err = e.svc.Transition(context.Background(), compliance.TransitionInput{Actor: a, UserID: u, To: compliance.KYCVerified, ReasonCode: "OK", Reason: "distinto", IdempotencyKey: k})
	mustErr(t, err, compliance.ErrIdempotencyKeyReuse)
	_, err = e.svc.Transition(context.Background(), compliance.TransitionInput{Actor: a, UserID: u, To: compliance.KYCSuspended, Reason: "x"})
	mustErr(t, err, compliance.ErrIdempotencyKeyRequired)
}

func TestExpiration_And_Renewal(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c := e.start(u)
	exp := e.clock.Now().Add(time.Hour)
	res, err := e.hook("evt-1", compliance.EvtApproved, c.ProviderReference, e.clock.Now(), "", &exp)
	if err != nil || res.Processing != compliance.WebhookApplied {
		t.Fatalf("%v %+v", err, res)
	}
	if e.status(u) != compliance.KYCVerified {
		t.Fatal("debe estar VERIFIED")
	}
	if n, _ := e.svc.ExpireDue(context.Background(), 10); n != 0 {
		t.Fatal("todavía no vence")
	}
	e.clock.Advance(2 * time.Hour)
	if e.status(u) != compliance.KYCExpired { // fail-safe at read time, before the worker runs
		t.Fatal("un VERIFIED vencido debe verse EXPIRED aunque el worker no haya corrido")
	}
	if n, err := e.svc.ExpireDue(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("expire: %d %v", n, err)
	}
	if n, _ := e.svc.ExpireDue(context.Background(), 10); n != 0 {
		t.Fatal("el worker debe ser idempotente")
	}
	all, _ := e.store.AllCases(context.Background(), u)
	if len(all) != 1 || all[0].Status != compliance.KYCExpired {
		t.Fatal("el caso debe conservarse como EXPIRED")
	}
	// re-verification creates a new case, old one remains in history
	n := e.start(u)
	if n.ID == c.ID || n.Status != compliance.KYCPending {
		t.Fatal("renovación")
	}
	all, _ = e.store.AllCases(context.Background(), u)
	if len(all) != 2 {
		t.Fatalf("casos=%d", len(all))
	}
}

func TestRenewalWindow(t *testing.T) {
	e := newEnv(t, func(c *compliance.Config) { c.KYCRenewalWindow = 30 * 24 * time.Hour })
	u := uuid.New()
	c := e.start(u)
	exp := e.clock.Now().Add(10 * 24 * time.Hour)
	if _, err := e.hook("e1", compliance.EvtApproved, c.ProviderReference, e.clock.Now(), "", &exp); err != nil {
		t.Fatal(err)
	}
	n := e.start(u) // inside the window: allowed while VERIFIED
	if n.ID == c.ID {
		t.Fatal("debe abrir un caso nuevo")
	}
	if e.status(u) != compliance.KYCVerified {
		t.Fatal("el estado efectivo sigue VERIFIED durante la renovación")
	}
	exp2 := e.clock.Now().Add(365 * 24 * time.Hour)
	if _, err := e.hook("e2", compliance.EvtApproved, n.ProviderReference, e.clock.Now().Add(time.Second), "", &exp2); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.store.CurrentCases(context.Background(), u)
	if len(cur) != 1 || cur[0].ID != n.ID {
		t.Fatal("el caso anterior debe quedar reemplazado (historial conservado)")
	}

	// outside the window (default config): renewal of a valid VERIFIED is refused
	e2 := newEnv(t)
	u2 := uuid.New()
	e2.verified(u2)
	_, err := e2.svc.StartVerification(context.Background(), compliance.StartInput{UserID: u2, IdempotencyKey: key()})
	mustErr(t, err, compliance.ErrRenewalNotAllowed)
}

func TestEffectiveStatus(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	mk := func(s compliance.KYCStatus, exp *time.Time) *compliance.KYCCase {
		return &compliance.KYCCase{Status: s, ExpiresAt: exp}
	}
	cases := []struct {
		name string
		in   []*compliance.KYCCase
		want compliance.KYCStatus
	}{
		{"vacío", nil, compliance.KYCNotStarted},
		{"verified", []*compliance.KYCCase{mk(compliance.KYCVerified, nil)}, compliance.KYCVerified},
		{"verified vencido", []*compliance.KYCCase{mk(compliance.KYCVerified, &past)}, compliance.KYCExpired},
		{"suspended gana", []*compliance.KYCCase{mk(compliance.KYCVerified, nil), mk(compliance.KYCSuspended, nil)}, compliance.KYCSuspended},
		{"verified + renovación pendiente", []*compliance.KYCCase{mk(compliance.KYCVerified, nil), mk(compliance.KYCPending, nil)}, compliance.KYCVerified},
		{"expirado + pendiente", []*compliance.KYCCase{mk(compliance.KYCExpired, nil), mk(compliance.KYCPending, nil)}, compliance.KYCPending},
		{"rejected", []*compliance.KYCCase{mk(compliance.KYCRejected, nil)}, compliance.KYCRejected},
	}
	for _, c := range cases {
		if got := compliance.EffectiveStatus(c.in, now); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}
