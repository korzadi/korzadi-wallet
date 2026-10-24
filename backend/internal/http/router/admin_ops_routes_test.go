package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	bw "korzadi-wallet/backend/internal/application/blockchainwithdrawal"
	"korzadi-wallet/backend/internal/http/handlers"
)

type fakeReviewer struct{ listed int }

func (f *fakeReviewer) ListNeedsReview(ctx context.Context, limit, offset int) ([]*bw.Withdrawal, error) {
	f.listed++
	return nil, nil
}
func (f *fakeReviewer) ResolveNeedsReview(ctx context.Context, in bw.ResolveInput) (*bw.Withdrawal, error) {
	return nil, bw.ErrNotInReview
}

func adminOpsEngine(roles []string, stepUp gin.HandlerFunc, rv *fakeReviewer) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	authed := r.Group("/api", func(c *gin.Context) { c.Set("user_id", "11111111-1111-1111-1111-111111111111") })
	admin := authed.Group("/admin")
	h := handlers.NewAdminOpsHandler(rv, nil, nil, nil, nil, nil)
	RegisterAdminOpsRoutes(admin, h, func(string) ([]string, error) { return roles, nil }, stepUp)
	return r
}

func twoFAHeader(c *gin.Context) {
	if c.GetHeader("X-2FA-Code") == "" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "2fa_required"})
		return
	}
	c.Next()
}

func do(r *gin.Engine, method, path, code string) int {
	req := httptest.NewRequest(method, path, nil)
	if code != "" {
		req.Header.Set("X-2FA-Code", code)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestAdminOps_RequiresSuperAdmin(t *testing.T) {
	rv := &fakeReviewer{}
	r := adminOpsEngine([]string{"admin"}, twoFAHeader, rv)
	if got := do(r, http.MethodGet, "/api/admin/withdrawals/review", "123456"); got != http.StatusForbidden {
		t.Fatalf("un admin normal debe recibir 403, obtuvo %d", got)
	}
	if rv.listed != 0 {
		t.Fatal("el handler no debe ejecutarse sin super_admin")
	}
}

func TestAdminOps_Requires2FA(t *testing.T) {
	rv := &fakeReviewer{}
	r := adminOpsEngine([]string{"super_admin"}, twoFAHeader, rv)
	if got := do(r, http.MethodGet, "/api/admin/withdrawals/review", ""); got != http.StatusForbidden {
		t.Fatalf("sin 2FA debe recibir 403, obtuvo %d", got)
	}
	if rv.listed != 0 {
		t.Fatal("el handler no debe ejecutarse sin 2FA")
	}
}

func TestAdminOps_SuperAdminWith2FA_Allowed(t *testing.T) {
	rv := &fakeReviewer{}
	r := adminOpsEngine([]string{"super_admin"}, twoFAHeader, rv)
	if got := do(r, http.MethodGet, "/api/admin/withdrawals/review", "123456"); got != http.StatusOK {
		t.Fatalf("super_admin + 2FA debe recibir 200, obtuvo %d", got)
	}
	if rv.listed != 1 {
		t.Fatalf("el handler debía ejecutarse una vez, fue %d", rv.listed)
	}
}

// Sin middleware de 2FA las rutas NO se registran (falla cerrado).
func TestAdminOps_NoStepUp_RoutesNotRegistered(t *testing.T) {
	r := adminOpsEngine([]string{"super_admin"}, nil, &fakeReviewer{})
	if got := do(r, http.MethodGet, "/api/admin/withdrawals/review", "123456"); got != http.StatusNotFound {
		t.Fatalf("sin stepUp las rutas no deben existir (404), obtuvo %d", got)
	}
}
