package compliance_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/compliance"
)

func TestAuthz_UserCannotMutateOwnOrOthersKYC(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.start(u)
	selfActor := compliance.Actor{Type: compliance.ActorUser, UserID: &u, Roles: []string{"user"}}
	for _, to := range []compliance.KYCStatus{compliance.KYCInReview, compliance.KYCVerified, compliance.KYCRejected, compliance.KYCSuspended} {
		_, err := e.svc.Transition(context.Background(), compliance.TransitionInput{Actor: selfActor, UserID: u, To: to, Reason: "x", ReasonCode: "X", IdempotencyKey: key()})
		mustErr(t, err, compliance.ErrForbidden)
	}
	// even claiming ADMIN type with only the base "user" role
	fake := compliance.Actor{Type: compliance.ActorAdmin, UserID: &u, Roles: []string{"user"}}
	_, err := e.svc.Transition(context.Background(), compliance.TransitionInput{Actor: fake, UserID: u, To: compliance.KYCVerified, Reason: "x", IdempotencyKey: key()})
	mustErr(t, err, compliance.ErrForbidden)
	if e.status(u) != compliance.KYCPending {
		t.Fatal("el estado no debe haber cambiado")
	}
}

func TestAuthz_GeneralAdminRolesAreNotComplianceRoles(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.start(u)
	a := adminActor("admin", "super_admin") // RBAC general: NO implica compliance
	_, err := e.svc.Review(context.Background(), a, u, nil, "D", "x", key())
	mustErr(t, err, compliance.ErrForbidden)
	_, err = e.svc.AdminGet(context.Background(), a, u)
	mustErr(t, err, compliance.ErrForbidden)
	_, err = e.svc.PlaceHold(context.Background(), compliance.PlaceHoldInput{Actor: a, UserID: u, Type: compliance.HoldCompliance, ReasonCode: "R", Reason: "x", IdempotencyKey: key()})
	mustErr(t, err, compliance.ErrForbidden)
}

func TestAuthz_ReviewerCannotDecide(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.start(u)
	rev := reviewer()
	if _, err := e.svc.Review(context.Background(), rev, u, nil, "D", "rev", key()); err != nil {
		t.Fatalf("el reviewer sí puede mover a IN_REVIEW: %v", err)
	}
	for name, fn := range map[string]func() error{
		"approve": func() error { _, err := e.svc.Approve(context.Background(), rev, u, nil, "OK", "x", key()); return err },
		"reject":  func() error { _, err := e.svc.Reject(context.Background(), rev, u, nil, "NO", "x", key()); return err },
		"suspend": func() error { _, err := e.svc.Suspend(context.Background(), rev, u, nil, "C", "x", key()); return err },
	} {
		mustErr(t, fn(), compliance.ErrForbidden)
		_ = name
	}
	if e.status(u) != compliance.KYCInReview {
		t.Fatal("estado")
	}
	if _, err := e.svc.AdminGet(context.Background(), rev, u); err != nil {
		t.Fatalf("el reviewer puede ver: %v", err)
	}
}

func TestAuthz_NoSelfAction(t *testing.T) {
	e := newEnv(t)
	adm := cAdmin()
	own := *adm.UserID
	e.start(own)
	_, err := e.svc.Review(context.Background(), adm, own, nil, "D", "x", key())
	mustErr(t, err, compliance.ErrSelfAction)
	_, err = e.svc.Approve(context.Background(), adm, own, nil, "OK", "x", key())
	mustErr(t, err, compliance.ErrSelfAction)
}

func TestIDOR_CaseOfAnotherUserIsNotReachable(t *testing.T) {
	e := newEnv(t)
	victim, other := uuid.New(), uuid.New()
	vc := e.start(victim)
	e.start(other)
	_, _ = e.svc.Review(context.Background(), reviewer(), other, nil, "D", "r", key())
	// an admin targeting `other` but passing the VICTIM's case id must not touch it
	_, err := e.svc.Approve(context.Background(), cAdmin(), other, &vc.ID, "OK", "x", key())
	mustErr(t, err, compliance.ErrCaseNotFound)
	if e.status(victim) != compliance.KYCPending {
		t.Fatal("el caso ajeno no debe modificarse")
	}
}

func TestIDOR_GetStatusIsScopedToCaller(t *testing.T) {
	e := newEnv(t)
	a, b := uuid.New(), uuid.New()
	e.verified(a)
	if e.status(b) != compliance.KYCNotStarted {
		t.Fatal("b no debe ver el estado de a")
	}
	va, _ := e.svc.GetStatus(context.Background(), a)
	vb, _ := e.svc.GetStatus(context.Background(), b)
	if va.CaseID != nil && vb.CaseID != nil && *va.CaseID == *vb.CaseID {
		t.Fatal("casos cruzados")
	}
}
