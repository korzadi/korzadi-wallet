package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"korzadi-wallet/backend/internal/application/auth"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
)

type mockAuthService struct {
	registerFn func() (*auth.AuthResult, error)
	loginFn    func() (*auth.AuthResult, error)
	refreshFn  func() (*auth.AuthResult, error)
	logoutFn   func() error
}

func (m *mockAuthService) Register(ctx interface{}, in auth.RegisterInput, ip, ua string) (*auth.AuthResult, error) {
	if m.registerFn != nil {
		return m.registerFn()
	}
	return nil, nil
}

func (m *mockAuthService) Login(ctx interface{}, email, password, ip, ua string) (*auth.AuthResult, error) {
	if m.loginFn != nil {
		return m.loginFn()
	}
	return nil, nil
}

func (m *mockAuthService) Refresh(ctx interface{}, token, ip, ua string) (*auth.AuthResult, error) {
	if m.refreshFn != nil {
		return m.refreshFn()
	}
	return nil, nil
}

func (m *mockAuthService) Logout(ctx interface{}, token string) error {
	if m.logoutFn != nil {
		return m.logoutFn()
	}
	return nil
}

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func TestAuthHandler_Register_BadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{"email":"bad"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	svc := auth.NewService(nil, nil, nil, authjwt.NewManager("test-secret-with-sufficient-length-123456", time.Hour, time.Hour), nil, nil, time.Hour)
	h := NewAuthHandler(svc)
	h.Register(ctx)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d", rec.Code)
	}
}

func TestAuthHandler_Login_BadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"email":"bad"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	h := NewAuthHandler(nil)
	h.Login(ctx)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d", rec.Code)
	}
}

func TestAuthHandler_Refresh_BadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/auth/refresh", strings.NewReader(`{}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	h := NewAuthHandler(nil)
	h.Refresh(ctx)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d", rec.Code)
	}
}

func TestAuthHandler_Logout_BadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader(`{}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	h := NewAuthHandler(nil)
	h.Logout(ctx)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d", rec.Code)
	}
}

func TestAuthHandler_LoginTwoFactor_BadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/auth/login/2fa", strings.NewReader(`{"challenge_token":""}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	h := NewAuthHandler(nil)
	h.LoginTwoFactor(ctx)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d", rec.Code)
	}
}

func TestRespondDomainError_Login2FACodes(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{&domain.DomainError{Code: "invalid_2fa_challenge", Message: "x"}, http.StatusUnauthorized},
		{&domain.DomainError{Code: "2fa_challenge_expired", Message: "x"}, http.StatusUnauthorized},
		{&domain.DomainError{Code: "2fa_required", Message: "x"}, http.StatusUnauthorized},
		{&domain.DomainError{Code: "invalid_2fa_code", Message: "x"}, http.StatusForbidden},
		{&domain.DomainError{Code: "2fa_unavailable", Message: "x"}, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		respondDomainError(ctx, tc.err)
		if rec.Code != tc.want {
			t.Fatalf("%v: expected %d, got %d", tc.err, tc.want, rec.Code)
		}
	}
}
