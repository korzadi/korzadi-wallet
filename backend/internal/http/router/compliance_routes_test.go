package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/compliance"
	"korzadi-wallet/backend/internal/application/compliance/compliancetest"
	"korzadi-wallet/backend/internal/http/handlers"
	"korzadi-wallet/backend/internal/http/router"
)

const (
	secret   = "0123456789abcdef0123456789abcdef" // test-only
	provider = "fake"
)

type rig struct {
	t                                  *testing.T
	r                                  *gin.Engine
	prov                               *compliancetest.FakeKYCProvider
	store                              *compliancetest.MemStore
	userA, userB, rev, adm, generalAdm string
}

func newRig(t *testing.T, withStepUp bool) *rig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	g := &rig{t: t, userA: uuid.NewString(), userB: uuid.NewString(), rev: uuid.NewString(), adm: uuid.NewString(), generalAdm: uuid.NewString()}
	roles := map[string][]string{
		g.userA: {"user"}, g.userB: {"user"},
		g.rev: {"user", "compliance_reviewer"}, g.adm: {"user", "compliance_admin"},
		g.generalAdm: {"user", "admin", "super_admin"},
	}
	getRoles := func(id string) ([]string, error) { return roles[id], nil }

	g.store = compliancetest.NewMemStore()
	g.prov = compliancetest.NewFakeKYCProvider(provider)
	reg := compliance.NewRegistry()
	reg.RegisterKYC(g.prov)
	cfg := compliance.Config{KYCEnabled: true, KYCProvider: provider, KYCWebhookSecret: secret, WebhookTolerance: 5 * time.Minute, AMLEnabled: true}
	svc := compliance.NewService(g.store, cfg, reg, &compliancetest.FakeAudit{}, nil)
	h := handlers.NewComplianceHandler(svc, getRoles)

	g.r = gin.New()
	api := g.r.Group("/api")
	authed := api.Group("")
	authed.Use(func(c *gin.Context) { // stand-in for middleware.AuthRequired
		u := c.GetHeader("X-Test-User")
		if u == "" {
			c.AbortWithStatusJSON(401, gin.H{"error": "no autenticado"})
			return
		}
		c.Set("user_id", u)
		c.Next()
	})
	var stepUp gin.HandlerFunc
	if withStepUp {
		stepUp = func(c *gin.Context) {
			if c.GetHeader("X-2FA-Code") != "123456" {
				c.AbortWithStatusJSON(403, gin.H{"error": "2fa"})
				return
			}
			c.Next()
		}
	}
	router.RegisterComplianceRoutes(api, authed, h, getRoles, stepUp, nil)
	return g
}

func (g *rig) do(method, path, user string, hdr map[string]string, body any) (int, map[string]any, string) {
	g.t.Helper()
	var rd *bytes.Reader
	switch b := body.(type) {
	case []byte:
		rd = bytes.NewReader(b)
	case nil:
		rd = bytes.NewReader(nil)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	g.r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out, w.Body.String()
}

func idem() map[string]string { return map[string]string{"Idempotency-Key": uuid.NewString()} }
func idem2fa() map[string]string {
	return map[string]string{"Idempotency-Key": uuid.NewString(), "X-2FA-Code": "123456"}
}

func TestRoutes_RequireAuthentication(t *testing.T) {
	g := newRig(t, true)
	for _, c := range [][2]string{
		{"GET", "/api/kyc/status"}, {"POST", "/api/kyc/start"},
		{"GET", "/api/admin/kyc/" + g.userA}, {"POST", "/api/admin/kyc/" + g.userA + "/approve"},
		{"POST", "/api/admin/compliance/holds/" + uuid.NewString() + "/release"},
	} {
		if code, _, _ := g.do(c[0], c[1], "", idem2fa(), nil); code != 401 {
			t.Errorf("%s %s sin JWT debe ser 401, fue %d", c[0], c[1], code)
		}
	}
}

func TestRoutes_NormalUserCannotUseAdminEndpoints(t *testing.T) {
	g := newRig(t, true)
	for _, u := range []string{g.userA, g.generalAdm} { // neither user nor admin/super_admin has compliance roles
		for _, c := range [][2]string{
			{"GET", "/api/admin/kyc/" + g.userB},
			{"POST", "/api/admin/kyc/" + g.userB + "/review"},
			{"POST", "/api/admin/kyc/" + g.userB + "/approve"},
			{"POST", "/api/admin/kyc/" + g.userB + "/reject"},
			{"POST", "/api/admin/kyc/" + g.userB + "/suspend"},
			{"POST", "/api/admin/kyc/" + g.userB + "/holds"},
			{"POST", "/api/admin/compliance/flags/" + uuid.NewString() + "/resolve"},
		} {
			if code, _, _ := g.do(c[0], c[1], u, idem2fa(), map[string]any{"reason": "x", "reason_code": "X"}); code != 403 {
				t.Errorf("%s %s como %s debe ser 403, fue %d", c[0], c[1], u[:4], code)
			}
		}
	}
}

func TestRoutes_UserCannotChangeOwnKYC(t *testing.T) {
	g := newRig(t, true)
	g.do("POST", "/api/kyc/start", g.userA, idem(), nil)
	for _, act := range []string{"review", "approve", "reject", "suspend"} {
		code, _, _ := g.do("POST", "/api/admin/kyc/"+g.userA+"/"+act, g.userA, idem2fa(), map[string]any{"reason": "x", "reason_code": "X"})
		if code != 403 {
			t.Errorf("%s sobre uno mismo debe ser 403, fue %d", act, code)
		}
	}
	_, body, _ := g.do("GET", "/api/kyc/status", g.userA, nil, nil)
	if body["status"] != "PENDING" {
		t.Fatalf("el estado no debe haber cambiado: %v", body)
	}
}

func TestRoutes_IDOR(t *testing.T) {
	g := newRig(t, true)
	g.do("POST", "/api/kyc/start", g.userA, idem(), nil)
	// B cannot read A's admin view, and /kyc/status never accepts an id
	if code, _, _ := g.do("GET", "/api/admin/kyc/"+g.userA, g.userB, nil, nil); code != 403 {
		t.Fatalf("B leyendo A: %d", code)
	}
	_, body, _ := g.do("GET", "/api/kyc/status?user_id="+g.userA, g.userB, nil, nil)
	if body["status"] != "NOT_STARTED" {
		t.Fatalf("B no debe ver el estado de A: %v", body)
	}
	if code, _, _ := g.do("GET", "/api/admin/kyc/not-a-uuid", g.rev, nil, nil); code != 400 {
		t.Fatalf("uuid inválido debe ser 400: %d", code)
	}
}

func TestRoutes_StartRequiresIdempotencyKeyAndIsIdempotent(t *testing.T) {
	g := newRig(t, true)
	if code, _, _ := g.do("POST", "/api/kyc/start", g.userA, nil, nil); code != 400 {
		t.Fatalf("sin Idempotency-Key: %d", code)
	}
	h := idem()
	c1, b1, _ := g.do("POST", "/api/kyc/start", g.userA, h, map[string]any{"country": "AR"})
	c2, b2, _ := g.do("POST", "/api/kyc/start", g.userA, h, map[string]any{"country": "AR"})
	if c1 != 200 || c2 != 200 || b1["case_id"] != b2["case_id"] || g.prov.Created != 1 {
		t.Fatalf("retry no idempotente: %d %d %v %v created=%d", c1, c2, b1, b2, g.prov.Created)
	}
	if b1["status"] != "PENDING" {
		t.Fatalf("%v", b1)
	}
}

func TestRoutes_ManualReviewFlowAndRBACSplit(t *testing.T) {
	g := newRig(t, true)
	g.do("POST", "/api/kyc/start", g.userA, idem(), nil)
	body := map[string]any{"reason": "documentación revisada", "reason_code": "DOC_OK"}

	if code, b, _ := g.do("POST", "/api/admin/kyc/"+g.userA+"/review", g.rev, idem(), body); code != 200 || b["status"] != "IN_REVIEW" {
		t.Fatalf("review: %d %v", code, b)
	}
	if code, _, _ := g.do("POST", "/api/admin/kyc/"+g.userA+"/approve", g.rev, idem2fa(), body); code != 403 {
		t.Fatalf("reviewer no puede aprobar: %d", code)
	}
	if code, _, _ := g.do("POST", "/api/admin/kyc/"+g.userA+"/approve", g.adm, idem(), body); code != 403 {
		t.Fatalf("sin step-up 2FA debe ser 403: %d", code)
	}
	code, b, _ := g.do("POST", "/api/admin/kyc/"+g.userA+"/approve", g.adm, idem2fa(), body)
	if code != 200 || b["status"] != "VERIFIED" {
		t.Fatalf("approve: %d %v", code, b)
	}
	// impossible transition -> 409, no change
	if code, _, _ := g.do("POST", "/api/admin/kyc/"+g.userA+"/reject", g.adm, idem2fa(), map[string]any{"reason": "x", "reason_code": "X"}); code != 409 {
		t.Fatalf("VERIFIED->REJECTED debe ser 409: %d", code)
	}
	if _, st, _ := g.do("GET", "/api/kyc/status", g.userA, nil, nil); st["status"] != "VERIFIED" {
		t.Fatalf("%v", st)
	}
	// missing reason -> 400
	g.do("POST", "/api/kyc/start", g.userB, idem(), nil)
	if code, _, _ := g.do("POST", "/api/admin/kyc/"+g.userB+"/review", g.rev, idem(), map[string]any{"reason": ""}); code != 400 {
		t.Fatalf("sin motivo: %d", code)
	}
}

func TestRoutes_PrivilegedRoutesFailClosedWithoutStepUp(t *testing.T) {
	g := newRig(t, false) // stepUp == nil
	code, _, _ := g.do("POST", "/api/admin/kyc/"+g.userA+"/approve", g.adm, idem2fa(), map[string]any{"reason": "x"})
	if code != 503 {
		t.Fatalf("debe fallar cerrado: %d", code)
	}
}

func TestRoutes_AdminViewDoesNotLeakSecretsOrProviderRefs(t *testing.T) {
	g := newRig(t, true)
	_, st, _ := g.do("POST", "/api/kyc/start", g.userA, idem(), nil)
	code, _, raw := g.do("GET", "/api/admin/kyc/"+g.userA, g.rev, nil, nil)
	if code != 200 {
		t.Fatal(code)
	}
	for _, bad := range []string{secret, "provider_reference", "document_hash", "fake-" + st["case_id"].(string)} {
		if strings.Contains(raw, bad) {
			t.Errorf("la vista admin no debe exponer %q", bad)
		}
	}
	_, _, raw = g.do("GET", "/api/kyc/status", g.userA, nil, nil)
	for _, bad := range []string{"risk", "hold", "flag"} {
		if strings.Contains(strings.ToLower(raw), bad) {
			t.Errorf("la vista del usuario expone %q", bad)
		}
	}
}

func TestRoutes_Webhook(t *testing.T) {
	g := newRig(t, true)
	_, st, _ := g.do("POST", "/api/kyc/start", g.userA, idem(), nil)
	ref := "fake-" + st["case_id"].(string)
	now := time.Now()
	body := compliancetest.Body("evt-1", compliance.EvtInReview, ref, now, "", nil)
	sig := compliancetest.Sign(secret, now, body)

	// no JWT needed: authenticated by signature
	if code, out, _ := g.do("POST", "/api/kyc/webhook/"+provider, "", sig, body); code != 200 || out["duplicate"] != false {
		t.Fatalf("válido: %d %v", code, out)
	}
	if code, out, _ := g.do("POST", "/api/kyc/webhook/"+provider, "", sig, body); code != 200 || out["duplicate"] != true {
		t.Fatalf("duplicado: %d %v", code, out)
	}
	bad := compliancetest.Sign("x-secreto-incorrecto-123456789", now, body)
	if code, _, _ := g.do("POST", "/api/kyc/webhook/"+provider, "", bad, body); code != 401 {
		t.Fatalf("firma inválida: %d", code)
	}
	old := compliancetest.Sign(secret, now.Add(-time.Hour), body)
	if code, _, _ := g.do("POST", "/api/kyc/webhook/"+provider, "", old, body); code != 400 {
		t.Fatalf("replay: %d", code)
	}
	if code, _, _ := g.do("POST", "/api/kyc/webhook/otro", "", sig, body); code != 404 {
		t.Fatalf("provider desconocido: %d", code)
	}
	if _, st2, _ := g.do("GET", "/api/kyc/status", g.userA, nil, nil); st2["status"] != "IN_REVIEW" {
		t.Fatalf("%v", st2)
	}
	// oversized body is refused
	huge := bytes.Repeat([]byte("a"), (1<<20)+10)
	if code, _, _ := g.do("POST", "/api/kyc/webhook/"+provider, "", sig, huge); code != 400 {
		t.Fatalf("body enorme: %d", code)
	}
}

func TestRoutes_HoldFlow(t *testing.T) {
	g := newRig(t, true)
	code, b, _ := g.do("POST", "/api/admin/kyc/"+g.userA+"/holds", g.adm, idem2fa(), map[string]any{"hold_type": "COMPLIANCE_HOLD", "reason_code": "REVIEW", "reason": "revisión interna"})
	if code != 200 || b["status"] != "ACTIVE" {
		t.Fatalf("%d %v", code, b)
	}
	id := b["id"].(string)
	if code, _, _ := g.do("POST", "/api/admin/compliance/holds/"+id+"/release", g.rev, idem2fa(), map[string]any{"reason": "x"}); code != 403 {
		t.Fatalf("reviewer no libera: %d", code)
	}
	if code, out, _ := g.do("POST", "/api/admin/compliance/holds/"+id+"/release", g.adm, idem2fa(), map[string]any{"reason": "concluida"}); code != 200 || out["status"] != "RELEASED" {
		t.Fatalf("%d %v", code, out)
	}
	if code, _, _ := g.do("POST", "/api/admin/compliance/holds/"+id+"/release", g.adm, idem2fa(), map[string]any{"reason": "otra"}); code != 409 {
		t.Fatalf("doble liberación: %d", code)
	}
	if _, err := g.store.ActiveHolds(context.Background(), uuid.MustParse(g.userA)); err != nil {
		t.Fatal(err)
	}
}
