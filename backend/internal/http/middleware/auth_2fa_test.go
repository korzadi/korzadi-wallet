package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"korzadi-wallet/backend/internal/infrastructure/authjwt"
)

// The pre-2FA login challenge is an opaque random token, not a JWT. It must
// never be accepted as an access token by protected endpoints.
func TestAuthRequiredRejectsLoginChallengeToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := authjwt.NewManager("test-only-secret-with-sufficient-length-123456", time.Minute, time.Hour)
	r := gin.New()
	r.GET("/protected", AuthRequired(manager), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer 3q2-7wLzYb0mQp1xJ8sV5nRk9tUa4cHe6fDgHiJoKlM")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("challenge token was accepted by AuthRequired: %d", rec.Code)
	}
}
