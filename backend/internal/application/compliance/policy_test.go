package compliance_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/application/compliance"
	"korzadi-wallet/backend/internal/application/security"
)

func TestDecide(t *testing.T) {
	cfg := compliance.Config{KYCEnabled: true, AMLEnabled: true, WithdrawalRequiresKYC: true, LimitedTransferMax: decimal.NewFromInt(100)}
	hold := []*compliance.Hold{{Type: compliance.HoldCompliance}}
	amt := func(n int64) *decimal.Decimal { d := decimal.NewFromInt(n); return &d }
	cases := []struct {
		name    string
		cfg     compliance.Config
		op      security.OperationType
		st      compliance.KYCStatus
		holds   []*compliance.Hold
		amount  *decimal.Decimal
		allowed bool
	}{
		{"retiro verified", cfg, security.OpWithdrawal, compliance.KYCVerified, nil, amt(5), true},
		{"retiro sin kyc", cfg, security.OpWithdrawal, compliance.KYCNotStarted, nil, nil, false},
		{"retiro pendiente", cfg, security.OpWithdrawal, compliance.KYCPending, nil, nil, false},
		{"retiro expirado", cfg, security.OpWithdrawal, compliance.KYCExpired, nil, nil, false},
		{"retiro suspendido", cfg, security.OpWithdrawal, compliance.KYCSuspended, nil, nil, false},
		{"transfer verified", cfg, security.OpTransfer, compliance.KYCVerified, nil, amt(1_000_000), true},
		{"transfer tier reducido ok", cfg, security.OpTransfer, compliance.KYCPending, nil, amt(100), true},
		{"transfer tier reducido excede", cfg, security.OpTransfer, compliance.KYCNotStarted, nil, amt(101), false},
		{"transfer rechazado", cfg, security.OpTransfer, compliance.KYCRejected, nil, amt(1), false},
		{"transfer suspendido", cfg, security.OpTransfer, compliance.KYCSuspended, nil, amt(1), false},
		{"hold bloquea aunque verified", cfg, security.OpTransfer, compliance.KYCVerified, hold, amt(1), false},
		{"depósito no se evalúa", cfg, "DEPOSIT", compliance.KYCSuspended, hold, nil, true},
		{"sweep no se evalúa", cfg, security.OpSweep, compliance.KYCRejected, hold, nil, true},
		{"kyc off, sin hold", compliance.Config{AMLEnabled: true}, security.OpWithdrawal, compliance.KYCNotStarted, nil, nil, true},
		{"kyc off, con hold", compliance.Config{AMLEnabled: true}, security.OpWithdrawal, compliance.KYCNotStarted, hold, nil, false},
		{"aml off ignora hold", compliance.Config{KYCEnabled: true, WithdrawalRequiresKYC: true}, security.OpWithdrawal, compliance.KYCVerified, hold, nil, true},
		{"sin tier reducido configurado", compliance.Config{KYCEnabled: true}, security.OpTransfer, compliance.KYCPending, nil, amt(1), false},
		{"retiro sin exigir kyc", compliance.Config{KYCEnabled: true}, security.OpWithdrawal, compliance.KYCNotStarted, nil, nil, true},
	}
	for _, c := range cases {
		d := compliance.Decide(c.cfg, c.op, c.st, c.holds, c.amount)
		if d.Allowed != c.allowed {
			t.Errorf("%s: allowed=%v want %v (code=%s)", c.name, d.Allowed, c.allowed, d.Code)
		}
		if !d.Allowed && d.Code == "" {
			t.Errorf("%s: denegación sin código", c.name)
		}
	}
}

func TestEngine_CheckOperationByKYCStatus(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	none := uuid.New()
	mustErr(t, e.eng.CheckOperation(ctx, security.OpWithdrawal, none, nil), security.ErrComplianceRestricted)
	ver := uuid.New()
	e.verified(ver)
	if err := e.eng.CheckOperation(ctx, security.OpWithdrawal, ver, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Suspend(ctx, cAdmin(), ver, nil, "COMPLIANCE", "suspendido", key()); err != nil {
		t.Fatal(err)
	}
	mustErr(t, e.eng.CheckOperation(ctx, security.OpWithdrawal, ver, nil), security.ErrComplianceRestricted)
	mustErr(t, e.eng.CheckOperation(ctx, security.OpTransfer, ver, nil), security.ErrComplianceRestricted)
}

func TestEngine_LimitedTierCapAppliedOnReserve(t *testing.T) {
	e := newEnv(t, func(c *compliance.Config) { c.LimitedTransferMax = decimal.NewFromInt(50) })
	u := uuid.New()
	e.start(u)
	small, big := decimal.NewFromInt(50), decimal.NewFromInt(51)
	if err := e.eng.CheckOperation(context.Background(), security.OpTransfer, u, nil); err != nil {
		t.Fatalf("PreAuthorize (sin monto) debe pasar en tier reducido: %v", err)
	}
	if err := e.eng.CheckOperation(context.Background(), security.OpTransfer, u, &small); err != nil {
		t.Fatal(err)
	}
	mustErr(t, e.eng.CheckOperation(context.Background(), security.OpTransfer, u, &big), security.ErrComplianceRestricted)
}

func TestEngine_InboundTransfers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	plain := uuid.New() // no KYC: may still RECEIVE
	if err := e.eng.CheckInbound(ctx, security.OpTransfer, plain); err != nil {
		t.Fatalf("recibir no debe exigir KYC: %v", err)
	}
	held := uuid.New()
	_, _ = e.svc.PlaceHold(ctx, compliance.PlaceHoldInput{Actor: cAdmin(), UserID: held, Type: compliance.HoldCompliance, ReasonCode: "R", Reason: "x", IdempotencyKey: key()})
	mustErr(t, e.eng.CheckInbound(ctx, security.OpTransfer, held), security.ErrComplianceRestricted)
	susp := uuid.New()
	e.start(susp)
	_, _ = e.svc.Review(ctx, reviewer(), susp, nil, "D", "r", key())
	_, _ = e.svc.Suspend(ctx, cAdmin(), susp, nil, "C", "s", key())
	mustErr(t, e.eng.CheckInbound(ctx, security.OpTransfer, susp), security.ErrComplianceRestricted)
	if err := e.eng.CheckInbound(ctx, security.OpWithdrawal, held); err != nil {
		t.Fatal("CheckInbound solo aplica a transferencias internas")
	}
}

func TestEngine_RestrictionIsGenericToCaller(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	err := e.eng.CheckOperation(context.Background(), security.OpWithdrawal, u, nil)
	status, code, ok := security.HTTPStatusFor(err)
	if !ok || status != 403 || code != "compliance_restricted" {
		t.Fatalf("mapeo HTTP: %d %s %v", status, code, ok)
	}
}

func TestConfig_FailSafeDefaults(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	c, err := compliance.LoadConfig(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.KYCEnabled || c.AMLEnabled || c.SanctionsEnabled || c.WorkersEnabled {
		t.Fatal("todo debe estar desactivado por defecto")
	}
	if c.LimitedTransferMax.IsPositive() || c.Retention.KYCHistoryDays != 0 {
		t.Fatal("no se deben inventar límites ni plazos de retención")
	}
	bad := []map[string]string{
		{"KYC_ENABLED": "true"},
		{"KYC_ENABLED": "true", "KYC_PROVIDER": "p"},
		{"KYC_ENABLED": "true", "KYC_PROVIDER": "p", "KYC_WEBHOOK_SECRET": "corto"},
		{"KYC_ENABLED": "quizas"},
		{"SANCTIONS_SCREENING_ENABLED": "true"},
		{"KYC_LIMITED_TRANSFER_MAX_AMOUNT": "abc"},
		{"KYC_LIMITED_TRANSFER_MAX_AMOUNT": "-5"},
		{"KYC_WEBHOOK_TOLERANCE_SECONDS": "0"},
		{"RETENTION_KYC_HISTORY_DAYS": "-1"},
	}
	for _, m := range bad {
		if _, err := compliance.LoadConfig(env(m)); err == nil {
			t.Errorf("config inválida aceptada: %v", m)
		} else if strings.Contains(err.Error(), "corto") {
			t.Error("el error no debe imprimir el valor del secreto")
		}
	}
	ok := map[string]string{"KYC_ENABLED": "true", "KYC_PROVIDER": "p", "KYC_WEBHOOK_SECRET": testSecret, "KYC_LIMITED_TRANSFER_MAX_AMOUNT": "25"}
	if c, err := compliance.LoadConfig(env(ok)); err != nil || !c.LimitedTransferMax.Equal(decimal.NewFromInt(25)) {
		t.Fatalf("%v", err)
	}
}
