package compliance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/compliance"
	"korzadi-wallet/backend/internal/application/compliance/compliancetest"
)

var forbiddenKeys = []string{"password", "token", "secret", "private_key", "seed", "mnemonic", "signature",
	"document_number", "document", "image", "selfie", "biometric", "full_name", "date_of_birth", "payload"}

func TestPrivacy_AuditHasNoSensitiveData(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c := e.verified(u)
	e.scr.Outcome = compliance.MatchPotential
	_, _ = e.svc.RequestScreening(context.Background(), reviewer(), u, compliance.ScreenSanctions, key())
	_, _ = e.svc.PlaceHold(context.Background(), compliance.PlaceHoldInput{Actor: cAdmin(), UserID: u, Type: compliance.HoldCompliance, ReasonCode: "R", Reason: "motivo interno", IdempotencyKey: key()})
	_, _ = e.hook("e1", compliance.EvtExpired, c.ProviderReference, e.clock.Now(), "", nil)

	entries := e.audit.Snapshot()
	if len(entries) < 5 {
		t.Fatalf("se esperaban eventos de auditoría, hay %d", len(entries))
	}
	required := []string{"action", "resource", "user_id", "old_state", "new_state", "reason", "request_id", "created_at"}
	for _, en := range entries {
		for _, k := range required {
			if _, ok := en.Metadata[k]; !ok {
				t.Errorf("%s: falta el campo %q en la auditoría", en.Action, k)
			}
		}
		for k, v := range en.Metadata {
			lk := strings.ToLower(k)
			for _, bad := range forbiddenKeys {
				if lk == bad {
					t.Errorf("%s: clave sensible %q en auditoría", en.Action, k)
				}
			}
			if strings.Contains(fmt.Sprint(v), testSecret) {
				t.Errorf("%s: el secreto del webhook apareció en %q", en.Action, k)
			}
		}
	}
}

func TestPrivacy_UserViewDoesNotExposeInternalControls(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	e.verified(u)
	_, _ = e.svc.PlaceHold(context.Background(), compliance.PlaceHoldInput{Actor: cAdmin(), UserID: u, Type: compliance.HoldCompliance, ReasonCode: "R", Reason: "x", IdempotencyKey: key()})
	_, _ = e.svc.SetRiskLevel(context.Background(), compliance.SetRiskInput{Actor: compliance.Actor{Type: compliance.ActorSystem}, UserID: u, Level: compliance.RiskHigh})
	v, _ := e.svc.GetStatus(context.Background(), u)
	raw, _ := json.Marshal(v)
	s := strings.ToLower(string(raw))
	for _, bad := range []string{"risk", "hold", "flag", "sanction", "screen", "provider", "reason"} {
		if strings.Contains(s, bad) {
			t.Errorf("la vista del usuario expone %q: %s", bad, s)
		}
	}
}

func TestPrivacy_ErrorsAndLogsDoNotLeak(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	e := newEnv(t)
	u := uuid.New()
	c := e.start(u)
	body := compliancetest.Body("e1", compliance.EvtApproved, c.ProviderReference, e.clock.Now(), "", nil)
	bad := compliancetest.Sign("otro-secreto-otro-secreto-1234", e.clock.Now(), body)
	_, err := e.svc.ProcessWebhook(context.Background(), provName, bad, body)
	if err == nil {
		t.Fatal("se esperaba error")
	}
	e.prov.Fail = true
	_, err2 := e.svc.StartVerification(context.Background(), compliance.StartInput{UserID: uuid.New(), IdempotencyKey: key()})
	e.prov.Fail = false

	for _, msg := range []string{err.Error(), err2.Error(), buf.String()} {
		for _, leak := range []string{testSecret, "otro-secreto", "provider down", c.ProviderReference} {
			if strings.Contains(msg, leak) {
				t.Errorf("filtración de %q en %q", leak, msg)
			}
		}
	}
	// every exported sentinel error is free of secrets/PII by construction
	for _, se := range []error{compliance.ErrWebhookSignature, compliance.ErrWebhookReplay, compliance.ErrProviderUnavailable, compliance.ErrForbidden} {
		if strings.Contains(strings.ToLower(se.Error()), "secret") {
			t.Errorf("mensaje de error sospechoso: %s", se)
		}
	}
}

func TestPrivacy_CaseStoresOnlyMetadata(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c := e.start(u)
	raw, _ := json.Marshal(c)
	s := strings.ToLower(string(raw))
	for _, bad := range []string{"password", "selfie", "image", "biometric", "document_number", "full_name", "birth"} {
		if strings.Contains(s, bad) {
			t.Errorf("KYCCase contiene %q", bad)
		}
	}
}
